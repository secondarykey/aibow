package aibow

// agent.go: エージェントループ。「生成する → ツールを実行する → 結果を渡してもう一度生成する」の繰り返し。
//
// モデルはツールを実行できない。返事の中に「このツールをこの引数で呼びたい」と書いて止まるだけ。
// アプリがそれを読み取って実行し、結果を発言として足して、もう一度モデルに続きを書かせる。
// これを、モデルがツールを呼ばずに答えを書き終えるまで繰り返すのが「エージェント」の中身:
//
//	1. 生成する(毎回、会話の上限に収める準備 = compact を通す。ツールの結果でプロンプトが伸びるため)
//	2. 返事(答え・思考・ツール呼び出し・統計)を保存する
//	3. ツール呼び出しが無ければ終わり
//	4. あれば順に実行し、結果を role tool の発言として保存する
//	5. 1 に戻る
//
// ここは書式(<tool_call> など)を知らない。呼び出しは Generator が ToolCall に読み取って渡してくる。
//
// 失敗しても送信は止めない。ツールのエラー・知らないツール・引数の間違い・書式の崩れは、
// 「エラー: …」という結果としてモデルに返し、モデル自身に立て直させる(小さいモデルは書式を崩しがちなので大事)。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
	"uuid"

	"github.com/secondarykey/aibow/format"
	"github.com/secondarykey/aibow/log"
	"github.com/secondarykey/aibow/tool"
)

const (
	// maxToolCalls は1回の送信でツールを実行する回数の上限。モデルが同じ呼び出しを繰り返して終わらないのを防ぐ。
	// コーディング(読む → 直す → 動かす)は回数がかさむので 20 回(0004 では 8 回だった。停止ボタンでも止められる)
	maxToolCalls = 20

	// maxBrokenRounds は、呼び出しが全部「読み取れない呼び出し」だった回が続いてよい数。これを超えたら止める。
	// 書式の崩れは maxToolCalls に数えない(立て直しの機会を減らさないため)ので、モデルが同じ崩れ方を繰り返すと
	// 終わらなくなる(2026-10-16 に起きた。答えの中の `<tool_call>` を呼び出しと読んでいた)。立て直せるのはふつう1〜2回なので 3 回
	maxBrokenRounds = 3
)

// allBroken は、呼び出しが全部、書式が崩れて読み取れなかったものかを返す。
func allBroken(calls []format.ToolCall) bool {
	for _, c := range calls {
		if c.Error == "" {
			return false
		}
	}
	return len(calls) > 0
}

// toolTimeout はツール1回の実行の時間の上限(テストで短くするので変数にしている)。
// 上限を自分で決めるツール(tool.TimeLimiter。run_command は 120 秒)は、そちらを使う
var toolTimeout = 10 * time.Second

// stopGrace は、時間切れ・停止のあと、ツールが後片付けをして返ってくるのを待つ時間。
// run_command は子プロセスを止めてから「ここまでの出力」を返すので、それを受け取れるように少し待つ
var stopGrace = 5 * time.Second

// errorPrefix はツールの結果がエラーのときに、結果の文の先頭に付ける。
const errorPrefix = "エラー: "

