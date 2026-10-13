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
	"time"
	"uuid"

	"aibow/tool"
)

const (
	// maxToolCalls は1回の送信でツールを実行する回数の上限。モデルが同じ呼び出しを繰り返して終わらないのを防ぐ
	maxToolCalls = 8
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
				res = cs.runTool(ctx, conv, c)
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
func (cs *Conversations) runTool(ctx context.Context, conv Conversation, c ToolCall) StoredMessage {
	start := time.Now()
	text, err := cs.callTool(ctx, conv.Settings, c)
	sec := time.Since(start).Seconds()
	if err != nil {
		return toolMessage(conv.ID, c, errorPrefix+err.Error(), true, sec)
	}
	return toolMessage(conv.ID, c, text, false, sec)
}

// callTool は呼び出しを確かめて、ツールを実行する。
func (cs *Conversations) callTool(ctx context.Context, set ConversationSettings, c ToolCall) (string, error) {
	if c.Error != "" {
		return "", errors.New(c.Error) // 書式が崩れていて読み取れなかった
	}
	tools := cs.toolsFor(set)
	t := tool.Find(tools, c.Name)
	if t == nil {
		return "", fmt.Errorf("ツール %s はありません(使えるツール: %s)", c.Name, tool.Names(tools))
	}
	args, err := tool.CheckArgs(t.Def(), c.Args)
	if err != nil {
		return "", err
	}

	// 時間の上限を付けて実行する。ツールが ctx を見ずに止まらなくても、待つのは上限まで
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

// toolMessage はツールの結果の発言を作る。
func toolMessage(conv uuid.UUID, c ToolCall, text string, isErr bool, sec float64) StoredMessage {
	return StoredMessage{
		ID: uuid.NewV7(), ConversationID: conv, Role: "tool", Content: text,
		ToolResult: &ToolResult{Call: c, Error: isErr, Sec: sec},
		CreatedAt:  time.Now(),
	}
}

// toolsFor は会話の設定で使えるツール。「ツールを使う」が OFF なら無し。
func (cs *Conversations) toolsFor(set ConversationSettings) []tool.Tool {
	if !set.Tools {
		return nil
	}
	return cs.tools
}
