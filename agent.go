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
	"slices"
	"strings"
	"time"
	"uuid"

	"github.com/secondarykey/aibow/log"
	"github.com/secondarykey/aibow/tool"
)

const (
	// maxToolCalls は1回の送信でツールを実行する回数の上限。モデルが同じ呼び出しを繰り返して終わらないのを防ぐ。
	// コーディング(読む → 直す → 動かす)は回数がかさむので 20 回(0004 では 8 回だった。停止ボタンでも止められる)
	maxToolCalls = 20
)

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
	calls := 0 // この送信でツールを実行した回数
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
		cs.emitUsage(emit, conv.ID, p.usage(history, cs.currentLimits().history))

		// 2. 生成して保存する
		r, err := cs.generate(ctx, conv, p.messages, emit)
		if err != nil || r == nil {
			return last, err // r == nil: 何も生成されなかった(すぐに停止した、など)
		}
		if err := cs.save(*r, ""); err != nil {
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
			if err := cs.save(res, ""); err != nil {
				return last, err
			}
			history = append(history, res)
			emit(ChatEvent{ConversationID: conv.ID, Type: "tool_result", Message: &res})
		}

		// 5. 止められた・上限に達したら、もう生成しない。そうでなければ結果を渡して続きを書かせる
		if ctx.Err() != nil {
			return last, nil
		}
		if overLimit {
			return last, fmt.Errorf("1回の送信で実行できるツール(%d 回)を超えたため、止めました。続けるときは、もう一度送信してください", maxToolCalls)
		}
	}

	// 返事が増えたあとのメーター。次の送信では、この返事もモデルに渡る
	if u, err := cs.usage(conv, history); err == nil {
		cs.emitUsage(emit, conv.ID, u)
	}
	return last, nil
}

// runTool はツールを1つ実行して、結果の発言(role tool)を作る。失敗もエラーの結果として返す。
//
// 書き換えるツール(tool.Confirmable)は、実行する前にユーザーの確認を待つ。
// 拒否されたら実行せず、「ユーザーが実行を拒否しました。理由: …」を結果としてモデルに返す(ループは続ける。
// 失敗しても送信は止めない、と同じ考え方で、モデルに別のやり方を考えさせる)。
func (cs *Conversations) runTool(ctx context.Context, conv Conversation, c ToolCall, emit func(ChatEvent)) StoredMessage {
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
func (cs *Conversations) runFound(ctx context.Context, conv Conversation, c ToolCall, t tool.Tool, args tool.Args, emit func(ChatEvent)) StoredMessage {
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
			res := toolMessage(conv.ID, c, rejected(conf.Reason), false, 0)
			res.ToolResult.Confirm = conf
			return res
		}
	}

	// 時間を測るのは実行だけ(確認を待った時間は入れない)
	start := time.Now()
	text, err := cs.execute(ctx, t, args)
	sec := time.Since(start).Seconds()
	res := toolMessage(conv.ID, c, text, false, sec)
	if err != nil {
		res = toolMessage(conv.ID, c, errorPrefix+err.Error(), true, sec)
	}
	res.ToolResult.Confirm = conf
	return res
}

// lookupTool は呼び出しを確かめて、実行するツールと引数を返す。
func (cs *Conversations) lookupTool(set ConversationSettings, c ToolCall) (tool.Tool, tool.Args, error) {
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
// 「確認しない」は今は画面から選べない(tools.json か会話の設定を手で書いたときだけ)。
// ふだんは確認するツール(書き換えるツール・コマンド・MCP のツール)を聞かずに実行するときは、ログに出す。
func (cs *Conversations) needsConfirm(set ConversationSettings, t tool.Tool) bool {
	if cs.toolPolicy(set, t) != ToolAllow {
		return true
	}
	_, confirmable := t.(tool.Confirmable)
	if confirmable {
		log.Infof("ツール %s は「確認しない」の扱いなので、聞かずに実行します", t.Def().Name)
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
func (cs *Conversations) confirm(ctx context.Context, conv uuid.UUID, c ToolCall, t tool.Confirmable, args tool.Args, emit func(ChatEvent)) (*Confirmation, error) {
	pv, err := t.Preview(args)
	if err != nil {
		return nil, err
	}
	req := ConfirmRequest{ID: uuid.NewV7().String(), Call: c, Preview: pv}
	p := &pendingConfirm{conv: conv, id: req.ID, answer: make(chan Confirmation, 1)}
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

// Confirm は、確認を待っているツールに答える(画面の「実行する」「拒否」)。
// 待っている確認が無い・ID が違う(停止した後に押した、など)ときはエラー。
func (cs *Conversations) Confirm(conv uuid.UUID, id string, approved bool, reason string) error {
	cs.confirmMu.Lock()
	defer cs.confirmMu.Unlock()
	p := cs.pending
	if p == nil || p.conv != conv || p.id != id {
		return errors.New("この確認はもう待っていません(停止したか、すでに答えています)")
	}
	cs.pending = nil // 2回押しても、答えは1回だけ届ける
	p.answer <- Confirmation{Approved: approved, Reason: strings.TrimSpace(reason)}
	return nil
}

// rejected は、拒否されたときにモデルに返す結果の文。
func rejected(reason string) string {
	if reason == "" {
		reason = "(書かれていません)"
	}
	return "ユーザーが実行を拒否しました。理由: " + reason
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
	limit := cs.currentLimits().history
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
func toolMessage(conv uuid.UUID, c ToolCall, text string, isErr bool, sec float64) StoredMessage {
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
//  1. 会話ごとの上書き(会話の設定の tool_policies)
//  2. PC の既定(tools.json)。グループ(組み込み / MCP のサーバー)ごと「使わない」なら使わない。
//     そうでなければ、ツールごと → グループごとの順(ToolPrefs.policy)
//  3. 決まりの既定: MCP のツールは確認する(中身が見えないので)。
//     組み込みのツールは、書き換えるツール・コマンド(tool.Confirmable)は確認する、読むだけのツールは確認しない(今までの動き)
//
// の順に見て、最初に決めてあったものを使う。
func (cs *Conversations) toolPolicy(set ConversationSettings, t tool.Tool) ToolPolicy {
	name := t.Def().Name
	if p, ok := set.ToolPolicies[name]; ok {
		return p
	}
	group := "" // 組み込みのツール
	if r, ok := t.(tool.Remote); ok {
		group = r.Server()
	}
	if p, ok := cs.prefs.policy(group, name); ok {
		return p
	}
	if _, ok := t.(tool.Confirmable); !ok && group == "" {
		return ToolAllow
	}
	return ToolAsk
}

// usableServers は、会話で選んだ MCP のサーバーのうち、PC の既定でサーバーごと「使わない」にしていないもの。
// 使わないサーバーは、選んであっても起動しない。
func (cs *Conversations) usableServers(set ConversationSettings) []string {
	var out []string
	for _, name := range set.MCPServers {
		if p, ok := cs.prefs.groupPolicy(name); !ok || p != ToolOff {
			out = append(out, name)
		}
	}
	return out
}

// localTools は、aibow の中で動くツール(組み込みと、作業フォルダのツール)。使うかどうかは見ない(toolsFor が見る)。
// 作業フォルダを決めた会話だけ、そのフォルダの中を扱うツールを足す(作業フォルダは会話ごとなので、ここで作る)。
func (cs *Conversations) localTools(set ConversationSettings) []tool.Tool {
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
	before, err1 := cs.gen.CountTokens(Prompt{Tools: tool.Defs(local)})
	after, err2 := cs.gen.CountTokens(Prompt{Tools: tool.Defs(all)})
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
