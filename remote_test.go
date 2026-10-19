package aibow

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/secondarykey/aibow/format"
	"github.com/secondarykey/aibow/tool"
)

// fakeRemote は MCP のサーバー「fake」の代わり(本物は mcp/client の Manager)。Start で起動したことにして、ツールを出す。
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

func (r *fakeRemote) Servers() []string { return []string{"fake"} }

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
		{toolCalls: []format.ToolCall{toolCall("remote_list", "paths", `["a", "b"]`)}},
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

// 会話で選んでいない(外した)サーバーは、起動もせず、ツールも見せない
func TestRemoteToolsNotSelected(t *testing.T) {
	g := &fakeGen{content: "こんにちは"}
	cs, c, _ := newWorkDirConversation(t, g) // MCP のサーバーをつなぐ前に作った会話なので、何も選んでいない
	r := &fakeRemote{tools: []tool.Tool{&remoteTool{name: "remote_list"}}}
	cs.SetRemoteTools(r)

	if len(c.Settings.MCPServers) != 0 {
		t.Errorf("何も選んでいない: %v", c.Settings.MCPServers)
	}
	sendAnswering(cs, context.Background(), c, "やあ", func(*ConfirmRequest) bool { return false })
	if r.started || slices.Contains(promptToolNames(g.calls[0]), "remote_list") {
		t.Errorf("選んでいないサーバーは起動しない・見せない: %v", promptToolNames(g.calls[0]))
	}
}

// 新しい会話では、プロファイルの「使うツールのグループ」(設定の画面の「ツール」のチェック)のサーバーを最初から選ぶ。
// プロファイルに書いてあっても、settings.json に無いサーバーは無視する。
// 「新しい会話の既定」を保存しても、使うグループは変わらない(決める場所は「ツール」だけ)
func TestDefaultServers(t *testing.T) {
	cs, st := newTestConversations(t, &fakeGen{})
	cs.SetRemoteTools(&fakeRemote{})

	if got := mustCreate(t, cs).Settings.MCPServers; len(got) != 0 {
		t.Errorf("プロファイルに書いていないサーバー(足したばかり)は最初から選ばない: %v", got)
	}
	if err := cs.SetDefaultGroup("fake", true); err != nil {
		t.Fatal(err)
	}
	if err := cs.SetDefaultGroup("gone", true); err != nil { // settings.json に無いサーバー
		t.Fatal(err)
	}
	if got := mustCreate(t, cs).Settings.MCPServers; !slices.Equal(got, []string{"fake"}) {
		t.Errorf("「ツール」でチェックしたサーバーだけを最初から選ぶ: %v", got)
	}
	cs.SetDefaultGroup("fake", false)
	cs.SetDefaultGroup("", false)
	if d := cs.Defaults(); len(d.MCPServers) != 0 || !d.BuiltinOff {
		t.Errorf("「ツール」でチェックを外したものは、新しい会話で最初から選ばない: %+v", d)
	}
	cs.SetDefaultGroup("", true)
	if d := cs.Defaults(); d.BuiltinOff {
		t.Error("チェックを戻すと選ぶ")
	}
	cs.SetDefaultGroup("fake", true)
	set := cs.Defaults()
	set.MCPServers, set.BuiltinOff = []string{}, true
	if err := cs.SetDefaults(set); err != nil {
		t.Fatal(err)
	}
	if d := cs.Defaults(); !slices.Equal(d.MCPServers, []string{"fake"}) || d.BuiltinOff {
		t.Errorf("既定を保存しても、使うグループは変わらない: %+v", d)
	}
	// グループのチェックはプロファイルに保存する
	if ps, _ := st.ListProfiles(); len(ps) != 1 || !slices.Equal(ps[0].Values.MCPServers, []string{"fake", "gone"}) {
		t.Errorf("プロファイルに保存する: %+v", ps)
	}
}

