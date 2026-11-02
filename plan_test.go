package aibow

import (
	"fmt"
	"strings"
	"testing"

	"github.com/secondarykey/aibow/format"
	"github.com/secondarykey/aibow/tool"
)

const testPlanItems = `[{"text": "main.go を読む", "status": "done"}, {"text": "直す", "status": "in_progress"}]`

// testPlanNote は testPlanItems のときにシステムの文の最後に足す予定。
const testPlanNote = "Current plan (from update_plan in the earlier part of this conversation, which is no longer shown):\n" +
	"- [done] main.go を読む\n- [in_progress] 直す"

// planTalk は、作業フォルダのある会話で、最初の組で update_plan を呼び、そのあと n 組話した会話を作る。
func planTalk(t *testing.T, g *fakeGen, compact string, n int) (*Conversations, Conversation) {
	t.Helper()
	g.replies = []fakeReply{
		{toolCalls: []format.ToolCall{toolCall(tool.PlanToolName, "items", testPlanItems)}},
		{content: "予定を書きました"},
	}
	cs, _, c := newToolConversation(t, g)
	c.Settings.Workspace = workspaceFor(t, cs, t.TempDir())
	c.Settings.Compact = compact
	if err := cs.UpdateSettings(ctx, c.ID, c.Settings); err != nil {
		t.Fatal(err)
	}
	send(t, cs, c, "バグを直して")
	g.replies = nil
	for i := range n {
		g.content = strings.Repeat("返", 50) + fmt.Sprint(i)
		send(t, cs, c, fmt.Sprintf("続けて%d", i))
	}
	return cs, c
}

// passedPlanCall は、モデルに渡した発言に update_plan の呼び出しが残っているか。
func passedPlanCall(msgs []format.Message) bool {
	for _, m := range msgs {
		for _, c := range m.ToolCalls {
			if c.Name == tool.PlanToolName {
				return true
			}
		}
	}
	return false
}

// truncate で update_plan の呼び出しを外しても、最後の予定がシステムの文の最後に残る(完了の条件 3)
func TestPlanSurvivesTruncate(t *testing.T) {
	g := &fakeGen{}
	cs, c := planTalk(t, g, CompactTruncate, 4)
	if passedPlanCall(g.gotMessages) && strings.Contains(systemOf(g.gotMessages), "Current plan") {
		t.Fatal("呼び出しが見えている間は、予定を足さない")
	}
	u, err := cs.Usage(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	setLimits(t, cs, &c, u.Tokens, 100) // 次の発言で超えるので、古い組から外れる
	g.content = "ok"
	evs := send(t, cs, c, "次へ")
	if cps := compacts(evs); len(cps) != 1 || cps[0].Method != CompactTruncate {
		t.Fatalf("truncate が起きていない: %+v", cps)
	}
	if passedPlanCall(g.gotMessages) {
		t.Fatal("update_plan の呼び出しが外れていない(テストの前提)")
	}
	// システムの文の最後(残した発言の前)に予定を足す
	if sys := systemOf(g.gotMessages); !strings.HasSuffix(sys, "\n\n"+testPlanNote) {
		t.Errorf("システムの文:\n%s", sys)
	}
}

// 要約しても、最後の予定が要約の後ろに残る(完了の条件 3)
func TestPlanSurvivesSummary(t *testing.T) {
	g := &fakeGen{}
	cs, c := planTalk(t, g, CompactSummary, 6)
	overLimit(t, cs, c)
	g.replies = []fakeReply{{content: "- 要約1"}, {content: "返事"}}
	send(t, cs, c, "次へ")
	if list, _ := cs.Compactions(ctx, c.ID); len(list) != 1 {
		t.Fatalf("要約が起きていない: %+v", list)
	}
	last := g.calls[len(g.calls)-1].messages
	if passedPlanCall(last) {
		t.Fatal("update_plan の呼び出しが要約に入っていない(テストの前提)")
	}
	sys := systemOf(last)
	if !strings.HasSuffix(sys, summaryHeading+"\n- 要約1\n\n"+testPlanNote) {
		t.Errorf("予定は要約の後ろ:\n%s", sys)
	}
	// メーターも同じ内容で数える(送信とメーターで同じ passFrom を使う)
	_, ms, _ := cs.Get(ctx, c.ID)
	p, err := cs.plan(ctx, c, forModel(ms))
	if err != nil || !strings.HasSuffix(systemOf(p.messages), testPlanNote) {
		t.Errorf("メーターの内容: %v\n%s", err, systemOf(p.messages))
	}
}

// 足すのは「渡さない部分の最後の成功した予定」。渡す部分の予定は足さない(KV キャッシュのため、文を変えない)
func TestLastPlan(t *testing.T) {
	result := func(items string, isErr bool, server string) StoredMessage {
		return StoredMessage{Role: "tool", ToolResult: &ToolResult{Call: toolCall(tool.PlanToolName, "items", items), Error: isErr, Server: server}}
	}
	a := `[{"text": "A", "status": "pending"}]`
	b := `[{"text": "B", "status": "done"}]`
	bad := `[{"text": "C", "status": "in_progress"}, {"text": "D", "status": "in_progress"}]`
	cases := []struct {
		name    string
		history []StoredMessage
		want    string
	}{
		{"無い", []StoredMessage{{Role: "user", Content: "x"}}, ""},
		{"最後のもの", []StoredMessage{result(a, false, ""), result(b, false, "")}, "- [done] B"},
		{"エラーの呼び出しは見ない", []StoredMessage{result(a, false, ""), result(bad, true, "")}, "- [pending] A"},
		{"MCP のサーバーのものは見ない", []StoredMessage{result(a, false, ""), result(b, false, "aibow")}, "- [pending] A"},
	}
	for _, c := range cases {
		got := planNote(lastPlan(c.history))
		if c.want == "" && got != "" || c.want != "" && !strings.HasSuffix(got, "\n"+c.want) {
			t.Errorf("%s: %q", c.name, got)
		}
	}
	// システムの文が無ければ作る
	if got := withPlan([]format.Message{{Role: "user", Content: "x"}}, "plan"); len(got) != 2 || got[0].Role != "system" || got[0].Content != "plan" {
		t.Errorf("system が無いとき: %+v", got)
	}
}
