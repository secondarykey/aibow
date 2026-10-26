package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/secondarykey/aibow"
	"github.com/secondarykey/aibow/format"
	"github.com/secondarykey/aibow/mcp/client"
	mcpserver "github.com/secondarykey/aibow/mcp/server"
	"github.com/secondarykey/aibow/tool"
)

// サーバーの MCP のサーバーと管理者の既定(0024 の 12-1b)のテスト。
// MCP のサーバーは、aibow の自作のサーバー(時刻・計算)を httptest で HTTP に出したもの(プロセスを起動しない)。
// aibow-server の server.json には、名前 web で URL を書いたことにする(ツールの名前は web__calculate)。

type mcpWeb struct {
	*testWeb
	prefs *aibow.ToolPrefs
}

// newMCPWeb は、MCP のサーバー web(HTTP)と wd(${workdir} を使う。ブラウザには出さない)を server.json に書いた aibow-server を立てる。
func newMCPWeb(t *testing.T) *mcpWeb {
	t.Helper()
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpserver.New(mcpserver.Options{}) }, nil)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	configs := map[string]client.ServerConfig{
		"web": {URL: srv.URL},
		"wd":  {Command: "never-started", Args: []string{"${workdir}"}},
	}
	m := client.New(configs, client.Options{Version: "0.0.1-test"})
	t.Cleanup(m.Close)
	prefs, err := aibow.LoadServerToolPrefs(filepath.Join(t.TempDir(), "server-tools.json"))
	if err != nil {
		t.Fatal(err)
	}
	w := newTestWeb(t, func(o *Options) { o.MCP, o.Prefs = m, prefs })
	return &mcpWeb{testWeb: w, prefs: prefs}
}

func webCall(name, expr string) []format.ToolCall {
	return []format.ToolCall{{Call: tool.Call{Name: name, Args: []tool.Arg{{Name: "expression", Value: expr}}}}}
}

// webConversation は、MCP のサーバー web を使う会話を作る。
func (w *mcpWeb) webConversation(t *testing.T, policies map[string]aibow.ToolPolicy) aibow.Conversation {
	t.Helper()
	c := w.newConversation(t)
	s := c.Settings
	s.MCPServers = []string{"web"}
	s.ToolPolicies = policies
	w.mustCall(t, nil, "UpdateConversationSettings", c.ID, s)
	c.Settings = s
	return c
}

// server.json の MCP のサーバーを、ブラウザの会話で使える。最初の送信で起動し、MCP のツールは確認する(決まりの既定)。
// ${workdir} を使うサーバーは出さない
func TestWebMCPServer(t *testing.T) {
	w := newMCPWeb(t)
	var servers []client.ServerInfo
	w.mustCall(t, &servers, "MCPServers")
	if len(servers) != 1 || servers[0].Name != "web" {
		t.Fatalf("ブラウザに出す MCP のサーバー: %+v", servers)
	}

	events := w.openEvents(t)
	c := w.webConversation(t, nil)
	w.eng.calls = [][]format.ToolCall{webCall("web__calculate", "6*7")}
	done := w.sendAsync(t, context.Background(), c.ID, "計算して")
	got := until(t, events, "confirm")
	req := got[len(got)-1].Confirm
	if req == nil || req.Call.Name != "web__calculate" || req.Preview.Server != "web" {
		t.Fatalf("MCP のツールの確認のカード: %+v", req)
	}
	w.mustCall(t, nil, "ConfirmTool", c.ID, req.ID, true, "", "")
	until(t, events, "done")
	<-done
	if r := w.toolResult(t, c.ID); r.Content != "42" || r.ToolResult.Server != "web" {
		t.Errorf("MCP のツールの結果: %q(サーバー %q)", r.Content, r.ToolResult.Server)
	}
	if names := strings.Join(w.eng.tools[0], ","); !strings.Contains(names, "web__calculate") || strings.Contains(names, "wd__") {
		t.Errorf("モデルに知らせたツール: %s", names)
	}

	// 起動したときに見えたツールを覚えるので、設定の画面の「ツール」に一覧が出る(誰でも見られる)
	var groups []aibow.ToolGroupState
	w.mustCall(t, &groups, "ToolDefaults")
	if len(groups) != 2 || groups[1].Server != "web" || len(groups[1].Tools) != 2 {
		t.Fatalf("ToolDefaults: %+v", groups)
	}
	for _, tl := range groups[0].Tools {
		if tl.NeedsWorkDir {
			t.Errorf("作業フォルダのツールを見せている: %s", tl.Name)
		}
	}
	var builtin []aibow.KnownTool
	w.mustCall(t, &builtin, "BuiltinTools")
	if len(builtin) != 2 {
		t.Errorf("BuiltinTools は時刻と計算だけ: %+v", builtin)
	}
}

