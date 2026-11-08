package aibow

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/secondarykey/aibow/config"
	"github.com/secondarykey/aibow/format"
	"github.com/secondarykey/aibow/tool"
)

// toolNamesOf はモデルに知らせたツールの名前
func toolNamesOf(defs []tool.Def) []string {
	var names []string
	for _, d := range defs {
		names = append(names, d.Name)
	}
	return names
}

// 作業フォルダを決めた会話だけ、ファイルのツールと環境の説明がプロンプトに入る。
// 環境の説明は、使うツールに合わせる(シェルの行は run_command を使うときだけ。作業フォルダを使うツールが無ければ書かない)
func TestWorkDirAddsFileToolsAndEnvironment(t *testing.T) {
	dir := t.TempDir()
	g := &fakeGen{content: "はい"}
	cs, _, c := newToolConversation(t, g)

	set := c.Settings
	prefs := &config.ToolPrefs{}
	cs.SetToolPrefs(prefs)
	steps := []struct {
		name      string
		workDir   string
		prefs     func()
		wantTools []string
		wantEnv   bool
		wantShell bool
	}{
		{"作業フォルダなし", "", func() {}, []string{"get_current_time", "calculate"}, false, false},
		{"作業フォルダあり", dir, func() {}, []string{"get_current_time", "calculate", "list_files", "read_file", "search_files", "write_file", "edit_file", "update_plan", "run_command"}, true, true},
		{"run_command を使わない", dir, func() { prefs.SetToolPolicy("run_command", config.ToolOff) },
			[]string{"get_current_time", "calculate", "list_files", "read_file", "search_files", "write_file", "edit_file", "update_plan"}, true, false},
		{"組み込みのツールを使わない", dir, func() { prefs.SetBuiltinPolicy(config.ToolOff) }, nil, false, false},
	}
	for i, s := range steps {
		set.Workspace = workspaceFor(t, cs, s.workDir)
		s.prefs()
		if err := cs.UpdateSettings(ctx, c.ID, set); err != nil {
			t.Fatal(err)
		}
		send(t, cs, c, "質問")
		call := g.calls[i]
		if got := toolNamesOf(call.tools); !slices.Equal(got, s.wantTools) {
			t.Errorf("%s: ツール %v(期待: %v)", s.name, got, s.wantTools)
		}
		if got := strings.Contains(call.prompt, "# Environment"); got != s.wantEnv {
			t.Errorf("%s: 環境の説明 %v(期待: %v)", s.name, got, s.wantEnv)
		}
		if got := strings.Contains(call.prompt, "Shell for run_command") || strings.Contains(call.prompt, "do not cd"); got != s.wantShell {
			t.Errorf("%s: シェルの行 %v(期待: %v)", s.name, got, s.wantShell)
		}
	}
	// 作業フォルダの無い会話に、前の会話のファイルのツールが残っていないこと(cs.tools を書き換えていない)
	if len(cs.tools) != 2 {
		t.Errorf("組み込みのツールが %d 個になっています", len(cs.tools))
	}
}

// 環境の説明は、システムプロンプトの後ろ・要約より前(会話の中で変わらないものを先に置き、KV キャッシュを再利用しやすくする)
func TestEnvironmentPlacement(t *testing.T) {
	cs, _ := newTestConversations(t, &fakeGen{})
	set := testDefaults
	work := filepath.Join(t.TempDir(), "example") // OS の書き方の絶対パス(Linux のテストでも名前が取れるように)
	if err := os.Mkdir(work, 0o755); err != nil {
		t.Fatal(err)
	}
	set.Workspace = workspaceFor(t, cs, work)
	msgs := promptMessages(cs.systemPrompt(set), "- 要約", nil)
	want := testDefaults.System + "\n\n# Environment\nWorking directory: " + work + "\nOS: " + runtime.GOOS +
		"\nShell for run_command: " + tool.ShellNoteFor(true) + "\nUse paths relative to the working directory.\n" +
		"Commands already run inside the working directory, so do not cd into it. \"example\" refers to the working directory itself.\n\n" +
		workingRules(cs.toolsFor(set)) + "\n\n" + // 作業の決まり(0041)も環境の説明に続けて、要約より前
		summaryHeading + "\n- 要約"
	if len(msgs) != 1 || msgs[0].Content != want {
		t.Errorf("system:\n%s\n期待:\n%s", msgs[0].Content, want)
	}
	// システムプロンプトが空でも、環境の説明だけで system を作る
	set.System = ""
	if got := cs.systemPrompt(set); !strings.HasPrefix(got, "# Environment\n") {
		t.Errorf("システムプロンプトが空のとき: %q", got)
	}
}