// runTurn は、保存済みのユーザーの発言(history の最後)への返事を、ツールを呼ばなくなるまで生成する。
// 保存した最後の返事を返す(何も生成しなかったら nil)。途中で止めたときは、その理由をエラーで返す。
func (cs *Conversations) runTurn(ctx context.Context, conv Conversation, history []StoredMessage, emit func(ChatEvent)) (*StoredMessage, error) {
	var last *StoredMessage
	calls := 0  // この送信でツールを実行した回数
	broken := 0 // 呼び出しが全部読み取れなかった回が、いくつ続いているか
	for round := 1; ; round++ {
		// 1. モデルに渡す内容を決める。2回目からは、ツールの結果を足しても収まるかを先に確かめる
		if round > 1 {
			if err := cs.checkFits(conv.Settings, history); err != nil {
				return last, fmt.Errorf("ツールの結果を足すと会話の上限を超えるため、ここで止めました。%w", err)
			}
		}
		p, err := cs.prepare(ctx, conv, history, emit)
		if err != nil {
			if ctx.Err() != nil { // 止められた(アプリの終了など)ときは、エラーを出さずに終わる
				return last, nil
			}
			return last, err
		}
		cs.emitUsage(emit, conv.ID, p.usage(history, cs.limitsOf(conv.Settings).history))

		// 2. 生成して保存する
		r, err := cs.generate(ctx, conv, p.messages, emit)
		if err != nil || r == nil {
			return last, err // r == nil: 何も生成されなかった(すぐに停止した、など)
		}
		if err := cs.save(ctx, *r, ""); err != nil {
			return last, err
		}
		last = r
		history = append(history, *r)
		emit(ChatEvent{ConversationID: conv.ID, Type: "reply", Reply: r})

		// 3. ツールを呼んでいなければ終わり
		if len(r.ToolCalls) == 0 {
			break
		}

		// 4. 順に実行して、結果を保存する。上限を超えた分・止められた後の分は、実行せずにその旨を結果にする
		// (どの呼び出しにも必ず結果がある形にしておくと、次の送信でモデルに渡す履歴が崩れない)
		overLimit := false
		for _, c := range r.ToolCalls {
			var res StoredMessage
			switch {
			case ctx.Err() != nil:
				res = toolMessage(conv.ID, c, errorPrefix+"停止されたため実行していません", true, 0)
			case calls >= maxToolCalls:
				overLimit = true
				res = toolMessage(conv.ID, c, fmt.Sprintf("%s1回の送信で実行できるツールは %d 回までのため、実行していません", errorPrefix, maxToolCalls), true, 0)
			default:
				if c.Error == "" {
					calls++ // 書式の崩れは実行していないので数えない(立て直しの機会を減らさないため)
				}
				emit(ChatEvent{ConversationID: conv.ID, Type: "tool_start", ToolCall: &c})
				res = cs.runTool(ctx, conv, c, emit)
				var err error
				if res, err = cs.fitResult(conv.Settings, history, res); err != nil {
					return last, err
				}
			}
			if err := cs.save(ctx, res, ""); err != nil {
				return last, err
			}
			history = append(history, res)
			emit(ChatEvent{ConversationID: conv.ID, Type: "tool_result", Message: &res})
		}

		// 5. 止められた・上限に達した・崩れた呼び出しが続いたら、もう生成しない。そうでなければ結果を渡して続きを書かせる
		if ctx.Err() != nil {
			return last, nil
		}
		if overLimit {
			return last, fmt.Errorf("1回の送信で実行できるツール(%d 回)を超えたため、止めました。続けるときは、もう一度送信してください", maxToolCalls)
		}
		if allBroken(r.ToolCalls) {
			broken++
		} else {
			broken = 0
		}
		if broken >= maxBrokenRounds {
			log.Warnf("読み取れない呼び出しが %d 回続いたため、止めました", broken)
			return last, fmt.Errorf("読み取れないツールの呼び出しが %d 回続いたため、止めました。"+
				"モデルが書式を立て直せていません。頼み方を変えるか、崩れた返事を「渡さない」にしてから、もう一度送信してください", broken)
		}
	}

	// 返事が増えたあとのメーター。次の送信では、この返事もモデルに渡る(止められたあとも出すので、止める合図は外して読む)
	if u, err := cs.usage(context.WithoutCancel(ctx), conv, history); err == nil {
		cs.emitUsage(emit, conv.ID, u)
	}
	return last, nil
}

// runTool はツールを1つ実行して、結果の発言(role tool)を作る。失敗もエラーの結果として返す。
//
// 書き換えるツール(tool.Confirmable)は、実行する前にユーザーの確認を待つ。
// 拒否されたら実行せず、「ユーザーが実行を拒否しました。理由: …」を結果としてモデルに返す(ループは続ける。
// 失敗しても送信は止めない、と同じ考え方で、モデルに別のやり方を考えさせる)。
//
// ツールの扱いは、呼ぶたびに会話の設定を読み直して決める。送信の途中(ループの中)で会話の上書きが変わっても
// (確認のカードの「この会話では今後聞かない」など。0010)、次の呼び出しから効くように。
func (cs *Conversations) runTool(ctx context.Context, conv Conversation, c format.ToolCall, emit func(ChatEvent)) StoredMessage {
	if fresh, _, err := cs.load(ctx, conv.ID); err == nil {
		conv.Settings = fresh.Settings
	}
	t, args, err := cs.lookupTool(conv.Settings, c)
	if err != nil {
		return toolMessage(conv.ID, c, errorPrefix+err.Error(), true, 0)
	}
	res := cs.runFound(ctx, conv, c, t, args, emit)
	if r, ok := t.(tool.Remote); ok {
		res.ToolResult.Server = r.Server() // どの MCP のサーバーが実行したか(画面のカードに出す)
	}
	return res
}

// runFound は、見つかったツールを(確認が要るなら確認してから)実行する。
func (cs *Conversations) runFound(ctx context.Context, conv Conversation, c format.ToolCall, t tool.Tool, args tool.Args, emit func(ChatEvent)) StoredMessage {
	var conf *Confirmation
	var err error
	if cs.needsConfirm(conv.Settings, t) {
		ct, ok := t.(tool.Confirmable)
		if !ok {
			ct = argsPreview{t} // 確認の内容を作れないツール(読むだけのツールを「確認する」にしたとき)は、引数を見せる
		}
		if conf, err = cs.confirm(ctx, conv.ID, c, ct, args, emit); err != nil {
			return toolMessage(conv.ID, c, errorPrefix+err.Error(), true, 0)
		}
		if !conf.Approved {
			text := tool.RejectedText(conf.Reason)
			if conf.Remember == ToolOff {
				// 「今後許可しない」: 次の送信からはモデルに見せないが、この送信の間は見えているので、呼び直さないように伝える
				text += "。この会話では、このツールは今後使えません"
			}
			res := toolMessage(conv.ID, c, text, false, 0)
			res.ToolResult.Confirm = conf
			return res
		}
	}

	// 時間を測るのは実行だけ(確認を待った時間は入れない)
	start := time.Now()
	var text string
	var waited time.Duration
	if it, ok := t.(tool.Interactive); ok {
		// MCP のサーバーのツールは、実行の途中でサーバーが確認を求めてくることがある(0007 の 5-3)。
		// 時間の上限はツールが自分で数える(確認を待つ時間を数えないため)ので、execute は通さない
		var asked *Confirmation
		text, err = it.RunInteractive(ctx, args, cs.askFor(ctx, conv, c, t, args, emit, &asked, &waited))
		if asked != nil {
			conf = asked // カードに残すのは、サーバーからの確認の答え(差分を見て決めたほう)
		}
	} else {
		text, err = cs.execute(ctx, t, args)
	}
	sec := (time.Since(start) - waited).Seconds()
	if err == nil && conf != nil && !conf.Approved && conf.Remember == ToolOff {
		text += "。この会話では、このツールは今後使えません" // サーバーからの確認を「今後許可しない」で拒否した(上と同じ)
	}
	res := toolMessage(conv.ID, c, text, false, sec)
	if err != nil {
		res = toolMessage(conv.ID, c, errorPrefix+err.Error(), true, sec)
	}
	res.ToolResult.Confirm = conf
	return res
}

