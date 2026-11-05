package aibow

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/secondarykey/aibow/format"
	"github.com/secondarykey/aibow/tool"
)

// elideResultRunes は、試しのツール big の結果の長さ(札より十分長い)。
const elideResultRunes = 2000

// elideScript は「計算する → big を4回読む → 答える」の送信。どの一歩にも思考がある。
// 1歩目の計算の結果は札より短い(縮めても結果はそのまま、思考だけが空になる)
func elideScript() []fakeReply {
	rs := []fakeReply{{thinking: strings.Repeat("考", 400), toolCalls: []format.ToolCall{calc("1234 * 5678")}}}
	for i := 2; i <= 5; i++ {
		rs = append(rs, fakeReply{thinking: strings.Repeat("思", 400), toolCalls: []format.ToolCall{toolCall("big", "x", fmt.Sprint(i))}})
	}
	return append(rs, fakeReply{thinking: "まとめる", content: "読み終えました"})
}

// elideConversation は、big を使える会話を compact の方式 compact で作り、会話の上限をツールの説明などの土台 + extra にする。
// workspace なら作業フォルダのある会話にする(update_plan を使えるように。ツールの説明も土台に入る)。
func elideConversation(t *testing.T, g *fakeGen, compact string, extra int, workspace bool) (*Conversations, *memStore, Conversation) {
	t.Helper()
	big := fakeTool{name: "big", run: func(_ context.Context, args tool.Args) (string, error) {
		return strings.Repeat("中", elideResultRunes) + fmt.Sprint(args["x"]), nil
	}}
	cs, st, c := newToolConversation(t, g, big)
	c.Settings.Compact = compact
	if workspace {
		c.Settings.Workspace = workspaceFor(t, cs, t.TempDir())
	}
	base, err := cs.turnTokens(c.Settings, []StoredMessage{{Role: "user", Content: "読んで"}})
	if err != nil {
		t.Fatal(err)
	}
	setLimits(t, cs, &c, base+extra, 100)
	return cs, st, c
}

// stepsOf は、モデルに渡した発言の、最後のユーザーの発言から後ろを一歩(返事 + 結果)ずつにする。
func stepsOf(msgs []format.Message) [][]format.Message {
	last := 0
	for i, m := range msgs {
		if m.Role == "user" {
			last = i
		}
	}
	var out [][]format.Message
	for _, m := range msgs[last+1:] {
		if m.Role == "assistant" {
			out = append(out, []format.Message{m})
		} else if len(out) > 0 {
			out[len(out)-1] = append(out[len(out)-1], m)
		}
	}
	return out
}