// サーバーの名前を落として呼んだら、正しい名前を「〜のことですか?」でモデルに返す(勝手に読み替えない)
func TestRemoteToolDidYouMean(t *testing.T) {
	g := &fakeGen{replies: []fakeReply{
		{toolCalls: []format.ToolCall{toolCall("list_directory", "path", ".")}},
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

// 起動できないサーバーはお知らせに出し、そのサーバー無しで送信を続ける
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
		{toolCalls: []format.ToolCall{toolCall("fake__write", "paths", `["a"]`)}},
		{content: "書きました"},
		{toolCalls: []format.ToolCall{toolCall("fake__write", "paths", `["a"]`)}},
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

// 組み込みのツールも同じ仕組みで扱う。決まりの既定は今までどおり(書き換えるツールは確認する、読むだけのツールは確認しない)。
// グループごと「使わない」なら、ツールごとの扱いにかかわらず使わない
func TestLocalToolPolicy(t *testing.T) {
	cs := &Conversations{}
	prefs := &ToolPrefs{}
	cs.SetToolPrefs(prefs)
	set := ConversationSettings{}
	write, read := tool.Files(".")[2], tool.Files(".")[1]
	if write.Def().Name != "write_file" || read.Def().Name != "read_file" {
		t.Fatalf("並び: %s %s", write.Def().Name, read.Def().Name)
	}
	if cs.toolPolicy(set, write) != ToolAsk || cs.toolPolicy(set, read) != ToolAllow {
		t.Error("決まりの既定: 書き換えるツールは確認する、読むだけのツールは確認しない")
	}
	prefs.SetToolPolicy("write_file", ToolAllow)
	if cs.toolPolicy(set, write) != ToolAllow {
		t.Error("ツールごとの PC の既定")
	}
	prefs.SetBuiltinPolicy(ToolOff)
	if cs.toolPolicy(set, write) != ToolOff || cs.toolPolicy(set, read) != ToolOff {
		t.Error("グループごと使わないなら、ツールごとの扱いにかかわらず使わない")
	}
	set.ToolPolicies = map[string]ToolPolicy{"read_file": ToolAsk}
	if cs.toolPolicy(set, read) != ToolAsk {
		t.Error("会話の上書きが一番優先")
	}
	if names := BuiltinTools(); len(names) != 7 || names[0].Name != "get_current_time" || names[6].Name != "run_command" {
		t.Errorf("組み込みのツールの一覧: %+v", names)
	}
}

// 組み込みのツールも「使わない」ならモデルに見せない。読むだけのツールを「確認する」にすると、引数を見せて確認する。
// サーバーごと「使わない」にした MCP のサーバーは、会話で選んであっても起動しない
func TestLocalToolPolicyInConversation(t *testing.T) {
	g := &fakeGen{replies: []fakeReply{
		{toolCalls: []format.ToolCall{toolCall("read_file", "path", "main.go")}},
		{content: "読みました"},
	}}
	cs, c, _ := newWorkDirConversation(t, g)
	useFake(t, cs, &c)
	r := &fakeRemote{tools: []tool.Tool{&remoteTool{name: "fake__write"}}}
	cs.SetRemoteTools(r)
	prefs := &ToolPrefs{}
	prefs.SetToolPolicy("run_command", ToolOff)
	prefs.SetToolPolicy("read_file", ToolAsk)
	prefs.SetServerPolicy("fake", ToolOff)
	cs.SetToolPrefs(prefs)

	var req *ConfirmRequest
	sendAnswering(cs, context.Background(), c, "読んで", func(q *ConfirmRequest) bool {
		req = q
		return cs.Confirm(c.ID, q.ID, true, "") == nil
	})
	names := promptToolNames(g.calls[0])
	if slices.Contains(names, "run_command") || !slices.Contains(names, "read_file") || slices.Contains(names, "fake__write") {
		t.Errorf("使わないツール・サーバーは見せない: %v", names)
	}
	if r.started {
		t.Error("サーバーごと使わないなら起動しない")
	}
	if req == nil || req.Preview.Note != "確認" || !strings.Contains(req.Preview.Args, `"path": "main.go"`) {
		t.Errorf("読むだけのツールの確認は引数を見せる: %+v", req)
	}
	if rs := toolResults(t, cs, c); len(rs) != 1 || rs[0].ToolResult.Error || !strings.Contains(rs[0].Content, "package main") {
		t.Errorf("確認して実行した: %+v", rs)
	}
}

// 会話ごとに組み込みのツールを外せる(入力欄の上の「この会話のツール」)。MCP のツールはそのまま使う
func TestBuiltinOffInConversation(t *testing.T) {
	g := &fakeGen{content: "はい"}
	cs, c, _ := newWorkDirConversation(t, g)
	useFake(t, cs, &c)
	cs.SetRemoteTools(&fakeRemote{tools: []tool.Tool{&remoteTool{name: "fake__write"}}})
	c.Settings.BuiltinOff = true
	if err := cs.UpdateSettings(c.ID, c.Settings); err != nil {
		t.Fatal(err)
	}
	sendAnswering(cs, context.Background(), c, "やあ", func(*ConfirmRequest) bool { return false })
	if names := promptToolNames(g.calls[0]); !slices.Equal(names, []string{"fake__write"}) {
		t.Errorf("組み込みのツールは外し、MCP のツールは使う: %v", names)
	}
	if strings.Contains(g.calls[0].prompt, "Shell for run_command") {
		t.Error("run_command を使わないので、シェルの行は書かない")
	}
	if d := cs.Defaults(); d.BuiltinOff {
		t.Error("新しい会話では組み込みのツールを使う")
	}
}

// 前の名前(「ローカル」と呼んでいたころ)の tools.json(local のキー)も読める。
// 前の画面で書いたグループごとの「使わない」は、「新しい会話で最初から選ばない」に読み替える(使えなくはしない)
func TestToolPrefsLegacy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tools.json")
	os.WriteFile(path, []byte(`{"local": "allow", "servers": {"Blender": "off", "filesystem": "allow"}}`), 0o644)
	p, err := LoadToolPrefs(path)
	if got, ok := p.groupPolicy(""); err != nil || !ok || got != ToolAllow {
		t.Fatalf("local を builtin として読む: %v %v %v", got, ok, err)
	}
	if _, ok := p.groupPolicy("Blender"); ok || p.groupDefault("Blender") || !p.groupDefault("filesystem") {
		t.Error("グループの off は「最初から選ばない」に読み替える")
	}
	if err := p.SetToolPolicy("x", ToolOff); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), `"builtin": "allow"`) || strings.Contains(string(b), `"local"`) || !strings.Contains(string(b), `"notDefault"`) {
		t.Errorf("書くときは新しい形にする: %s", b)
	}
	os.WriteFile(path, []byte(`{"builtin": "off"}`), 0o644)
	if p, _ := LoadToolPrefs(path); p.groupDefault("") {
		t.Error("組み込みの off も読み替える")
	}
}

