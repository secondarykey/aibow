package aibow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/secondarykey/aibow/format"
	"github.com/secondarykey/aibow/tool"
)

// fakeTool はテスト用のツール。引数 x を1つ取り、run の結果を返す。
type fakeTool struct {
	name string
	run  func(ctx context.Context, args tool.Args) (string, error)
}

func (f fakeTool) Def() tool.Def {
	return tool.Def{Name: f.name, Description: "test tool",
		Parameters: json.RawMessage(`{"type": "object", "properties": {"x": {"type": "string"}}, "required": []}`)}
}

func (f fakeTool) Run(ctx context.Context, args tool.Args) (string, error) {
	return f.run(ctx, args)
}

// newToolConversation はツール(時刻・計算と extra)を使える会話を作る。
func newToolConversation(t *testing.T, g *fakeGen, extra ...tool.Tool) (*Conversations, *memStore, Conversation) {
	t.Helper()
	cs, st := newTestConversations(t, g)
	cs.tools = append(tool.Builtin(), extra...)
	set := testDefaults
	c, err := cs.Create(&set)
	if err != nil {
		t.Fatal(err)
	}
	return cs, st, c
}

// toolCall はモデルが書いた(書式から読み取った)呼び出しを作る。kv は引数の名前と値を交互に並べる。
func toolCall(name string, kv ...string) format.ToolCall {
	args := []tool.Arg{}
	for i := 0; i+1 < len(kv); i += 2 {
		args = append(args, tool.Arg{Name: kv[i], Value: kv[i+1]})
	}
	return format.ToolCall{Call: tool.Call{Name: name, Args: args}}
}

func calc(expr string) format.ToolCall {
	return toolCall("calculate", "expression", expr)
}

// roles は発言の role を並べた文字列。例: "user,assistant,tool,assistant"
func roles(ms []StoredMessage) string {
	var rs []string
	for _, m := range ms {
		rs = append(rs, m.Role)
	}
	return strings.Join(rs, ",")
}

func eventTypes(evs []ChatEvent) string {
	var ts []string
	for _, ev := range evs {
		ts = append(ts, ev.Type)
	}
	return strings.Join(ts, ",")
}

// 呼び出し → 結果 → 答え、の流れ。結果は role tool で保存され、次の生成に渡る
func TestAgentCallsToolAndAnswers(t *testing.T) {
	g := &fakeGen{replies: []fakeReply{
		{thinking: "計算しよう", toolCalls: []format.ToolCall{calc("1234 * 5678")}},
		{thinking: "結果が出た", content: "7006652 です"},
	}}
	cs, _, c := newToolConversation(t, g)

	evs := send(t, cs, c, "1234 × 5678 は?")
	if got := eventTypes(evs); got != "user,usage,prompt,thinking,reply,tool_start,tool_result,usage,prompt,thinking,content,reply,usage,done" {
		t.Errorf("イベントの順番: %s", got)
	}
	_, ms, _ := cs.Get(c.ID)
	if roles(ms) != "user,assistant,tool,assistant" {
		t.Fatalf("保存した発言: %s", roles(ms))
	}
	call, res, answer := ms[1], ms[2], ms[3]
	if len(call.ToolCalls) != 1 || call.Content != "" || call.Stats == nil || call.Stats.NoAnswer {
		t.Errorf("呼び出しの返事: %+v(ツールを呼んで答えが空なのは正常)", call)
	}
	if res.Content != "7006652" || res.ToolResult == nil || res.ToolResult.Error || res.ToolResult.Call.Name != "calculate" {
		t.Errorf("ツールの結果: %+v / %+v", res, res.ToolResult)
	}
	if done := evs[len(evs)-1]; done.Reply == nil || done.Reply.ID != answer.ID || answer.Content != "7006652 です" {
		t.Errorf("done の返事は最後の返事: %+v", done.Reply)
	}

	// 2回目の生成には、ツールの定義・呼び出し・結果が渡る。ループの途中の思考もプロンプトに残る
	second := g.calls[1]
	if len(second.tools) != 2 || second.messages[len(second.messages)-1].Role != "tool" {
		t.Errorf("2回目に渡した内容: %+v", second.messages)
	}
	if !strings.Contains(second.prompt, "<think>\n計算しよう\n</think>\n\n<tool_call>") ||
		!strings.Contains(second.prompt, "<tool_response>\n7006652\n</tool_response>") {
		t.Errorf("2回目のプロンプト:\n%s", second.prompt)
	}

	// 次のユーザーの発言の後は、前の質問のループの思考は捨てる(答えだけ)
	g.replies = []fakeReply{{content: "どういたしまして"}}
	send(t, cs, c, "ありがとう")
	if p := g.calls[2].prompt; strings.Contains(p, "計算しよう") || strings.Contains(p, "結果が出た") || !strings.Contains(p, "<tool_call>") {
		t.Errorf("3回目のプロンプト:\n%s", p)
	}
}