// askFor は、MCP のサーバーが実行の途中で確認を求めてきたときに、ユーザーに聞く関数を作る(0007 の 5-3)。
//
// 聞くかどうかは、呼ぶ前の確認と同じく、ツールの扱いで決める(会話の設定を読み直す。カードで「この会話では今後聞かない」にしたら、すぐ効く)。
// 「確認しない」なら聞かずに受け入れる。聞いたときは、今の確認のカード(サーバーが送ってきた差分など)を出し、答えを asked に、
// 待った時間を waited に入れる(結果に答えを残すためと、実行の秒数から待った時間を外すため)。
//
// 会話を読み直すときは、送信の ctx(sendCtx。利用者が入っている)を使う。確認を求める呼び出しの ctx は MCP のつながりのもので、利用者を持たないため。
func (cs *Conversations) askFor(sendCtx context.Context, conv Conversation, c format.ToolCall, t tool.Tool, args tool.Args, emit func(ChatEvent), asked **Confirmation, waited *time.Duration) tool.Ask {
	return func(ctx context.Context, pv tool.Preview) (tool.Answer, error) {
		set := conv.Settings
		if fresh, _, err := cs.load(sendCtx, conv.ID); err == nil {
			set = fresh.Settings
		}
		if cs.toolPolicy(set, t) == ToolAllow {
			log.Noticef("ツール %s: サーバーからの確認を、「確認しない」の扱いなので聞かずに受け入れます", t.Def().Name)
			return tool.Answer{Approved: true}, nil
		}
		begin := time.Now()
		got, err := cs.confirm(ctx, conv.ID, c, fixedPreview{Tool: t, pv: pv}, args, emit)
		*waited += time.Since(begin)
		if err != nil {
			return tool.Answer{}, err
		}
		*asked = got
		return tool.Answer{Approved: got.Approved, Reason: got.Reason}, nil
	}
}

// fixedPreview は、確認の内容が先に決まっているもの(サーバーが送ってきた差分など)を、確認のカードに出すための入れ物。
type fixedPreview struct {
	tool.Tool
	pv tool.Preview
}

func (f fixedPreview) Preview(tool.Args) (tool.Preview, error) { return f.pv, nil }

// lookupTool は呼び出しを確かめて、実行するツールと引数を返す。
func (cs *Conversations) lookupTool(set ConversationSettings, c format.ToolCall) (tool.Tool, tool.Args, error) {
	if c.Error != "" {
		return nil, nil, errors.New(c.Error) // 書式が崩れていて読み取れなかった
	}
	tools := cs.toolsFor(set)
	t := tool.Find(tools, c.Name)
	if t == nil {
		return nil, nil, fmt.Errorf("ツール %s はありません%s(使えるツール: %s)", c.Name, didYouMean(tools, c.Name), tool.Names(tools))
	}
	args, err := tool.CheckArgs(t.Def(), c.Args)
	if err != nil {
		return nil, nil, err
	}
	return t, args, nil
}

// needsConfirm は、ツールを確認してから実行するか(扱いが「確認する」なら確認する)。
// 「確認しない」は、設定の画面の「ツール」(PC の既定)と会話の設定の画面の「ツール」(この会話だけ)で選ぶ(0010)。
// ふだんは確認するツール(書き換えるツール・コマンド・MCP のツール)を聞かずに実行するときは、ログに出す。
func (cs *Conversations) needsConfirm(set ConversationSettings, t tool.Tool) bool {
	if _, ok := t.(tool.SelfConfirming); ok {
		// 確認は、実行の途中でツール(aibow の中の自作の MCP サーバー)が差分を送ってきたときに1回だけ聞く(askFor。聞くかは扱いで決める)
		return false
	}
	if cs.toolPolicy(set, t) != ToolAllow {
		return true
	}
	_, confirmable := t.(tool.Confirmable)
	if confirmable {
		log.Noticef("ツール %s は「確認しない」の扱いなので、聞かずに実行します", t.Def().Name)
	}
	return false
}

// argsPreview は、確認の内容を作れないツール(読むだけのツール)を確認するときに、引数を見せる。
type argsPreview struct{ tool.Tool }

func (a argsPreview) Preview(args tool.Args) (tool.Preview, error) {
	b, err := json.MarshalIndent(args, "", "  ")
	if err != nil {
		return tool.Preview{}, err
	}
	return tool.Preview{Note: "確認", Args: string(b)}, nil
}

