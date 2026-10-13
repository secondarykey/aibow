package aibow

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"aibow/tool"
)

// fakeRemote は MCP のサーバー「fake」の代わり(本物は mcpclient.Manager)。Start で起動したことにして、ツールを出す。
// 会話で fake を選んでいなければ、起動もせず、ツールも出さない。
type fakeRemote struct {
	tools    []tool.Tool
	startErr error
	started  bool
	workDirs []string // Start に渡された作業フォルダ
}

func (r *fakeRemote) Start(_ context.Context, workDir string, use []string) ([]string, []error) {
	if !slices.Contains(use, "fake") {
		return nil, nil
	}
	r.workDirs = append(r.workDirs, workDir)
	if r.startErr != nil {
		return nil, []error{r.startErr}
	}
	if r.started {
		return nil, nil
	}
	r.started = true
	return []string{"fake"}, nil
}

func (r *fakeRemote) ListTools(_ context.Context, server string) ([]tool.Tool, error) {
	if server != "fake" {
		return nil, errors.New("知らないサーバー")
	}
	return r.tools, nil
}

func (r *fakeRemote) Tools(_ string, use []string) []tool.Tool {
	if !r.started || !slices.Contains(use, "fake") {
		return nil
	}
	return r.tools
}

// remoteTool は MCP のサーバーのツールの代わり。実行したら、受け取った配列の長さだけ x を返す。
type remoteTool struct {
	name string
	ran  bool
}

func (t *remoteTool) Def() tool.Def {
	return tool.Def{Name: t.name, Description: "Remote tool.\nSecond line.", Parameters: []byte(`{"type": "object", "properties": {"paths": {"type": "array"}}}`)}
}
func (t *remoteTool) Server() string { return "fake" }
func (t *remoteTool) Preview(args tool.Args) (tool.Preview, error) {
	return tool.Preview{Note: "MCP", Server: "fake", Args: "{}"}, nil
}
func (t *remoteTool) Run(_ context.Context, args tool.Args) (string, error) {
	t.ran = true
	p, _ := args["paths"].([]any)
	return strings.Repeat("x", len(p)), nil
}

// useFake は、会話で MCP のサーバー fake を使うようにする(新しい会話の既定は「使わない」)。
func useFake(t *testing.T, cs *Conversations, c *Conversation) {
	t.Helper()
	c.Settings.MCPServers = []string{"fake"}
	if err := cs.UpdateSettings(c.ID, c.Settings); err != nil {
		t.Fatal(err)
	}
}

func promptToolNames(call fakeCall) []string {
	var names []string
	for _, d := range call.tools {
		names = append(names, d.Name)
	}
	return names
}

// 送信の前にサーバーを起動し、そのツールをプロンプトに入れる。実行の前に確認し、結果にサーバーの名前を残す
func TestRemoteTools(t *testing.T) {
	g := &fakeGen{replies: []fakeReply{
		{toolCalls: []ToolCall{toolCall("remote_list", "paths", `["a", "b"]`)}},
		{content: "2つあります"},
	}}
	cs, c, dir := newWorkDirConversation(t, g)
	useFake(t, cs, &c)
	rt := &remoteTool{name: "remote_list"}
	clash := &remoteTool{name: "read_file"} // 組み込みのツールと同じ名前
	r := &fakeRemote{tools: []tool.Tool{rt, clash}}
	cs.SetRemoteTools(r)

	var req *ConfirmRequest
	evs := sendAnswering(cs, context.Background(), c, "並べて", func(q *ConfirmRequest) bool {
		req = q
		return cs.Confirm(c.ID, q.ID, true, "") == nil
	})
	if errs := errorsOf(evs); len(errs) != 0 {
		t.Fatalf("エラー: %v", errs)
	}
	if !slices.Equal(r.workDirs, []string{dir}) {
		t.Errorf("会話の作業フォルダで起動する: %v", r.workDirs)
	}

	names := promptToolNames(g.calls[0])
	if !slices.Contains(names, "remote_list") || slices.Index(names, "remote_list") < slices.Index(names, "run_command") {
		t.Errorf("MCP のツールは組み込みの後ろに並ぶ: %v", names)
	}
	if n := len(slices.DeleteFunc(slices.Clone(names), func(s string) bool { return s != "read_file" })); n != 1 {
		t.Errorf("名前が重なったツールは1つだけ(組み込み): %v", names)
	}

	if req == nil || req.Preview.Server != "fake" {
		t.Errorf("MCP のツールは確認する: %+v", req)
	}
	rs := toolResults(t, cs, c)
	if len(rs) != 1 || rs[0].Content != "xx" || rs[0].ToolResult.Server != "fake" || !rt.ran || clash.ran {
		t.Errorf("結果: %+v", rs)
	}
}