// 1回の送信で、ツールを2回以上呼ぶ流れ(時刻を聞いてから計算する)
func TestAgentRunsSeveralRounds(t *testing.T) {
	g := &fakeGen{replies: []fakeReply{
		{content: "時刻を見ます。", toolCalls: []format.ToolCall{toolCall("get_current_time", "timezone", "Asia/Tokyo")}},
		{toolCalls: []format.ToolCall{calc("365 - 286"), calc("7/2")}},
		{content: "あと 79 日です"},
	}}
	cs, _, c := newToolConversation(t, g)
	send(t, cs, c, "今年はあと何日?")
	_, ms, _ := cs.Get(c.ID)
	if roles(ms) != "user,assistant,tool,assistant,tool,tool,assistant" || len(g.calls) != 3 {
		t.Fatalf("発言: %s / 生成 %d 回", roles(ms), len(g.calls))
	}
	if !strings.Contains(ms[2].Content, "(Asia/Tokyo, UTC+09:00)") || ms[4].Content != "79" || ms[5].Content != "3.5" {
		t.Errorf("結果: %q %q %q", ms[2].Content, ms[4].Content, ms[5].Content)
	}
}

// 書式の崩れ・知らないツール・引数の間違い・ツールのエラーは、エラーの結果としてモデルに返し、送信は止めない
func TestAgentReturnsErrorsToTheModel(t *testing.T) {
	failing := fakeTool{name: "fail", run: func(context.Context, tool.Args) (string, error) {
		return "", errors.New("壊れています")
	}}
	g := &fakeGen{replies: []fakeReply{
		{toolCalls: []format.ToolCall{
			{Raw: "<tool_call>{}</tool_call>", Error: "書式が崩れていて呼び出しを読み取れません"},
			toolCall("web_search"),
			toolCall("calculate", "expr", "1+1"),
			calc("1/0"),
			toolCall("fail"),
		}},
		{content: "うまくいきませんでした"},
	}}
	cs, _, c := newToolConversation(t, g, failing)
	evs := send(t, cs, c, "やって")

	_, ms, _ := cs.Get(c.ID)
	if roles(ms) != "user,assistant,tool,tool,tool,tool,tool,assistant" {
		t.Fatalf("発言: %s", roles(ms))
	}
	wants := []string{"書式が崩れていて", "ツール web_search はありません(使えるツール: calculate, fail, get_current_time)",
		"引数 expr はありません(使える引数: expression)", "0 で割っています", "壊れています"}
	for i, want := range wants {
		m := ms[2+i]
		if !strings.HasPrefix(m.Content, errorPrefix) || !strings.Contains(m.Content, want) || !m.ToolResult.Error {
			t.Errorf("%d 件目の結果: %q(期待: %q を含むエラー)", i, m.Content, want)
		}
	}
	if len(errorsOf(evs)) != 0 || ms[7].Content != "うまくいきませんでした" {
		t.Errorf("送信は止まらずに最後まで答えるはず: %v", errorsOf(evs))
	}
}

// 実行は maxToolCalls 回まで。超えた分は実行せずにその旨を結果にして、止めて理由を出す
func TestAgentStopsAtTheLimit(t *testing.T) {
	g := &fakeGen{}
	for range maxToolCalls + 5 {
		g.replies = append(g.replies, fakeReply{toolCalls: []format.ToolCall{calc("1+1")}})
	}
	cs, _, c := newToolConversation(t, g)
	evs := sendRaw(cs, c, "ずっと計算して")

	errs := errorsOf(evs)
	if len(errs) != 1 || !strings.Contains(errs[0], fmt.Sprintf("(%d 回)を超えた", maxToolCalls)) {
		t.Errorf("エラー: %v", errs)
	}
	_, ms, _ := cs.Get(c.ID)
	var ok, skipped int
	for _, m := range ms {
		if m.Role == "tool" {
			if m.ToolResult.Error {
				skipped++
			} else {
				ok++
			}
		}
	}
	if ok != maxToolCalls || skipped != 1 || len(g.calls) != maxToolCalls+1 || ms[len(ms)-1].Role != "tool" {
		t.Errorf("実行 %d 回・実行しなかった %d 回・生成 %d 回・最後 %s", ok, skipped, len(g.calls), ms[len(ms)-1].Role)
	}
}

