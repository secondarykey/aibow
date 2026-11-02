package aibow

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// ワークスペースを作る・名前を変える・消すと workspaces.json に書き、読み直すと同じものが戻る(0035 の完了の条件 1)
func TestWorkspacesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspaces.json")
	w, err := LoadWorkspaces(path)
	if err != nil || len(w.List()) != 0 {
		t.Fatalf("ファイルが無いときは空: %v, %v", w.List(), err)
	}
	dir := filepath.Join(t.TempDir(), "example")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ws, err := w.CreateFromFolder(dir)
	if err != nil {
		t.Fatal(err)
	}
	if ws.Name != "example" || len(ws.Folders) != 1 || ws.MainFolder() != (WorkspaceFolder{Path: dir, Name: "example", Main: true, Writable: true}) {
		t.Errorf("フォルダから作ったワークスペース: %+v", ws)
	}
	// 同じフォルダ1つだけのワークスペースは、新しく作らずに今あるものを返す
	if again, err := w.CreateFromFolder(dir); err != nil || again.ID != ws.ID || len(w.List()) != 1 {
		t.Errorf("同じフォルダ: %+v(一覧 %d), %v", again, len(w.List()), err)
	}
	renamed := ws
	renamed.Name = "  サンプル  "
	if err := w.Update(renamed); err != nil {
		t.Fatal(err)
	}
	r, err := LoadWorkspaces(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := r.Get(ws.ID); !ok || got.Name != "サンプル" || got.MainFolder().Path != dir {
		t.Errorf("読み直したワークスペース: %+v, %v", got, ok)
	}
	renamed.Name = " "
	if err := w.Update(renamed); err == nil {
		t.Error("空の名前はエラーのはず")
	}
	if err := w.Delete(ws.ID); err != nil {
		t.Fatal(err)
	}
	if r, _ = LoadWorkspaces(path); len(r.List()) != 0 {
		t.Errorf("消したあと: %+v", r.List())
	}
	// 無いフォルダ・相対パスからは作らない
	for _, p := range []string{filepath.Join(dir, "none"), "relative"} {
		if _, err := w.CreateFromFolder(p); err == nil {
			t.Errorf("%s から作れてしまった", p)
		}
	}
}

// 壊れた workspaces.json は書き換えない(ワークスペースが無いものとして動く)
func TestWorkspacesBrokenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspaces.json")
	for _, body := range []string{"{", `{"workspaces":[{"id":"a","name":"x","folders":[{"path":"/a","name":"a"}]}]}`} {
		os.WriteFile(path, []byte(body), 0o644)
		w, err := LoadWorkspaces(path)
		if err == nil || w == nil || len(w.List()) != 0 {
			t.Fatalf("%s: 壊れたファイルはエラーのはず: %v", body, err)
		}
		if _, err := w.CreateFromFolder(t.TempDir()); err == nil {
			t.Errorf("%s: 読めなかったファイルに書いてしまう", body)
		}
		if b, _ := os.ReadFile(path); string(b) != body {
			t.Errorf("%s: ファイルが書き換わった: %s", body, b)
		}
	}
}

