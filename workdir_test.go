package aibow

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"aibow/tool"
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
// 決めていない会話・ツールが OFF の会話には、どちらも入らない
func TestWorkDirAddsFileToolsAndEnvironment(t *testing.T) {
	dir := t.TempDir()
	g := &fakeGen{content: "はい"}
	cs, _, c := newToolConversation(t, g)

	set := c.Settings
	steps := []struct {
		name      string
		workDir   string
		tools     bool
		wantTools []string
		wantEnv   bool
	}{
		{"作業フォルダなし", "", true, []string{"get_current_time", "calculate"}, false},
		{"作業フォルダあり", dir, true, []string{"get_current_time", "calculate", "list_files", "read_file"}, true},
		{"ツールが OFF", dir, false, nil, false},
	}
	for i, s := range steps {
		set.WorkDir, set.Tools = s.workDir, s.tools
		if err := cs.UpdateSettings(c.ID, set); err != nil {
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
	set.Tools, set.WorkDir = true, `D:\Work\example`
	msgs := promptMessages(cs.systemPrompt(set), "- 要約", nil)
	want := testDefaults.System + "\n\n# Environment\nWorking directory: D:\\Work\\example\nOS: " + runtime.GOOS +
		"\nUse paths relative to the working directory.\n\n" + summaryHeading + "\n- 要約"
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
		{toolCalls: []ToolCall{toolCall("list_files")}},
		{toolCalls: []ToolCall{toolCall("read_file", "path", "main.go", "offset", "3")}}, // 数の引数も文字列で届く
		{content: "main 関数だけがあります"},
	}}
	cs, _, c := newToolConversation(t, g)
	set := c.Settings
	set.WorkDir = dir
	cs.UpdateSettings(c.ID, set)
	send(t, cs, c, "このフォルダに何がある?")

	_, ms, _ := cs.Get(c.ID)
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

// 作業フォルダは新しい会話の既定に入れない(前の会話のフォルダを引き継がない)
func TestWorkDirIsNotADefault(t *testing.T) {
	cs, _ := newTestConversations(t, &fakeGen{})
	set := testDefaults
	set.WorkDir = t.TempDir()
	if err := cs.SetDefaults(set); err != nil {
		t.Fatal(err)
	}
	if cs.Defaults().WorkDir != "" {
		t.Errorf("既定に作業フォルダが入っています: %q", cs.Defaults().WorkDir)
	}
	saved, _, err := LoadAppSettings(cs.store, AppSettings{})
	if err != nil || saved.Defaults.WorkDir != "" {
		t.Errorf("保存した既定: %+v, %v", saved.Defaults, err)
	}
	c, _ := cs.Create(nil)
	if c.Settings.WorkDir != "" {
		t.Errorf("既定から作った会話: %q", c.Settings.WorkDir)
	}
	// 新しい会話の画面で選んだフォルダは、その会話には入る
	c, err = cs.Create(&set)
	if err != nil || c.Settings.WorkDir != set.WorkDir {
		t.Errorf("選んで作った会話: %q, %v", c.Settings.WorkDir, err)
	}
	// 相対パスは受け付けない
	set.WorkDir = "work"
	if err := cs.UpdateSettings(c.ID, set); err == nil {
		t.Error("相対パスの作業フォルダはエラーのはず")
	}
}
