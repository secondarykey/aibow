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
	"github.com/secondarykey/aibow/config"
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
	prefs *config.ToolPrefs
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
	prefs, err := config.LoadServerToolPrefs(filepath.Join(t.TempDir(), "server-tools.json"))
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
func (w *mcpWeb) webConversation(t *testing.T, policies map[string]config.ToolPolicy) aibow.Conversation {
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
	var builtin []config.KnownTool
	w.mustCall(t, &builtin, "BuiltinTools")
	if len(builtin) != 3 {
		t.Errorf("BuiltinTools は時刻・計算・URL を読むだけ: %+v", builtin)
	}
}

// ツールの扱いの既定は、管理者だけが変えられる。ほかの人は見るだけで、ファイルの場所も見えない(0024 の決定事項 21・25)
func TestWebAdminToolDefaults(t *testing.T) {
	w := newMCPWeb(t)
	bob := w.addUser(t, "bob")
	w.user.Store(bob)

	var v config.ToolPrefsView
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
	w.mustCall(t, nil, "SetToolPolicy", "web__calculate", config.ToolAllow)
	w.admin.Store(false)

	events := w.openEvents(t)
	c := w.webConversation(t, nil)
	w.eng.calls = [][]format.ToolCall{webCall("web__calculate", "2+3")}
	w.mustCall(t, nil, "Send", c.ID, "計算して", nil)
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
	w.mustCall(t, nil, "SetToolPolicy", "calculate", config.ToolOff) // 組み込みの計算だけ
	w.mustCall(t, nil, "SetGroupPolicy", "web", config.ToolOff)      // サーバーごと
	w.admin.Store(false)

	events := w.openEvents(t)
	c := w.webConversation(t, map[string]config.ToolPolicy{"calculate": config.ToolOn, "web__calculate": config.ToolOn})
	var groups []aibow.ToolGroupState
	w.mustCall(t, &groups, "ToolStates", c.ID, c.Settings)
	for _, g := range groups {
		for _, tl := range g.Tools {
			banned := tl.Name == "calculate" || g.Server == "web"
			if banned && (tl.Policy != config.ToolOff || tl.From != aibow.PolicyFromAdmin) {
				t.Errorf("%s は管理者の禁止のはず: %+v", tl.Name, tl)
			}
		}
	}

	w.mustCall(t, nil, "Send", c.ID, "計算して", nil)
	until(t, events, "done")
	names := w.eng.tools[len(w.eng.tools)-1]
	if slices.Contains(names, "calculate") || slices.ContainsFunc(names, func(n string) bool { return strings.HasPrefix(n, "web__") }) {
		t.Errorf("禁止したツールをモデルに見せている: %v", names)
	}
	if !slices.Contains(names, "get_current_time") {
		t.Errorf("禁止していないツールは見せるはず: %v", names)
	}
}

// URL を読むツール(fetch_url)は、管理者が「ツール」のタブでツールごとに有効にするまで使えない(0036 の決定事項 14)。
// 組み込みのグループを「確認しない」にしても有効にならない。有効にしたら、確認つきで使える
func TestWebFetchOptIn(t *testing.T) {
	w := newMCPWeb(t)
	events := w.openEvents(t)
	c := w.webConversation(t, map[string]config.ToolPolicy{"fetch_url": config.ToolOn})
	fetchState := func() aibow.ToolState {
		t.Helper()
		var groups []aibow.ToolGroupState
		w.mustCall(t, &groups, "ToolStates", c.ID, c.Settings)
		for _, tl := range groups[0].Tools {
			if tl.Name == "fetch_url" {
				return tl
			}
		}
		t.Fatal("組み込みのツールの一覧に fetch_url が無い")
		return aibow.ToolState{}
	}
	sees := func() bool {
		t.Helper()
		w.mustCall(t, nil, "Send", c.ID, "読んで", nil)
		until(t, events, "done")
		return slices.Contains(w.eng.tools[len(w.eng.tools)-1], "fetch_url")
	}

	if s := fetchState(); s.Policy != config.ToolOff || s.From != aibow.PolicyFromAdmin {
		t.Errorf("有効にする前は管理者の禁止のはず: %+v", s)
	}
	if sees() {
		t.Error("有効にする前に、モデルに見せている")
	}

	w.admin.Store(true)
	w.mustCall(t, nil, "SetGroupPolicy", "", config.ToolAllow)
	w.admin.Store(false)
	if s := fetchState(); s.Policy != config.ToolOff {
		t.Errorf("組み込みのグループの扱いで有効になった: %+v", s)
	}

	w.admin.Store(true)
	w.mustCall(t, nil, "SetToolPolicy", "fetch_url", config.ToolAsk)
	w.admin.Store(false)
	if s := fetchState(); s.Policy != config.ToolAsk {
		t.Errorf("有効にしたら確認するはず: %+v", s)
	}
	if !sees() {
		t.Error("有効にしたのに、モデルに見せていない")
	}
}