// didYouMean は、知らない名前で呼ばれたとき、MCP のツールのうち「__名前」で終わるものを「〜のことですか?」として返す。
//
// MCP のツールの名前にはサーバーの名前が前に付く(filesystem__list_directory)が、モデルは前の部分を落として書くことがある
// (名前空間を付ける前の会話の履歴をまねしたときなど)。勝手に読み替えず(取り違えが見えなくなるため)、正しい名前を教えて、モデルに呼び直させる。
func didYouMean(tools []tool.Tool, name string) string {
	var cands []string
	for _, t := range tools {
		if n := t.Def().Name; strings.HasSuffix(n, "__"+name) {
			cands = append(cands, n)
		}
	}
	if len(cands) == 0 {
		return ""
	}
	return "。" + strings.Join(cands, " か ") + " のことですか?"
}

// confirm は、実行してよいかをユーザーに確かめ、答えを待つ。
//
// 確認の内容(差分など)をツールに作らせ、confirm イベントで画面に送り、Confirm で答えが届くまで止まって待つ。
// 「ループを途中で止めて、ユーザーの返事を待つ」のは、チャネルで待つだけでできる。
// 待つ間は時間の上限に数えない(ユーザーが読む時間なので)。停止ボタン(ctx)では止まる。
// 確認の内容を作れないとき(置き換える場所が見つからない、など)は、聞かずにエラーを返す。
func (cs *Conversations) confirm(ctx context.Context, conv uuid.UUID, c format.ToolCall, t tool.Confirmable, args tool.Args, emit func(ChatEvent)) (*Confirmation, error) {
	pv, err := t.Preview(args)
	if err != nil {
		return nil, err
	}
	req := ConfirmRequest{ID: uuid.NewV7().String(), Call: c, Preview: pv}
	p := &pendingConfirm{conv: conv, id: req.ID, tool: c.Name, req: req, answer: make(chan Confirmation, 1)}
	cs.confirmMu.Lock()
	cs.pending = p
	cs.confirmMu.Unlock()
	defer func() {
		cs.confirmMu.Lock()
		if cs.pending == p {
			cs.pending = nil
		}
		cs.confirmMu.Unlock()
	}()

	emit(ChatEvent{ConversationID: conv, Type: "confirm", Confirm: &req})
	select {
	case a := <-p.answer:
		return &a, nil
	case <-ctx.Done():
		return nil, errors.New("停止されたため実行していません")
	}
}

// Pending は、会話 conv で答えを待っている確認(待っていなければ nil)。
// 画面は confirm イベントでカードを出すが、ブラウザでは知らせ(SSE)が切れている間のイベントが届かない。
// つなぎ直したときにこれで取り直し、カードを出し直す(0024 の決定事項 15)。
// ほかの人の会話の確認は見せない(会話が ctx の利用者のものかを、保存先で確かめる)。
func (cs *Conversations) Pending(ctx context.Context, conv uuid.UUID) (*ConfirmRequest, error) {
	if _, _, err := cs.store.Conversation(ctx, conv); err != nil {
		return nil, err
	}
	cs.confirmMu.Lock()
	defer cs.confirmMu.Unlock()
	if p := cs.pending; p != nil && p.conv == conv {
		req := p.req
		return &req, nil
	}
	return nil, nil
}

// Confirm は、確認を待っているツールに答える(画面の「実行する」「拒否」)。この1回だけの答え。
// 待っている確認が無い・ID が違う(停止した後に押した、など)ときはエラー。
func (cs *Conversations) Confirm(ctx context.Context, conv uuid.UUID, id string, approved bool, reason string) error {
	return cs.Answer(ctx, conv, id, Confirmation{Approved: approved, Reason: reason})
}

// Answer は、確認を待っているツールに答える。a.Remember があれば、そのツールのこの会話でのこれから先の扱いにする(0010)。
// 扱いは答えを渡す前に保存する(返ったときには保存済みなので、画面はすぐ会話の設定を読み直せる)。保存できなければ答えずにエラーを返す。
func (cs *Conversations) Answer(ctx context.Context, conv uuid.UUID, id string, a Confirmation) error {
	if a.Remember != "" && !a.Remember.validInConversation() {
		return fmt.Errorf("これから先の扱い %q は知らない値です", a.Remember)
	}
	cs.confirmMu.Lock()
	defer cs.confirmMu.Unlock()
	p := cs.pending
	if p == nil || p.conv != conv || p.id != id {
		return errors.New("この確認はもう待っていません(停止したか、すでに答えています)")
	}
	if a.Remember != "" {
		if err := cs.SetToolPolicy(ctx, conv, p.tool, a.Remember); err != nil {
			return fmt.Errorf("ツール %s のこれから先の扱いを保存できません: %w", p.tool, err)
		}
		log.Noticef("ツール %s のこの会話での扱いを %s にしました(確認のカードから)", p.tool, a.Remember)
	}
	cs.pending = nil // 2回押しても、答えは1回だけ届ける
	a.Reason = strings.TrimSpace(a.Reason)
	p.answer <- a
	return nil
}

