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
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"uuid"

	"aibow/tool"
)

const (
	// maxToolCalls は1回の送信でツールを実行する回数の上限。モデルが同じ呼び出しを繰り返して終わらないのを防ぐ。
	// コーディング(読む → 直す → 動かす)は回数がかさむので 20 回(0004 では 8 回だった。停止ボタンでも止められる)
	maxToolCalls = 20
)

// toolTimeout はツール1回の実行の時間の上限(テストで短くするので変数にしている)
var toolTimeout = 10 * time.Second

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
	var conf *Confirmation
	if ct, ok := t.(tool.Confirmable); ok {
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
		return nil, nil, fmt.Errorf("ツール %s はありません(使えるツール: %s)", c.Name, tool.Names(tools))
	}
	args, err := tool.CheckArgs(t.Def(), c.Args)
	if err != nil {
		return nil, nil, err
	}
	return t, args, nil
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
	tctx, cancel := context.WithTimeout(ctx, toolTimeout)
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
		if ctx.Err() != nil {
			return "", errors.New("停止されました")
		}
		return "", fmt.Errorf("時間の上限(%d 秒)を超えたため止めました", int(toolTimeout.Seconds()))
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

// toolsFor は会話の設定で使えるツール。「ツールを使う」が OFF なら無し。
// 作業フォルダを決めた会話だけ、そのフォルダの中を扱うツールを足す(作業フォルダは会話ごとなので、ここで作る)。
func (cs *Conversations) toolsFor(set ConversationSettings) []tool.Tool {
	if !set.Tools {
		return nil
	}
	if set.WorkDir == "" {
		return cs.tools
	}
	return append(slices.Clip(cs.tools), tool.Files(set.WorkDir)...)
}