// ツールの扱いの既定は、管理者だけが変えられる。ほかの人は見るだけで、ファイルの場所も見えない(0024 の決定事項 21・25)
func TestWebAdminToolDefaults(t *testing.T) {
	w := newMCPWeb(t)
	bob := w.addUser(t, "bob")
	w.user.Store(bob)

	var v aibow.ToolPrefsView
	w.mustCall(t, &v, "ToolPrefs")
	if v.Path != "" || !v.Ban {
		t.Errorf("管理者でない人に既定のファイルの場所を見せている: %+v", v)
	}
	for _, c := range []struct {
		name string
		args []any
	}{{"SetToolPolicy", []any{"web__calculate", "allow"}}, {"SetGroupPolicy", []any{"web", "off"}}, {"LoadToolList", []any{"web"}}} {
		if status, body := w.call(t, context.Background(), c.name, c.args...); status != http.StatusForbidden {
			t.Errorf("%s は管理者でなければ 403 のはず: %d %s", c.name, status, body)
		}
	}
	if pv := w.prefs.View(); len(pv.Tools) != 0 || len(pv.Servers) != 0 {
		t.Errorf("断ったのに変わっている: %+v", pv)
	}

	// 管理者は、一覧を読み直し、「確認しない」にできる。すると、利用者の会話でも聞かずに実行する
	w.admin.Store(true)
	w.mustCall(t, &v, "ToolPrefs")
	if v.Path == "" {
		t.Error("管理者には既定のファイルの場所を見せるはず")
	}
	w.mustCall(t, nil, "LoadToolList", "web")
	if known := w.prefs.View().Known["web"]; len(known) != 2 {
		t.Errorf("読み直した一覧: %+v", known)
	}
	w.mustCall(t, nil, "SetToolPolicy", "web__calculate", aibow.ToolAllow)
	w.admin.Store(false)

	events := w.openEvents(t)
	c := w.webConversation(t, nil)
	w.eng.calls = [][]format.ToolCall{webCall("web__calculate", "2+3")}
	w.mustCall(t, nil, "Send", c.ID, "計算して")
	for _, e := range until(t, events, "done") {
		if e.Type == "confirm" {
			t.Errorf("管理者が確認しないにしたのに聞いた: %+v", e.Confirm)
		}
	}
	if r := w.toolResult(t, c.ID); r.Content != "5" {
		t.Errorf("結果: %q", r.Content)
	}
}

// 管理者が「使わない」にしたツールとグループは禁止。利用者が会話で「使う」にしても、モデルに見せない(0024 の決定事項 22)
func TestWebAdminBan(t *testing.T) {
	w := newMCPWeb(t)
	w.admin.Store(true)
	w.mustCall(t, nil, "LoadToolList", "web")
	w.mustCall(t, nil, "SetToolPolicy", "calculate", aibow.ToolOff) // 組み込みの計算だけ
	w.mustCall(t, nil, "SetGroupPolicy", "web", aibow.ToolOff)      // サーバーごと
	w.admin.Store(false)

	events := w.openEvents(t)
	c := w.webConversation(t, map[string]aibow.ToolPolicy{"calculate": aibow.ToolOn, "web__calculate": aibow.ToolOn})
	var groups []aibow.ToolGroupState
	w.mustCall(t, &groups, "ToolStates", c.ID, c.Settings)
	for _, g := range groups {
		for _, tl := range g.Tools {
			banned := tl.Name == "calculate" || g.Server == "web"
			if banned && (tl.Policy != aibow.ToolOff || tl.From != aibow.PolicyFromAdmin) {
				t.Errorf("%s は管理者の禁止のはず: %+v", tl.Name, tl)
			}
		}
	}

	w.mustCall(t, nil, "Send", c.ID, "計算して")
	until(t, events, "done")
	names := w.eng.tools[len(w.eng.tools)-1]
	if slices.Contains(names, "calculate") || slices.ContainsFunc(names, func(n string) bool { return strings.HasPrefix(n, "web__") }) {
		t.Errorf("禁止したツールをモデルに見せている: %v", names)
	}
	if !slices.Contains(names, "get_current_time") {
		t.Errorf("禁止していないツールは見せるはず: %v", names)
	}
}