// SetToolPolicy は、会話のツールの扱いの上書きを1つだけ変えて保存する(policy が空なら上書きを消す)。
// 会話の設定を丸ごと書き戻さないので、画面が持っている設定と関係なく、すぐ効く(確認のカードから使う。0010)。
func (cs *Conversations) SetToolPolicy(ctx context.Context, id uuid.UUID, name string, policy ToolPolicy) error {
	if policy != "" && !policy.validInConversation() {
		return fmt.Errorf("ツール %s の扱い %q は知らない値です", name, policy)
	}
	c, _, err := cs.store.Conversation(ctx, id)
	if err != nil {
		return err
	}
	// プロファイルで使えないツールは、会話の上書きを書けない(確認のカードの「この会話では今後聞かない」なども)
	p, err := cs.profileFor(ctx, c.ProfileID)
	if err != nil {
		return err
	}
	if g, ok := cs.groupOfTool(name); policy != "" && p != nil && (slices.Contains(p.Rules.Off.Tools, name) || ok && p.Rules.offGroup(g)) {
		return errOff(p, "ツール "+name)
	}
	set := c.Settings.normalized()
	set.ToolPolicies = maps.Clone(set.ToolPolicies)
	if policy == "" {
		delete(set.ToolPolicies, name)
	} else {
		if set.ToolPolicies == nil {
			set.ToolPolicies = map[string]ToolPolicy{}
		}
		set.ToolPolicies[name] = policy
	}
	c.Settings = set
	return cs.store.UpdateConversation(ctx, c)
}

// execute はツールを実行する。時間の上限を付け、ツールが ctx を見ずに止まらなくても、待つのは上限まで。
func (cs *Conversations) execute(ctx context.Context, t tool.Tool, args tool.Args) (string, error) {
	limit, grace := toolTimeout, time.Duration(0)
	if l, ok := t.(tool.TimeLimiter); ok {
		// 上限を自分で決めるツールは、ctx が止まったら後片付けをして返ってくる約束(run_command は子プロセスを止めてから、ここまでの出力を返す)。
		// その結果を受け取れるよう、少し待つ。ほかのツールは今までどおり、上限で待つのをやめる
		limit, grace = l.TimeLimit(), stopGrace
	}
	tctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	type result struct {
		text string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		text, err := t.Run(tctx, args)
		done <- result{text, err}
	}()
	select {
	case r := <-done:
		return r.text, r.err
	case <-tctx.Done():
		if grace > 0 {
			select {
			case r := <-done:
				return r.text, r.err
			case <-time.After(grace):
			}
		}
		if ctx.Err() != nil {
			return "", errors.New("停止されました")
		}
		return "", fmt.Errorf("時間の上限(%d 秒)を超えたため止めました", int(limit.Seconds()))
	}
}

// fitResult は、結果を足すと今の組が会話の上限を超えるとき、結果の代わりに「長すぎて渡せない」エラーの結果にする。
//
// 結果をそのまま保存すると、次の生成の前の checkFits で止まり、会話は上限を超えたまま残ってしまう
// (最新の組は要約しないので、次の質問を送るまで「今すぐ要約」もできない)。
// 代わりにエラーとして返せば、モデルは範囲を絞って呼び直せる(失敗しても送信は止めない、と同じ考え方)。
// ツールは実行済みなので、結果を捨てるだけ。
func (cs *Conversations) fitResult(set ConversationSettings, history []StoredMessage, res StoredMessage) (StoredMessage, error) {
	limit := cs.limitsOf(set).history
	with, err := cs.turnTokens(set, append(slices.Clip(history), res))
	if err != nil || with <= limit {
		return res, err
	}
	without, err := cs.turnTokens(set, history)
	if err != nil {
		return res, err
	}
	res.Content = fmt.Sprintf("%s結果が長すぎて会話の上限に入らないため、渡せません(結果は約 %d トークン、残りは約 %d トークン)。"+
		"範囲を絞って呼び直してください(list_files なら path で下のフォルダを、read_file なら offset と limit で行を指定する)",
		errorPrefix, with-without, max(0, limit-without))
	res.ToolResult.Error = true
	return res, nil
}

// toolMessage はツールの結果の発言を作る。
func toolMessage(conv uuid.UUID, c format.ToolCall, text string, isErr bool, sec float64) StoredMessage {
	return StoredMessage{
		ID: uuid.NewV7(), ConversationID: conv, Role: "tool", Content: text,
		ToolResult: &ToolResult{Call: c, Error: isErr, Sec: sec},
		CreatedAt:  time.Now(),
	}
}

// toolsFor は会話で使えるツール。使うかどうかは toolPolicy で決める(設定の画面の「ツール」と、会話の上書き)。
// 組み込みのツールの後ろに、起動済みの MCP のサーバーのツールを並べる。
func (cs *Conversations) toolsFor(set ConversationSettings) []tool.Tool {
	// 「使わない」にしたツールはモデルに見せない(説明もプロンプトに入らない)
	// 組み込みのツールは、この会話で外していなければ(入力欄の上の「この会話のツール」)
	var tools []tool.Tool
	for _, t := range cs.localTools(set) {
		if !set.BuiltinOff && cs.toolPolicy(set, t) != ToolOff {
			tools = append(tools, t)
		}
	}
	if cs.remote == nil {
		return tools
	}
	for _, t := range cs.remote.Tools(set.WorkDir, cs.usableServers(set)) {
		if cs.toolPolicy(set, t) == ToolOff {
			continue
		}
		// MCP のツールの名前にはサーバーの名前が前に付いている(filesystem__read_file。mcp/client の ToolName)ので、ふつうは重ならない。
		// それでも重なったら出さない(先にあるほう = 組み込みのツールを使う)。同じ名前が2つあると、モデルも aibow も区別できないため。
		// 重なったものは、起動したときにログに出す(logRemoteTools)
		if tool.Find(tools, t.Def().Name) == nil {
			tools = append(tools, t)
		}
	}
	return tools
}