// モデルが read_file を呼ぶと、作業フォルダのファイルを読んだ結果が返り、続きを書く
func TestAgentReadsFileInWorkDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g := &fakeGen{replies: []fakeReply{
		{toolCalls: []format.ToolCall{toolCall("list_files")}},
		{toolCalls: []format.ToolCall{toolCall("read_file", "path", "main.go", "offset", "3")}}, // 数の引数も文字列で届く
		{content: "main 関数だけがあります"},
	}}
	cs, _, c := newToolConversation(t, g)
	set := c.Settings
	set.Workspace = workspaceFor(t, cs, dir)
	cs.UpdateSettings(ctx, c.ID, set)
	send(t, cs, c, "このフォルダに何がある?")

	_, ms, _ := cs.Get(ctx, c.ID)
	var results []string
	for _, m := range ms {
		if m.Role == "tool" {
			results = append(results, m.Content)
		}
	}
	if !slices.Equal(results, []string{"main.go", "3\tfunc main() {}"}) || ms[len(ms)-1].Content != "main 関数だけがあります" {
		t.Errorf("ツールの結果: %q / 最後: %q", results, ms[len(ms)-1].Content)
	}
}

// ワークスペースは新しい会話の既定に入れない(前の会話のフォルダを引き継がない)
func TestWorkspaceIsNotADefault(t *testing.T) {
	cs, _ := newTestConversations(t, &fakeGen{})
	set := testDefaults
	set.Workspace = workspaceFor(t, cs, t.TempDir())
	if err := cs.SetDefaults(ctx, set); err != nil {
		t.Fatal(err)
	}
	if mustDefaults(t, cs).Workspace != "" {
		t.Errorf("既定にワークスペースが入っています: %q", mustDefaults(t, cs).Workspace)
	}
	saved, _, err := LoadAppSettings(ctx, cs.store, AppSettings{})
	if err != nil || saved.Defaults.Workspace != "" {
		t.Errorf("保存した既定: %+v, %v", saved.Defaults, err)
	}
	c, _ := cs.Create(ctx, nil)
	if c.Settings.Workspace != "" {
		t.Errorf("既定から作った会話: %q", c.Settings.Workspace)
	}
	// 新しい会話の画面で選んだワークスペースは、その会話には入る
	c, err = cs.Create(ctx, &set)
	if err != nil || c.Settings.Workspace != set.Workspace {
		t.Errorf("選んで作った会話: %q, %v", c.Settings.Workspace, err)
	}
}

// 前の作業フォルダ(work_dir)は、画面からは書けない(作る会話にも、設定を変えるときにも。0035)
func TestWorkDirIsNotWritable(t *testing.T) {
	cs, _ := newTestConversations(t, &fakeGen{})
	set := testDefaults
	set.WorkDir = t.TempDir()
	c, err := cs.Create(ctx, &set)
	if err != nil || c.Settings.WorkDir != "" {
		t.Fatalf("作った会話: %q, %v", c.Settings.WorkDir, err)
	}
	if err := cs.UpdateSettings(ctx, c.ID, set); err != nil {
		t.Fatal(err)
	}
	if stored, _, _ := cs.store.Conversation(ctx, c.ID); stored.Settings.WorkDir != "" {
		t.Errorf("設定を変えて作業フォルダが入った: %q", stored.Settings.WorkDir)
	}
}

// workspaceFor は、フォルダ dir 1つのワークスペースを cs に登録して、その ID を返す(ファイルは使わない)。dir が空なら空
func workspaceFor(t *testing.T, cs *Conversations, dir string) string {
	t.Helper()
	if dir == "" {
		return ""
	}
	if cs.workspaces == nil {
		cs.workspaces, _ = config.LoadWorkspaces("")
	}
	ws, err := cs.workspaces.CreateFromFolder(dir)
	if err != nil {
		t.Fatal(err)
	}
	return ws.ID
}