// 読み取れない呼び出しは maxToolCalls に数えないので、それだけの回が maxBrokenRounds 回続いたら止める。
// 間に読み取れた呼び出しがあれば数え直す(立て直せたのなら続けてよい)
func TestAgentStopsOnRepeatedBrokenCalls(t *testing.T) {
	broken := fakeReply{toolCalls: []format.ToolCall{{Raw: "<tool_call>\n| 表 |", Error: "書式が崩れていて呼び出しを読み取れません"}}}
	g := &fakeGen{replies: []fakeReply{broken, broken, {toolCalls: []format.ToolCall{calc("1+1")}}}}
	for range maxBrokenRounds + 5 {
		g.replies = append(g.replies, broken)
	}
	cs, _, c := newToolConversation(t, g)
	evs := sendRaw(cs, c, "まとめて")

	errs := errorsOf(evs)
	if len(errs) != 1 || !strings.Contains(errs[0], fmt.Sprintf("%d 回続いた", maxBrokenRounds)) {
		t.Errorf("エラー: %v", errs)
	}
	// 崩れ 2 回 → 読めた 1 回(数え直し)→ 崩れ maxBrokenRounds 回で止まる
	if want := 3 + maxBrokenRounds; len(g.calls) != want {
		t.Errorf("生成 %d 回(期待: %d 回)", len(g.calls), want)
	}
}

// 停止ボタン: ツールの実行中に止めても、ループごと止まる(結果は「停止」として残る)
func TestAgentStopDuringTool(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	blocking := fakeTool{name: "wait", run: func(ctx context.Context, _ tool.Args) (string, error) {
		cancel() // 実行中に停止ボタンが押された
		<-ctx.Done()
		return "", ctx.Err()
	}}
	g := &fakeGen{replies: []fakeReply{
		{toolCalls: []format.ToolCall{toolCall("wait"), calc("1+1")}},
		{content: "ここは生成されない"},
	}}
	cs, _, c := newToolConversation(t, g, blocking)
	var evs []ChatEvent
	cs.Send(ctx, c.ID, "待って", func(ev ChatEvent) { evs = append(evs, ev) })

	_, ms, _ := cs.Get(c.ID)
	if roles(ms) != "user,assistant,tool,tool" || len(g.calls) != 1 || len(errorsOf(evs)) != 0 {
		t.Fatalf("発言: %s / 生成 %d 回 / エラー %v", roles(ms), len(g.calls), errorsOf(evs))
	}
	if ms[2].Content != errorPrefix+"停止されました" || !strings.Contains(ms[3].Content, "停止されたため実行していません") {
		t.Errorf("結果: %q / %q", ms[2].Content, ms[3].Content)
	}
}

// 停止ボタン: 生成中に止めたら、書きかけの呼び出しは実行しない
func TestAgentStopDuringGeneration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	g := &fakeGen{replies: []fakeReply{{waitCancel: true, toolCalls: []format.ToolCall{calc("1+1")}}}}
	cs, _, c := newToolConversation(t, g)
	go func() {
		for cs.Generating() == nil { // 生成の記録(g.calls)はここから読まない(別の goroutine が書くため)
			time.Sleep(time.Millisecond)
		}
		cancel()
	}()
	cs.Send(ctx, c.ID, "計算して", func(ChatEvent) {})
	_, ms, _ := cs.Get(c.ID)
	if roles(ms) != "user,assistant,tool" || !strings.Contains(ms[2].Content, "停止されたため実行していません") {
		t.Errorf("発言: %s / %+v", roles(ms), ms)
	}
}

// ツールが時間の上限を超えたら止めて、エラーの結果にする(ツールが ctx を見なくても待たない)
func TestAgentToolTimeout(t *testing.T) {
	defer func(d time.Duration) { toolTimeout = d }(toolTimeout)
	toolTimeout = 20 * time.Millisecond
	slow := fakeTool{name: "slow", run: func(context.Context, tool.Args) (string, error) {
		time.Sleep(time.Second)
		return "遅い", nil
	}}
	g := &fakeGen{replies: []fakeReply{{toolCalls: []format.ToolCall{toolCall("slow")}}, {content: "だめでした"}}}
	cs, _, c := newToolConversation(t, g, slow)
	start := time.Now()
	send(t, cs, c, "遅いのを")
	_, ms, _ := cs.Get(c.ID)
	if !strings.Contains(ms[2].Content, "時間の上限") || time.Since(start) > 500*time.Millisecond {
		t.Errorf("結果: %q(%v)", ms[2].Content, time.Since(start))
	}
}