// toolPolicy は、会話でのツールの扱い(使わない / 確認する / 確認しない)を決める。扱いを決めるのはここだけ。
//
//  0. プロファイルで使えない(0014 の 5。会話のプロファイルの決まり)。会話の上書きでも使えるようにはできない
//  1. 会話ごとの上書き(会話の設定の tool_policies)。「使う」(on)は使うかだけを決め、確認するかは 2・3 で決める
//  2. PC の既定(tools.json)。グループ(組み込み / MCP のサーバー)ごと「使わない」なら使わない。
//     そうでなければ、ツールごと → グループごとの順(ToolPrefs.policy)
//  3. 決まりの既定: MCP のツールは確認する(中身が見えないので)。
//     組み込みのツールは、書き換えるツール・コマンド(tool.Confirmable)は確認する、読むだけのツールは確認しない(今までの動き)
//
// の順に見て、最初に決めてあったものを使う。
func (cs *Conversations) toolPolicy(set ConversationSettings, t tool.Tool) ToolPolicy {
	group := "" // 組み込みのツール
	if r, ok := t.(tool.Remote); ok {
		group = r.Server()
	}
	_, confirmable := t.(tool.Confirmable)
	p, _ := cs.policyOf(set, group, t.Def().Name, confirmable)
	return p
}

// どこでツールの扱いが決まったか(会話の設定の画面に出す。ToolState.From)
const (
	PolicyFromProfile      = "profile"      // プロファイルの決まり(使えない。0014)
	PolicyFromConversation = "conversation" // 会話ごとの上書き(tool_policies)
	PolicyFromPC           = "pc"           // PC の既定(tools.json)
	PolicyFromDefault      = "default"      // 決まりの既定
)

// policyOf は toolPolicy の中身。ツールの実体が無くても(起動していない MCP のサーバーのツールでも)決められるよう、
// グループ・名前・書き換えるツールか(tool.Confirmable)で受け取り、どこで決まったかも返す。
//
// 返す扱いは off / ask / allow のどれか(on は返さない。使うことにしたうえで、確認するかを返す)。
func (cs *Conversations) policyOf(set ConversationSettings, group, name string, confirmable bool) (ToolPolicy, string) {
	if set.profile != nil && set.profile.Rules.offTool(group, name) {
		return ToolOff, PolicyFromProfile
	}
	p, ok := set.ToolPolicies[name]
	if ok && p != ToolOn {
		return p, PolicyFromConversation
	}
	if ok {
		// この会話では使う。PC の既定の「使わない」は「既定では使わない」の意味なので打ち消し、確認するかだけを PC の既定から取る
		if c, cok := cs.prefs.confirmPolicy(group, name); cok {
			return c, PolicyFromConversation
		}
		return defaultPolicy(group, confirmable), PolicyFromConversation
	}
	if p, ok := cs.prefs.policy(group, name); ok {
		return p, PolicyFromPC
	}
	return defaultPolicy(group, confirmable), PolicyFromDefault
}

// defaultPolicy は決まりの既定: MCP のツールは確認する。組み込みのツールは、書き換えるツール・コマンドは確認する、読むだけのツールは確認しない。
func defaultPolicy(group string, confirmable bool) ToolPolicy {
	if !confirmable && group == "" {
		return ToolAllow
	}
	return ToolAsk
}

// ToolGroupState は、会話の設定の画面に出すツールのグループ1つ(組み込み / MCP のサーバー1つ)。
type ToolGroupState struct {
	Server string      `json:"server"` // MCP のサーバーの名前。組み込みのツールなら空
	Tools  []ToolState `json:"tools"`  // MCP のサーバーは、最後に起動したときに見えたツール(tools.json の known)。まだ無ければ空
}

// ToolState は、会話でのツール1つの扱い。
type ToolState struct {
	Name         string     `json:"name"`           // モデルに見せる名前
	Description  string     `json:"description"`    // 説明の1行目
	Policy       ToolPolicy `json:"policy"`         // 使わない / 確認する / 確認しない(toolPolicy と同じ決め方)
	From         string     `json:"from"`           // どこで決まったか(PolicyFrom…)
	Default      ToolPolicy `json:"default"`        // 会話の上書きが無いときの扱い(PC の既定か決まりの既定)。画面は、これと同じにするなら上書きを消す
	NeedsWorkDir bool       `json:"needs_work_dir"` // 作業フォルダを決めた会話でだけ使える(組み込みのファイル・コマンドのツール)
}