// 「ツール」でチェックを外した(最初から選ばない)サーバーでも、会話で選べば使う
func TestNotDefaultServerCanBeSelected(t *testing.T) {
	g := &fakeGen{content: "はい"}
	cs, c, _ := newWorkDirConversation(t, g)
	r := &fakeRemote{tools: []tool.Tool{&remoteTool{name: "fake__write"}}}
	cs.SetRemoteTools(r)
	cs.SetDefaultGroup("fake", false)
	useFake(t, cs, &c)
	sendAnswering(cs, context.Background(), c, "やあ", func(*ConfirmRequest) bool { return false })
	if !r.started || !slices.Contains(promptToolNames(g.calls[0]), "fake__write") {
		t.Errorf("会話で選べば使う: 起動 %v, %v", r.started, promptToolNames(g.calls[0]))
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

// 会話の設定の画面に出すツールの扱いは、toolPolicy と同じ決め方で、どこで決まったかも分かる。
// MCP のサーバーのツールは、起動していなくても、覚えた一覧(tools.json の known)から出す
func TestToolStates(t *testing.T) {
	cs := &Conversations{}
	prefs := &ToolPrefs{}
	cs.SetToolPrefs(prefs)
	cs.SetRemoteTools(&fakeRemote{})
	prefs.SetToolPolicy("read_file", ToolOff)
	if err := prefs.setKnown("fake", []KnownTool{{Name: "fake__write", Description: "Remote tool."}}); err != nil {
		t.Fatal(err)
	}
	set := ConversationSettings{ToolPolicies: map[string]ToolPolicy{"fake__write": ToolOff}}

	groups := cs.ToolStates(set)
	if len(groups) != 2 || groups[0].Server != "" || groups[1].Server != "fake" {
		t.Fatalf("グループ: %+v", groups)
	}
	find := func(g ToolGroupState, name string) ToolState {
		for _, s := range g.Tools {
			if s.Name == name {
				return s
			}
		}
		t.Fatalf("%s がありません", name)
		return ToolState{}
	}
	if s := find(groups[0], "get_current_time"); s.Policy != ToolAllow || s.From != PolicyFromDefault || s.NeedsWorkDir {
		t.Errorf("決まりの既定(読むだけ): %+v", s)
	}
	if s := find(groups[0], "write_file"); s.Policy != ToolAsk || s.From != PolicyFromDefault || !s.NeedsWorkDir {
		t.Errorf("決まりの既定(書き換える・作業フォルダが要る): %+v", s)
	}
	if s := find(groups[0], "read_file"); s.Policy != ToolOff || s.From != PolicyFromPC {
		t.Errorf("PC の既定: %+v", s)
	}
	if s := find(groups[1], "fake__write"); s.Policy != ToolOff || s.From != PolicyFromConversation || s.Description != "Remote tool." {
		t.Errorf("会話の上書き: %+v", s)
	}
}

// PC の既定の「使わない」は「既定では使わない」の意味で、会話で「使う」(on)にすれば使える。
// 確認するかは変えない(PC の既定のツールごと・グループごとの確認の扱い → 決まりの既定)
func TestToolOnOverridesPCOff(t *testing.T) {
	cs := &Conversations{}
	prefs := &ToolPrefs{}
	cs.SetToolPrefs(prefs)
	prefs.SetToolPolicy("get_current_time", ToolOff)
	prefs.SetServerPolicy("fake", ToolAllow)
	prefs.SetToolPolicy("fake__write", ToolOff)
	set := ConversationSettings{}

	now := tool.Builtin()[0]
	if now.Def().Name != "get_current_time" {
		t.Fatalf("並び: %s", now.Def().Name)
	}
	if cs.toolPolicy(set, now) != ToolOff {
		t.Fatal("PC の既定で使わないツールは、会話で何もしなければ使わない")
	}
	set.ToolPolicies = map[string]ToolPolicy{"get_current_time": ToolOn, "fake__write": ToolOn}
	if got := cs.toolPolicy(set, now); got != ToolAllow {
		t.Errorf("会話で使うにしたら使う。読むだけの組み込みのツールは決まりの既定(確認しない)のまま: %v", got)
	}
	if got := cs.toolPolicy(set, &remoteTool{name: "fake__write"}); got != ToolAllow {
		t.Errorf("確認の扱いはサーバーごとの PC の既定から取る: %v", got)
	}

	// グループごと使わないにした組み込みのツールも、会話で使うにできる。書き換えるツールは確認する
	prefs.SetBuiltinPolicy(ToolOff)
	set.ToolPolicies = map[string]ToolPolicy{"write_file": ToolOn}
	if got := cs.toolPolicy(set, tool.Files(".")[2]); got != ToolAsk {
		t.Errorf("書き換えるツールは確認する: %v", got)
	}
	if err := set.validate(); err != nil {
		t.Errorf("on は会話の設定に保存できる: %v", err)
	}

	states := cs.ToolStates(set)
	for _, s := range states[0].Tools {
		if s.Name == "write_file" && (s.Policy != ToolAsk || s.From != PolicyFromConversation || s.Default != ToolOff) {
			t.Errorf("画面に出す状態(上書きが無ければ使わない): %+v", s)
		}
	}
}

// settings.json に足したばかりの MCP のサーバーは、新しい会話で最初から使わない。
// 一度見たサーバーは、settings.json から消して戻しても前の扱いのまま。
// この仕組みより前の tools.json(seen が無い)なら、今あるサーバーの扱いは変えない
func TestNoteServers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tools.json")
	p, _ := LoadToolPrefs(path)
	if added, err := p.NoteServers(nil); err != nil || added != nil {
		t.Fatalf("サーバーが無ければ何もしない: %v %v", added, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("サーバーが無ければ tools.json を作らない: %v", err)
	}
	added, err := p.NoteServers([]string{"filesystem"})
	if err != nil || !slices.Equal(added, []string{"filesystem"}) || p.groupDefault("filesystem") {
		t.Fatalf("足したばかりのサーバーは最初から使わない: %v %v", added, err)
	}
	p.update(func(f *toolsFile) { f.NotDefault.Servers = nil }, "") // 使う人がチェックした(プロファイルより前の画面)

	again, _ := LoadToolPrefs(path)
	added, _ = again.NoteServers([]string{"Blender", "filesystem"})
	if !slices.Equal(added, []string{"Blender"}) || !again.groupDefault("filesystem") || again.groupDefault("Blender") {
		t.Errorf("見たサーバーはそのまま、足したサーバーだけ使わない: %v", added)
	}
	again.NoteServers([]string{"Blender"}) // filesystem を settings.json から消した
	if added, _ := again.NoteServers([]string{"Blender", "filesystem"}); len(added) != 0 || !again.groupDefault("filesystem") {
		t.Errorf("消して戻しても前の扱いのまま: %v", added)
	}

	// この仕組みより前のファイル: 今あるサーバーは全部見たことにする(最初から使っていたものを急に外さない)
	old := filepath.Join(t.TempDir(), "tools.json")
	os.WriteFile(old, []byte(`{"notDefault":{"servers":["Blender"]}}`), 0o644)
	q, err := LoadToolPrefs(old)
	if err != nil {
		t.Fatal(err)
	}
	if added, _ := q.NoteServers([]string{"Blender", "filesystem"}); len(added) != 0 || !q.groupDefault("filesystem") || q.groupDefault("Blender") {
		t.Errorf("前のファイルは扱いを変えない: %v", added)
	}
	q2, _ := LoadToolPrefs(old)
	if added, _ := q2.NoteServers([]string{"Blender", "filesystem", "fetch"}); !slices.Equal(added, []string{"fetch"}) || q2.groupDefault("fetch") {
		t.Errorf("次の起動からは、足したサーバーを使わない: %v", added)
	}
}

// 確認の答えで「これから先の扱い」も答えると、会話の上書きに書き、同じ送信の次の呼び出しから効く(0010)。
// allow(この会話では今後聞かない)なら次は聞かずに実行し、off(今後許可しない)なら次は「ありません」を返す
func TestConfirmRemember(t *testing.T) {
	for _, tc := range []struct {
		name     string
		remember ToolPolicy
		asked    int
		ran      bool // 2回目を実行したか
	}{
		{"この会話では今後聞かない", ToolAllow, 1, true},
		{"今後許可しない", ToolOff, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := &fakeGen{replies: []fakeReply{
				{toolCalls: []format.ToolCall{toolCall("fake__write", "paths", `["a"]`)}},
				{toolCalls: []format.ToolCall{toolCall("fake__write", "paths", `["b", "c"]`)}},
				{content: "書きました"},
			}}
			cs, c, _ := newWorkDirConversation(t, g)
			write := &remoteTool{name: "fake__write"}
			cs.SetRemoteTools(&fakeRemote{tools: []tool.Tool{write}})
			cs.SetToolPrefs(&ToolPrefs{})
			useFake(t, cs, &c)

			asked := 0
			evs := sendAnswering(cs, context.Background(), c, "書いて", func(q *ConfirmRequest) bool {
				asked++
				approved := tc.remember != ToolOff
				return cs.Answer(c.ID, q.ID, Confirmation{Approved: approved, Remember: tc.remember}) == nil
			})
			if asked != tc.asked {
				t.Errorf("聞いた回数: %d", asked)
			}
			_, ms, _ := cs.Get(c.ID)
			second := ms[4] // user, assistant, tool, assistant, tool
			if second.Role != "tool" || (second.Content == "xx") != tc.ran {
				t.Errorf("2回目の結果: %q(実行したはず: %v)%v", second.Content, tc.ran, errorsOf(evs))
			}
			if !tc.ran && !strings.Contains(second.Content, "ありません") {
				t.Errorf("隠したツールは「ありません」: %q", second.Content)
			}
			if !tc.ran && !strings.Contains(ms[2].Content, "今後使えません") {
				t.Errorf("今後許可しないなら、拒否の結果で伝える: %q", ms[2].Content)
			}
			if got, _, _ := cs.Get(c.ID); got.Settings.ToolPolicies["fake__write"] != tc.remember {
				t.Errorf("会話の上書きに書く: %v", got.Settings.ToolPolicies)
			}
			if first := ms[2].ToolResult.Confirm; first == nil || first.Remember != tc.remember {
				t.Errorf("答えに残す: %+v", first)
			}
		})
	}
}

// 会話の上書きを1つだけ書き換える。知らない値は書かない。空なら上書きを消す
func TestSetToolPolicy(t *testing.T) {
	cs, _ := newTestConversations(t, &fakeGen{})
	c := mustCreate(t, cs)
	if err := cs.SetToolPolicy(c.ID, "write_file", ToolAllow); err != nil {
		t.Fatal(err)
	}
	if err := cs.SetToolPolicy(c.ID, "write_file", "yes"); err == nil {
		t.Error("知らない値は書かない")
	}
	if got, _, _ := cs.Get(c.ID); got.Settings.ToolPolicies["write_file"] != ToolAllow {
		t.Errorf("書いた値: %v", got.Settings.ToolPolicies)
	}
	cs.SetToolPolicy(c.ID, "write_file", "")
	if got, _, _ := cs.Get(c.ID); len(got.Settings.ToolPolicies) != 0 {
		t.Errorf("空なら消す: %v", got.Settings.ToolPolicies)
	}
	p := &ToolPrefs{}
	if err := p.SetGroupPolicy("fake", ToolOff); err == nil {
		t.Error("グループの扱いは確認する・確認しないだけ")
	}
	if err := p.SetGroupPolicy("", ToolAllow); err != nil || p.View().Builtin != ToolAllow {
		t.Errorf("組み込みのグループ: %v %v", err, p.View().Builtin)
	}
}

// askingTool は、実行の途中でユーザーに確認を求める MCP のツールの代わり(aibow-mcp の書き換えるツール。tool.Interactive)。
// 確認には差分を見せ、受け入れられたら「書きました」、拒否されたら拒否の文を返す(サーバーと同じ)
type askingTool struct {
	remoteTool
	asked int
}

func (t *askingTool) RunInteractive(ctx context.Context, args tool.Args, ask tool.Ask) (string, error) {
	t.asked++
	a, err := ask(ctx, tool.Preview{Path: "a.txt", Note: "上書き", Diff: "+hello\n"})
	if err != nil {
		return "", err
	}
	if !a.Approved {
		return tool.RejectedText(a.Reason), nil
	}
	t.ran = true
	return "書きました", nil
}

// サーバーからの確認は、今の確認のカード(差分)で出す。settings.json のサーバーは、呼ぶ前の確認(引数)も出す(2回)。
// 答えはカードに残し、確認を待った時間は実行の秒数に入れない
func TestServerAsks(t *testing.T) {
	g := &fakeGen{replies: []fakeReply{
		{toolCalls: []format.ToolCall{toolCall("fake__write", "paths", `["a"]`)}},
		{content: "書きました"},
	}}
	cs, c, _ := newWorkDirConversation(t, g)
	write := &askingTool{remoteTool: remoteTool{name: "fake__write"}}
	cs.SetRemoteTools(&fakeRemote{tools: []tool.Tool{write}})
	cs.SetToolPrefs(&ToolPrefs{})
	useFake(t, cs, &c)

	var reqs []*ConfirmRequest
	evs := sendAnswering(cs, context.Background(), c, "書いて", func(q *ConfirmRequest) bool {
		reqs = append(reqs, q)
		time.Sleep(300 * time.Millisecond) // ユーザーがカードを読んでいる時間
		return cs.Confirm(c.ID, q.ID, true, "") == nil
	})
	if errs := errorsOf(evs); len(errs) != 0 {
		t.Fatalf("エラー: %v", errs)
	}
	if len(reqs) != 2 || reqs[0].Preview.Server != "fake" || reqs[1].Preview.Diff != "+hello\n" {
		t.Fatalf("呼ぶ前の確認(引数)とサーバーからの確認(差分)の2回: %+v", reqs)
	}
	rs := toolResults(t, cs, c)
	if len(rs) != 1 || rs[0].Content != "書きました" || !write.ran {
		t.Fatalf("結果: %+v", rs)
	}
	if rs[0].ToolResult.Confirm == nil || !rs[0].ToolResult.Confirm.Approved {
		t.Errorf("サーバーからの確認の答えを残す: %+v", rs[0].ToolResult.Confirm)
	}
	if sec := rs[0].ToolResult.Sec; sec >= 0.3 {
		t.Errorf("確認を待った時間は実行の秒数に入れない: %.2f 秒", sec)
	}
}

// 「確認しない」の扱いなら、呼ぶ前もサーバーからの確認も聞かずに受け入れる
func TestServerAsksAllowed(t *testing.T) {
	g := &fakeGen{replies: []fakeReply{
		{toolCalls: []format.ToolCall{toolCall("fake__write", "paths", `["a"]`)}},
		{content: "書きました"},
	}}
	cs, c, _ := newWorkDirConversation(t, g)
	write := &askingTool{remoteTool: remoteTool{name: "fake__write"}}
	cs.SetRemoteTools(&fakeRemote{tools: []tool.Tool{write}})
	prefs := &ToolPrefs{}
	prefs.SetServerPolicy("fake", ToolAllow)
	cs.SetToolPrefs(prefs)
	useFake(t, cs, &c)

	asked := 0
	sendAnswering(cs, context.Background(), c, "書いて", func(*ConfirmRequest) bool { asked++; return false })
	if asked != 0 || write.asked != 1 || !write.ran {
		t.Errorf("聞かずに受け入れる: カード %d 回・サーバーの確認 %d 回・実行 %v", asked, write.asked, write.ran)
	}
}

// サーバーからの確認を「今後許可しない」で拒否したら、拒否の文に「今後使えません」を添え、会話の上書きを off にする
func TestServerAsksRememberOff(t *testing.T) {
	g := &fakeGen{replies: []fakeReply{
		{toolCalls: []format.ToolCall{toolCall("fake__write", "paths", `["a"]`)}},
		{content: "やめました"},
	}}
	cs, c, _ := newWorkDirConversation(t, g)
	write := &askingTool{remoteTool: remoteTool{name: "fake__write"}}
	cs.SetRemoteTools(&fakeRemote{tools: []tool.Tool{write}})
	cs.SetToolPrefs(&ToolPrefs{})
	useFake(t, cs, &c)

	n := 0
	sendAnswering(cs, context.Background(), c, "書いて", func(q *ConfirmRequest) bool {
		n++
		if n == 1 {
			return cs.Confirm(c.ID, q.ID, true, "") == nil // 呼ぶ前の確認は通す
		}
		return cs.Answer(c.ID, q.ID, Confirmation{Reason: "違う", Remember: ToolOff}) == nil
	})
	rs := toolResults(t, cs, c)
	if len(rs) != 1 || write.ran || !strings.Contains(rs[0].Content, "理由: 違う") || !strings.Contains(rs[0].Content, "今後使えません") {
		t.Fatalf("拒否の結果: %+v", rs)
	}
	if got, _, _ := cs.Get(c.ID); got.Settings.ToolPolicies["fake__write"] != ToolOff {
		t.Errorf("会話の上書きに書く: %v", got.Settings.ToolPolicies)
	}
}
