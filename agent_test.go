package aibow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// fakeTool はテスト用のツール。引数 x を1つ取り、run の結果を返す。
type fakeTool struct {
	name string
	run  func(ctx context.Context, args map[string]string) (string, error)
}

func (f fakeTool) Def() ToolDef {
	return ToolDef{Name: f.name, Description: "test tool",
		Parameters: json.RawMessage(`{"type": "object", "properties": {"x": {"type": "string"}}, "required": []}`)}
}

func (f fakeTool) Run(ctx context.Context, args map[string]string) (string, error) {
	return f.run(ctx, args)
}

// newToolConversation は「ツールを使う」が ON の会話を作る。
func newToolConversation(t *testing.T, g *fakeGen, extra ...Tool) (*Conversations, *memStore, Conversation) {
	t.Helper()
	cs, st := newTestConversations(t, g)
	cs.tools = append(BuiltinTools(), extra...)
	set := testDefaults
	set.Tools = true
	c, err := cs.Create(&set)
	if err != nil {
		t.Fatal(err)
	}
	return cs, st, c
}

func calc(expr string) ToolCall {
	return ToolCall{Name: "calculate", Args: []ToolArg{{"expression", expr}}}
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
		{thinking: "計算しよう", toolCalls: []ToolCall{calc("1234 * 5678")}},
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
		{content: "時刻を見ます。", toolCalls: []ToolCall{{Name: "get_current_time", Args: []ToolArg{{"timezone", "Asia/Tokyo"}}}}},
		{toolCalls: []ToolCall{calc("365 - 286"), calc("7/2")}},
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
	failing := fakeTool{name: "fail", run: func(context.Context, map[string]string) (string, error) {
		return "", errors.New("壊れています")
	}}
	g := &fakeGen{replies: []fakeReply{
		{toolCalls: []ToolCall{
			{Raw: "<tool_call>{}</tool_call>", Error: "書式が崩れていて呼び出しを読み取れません"},
			{Name: "web_search", Args: []ToolArg{}},
			{Name: "calculate", Args: []ToolArg{{"expr", "1+1"}}},
			calc("1/0"),
			{Name: "fail", Args: []ToolArg{}},
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

// 実行は 8 回まで。超えた分は実行せずにその旨を結果にして、止めて理由を出す
func TestAgentStopsAtTheLimit(t *testing.T) {
	g := &fakeGen{}
	for range 20 {
		g.replies = append(g.replies, fakeReply{toolCalls: []ToolCall{calc("1+1")}})
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

// 停止ボタン: ツールの実行中に止めても、ループごと止まる(結果は「停止」として残る)
func TestAgentStopDuringTool(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	blocking := fakeTool{name: "wait", run: func(ctx context.Context, _ map[string]string) (string, error) {
		cancel() // 実行中に停止ボタンが押された
		<-ctx.Done()
		return "", ctx.Err()
	}}
	g := &fakeGen{replies: []fakeReply{
		{toolCalls: []ToolCall{{Name: "wait", Args: []ToolArg{}}, calc("1+1")}},
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
	g := &fakeGen{replies: []fakeReply{{waitCancel: true, toolCalls: []ToolCall{calc("1+1")}}}}
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
	slow := fakeTool{name: "slow", run: func(context.Context, map[string]string) (string, error) {
		time.Sleep(time.Second)
		return "遅い", nil
	}}
	g := &fakeGen{replies: []fakeReply{{toolCalls: []ToolCall{{Name: "slow", Args: []ToolArg{}}}}, {content: "だめでした"}}}
	cs, _, c := newToolConversation(t, g, slow)
	start := time.Now()
	send(t, cs, c, "遅いのを")
	_, ms, _ := cs.Get(c.ID)
	if !strings.Contains(ms[2].Content, "時間の上限") || time.Since(start) > 500*time.Millisecond {
		t.Errorf("結果: %q(%v)", ms[2].Content, time.Since(start))
	}
}

// 「ツールを使う」が OFF なら、ツールの定義をプロンプトに入れない(トークン数も減る)
func TestToolsOffLeavesToolsOutOfThePrompt(t *testing.T) {
	g := &fakeGen{content: "こんにちは"}
	cs, _, c := newToolConversation(t, g)
	send(t, cs, c, "やあ")
	on, _ := cs.Usage(c.ID)
	if len(g.calls[0].tools) != 2 || !strings.Contains(g.calls[0].prompt, "# Tools") {
		t.Errorf("ON: ツールの定義が入っていません")
	}

	set := c.Settings
	set.Tools = false
	cs.UpdateSettings(c.ID, set)
	send(t, cs, c, "もう一度")
	off, _ := cs.Usage(c.ID)
	if len(g.calls[1].tools) != 0 || strings.Contains(g.calls[1].prompt, "# Tools") {
		t.Errorf("OFF: ツールの定義が入っています")
	}
	if off.Tokens >= on.Tokens {
		t.Errorf("OFF のほうがトークン数が少ないはず: ON %d / OFF %d", on.Tokens, off.Tokens)
	}
	// OFF の会話でモデルがツールを呼んでも、実行しない(知らないツールのエラーとして返す)
	g.replies = []fakeReply{{toolCalls: []ToolCall{calc("1+1")}}, {content: "失礼しました"}}
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
		g.replies = []fakeReply{{toolCalls: []ToolCall{calc(fmt.Sprint(i))}}, {content: strings.Repeat("答", 50)}}
		send(t, cs, c, fmt.Sprintf("質問%d", i))
	}
	_, ms, _ := cs.Get(c.ID)
	if starts := turnStarts(ms, 0); len(starts) != 3 || starts[1] != 4 || starts[2] != 8 {
		t.Fatalf("組の始まり: %v(%s)", starts, roles(ms))
	}

	// 最後の組だけが収まる上限にする
	next := StoredMessage{Role: "user", Content: "質問3"}
	fit, _ := g.CountTokens(cs.promptFor(set, promptMessages(set.System, "", append(ms[8:], next))))
	cs.SetLimits(fit+10, 100)
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
		{Role: "assistant", ToolCalls: []ToolCall{calc("1+1")}},
		{Role: "tool", Content: strings.Repeat("結", summaryToolResultRunes+50), ToolResult: &ToolResult{Call: calc("1+1")}},
		{Role: "assistant", Content: "2 です"},
	}
	text := summaryMessages("", part)[1].Content
	for _, want := range []string{"ユーザー: 計算して\n", "アシスタント: (ツール calculate を呼んだ: expression=1+1)\n",
		"ツールの結果(calculate): " + strings.Repeat("結", summaryToolResultRunes) + "…\n", "アシスタント: 2 です\n"} {
		if !strings.Contains(text, want) {
			t.Errorf("要約に渡す文に %q がありません:\n%s", want, text)
		}
	}
}

// ツールの結果で今の組が会話の上限を超えたら、そこで止めてエラーを出す
func TestAgentStopsWhenToolResultsOverflow(t *testing.T) {
	huge := fakeTool{name: "huge", run: func(context.Context, map[string]string) (string, error) {
		return strings.Repeat("大", 5000), nil
	}}
	g := &fakeGen{replies: []fakeReply{{toolCalls: []ToolCall{{Name: "huge", Args: []ToolArg{}}}}, {content: "ここは生成されない"}}}
	cs, _, c := newToolConversation(t, g, huge)
	cs.SetLimits(4000, 100)
	evs := sendRaw(cs, c, "大きいのを")
	if errs := errorsOf(evs); len(errs) != 1 || !strings.Contains(errs[0], "ツールの結果を足すと会話の上限を超える") {
		t.Errorf("エラー: %v", errs)
	}
	if len(g.calls) != 1 {
		t.Errorf("2回目の生成はしないはず: %d 回", len(g.calls))
	}
}