// フォルダの組の決まり(メインはちょうど1つ・名前は重ならない・パスに見える名前は使わない)
func TestWorkspaceValidate(t *testing.T) {
	abs := t.TempDir()
	ok := WorkspaceFolder{Path: abs, Name: "main", Main: true, Writable: true}
	cases := []struct {
		name    string
		folders []WorkspaceFolder
		want    string // エラーに含まれる文字(空なら通る)
	}{
		{"メイン1つ", []WorkspaceFolder{ok, {Path: abs, Name: "notes"}}, ""},
		{"メインなし", []WorkspaceFolder{{Path: abs, Name: "a"}}, "メイン"},
		{"メイン2つ", []WorkspaceFolder{ok, {Path: abs, Name: "b", Main: true}}, "メイン"},
		{"名前が重なる", []WorkspaceFolder{ok, {Path: abs, Name: "MAIN"}}, "2つ"},
		{"パスに見える名前", []WorkspaceFolder{ok, {Path: abs, Name: "a/b"}}, "使えません"},
		{"相対パス", []WorkspaceFolder{ok, {Path: "rel", Name: "rel"}}, "絶対パス"},
	}
	for _, c := range cases {
		err := Workspace{ID: "x", Name: "w", Folders: c.folders}.validate()
		if c.want == "" && err != nil || c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

// 今ある会話の work_dir は、この PC にあるパスならワークスペースに移り、無いパスはそのまま残って使われない(0035 の完了の条件 3)
func TestMigrateWorkDirs(t *testing.T) {
	g := &fakeGen{content: "はい"}
	cs, st, _ := newToolConversation(t, g)
	cs.workspaces, _ = LoadWorkspaces(filepath.Join(t.TempDir(), "workspaces.json"))
	here := t.TempDir()
	gone := filepath.Join(t.TempDir(), "other-pc")
	var convs []Conversation
	for _, dir := range []string{here, here, gone, ""} {
		c := mustCreate(t, cs)
		c.Settings.WorkDir = dir // 前の版で保存した会話(画面からは書けないので、保存先に直接書く)
		if err := st.UpdateConversation(ctx, c); err != nil {
			t.Fatal(err)
		}
		convs = append(convs, c)
	}
	moved, kept, err := cs.MigrateWorkDirs(ctx)
	if err != nil || moved != 2 || kept != 1 {
		t.Fatalf("移した %d・残した %d, %v", moved, kept, err)
	}
	list := cs.workspaces.List()
	if len(list) != 1 || list[0].MainFolder().Path != here {
		t.Fatalf("パスごとに1つのワークスペース: %+v", list)
	}
	stored := func(i int) ConversationSettings {
		c, _, err := st.Conversation(ctx, convs[i].ID)
		if err != nil {
			t.Fatal(err)
		}
		return c.Settings
	}
	for i := range 2 {
		if s := stored(i); s.Workspace != list[0].ID || s.WorkDir != "" {
			t.Errorf("移した会話 %d: %+v", i, s)
		}
	}
	if s := stored(2); s.Workspace != "" || s.WorkDir != gone {
		t.Errorf("この PC に無いパスは残す: %+v", s)
	}
	// 残した会話では、前のパスでフォルダのツールを出さない
	send(t, cs, convs[2], "質問")
	if got := toolNamesOf(g.calls[len(g.calls)-1].tools); slices.Contains(got, "read_file") {
		t.Errorf("この PC に無い作業フォルダのツールが出ている: %v", got)
	}
	// もう一度起動しても、移し直さない
	if moved, kept, err := cs.MigrateWorkDirs(ctx); err != nil || moved != 0 || kept != 1 || len(cs.workspaces.List()) != 1 {
		t.Errorf("2回目: 移した %d・残した %d, %v", moved, kept, err)
	}
}

// この PC に無いワークスペースを選んだ会話(別の PC で選んだもの・消したもの)では、
// フォルダのツールと環境の説明を出さない(0035 の完了の条件 4。workspaces.json を空にしたのと同じ)
func TestMissingWorkspace(t *testing.T) {
	g := &fakeGen{content: "はい"}
	cs, c, _ := newWorkDirConversation(t, g)
	send(t, cs, c, "質問")
	if got := toolNamesOf(g.calls[0].tools); !slices.Contains(got, "read_file") {
		t.Fatalf("ワークスペースのある会話: %v", got)
	}
	cs.workspaces, _ = LoadWorkspaces("") // 別の PC(何も登録していない)
	send(t, cs, c, "質問")
	call := g.calls[1]
	if got := toolNamesOf(call.tools); !slices.Equal(got, []string{"get_current_time", "calculate"}) || strings.Contains(call.prompt, "# Environment") {
		t.Errorf("見つからないワークスペース: %v / %q", got, call.prompt)
	}
}
