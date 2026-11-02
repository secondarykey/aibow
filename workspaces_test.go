package aibow

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/secondarykey/aibow/format"
	"github.com/secondarykey/aibow/mcp/client"
	"github.com/secondarykey/aibow/tool"
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

// newFoldersConversation は、フォルダ2つ(メイン app・書く / notes・読むだけ)のワークスペースを選んだ会話を作る(0035 の段2)。
func newFoldersConversation(t *testing.T, g *fakeGen) (*Conversations, Conversation, string, string) {
	t.Helper()
	cs, c, main := newWorkDirConversation(t, g)
	notes := t.TempDir()
	os.WriteFile(filepath.Join(notes, "memo.md"), []byte("# メモ\n"), 0o644)
	ws, _ := cs.workspaces.Get(c.Settings.Workspace)
	ws.Folders = []WorkspaceFolder{
		{Path: main, Name: "app", Main: true, Writable: true},
		{Path: notes, Name: "notes"},
	}
	if err := cs.workspaces.Update(ws); err != nil {
		t.Fatal(err)
	}
	return cs, c, main, notes
}

// フォルダを複数にしたワークスペースで、folder を付けて読み、読むだけのフォルダへの書き込みはエラーとしてモデルに返る(完了の条件 5)。
// aibow の中の MCP サーバー越し(アプリと同じ形)でも、直接呼ぶ形でも同じ
func TestWorkspaceFolders(t *testing.T) {
	for _, viaMCP := range []bool{false, true} {
		g := &fakeGen{replies: []fakeReply{
			{toolCalls: []format.ToolCall{toolCall("read_file", "folder", "notes", "path", "memo.md")}},
			{toolCalls: []format.ToolCall{toolCall("write_file", "folder", "notes", "path", "memo.md", "content", "消した")}},
			{content: "読むだけでした"},
		}}
		cs, c, _, notes := newFoldersConversation(t, g)
		if viaMCP {
			l := client.NewLocal(client.Options{Version: "test"})
			t.Cleanup(l.Close)
			cs.SetLocalTools(l)
		}
		asked := 0
		evs := sendAnswering(cs, context.Background(), c, "メモを読んで直して", func(q *ConfirmRequest) bool {
			asked++
			return cs.Confirm(ctx, c.ID, q.ID, true, "") == nil
		})
		if errs := errorsOf(evs); len(errs) != 0 {
			t.Fatalf("MCP %v: エラー: %v", viaMCP, errs)
		}
		rs := toolResults(t, cs, c)
		if len(rs) != 2 || rs[0].Content != "1\t# メモ" || !rs[1].ToolResult.Error || !strings.Contains(rs[1].Content, "notes は読むだけのフォルダなので、書き換えられません") {
			t.Errorf("MCP %v: ツールの結果: %+v", viaMCP, rs)
		}
		if asked != 0 {
			t.Errorf("MCP %v: 読むだけのフォルダへの書き込みで確認のカードを出した", viaMCP)
		}
		if b, _ := os.ReadFile(filepath.Join(notes, "memo.md")); string(b) != "# メモ\n" {
			t.Errorf("MCP %v: 読むだけのフォルダが書き換わった: %q", viaMCP, b)
		}
		// モデルに知らせた定義には folder(名前の enum)があり、環境の説明にフォルダが並ぶ
		call := g.calls[0]
		if d := tool.ArgTypesFor(call.tools, "read_file"); d["folder"] != "string" {
			t.Errorf("MCP %v: read_file に folder が無い: %v", viaMCP, d)
		}
		for _, want := range []string{"Folders (pass the folder argument; omit it for the main folder):\n- app (main, writable): ", "\n- notes (read-only): " + notes + "\n", "Each command starts in the main folder \"app\" unless you pass folder; a cd does not carry over to the next command. To run in another folder of the list, pass folder instead of cd."} {
			if !strings.Contains(call.prompt, want) {
				t.Errorf("MCP %v: 環境の説明に %q が無い:\n%s", viaMCP, want, call.prompt)
			}
		}
	}
}

// run_command も folder でフォルダを選んで動く。読むだけのフォルダでも動かせ、確認のカードにフォルダの名前と「読むだけのフォルダ」が出る。
// 以前はメインでしか動かず、git の印の付いたほかのフォルダで Git を使えなかった(2026-11-02、ユーザーの報告)
func TestWorkspaceRunCommandFolder(t *testing.T) {
	list := "ls"
	if runtime.GOOS == "windows" {
		list = "dir /b"
	}
	for _, viaMCP := range []bool{false, true} {
		g := &fakeGen{replies: []fakeReply{
			{toolCalls: []format.ToolCall{toolCall("run_command", "folder", "notes", "command", list)}},
			{content: "memo.md がありました"},
		}}
		cs, c, _, _ := newFoldersConversation(t, g)
		if viaMCP {
			l := client.NewLocal(client.Options{Version: "test"})
			t.Cleanup(l.Close)
			cs.SetLocalTools(l)
		}
		var req *ConfirmRequest
		evs := sendAnswering(cs, context.Background(), c, "notes の中を見て", func(q *ConfirmRequest) bool {
			req = q
			return cs.Confirm(ctx, c.ID, q.ID, true, "") == nil
		})
		if errs := errorsOf(evs); len(errs) != 0 {
			t.Fatalf("MCP %v: エラー: %v", viaMCP, errs)
		}
		if req == nil || req.Preview.Folder != "notes" || req.Preview.Note != "コマンド(読むだけのフォルダ)" {
			t.Errorf("MCP %v: 確認: %+v", viaMCP, req)
		}
		if rs := toolResults(t, cs, c); len(rs) != 1 || !strings.Contains(rs[0].Content, "memo.md") {
			t.Errorf("MCP %v: 結果: %+v", viaMCP, rs)
		}
		if d := tool.ArgTypesFor(g.calls[0].tools, "run_command"); d["folder"] != "string" {
			t.Errorf("MCP %v: run_command に folder が無い: %v", viaMCP, d)
		}
	}
}

// フォルダが1つなら、ツールの定義と環境の説明は段1と同じ(folder の引数・Folders の行を出さない。完了の条件 6)
func TestWorkspaceOneFolder(t *testing.T) {
	g := &fakeGen{content: "はい"}
	cs, c, dir := newWorkDirConversation(t, g)
	send(t, cs, c, "質問")
	call := g.calls[0]
	for _, d := range call.tools {
		if strings.Contains(string(d.Parameters), `"folder"`) {
			t.Errorf("%s に folder がある", d.Name)
		}
	}
	if !strings.Contains(call.prompt, "Working directory: "+dir+"\n") || strings.Contains(call.prompt, "Folders") {
		t.Errorf("環境の説明:\n%s", call.prompt)
	}
}