// 今の組が水位を超えると、古い一歩の結果が札に、思考が空になって渡る。新しい 2 歩・短い結果・呼び出しは変わらない(完了の条件 1)。
// 印は保存され、メーターも縮めた形で数える(完了の条件 2)
func TestElideOldSteps(t *testing.T) {
	g := &fakeGen{replies: elideScript()}
	cs, st, c := elideConversation(t, g, CompactTruncate, 10000, false)
	evs := sendRaw(cs, c, "読んで")
	if errs := errorsOf(evs); len(errs) != 0 {
		t.Fatalf("エラー: %v", errs)
	}
	if len(g.calls) != 6 {
		t.Fatalf("生成は 6 回のはず: %d", len(g.calls))
	}

	// 最後の生成(答え)に渡したもの: 一歩 1〜3 は縮め、4・5 はそのまま
	steps := stepsOf(g.calls[5].messages)
	if len(steps) != 5 {
		t.Fatalf("一歩の数: %d", len(steps))
	}
	for i, s := range steps {
		elided := i < 3
		if len(s[0].ToolCalls) == 0 {
			t.Errorf("一歩 %d: 呼び出しが消えている", i+1)
		}
		if (s[0].Thinking == "") != elided {
			t.Errorf("一歩 %d: 思考 %q(縮めた: %v)", i+1, headRunes(s[0].Thinking, 10), elided)
		}
		res := s[1].Content
		switch {
		case i == 0:
			if strings.Contains(res, "省略") || res == "" {
				t.Errorf("短い結果はそのまま渡す: %q", res)
			}
		case elided:
			if res != elidedNote(elideResultRunes+1) {
				t.Errorf("一歩 %d: 札になっていない: %q", i+1, headRunes(res, 40))
			}
		default:
			if utf8.RuneCountInString(res) != elideResultRunes+1 {
				t.Errorf("一歩 %d: 結果はそのまま渡す: %q", i+1, headRunes(res, 40))
			}
		}
	}

	// 知らせ: elide の compact イベント。縮めたぶん減っている
	var elides []CompactInfo
	for _, cp := range compacts(evs) {
		if cp.Method == CompactElide {
			elides = append(elides, cp)
		}
	}
	if len(elides) == 0 || elides[0].AfterTokens >= elides[0].BeforeTokens || elides[0].BeforeTokens <= elideHigh(c.Settings.HistoryTokens) {
		t.Errorf("elide の知らせ: %+v", elides)
	}

	// 印: 一歩 1〜3 の返事と結果に付き、ユーザーの発言・一歩 4・5・答えには付かない。DB の文と思考は消さない
	ms := st.messages
	if roles(ms) != "user,assistant,tool,assistant,tool,assistant,tool,assistant,tool,assistant,tool,assistant" {
		t.Fatalf("発言: %s", roles(ms))
	}
	for i, m := range ms {
		if want := i >= 1 && i <= 6; m.Elided != want {
			t.Errorf("発言 %d(%s)の印: %v", i, m.Role, m.Elided)
		}
	}
	if ms[1].Thinking == "" || utf8.RuneCountInString(ms[4].Content) != elideResultRunes+1 {
		t.Error("保存した思考と結果は消さない")
	}

	// メーターは縮めた形で数える(印を外して数えたものより小さい)
	u, err := cs.Usage(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	plain := slices.Clone(ms)
	for i := range plain {
		plain[i].Elided = false
	}
	if full, err := cs.passFrom(c.Settings, nil, plain, 0); err != nil || u.Tokens >= full.tokens {
		t.Errorf("メーター: %d(印を外すと %d)%v", u.Tokens, full.tokens, err)
	}
	// 次の送信も、縮めた形で数えて渡す(生成の前のメーター = 渡した文字列)
	g.replies = []fakeReply{{content: "続けます"}}
	evs = sendRaw(cs, c, "続けて")
	us := usages(evs)
	last := g.calls[len(g.calls)-1]
	if len(us) == 0 || us[0].Tokens != utf8.RuneCountInString(last.prompt) {
		t.Errorf("生成の前のメーター %+v / 渡した %d", us, utf8.RuneCountInString(last.prompt))
	}
	// 上限で止まったあとの「続けて」と同じく、前の組は元の長さに戻らない
	if !strings.Contains(last.prompt, elidedNote(elideResultRunes+1)) {
		t.Error("次の送信でも、省略した結果は札のまま渡す")
	}
}

// none の会話は、水位を超えても縮めない。モデルに渡すものは1文字も変わらない(完了の条件 3)
func TestElideNotInNone(t *testing.T) {
	// まず上限を気にしなくてよい大きさで送り、渡したものを覚える
	g1 := &fakeGen{replies: elideScript()}
	cs1, _, c1 := elideConversation(t, g1, CompactNone, 100000, false)
	sendRaw(cs1, c1, "読んで")
	u, err := cs1.Usage(ctx, c1.ID)
	if err != nil {
		t.Fatal(err)
	}

	// 上限を、最後には水位(3/4)を超えるが上限には入る大きさにして、同じ送信をする
	g2 := &fakeGen{replies: elideScript()}
	cs2, st2, c2 := elideConversation(t, g2, CompactNone, 0, false)
	setLimits(t, cs2, &c2, u.Tokens*6/5, 100)
	evs := sendRaw(cs2, c2, "読んで")
	if errs := errorsOf(evs); len(errs) != 0 {
		t.Fatalf("エラー: %v", errs)
	}
	if u.Tokens <= elideHigh(c2.Settings.HistoryTokens) {
		t.Fatalf("テストの前提: 水位を超えていない(%d / %d)", u.Tokens, c2.Settings.HistoryTokens)
	}
	if len(compacts(evs)) != 0 || slices.ContainsFunc(st2.messages, func(m StoredMessage) bool { return m.Elided }) {
		t.Errorf("none では縮めない: %+v", compacts(evs))
	}
	if len(g1.calls) != len(g2.calls) {
		t.Fatalf("生成の回数: %d / %d", len(g1.calls), len(g2.calls))
	}
	for i := range g1.calls {
		if g1.calls[i].prompt != g2.calls[i].prompt {
			t.Errorf("生成 %d 回目に渡したものが変わった", i+1)
		}
	}
}

// 縮めた組も、summary では今までどおり要約に入る(結果は先頭 200 文字。縮めた結果も同じ扱い)。
// 縮めた一歩に update_plan があれば、呼び出しの引数に予定が残る(完了の条件 2)
func TestElideWithSummaryAndPlan(t *testing.T) {
	g := &fakeGen{replies: append([]fakeReply{
		{thinking: strings.Repeat("計", 300), toolCalls: []format.ToolCall{toolCall(tool.PlanToolName, "items", testPlanItems)}},
	}, elideScript()[1:]...)}
	cs, st, c := elideConversation(t, g, CompactSummary, 10000, true)
	evs := sendRaw(cs, c, "読んで")
	if errs := errorsOf(evs); len(errs) != 0 {
		t.Fatalf("エラー: %v", errs)
	}
	for _, m := range st.messages {
		if strings.Contains(m.Content, "ここで切りました") {
			t.Errorf("縮めれば入るので、結果は切らない: %q", headRunes(m.Content, 40))
		}
	}
	if !st.messages[1].Elided {
		t.Fatal("update_plan の一歩が縮んでいない(テストの前提)")
	}
	steps := stepsOf(g.calls[len(g.calls)-1].messages)
	if !passedPlanCall(steps[0][0:1]) || steps[0][0].Thinking != "" {
		t.Errorf("縮めた一歩の update_plan の呼び出しは残る: %+v", steps[0][0])
	}

	// 次の送信で、縮めた組を要約する(上限を下げて、前の組が入らないようにする)
	u, _ := cs.Usage(ctx, c.ID)
	setLimits(t, cs, &c, u.Tokens*2/3, 100)
	g.replies = []fakeReply{{content: "- big を4回読んだ"}, {content: "続けます"}}
	evs = sendRaw(cs, c, "続けて")
	if errs := errorsOf(evs); len(errs) != 0 {
		t.Fatalf("次の送信のエラー: %v", errs)
	}
	sum := g.calls[len(g.calls)-2].messages
	if len(sum) != 2 || !strings.Contains(sum[1].Content, "ツールの結果(big): "+strings.Repeat("中", summaryToolResultRunes)+"…") {
		t.Errorf("要約に渡す結果は、縮めても先頭 200 文字: %q", headRunes(sum[len(sum)-1].Content, 200))
	}
	if list, _ := cs.Compactions(ctx, c.ID); len(list) != 1 {
		t.Errorf("要約: %+v", list)
	}
}

// promptMessages は、印の付いた tool を札に(札より短い結果はそのまま)、印の付いた assistant の思考を空にする。呼び出しは残す
func TestPromptMessagesElided(t *testing.T) {
	long := strings.Repeat("長", 300)
	got := promptMessages("", "", []StoredMessage{
		{Role: "user", Content: "q", Elided: true},
		{Role: "assistant", Thinking: "考え", ToolCalls: []format.ToolCall{calc("1+1")}, Elided: true},
		{Role: "tool", Content: long, ToolResult: &ToolResult{Call: calc("1+1")}, Elided: true},
		{Role: "tool", Content: "2", ToolResult: &ToolResult{Call: calc("1+1")}, Elided: true},
		{Role: "assistant", Thinking: "まだ", ToolCalls: []format.ToolCall{calc("2+2")}},
		{Role: "tool", Content: long, ToolResult: &ToolResult{Call: calc("2+2")}},
	})
	if got[0].Content != "q" || got[1].Thinking != "" || len(got[1].ToolCalls) != 1 ||
		got[2].Content != elidedNote(300) || got[3].Content != "2" || got[4].Thinking != "まだ" || got[5].Content != long {
		t.Errorf("渡す形: %+v", got)
	}
}