// 会話で選んでいないサーバーは、起動もせず、ツールも見せない(新しい会話の既定は「使わない」)
func TestRemoteToolsNotSelected(t *testing.T) {
	g := &fakeGen{content: "こんにちは"}
	cs, c, _ := newWorkDirConversation(t, g)
	r := &fakeRemote{tools: []tool.Tool{&remoteTool{name: "remote_list"}}}
	cs.SetRemoteTools(r)

	if len(c.Settings.MCPServers) != 0 {
		t.Errorf("新しい会話の既定は使わない: %v", c.Settings.MCPServers)
	}
	sendAnswering(cs, context.Background(), c, "やあ", func(*ConfirmRequest) bool { return false })
	if r.started || slices.Contains(promptToolNames(g.calls[0]), "remote_list") {
		t.Errorf("選んでいないサーバーは起動しない・見せない: %v", promptToolNames(g.calls[0]))
	}
}

// サーバーの名前を落として呼んだら、正しい名前を「〜のことですか?」でモデルに返す(勝手に読み替えない)
func TestRemoteToolDidYouMean(t *testing.T) {
	g := &fakeGen{replies: []fakeReply{
		{toolCalls: []ToolCall{toolCall("list_directory", "path", ".")}},
		{content: "呼び直します"},
	}}
	cs, c, _ := newWorkDirConversation(t, g)
	useFake(t, cs, &c)
	rt := &remoteTool{name: "filesystem__list_directory"}
	cs.SetRemoteTools(&fakeRemote{tools: []tool.Tool{rt}})

	evs := sendAnswering(cs, context.Background(), c, "並べて", func(*ConfirmRequest) bool { return false })
	if errs := errorsOf(evs); len(errs) != 0 {
		t.Fatalf("エラー: %v", errs)
	}
	rs := toolResults(t, cs, c)
	if len(rs) != 1 || !strings.Contains(rs[0].Content, "ツール list_directory はありません。filesystem__list_directory のことですか?") || rt.ran {
		t.Errorf("結果: %+v", rs)
	}
}

// 起動できないサーバーはお知らせに出し、そのサーバー無しで送信を続ける。ツールが OFF なら起動しない
func TestRemoteToolsStartError(t *testing.T) {
	g := &fakeGen{content: "こんにちは"}
	cs, _, c := newToolConversation(t, g)
	useFake(t, cs, &c)
	r := &fakeRemote{startErr: errors.New("MCP のサーバー fake を起動できません: 見つかりません")}
	cs.SetRemoteTools(r)

	var notices []string
	cs.Send(context.Background(), c.ID, "やあ", func(ev ChatEvent) {
		if ev.Type == "notice" {
			notices = append(notices, ev.Text)
		}
	})
	if len(notices) != 1 || !strings.Contains(notices[0], "起動できません") {
		t.Errorf("お知らせ: %v", notices)
	}
	if len(g.calls) != 1 {
		t.Errorf("送信は続ける: %d", len(g.calls))
	}

	c.Settings.Tools = false
	if err := cs.UpdateSettings(c.ID, c.Settings); err != nil {
		t.Fatal(err)
	}
	cs.Send(context.Background(), c.ID, "もう一度", func(ChatEvent) {})
	if len(r.workDirs) != 1 {
		t.Errorf("ツールが OFF の会話では起動しない: %v", r.workDirs)
	}
}

// ツールの扱い: 会話の上書き → PC の既定(ツール → サーバー)→ 確認する、の順に決まる
func TestToolPolicy(t *testing.T) {
	cs := &Conversations{}
	rt := &remoteTool{name: "fake__write"}
	prefs := &ToolPrefs{}
	cs.SetToolPrefs(prefs)
	set := ConversationSettings{}

	if got := cs.toolPolicy(set, rt); got != ToolAsk {
		t.Errorf("何も決めていなければ確認する: %v", got)
	}
	prefs.SetServerPolicy("fake", ToolAllow)
	if got := cs.toolPolicy(set, rt); got != ToolAllow {
		t.Errorf("サーバーごとの既定: %v", got)
	}
	prefs.SetToolPolicy("fake__write", ToolAsk)
	if got := cs.toolPolicy(set, rt); got != ToolAsk {
		t.Errorf("ツールごとの既定はサーバーごとより優先: %v", got)
	}
	set.ToolPolicies = map[string]ToolPolicy{"fake__write": ToolOff}
	if got := cs.toolPolicy(set, rt); got != ToolOff {
		t.Errorf("会話の上書きが一番優先: %v", got)
	}
}

