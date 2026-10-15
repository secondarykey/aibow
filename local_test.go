package aibow

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/secondarykey/aibow/format"
	"github.com/secondarykey/aibow/mcp/client"
)

// 組み込みのツールを aibow の中の自作の MCP サーバー越しに呼ぶ(0007 の 5-4)。書き換えるツールの確認は、サーバーが送ってきた差分の1回だけ
func TestLocalToolsThroughMCP(t *testing.T) {
	g := &fakeGen{replies: []fakeReply{
		{toolCalls: []format.ToolCall{toolCall("write_file", "path", "a.txt", "content", "hello\n")}},
		{content: "書きました"},
	}}
	cs, c, dir := newWorkDirConversation(t, g)
	l := client.NewLocal(client.Options{Version: "test"})
	t.Cleanup(l.Close)
	cs.SetLocalTools(l)

	var reqs []*ConfirmRequest
	evs := sendAnswering(cs, context.Background(), c, "書いて", func(q *ConfirmRequest) bool {
		reqs = append(reqs, q)
		return cs.Confirm(c.ID, q.ID, true, "") == nil
	})
	if errs := errorsOf(evs); len(errs) != 0 {
		t.Fatalf("エラー: %v", errs)
	}
	if len(reqs) != 1 || reqs[0].Preview.Diff == "" || reqs[0].Preview.Server != "" {
		t.Fatalf("確認はサーバーの差分で1回(サーバーの名前は出さない): %+v", reqs)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(b) != "hello\n" {
		t.Errorf("書いた: %q", b)
	}
	rs := toolResults(t, cs, c)
	if len(rs) != 1 || rs[0].ToolResult.Server != "" || rs[0].ToolResult.Confirm == nil || !rs[0].ToolResult.Confirm.Approved {
		t.Errorf("組み込みのツールとして残す: %+v", rs)
	}
}

// 「確認しない」にした組み込みのツールは、サーバーからの確認も聞かずに受け入れる
func TestLocalToolsAllowed(t *testing.T) {
	g := &fakeGen{replies: []fakeReply{
		{toolCalls: []format.ToolCall{toolCall("write_file", "path", "a.txt", "content", "hi")}},
		{content: "書きました"},
	}}
	cs, c, dir := newWorkDirConversation(t, g)
	l := client.NewLocal(client.Options{Version: "test"})
	t.Cleanup(l.Close)
	cs.SetLocalTools(l)
	prefs := &ToolPrefs{}
	prefs.SetToolPolicy("write_file", ToolAllow)
	cs.SetToolPrefs(prefs)

	asked := 0
	sendAnswering(cs, context.Background(), c, "書いて", func(*ConfirmRequest) bool { asked++; return false })
	if b, _ := os.ReadFile(filepath.Join(dir, "a.txt")); asked != 0 || string(b) != "hi" {
		t.Errorf("聞かずに書く: 聞いた %d 回・ファイル %q", asked, b)
	}
}