// 組み込みのツールを「使わない」にすると、ツールの定義をプロンプトに入れない(トークン数も減る)
func TestToolsOffLeavesToolsOutOfThePrompt(t *testing.T) {
	g := &fakeGen{content: "こんにちは"}
	cs, _, c := newToolConversation(t, g)
	send(t, cs, c, "やあ")
	on, _ := cs.Usage(c.ID)
	if len(g.calls[0].tools) != 2 || !strings.Contains(g.calls[0].prompt, "# Tools") {
		t.Errorf("ON: ツールの定義が入っていません")
	}

	prefs := &ToolPrefs{}
	prefs.SetBuiltinPolicy(ToolOff) // 設定の画面の「ツール」で、組み込みのグループを使わないにした
	cs.SetToolPrefs(prefs)
	send(t, cs, c, "もう一度")
	off, _ := cs.Usage(c.ID)
	if len(g.calls[1].tools) != 0 || strings.Contains(g.calls[1].prompt, "# Tools") {
		t.Errorf("OFF: ツールの定義が入っています")
	}
	if off.Tokens >= on.Tokens {
		t.Errorf("OFF のほうがトークン数が少ないはず: ON %d / OFF %d", on.Tokens, off.Tokens)
	}
	// OFF の会話でモデルがツールを呼んでも、実行しない(知らないツールのエラーとして返す)
	g.replies = []fakeReply{{toolCalls: []format.ToolCall{calc("1+1")}}, {content: "失礼しました"}}
	send(t, cs, c, "計算して")
	_, ms, _ := cs.Get(c.ID)
	if m := ms[len(ms)-2]; m.Role != "tool" || !strings.Contains(m.Content, "ツール calculate はありません(使えるツール: なし)") {
		t.Errorf("OFF の会話での呼び出し: %+v", m)
	}
}

// compact(truncate)は、ツールの発言を含めて組ごとに外す(返事やツールの結果から始まらない)
func TestTruncateKeepsToolTurnsTogether(t *testing.T) {
	g := &fakeGen{}
	cs, _, c := newToolConversation(t, g)
	set := c.Settings
	set.Compact = CompactTruncate
	cs.UpdateSettings(c.ID, set)
	c.Settings = set
	for i := range 3 {
		g.replies = []fakeReply{{toolCalls: []format.ToolCall{calc(fmt.Sprint(i))}}, {content: strings.Repeat("答", 50)}}
		send(t, cs, c, fmt.Sprintf("質問%d", i))
	}
	_, ms, _ := cs.Get(c.ID)
	if starts := turnStarts(ms, 0); len(starts) != 3 || starts[1] != 4 || starts[2] != 8 {
		t.Fatalf("組の始まり: %v(%s)", starts, roles(ms))
	}

	// 最後の組だけが収まる上限にする
	next := StoredMessage{Role: "user", Content: "質問3"}
	fit, _ := g.CountTokens(cs.promptFor(set, promptMessages(set.System, "", append(ms[8:], next))))
	setLimits(t, cs, &c, fit+10, 100)
	g.replies = []fakeReply{{content: "ok"}}
	send(t, cs, c, "質問3")
	if got := g.gotMessages; got[1].Role != "user" || got[1].Content != "質問2" || len(got) != 1+5 {
		t.Errorf("外したあとの最初の発言は user のはず: %+v", got)
	}
}

// 要約(summary)に渡す会話には、ツールの呼び出しと結果も入る。長い結果は先頭だけ
func TestSummaryIncludesToolCalls(t *testing.T) {
	part := []StoredMessage{
		{Role: "user", Content: "計算して"},
		{Role: "assistant", ToolCalls: []format.ToolCall{calc("1+1")}},
		{Role: "tool", Content: strings.Repeat("結", summaryToolResultRunes+50), ToolResult: &ToolResult{Call: calc("1+1")}},
		{Role: "assistant", Content: "2 です"},
	}
	text := summaryMessages("", part, minSummaryTokens)[1].Content
	for _, want := range []string{"ユーザー: 計算して\n", "アシスタント: (ツール calculate を呼んだ: expression=1+1)\n",
		"ツールの結果(calculate): " + strings.Repeat("結", summaryToolResultRunes) + "…\n", "アシスタント: 2 です\n"} {
		if !strings.Contains(text, want) {
			t.Errorf("要約に渡す文に %q がありません:\n%s", want, text)
		}
	}
}