// 「使わない」はモデルに見せず、「確認しない」は聞かずに実行する。会話A で許し、会話B では許さない、ができる
func TestToolPolicyInConversations(t *testing.T) {
	g := &fakeGen{replies: []fakeReply{
		{toolCalls: []ToolCall{toolCall("fake__write", "paths", `["a"]`)}},
		{content: "書きました"},
		{toolCalls: []ToolCall{toolCall("fake__write", "paths", `["a"]`)}},
		{content: "書きました"},
	}}
	cs, a, _ := newWorkDirConversation(t, g)
	write := &remoteTool{name: "fake__write"}
	hidden := &remoteTool{name: "fake__screenshot"}
	cs.SetRemoteTools(&fakeRemote{tools: []tool.Tool{write, hidden}})
	prefs := &ToolPrefs{}
	prefs.SetToolPolicy("fake__screenshot", ToolOff) // PC の既定で使わない
	cs.SetToolPrefs(prefs)

	// 会話A: write を「確認しない」に上書きした
	a.Settings.ToolPolicies = map[string]ToolPolicy{"fake__write": ToolAllow}
	useFake(t, cs, &a)
	asked := 0
	sendAnswering(cs, context.Background(), a, "書いて", func(q *ConfirmRequest) bool {
		asked++
		return cs.Confirm(a.ID, q.ID, true, "") == nil
	})
	if asked != 0 || !write.ran {
		t.Errorf("会話A は聞かずに実行する: 聞いた回数 %d, 実行 %v", asked, write.ran)
	}
	if names := promptToolNames(g.calls[0]); slices.Contains(names, "fake__screenshot") || !slices.Contains(names, "fake__write") {
		t.Errorf("使わないツールはモデルに見せない: %v", names)
	}

	// 会話B: 何も決めていない(PC の既定 = 確認する)
	b := mustCreate(t, cs)
	b.Settings.Tools = true
	useFake(t, cs, &b)
	write.ran = false
	sendAnswering(cs, context.Background(), b, "書いて", func(q *ConfirmRequest) bool {
		asked++
		return cs.Confirm(b.ID, q.ID, false, "書かないで") == nil
	})
	if asked != 1 || write.ran {
		t.Errorf("会話B は聞く(拒否したので実行しない): 聞いた回数 %d, 実行 %v", asked, write.ran)
	}
}

// 起動したサーバーのツールの一覧を、PC の既定(tools.json)に覚える(設定の画面に出すため)
func TestRememberTools(t *testing.T) {
	g := &fakeGen{content: "こんにちは"}
	cs, c, _ := newWorkDirConversation(t, g)
	useFake(t, cs, &c)
	cs.SetRemoteTools(&fakeRemote{tools: []tool.Tool{&remoteTool{name: "fake__write"}}})
	path := filepath.Join(t.TempDir(), "tools.json")
	prefs, err := LoadToolPrefs(path)
	if err != nil {
		t.Fatal(err)
	}
	cs.SetToolPrefs(prefs)

	sendAnswering(cs, context.Background(), c, "やあ", func(*ConfirmRequest) bool { return false })
	again, err := LoadToolPrefs(path)
	if err != nil {
		t.Fatal(err)
	}
	known := again.View().Known["fake"]
	if len(known) != 1 || known[0].Name != "fake__write" || known[0].Description != "Remote tool." {
		t.Errorf("覚えた一覧(説明は1行目だけ): %+v", known)
	}
}

// 設定の画面のために、サーバーを一時的に起動してツールの一覧を覚える(会話で使う前に)
func TestLoadToolList(t *testing.T) {
	cs := &Conversations{}
	cs.SetRemoteTools(&fakeRemote{tools: []tool.Tool{&remoteTool{name: "fake__write"}}})
	prefs := &ToolPrefs{}
	cs.SetToolPrefs(prefs)
	if err := cs.LoadToolList(context.Background(), "fake"); err != nil {
		t.Fatal(err)
	}
	if known := prefs.View().Known["fake"]; len(known) != 1 || known[0].Name != "fake__write" {
		t.Errorf("覚えた一覧: %+v", known)
	}
	if err := cs.LoadToolList(context.Background(), "other"); err == nil {
		t.Error("起動できなければエラー")
	}
}

// tools.json の読み書き。壊れたファイルは読まずに、書き換えもしない(人が書いたものを消さないため)
func TestToolPrefsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aibow", "tools.json")
	p, err := LoadToolPrefs(path)
	if err != nil {
		t.Fatalf("ファイルが無ければ空: %v", err)
	}
	if err := p.SetToolPolicy("Blender__render", ToolOff); err != nil {
		t.Fatal(err)
	}
	if err := p.SetToolPolicy("Blender__render", "maybe"); err == nil {
		t.Error("知らない値はエラー")
	}
	again, err := LoadToolPrefs(path)
	if got, ok := again.policy("Blender", "Blender__render"); err != nil || !ok || got != ToolOff {
		t.Errorf("書いたものを読める: %v %v %v", got, ok, err)
	}
	if err := again.SetToolPolicy("Blender__render", ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := again.policy("Blender", "Blender__render"); ok {
		t.Error("空にすると決めていない状態に戻る")
	}

	broken := filepath.Join(t.TempDir(), "tools.json")
	os.WriteFile(broken, []byte(`{"tools": {"x": "sometimes"}}`), 0o644)
	bp, err := LoadToolPrefs(broken)
	if err == nil || bp == nil || bp.View().Error == "" {
		t.Fatalf("壊れたファイルはエラーを返す: %v", err)
	}
	if err := bp.SetToolPolicy("x", ToolOff); err == nil {
		t.Error("読めなかったファイルは書き換えない")
	}
	if b, _ := os.ReadFile(broken); string(b) != `{"tools": {"x": "sometimes"}}` {
		t.Errorf("ファイルはそのまま: %s", b)
	}
}