// ToolStates は、会話の設定 set でのツールの扱いを、グループごとに返す(会話の設定の画面に出す)。
// 会話で使うグループを選んでいるか(builtin_off / mcp_servers)は set を見れば分かるので、ここでは見ない。
// 扱いは toolPolicy と同じ policyOf で決める(扱いを決める場所を1つにするため。画面で同じ判断を書き直さない)。
func (cs *Conversations) ToolStates(set ConversationSettings) []ToolGroupState {
	builtin := ToolGroupState{Tools: []ToolState{}}
	add := func(t tool.Tool, needsWorkDir bool) {
		_, confirmable := t.(tool.Confirmable)
		p, from := cs.policyOf(set, "", t.Def().Name, confirmable)
		def, _ := cs.policyOf(ConversationSettings{profile: set.profile}, "", t.Def().Name, confirmable)
		builtin.Tools = append(builtin.Tools, ToolState{
			Name: t.Def().Name, Description: firstLine(t.Def().Description), Policy: p, From: from, Default: def, NeedsWorkDir: needsWorkDir,
		})
	}
	for _, t := range tool.Builtin() {
		add(t, false)
	}
	// 作業フォルダのツールは、定義が作業フォルダによらないので仮のフォルダで作る(BuiltinTools と同じ)
	for _, t := range append(tool.Files("."), tool.RunCommand(".")) {
		add(t, true)
	}
	groups := []ToolGroupState{builtin}
	if cs.remote == nil {
		return groups
	}
	known := cs.prefs.View().Known
	for _, server := range cs.remote.Servers() {
		g := ToolGroupState{Server: server, Tools: []ToolState{}}
		for _, k := range known[server] {
			p, from := cs.policyOf(set, server, k.Name, false)
			def, _ := cs.policyOf(ConversationSettings{profile: set.profile}, server, k.Name, false)
			g.Tools = append(g.Tools, ToolState{Name: k.Name, Description: k.Description, Policy: p, From: from, Default: def})
		}
		groups = append(groups, g)
	}
	return groups
}

// ConversationToolStates は、会話 id のプロファイルの決まりを当てて、設定 set でのツールの扱いを返す(ToolStates)。
// id が空なら、新しい会話(新しい会話で最初に選ばれるプロファイル)。画面から来た set は決まりを持たないので、ここで足す。
func (cs *Conversations) ConversationToolStates(ctx context.Context, id uuid.UUID, set ConversationSettings) ([]ToolGroupState, error) {
	var p *Profile
	if id == (uuid.UUID{}) {
		d, err := cs.DefaultProfile(ctx)
		if err != nil {
			return nil, err
		}
		if d.ID != (uuid.UUID{}) {
			p = &d
		}
	} else {
		c, _, err := cs.store.Conversation(ctx, id)
		if err != nil {
			return nil, err
		}
		if p, err = cs.profileFor(ctx, c.ProfileID); err != nil {
			return nil, err
		}
	}
	set.profile = p
	return cs.ToolStates(cs.forUse(set)), nil
}

// usableServers は、会話で選んだ MCP のサーバーのうち、プロファイルで使えないものでも、PC の既定でサーバーごと「使わない」にしたものでもないもの。
// 使わないサーバーは、選んであっても起動しない。
func (cs *Conversations) usableServers(set ConversationSettings) []string {
	var out []string
	for _, name := range set.MCPServers {
		if set.profile != nil && set.profile.Rules.offGroup(name) {
			continue
		}
		if p, ok := cs.prefs.groupPolicy(name); !ok || p != ToolOff {
			out = append(out, name)
		}
	}
	return out
}

// LocalTools は、aibow の中で動くツール(組み込みと作業フォルダのツール)を渡す係(0007 の 5-4)。
//
// 会話の仕組みは、ツールが tool パッケージを直接呼ぶものか、aibow の中の自作の MCP サーバー越しに呼ぶものかを知らない。
// 名前・グループ(組み込み)・扱いの決まりの既定は、どちらでも同じ。
type LocalTools interface {
	// Tools は、作業フォルダ workDir の会話で使う、aibow の中のツール(workDir が空なら、作業フォルダを使わないツールだけ)
	Tools(workDir string) []tool.Tool
}

// SetLocalTools は、aibow の中で動くツールを渡す係を決める(アプリの起動時に1回)。決めなければ tool パッケージのツールを直接呼ぶ。
func (cs *Conversations) SetLocalTools(l LocalTools) {
	cs.local = l
}

// localTools は、aibow の中で動くツール(組み込みと、作業フォルダのツール)。使うかどうかは見ない(toolsFor が見る)。
// 作業フォルダを決めた会話だけ、そのフォルダの中を扱うツールを足す(作業フォルダは会話ごとなので、ここで作る)。
func (cs *Conversations) localTools(set ConversationSettings) []tool.Tool {
	if cs.local != nil {
		return cs.local.Tools(set.WorkDir)
	}
	if set.WorkDir == "" {
		return cs.tools
	}
	return append(append(slices.Clip(cs.tools), tool.Files(set.WorkDir)...), tool.RunCommand(set.WorkDir))
}