// ツールの結果を足すと今の組が会話の上限を超えるときは、結果の代わりに「長すぎて渡せない」エラーを返し、ループは続ける。
// モデルは範囲を絞って呼び直せる。会話は上限を超えた状態にならない
func TestAgentRejectsResultThatDoesNotFit(t *testing.T) {
	huge := fakeTool{name: "huge", run: func(context.Context, tool.Args) (string, error) {
		return strings.Repeat("大", 5000), nil
	}}
	g := &fakeGen{replies: []fakeReply{{toolCalls: []format.ToolCall{toolCall("huge")}}, {content: "範囲を絞ります"}}}
	cs, _, c := newToolConversation(t, g, huge)
	setLimits(t, cs, &c, 4000, 100)
	evs := sendRaw(cs, c, "大きいのを")
	if errs := errorsOf(evs); len(errs) != 0 {
		t.Errorf("エラー: %v", errs)
	}
	_, ms, _ := cs.Get(c.ID)
	res := ms[2]
	if res.Role != "tool" || !res.ToolResult.Error || !strings.Contains(res.Content, "会話の上限に入らないため、渡せません") || strings.Contains(res.Content, "大大") {
		t.Errorf("結果: %+v", res)
	}
	if len(g.calls) != 2 || ms[len(ms)-1].Content != "範囲を絞ります" {
		t.Errorf("ループは続くはず: 生成 %d 回 / 最後 %q", len(g.calls), ms[len(ms)-1].Content)
	}
	if u, _ := cs.Usage(c.ID); u.Tokens > u.Limit {
		t.Errorf("上限を超えています: %d / %d", u.Tokens, u.Limit)
	}
}

// 最新の組だけで会話の上限を超えている会話(上限を下げた・この仕組みより前に保存された)では、
// 今すぐ要約しても要約するものが無い。エラーではなくお知らせで、次に送信すれば要約されると伝える。
// 次の質問を送ると、前の組が要約の対象になり、要約してから答える
func TestCompactNowWhenLatestTurnIsTooLong(t *testing.T) {
	big := fakeTool{name: "big", run: func(context.Context, tool.Args) (string, error) {
		return strings.Repeat("大", 2500), nil
	}}
	g := &fakeGen{replies: []fakeReply{{toolCalls: []format.ToolCall{toolCall("big")}}, {content: "受け取りました"}}}
	cs, _, c := newToolConversation(t, g, big)
	setLimits(t, cs, &c, 4000, 100)
	send(t, cs, c, "大きいのを")
	setLimits(t, cs, &c, 2000, 100) // 最新の組だけで上限を超えた状態にする
	if u, _ := cs.Usage(c.ID); u.Tokens <= u.Limit {
		t.Fatalf("メーター: %d / %d(超えているはず)", u.Tokens, u.Limit)
	}

	var evs []ChatEvent
	cs.Compact(context.Background(), c.ID, func(ev ChatEvent) { evs = append(evs, ev) })
	// 「最近の発言が上限の半分に収まっています」は事実と逆なので出さない。
	// 会話の上限を変えたので、先に作業領域を作り直したお知らせも来る
	if errs, notes := errorsOf(evs), noticesOf(evs); len(errs) != 0 || len(notes) != 2 || !isResizeNotice(notes[0]) || notes[1] != errOnlyLatestTurn.Error() {
		t.Errorf("今すぐ要約: エラー %v / お知らせ %v", errs, notes)
	}

	g.replies = []fakeReply{{content: "- 大きい結果を受け取った"}, {content: "続きです"}}
	evs = sendRaw(cs, c, "続けて")
	if errs := errorsOf(evs); len(errs) != 0 {
		t.Fatalf("次の送信のエラー: %v", errs)
	}
	if list, _ := cs.Compactions(c.ID); len(list) != 1 || list[0].Summary != "- 大きい結果を受け取った" {
		t.Errorf("要約: %+v", list)
	}
	if _, ms, _ := cs.Get(c.ID); ms[len(ms)-1].Content != "続きです" {
		t.Errorf("最後の返事: %q", ms[len(ms)-1].Content)
	}
	if u, _ := cs.Usage(c.ID); u.Tokens > u.Limit {
		t.Errorf("要約のあとも超えています: %d / %d", u.Tokens, u.Limit)
	}
}

// noticesOf はイベントからお知らせ(notice)の文だけを取り出す
func noticesOf(evs []ChatEvent) []string {
	var out []string
	for _, ev := range evs {
		if ev.Type == "notice" {
			out = append(out, ev.Text)
		}
	}
	return out
}