// RemoteTools は、aibow の外のプログラムが実行するツールの置き場(MCP のサーバー。本物は mcp/client の Manager)。
// 会話の仕組みは MCP を知らない。ツールが tool.Tool の形で並んでいれば、エージェントループはそのまま使える。
type RemoteTools interface {
	// Servers は settings.json に書いたサーバーの名前(名前の順)
	Servers() []string
	// Start は、作業フォルダ workDir の会話で使うサーバー(use)のうち、動いていないものを起動する。
	// 起動したサーバーの名前と、起動できなかったサーバーのエラーを返す
	Start(ctx context.Context, workDir string, use []string) (started []string, errs []error)
	// Tools は、会話で使うサーバー(use)の、起動済みのツール(起動はしない。メーターの計算などからも呼ばれるため)
	Tools(workDir string, use []string) []tool.Tool
	// ListTools は、サーバーを一時的に起動してツールの一覧を受け取る(設定の画面で、会話で使う前にツールを選べるように)
	ListTools(ctx context.Context, server string) ([]tool.Tool, error)
}

// LoadToolList は、サーバーを一時的に起動してツールの一覧を受け取り、PC の既定(tools.json)に覚える。
func (cs *Conversations) LoadToolList(ctx context.Context, server string) error {
	if cs.remote == nil {
		return errors.New("MCP のサーバーがありません(settings.json の mcpServers)")
	}
	tools, err := cs.remote.ListTools(ctx, server)
	if err != nil {
		return err
	}
	return cs.prefs.setKnown(server, knownTools(tools))
}

// BuiltinTools は、組み込みのツール(aibow の中で動くツール)の一覧(設定の画面に出す)。
// 作業フォルダのツールも入れる(作業フォルダを決めた会話でだけ使える)。定義は作業フォルダによらないので、仮のフォルダで作る。
func BuiltinTools() []KnownTool {
	tools := append(tool.Builtin(), tool.Files(".")...)
	return knownTools(append(tools, tool.RunCommand(".")))
}

// knownTools は、設定の画面に出すツールの一覧(名前と説明の1行目)。
func knownTools(tools []tool.Tool) []KnownTool {
	known := make([]KnownTool, 0, len(tools))
	for _, t := range tools {
		known = append(known, KnownTool{Name: t.Def().Name, Description: firstLine(t.Def().Description)})
	}
	return known
}

// SetToolPrefs は、ツールの扱いの PC の既定(tools.json)を使えるようにする(アプリの起動時に1回)。
func (cs *Conversations) SetToolPrefs(p *ToolPrefs) {
	cs.prefs = p
}

// SetRemoteTools は MCP のサーバーのツールを使えるようにする(アプリの起動時に1回)。
func (cs *Conversations) SetRemoteTools(r RemoteTools) {
	cs.remote = r
}

// startRemote は、会話で使う MCP のサーバーを、送信の前に起動する。
// 起動できなかったサーバーは、お知らせ(トースト)に出して、そのサーバーのツール無しで続ける。
func (cs *Conversations) startRemote(ctx context.Context, conv Conversation, emit func(ChatEvent)) {
	set := conv.Settings
	if cs.remote == nil {
		return
	}
	started, errs := cs.remote.Start(ctx, set.WorkDir, cs.usableServers(set))
	for _, err := range errs {
		log.Warn(err.Error())
		emit(ChatEvent{ConversationID: conv.ID, Type: "notice", Text: err.Error() + "(このサーバーのツールは使わずに続けます)"})
	}
	if len(started) > 0 {
		cs.rememberTools(set, started)
		cs.logRemoteTools(set)
	}
}

// rememberTools は、起動したサーバーのツールの一覧を PC の既定(tools.json)に覚える。
// 設定の画面で、サーバーを起動していなくてもツールの一覧を出して、使う / 使わないを決められるようにするため。
func (cs *Conversations) rememberTools(set ConversationSettings, started []string) {
	for _, server := range started {
		if err := cs.prefs.setKnown(server, knownTools(cs.remote.Tools(set.WorkDir, []string{server}))); err != nil {
			log.Warnf("MCP のサーバー %s のツールの一覧を覚えられません: %v", server, err)
		}
	}
}

// logRemoteTools は、MCP のツールの数と、その定義がプロンプトをどれだけ長くするかをログに出す(学習用)。
// ツールの定義はシステムプロンプトに毎回入るので、会話の上限を使う。既製のサーバーはツールが多く、説明も長い。
func (cs *Conversations) logRemoteTools(set ConversationSettings) {
	local, all := cs.localTools(set), cs.toolsFor(set)
	var skipped []string
	for _, t := range cs.remote.Tools(set.WorkDir, cs.usableServers(set)) {
		if tool.Find(local, t.Def().Name) != nil {
			skipped = append(skipped, t.Def().Name)
		}
	}
	before, err1 := cs.gen.CountTokens(format.Prompt{Tools: tool.Defs(local)})
	after, err2 := cs.gen.CountTokens(format.Prompt{Tools: tool.Defs(all)})
	if err := errors.Join(err1, err2); err != nil {
		log.Warnf("ツールの定義のトークン数を数えられません: %v", err)
		return
	}
	log.Infof("ツール: 組み込み %d 個 + MCP %d 個。MCP のツールの定義で、プロンプトが約 %d トークン長くなります(ツールの定義の合計は約 %d トークン)",
		len(local), len(all)-len(local), after-before, after)
	if len(skipped) > 0 {
		log.Warnf("MCP のツールのうち、組み込みのツールと名前が重なる %s は出しません", strings.Join(skipped, ", "))
	}
}
