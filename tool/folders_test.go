package tool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// twoFolders は、メイン(書く)の main と、読むだけの notes のフォルダの組を作る(0035 の段2)。
func twoFolders(t *testing.T) (main, notes string, tools []Tool) {
	t.Helper()
	main, notes = t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(main, "app.go"), []byte("package app\n"), 0o644)
	os.WriteFile(filepath.Join(notes, "memo.md"), []byte("# メモ\n"), 0o644)
	tools = FilesIn([]Folder{
		{Name: "app", Path: main, Main: true, Writable: true},
		{Name: "notes", Path: notes},
	})
	return main, notes, tools
}

// folder を付けて読み、付けなければメイン。読むだけのフォルダへの書き込みはエラーとしてモデルに返る(完了の条件 5)
func TestFolderArgument(t *testing.T) {
	main, notes, tools := twoFolders(t)
	read, list, write, edit := Find(tools, "read_file"), Find(tools, "list_files"), Find(tools, "write_file"), Find(tools, "edit_file")

	if got, err := run(t, read, "folder", "notes", "path", "memo.md"); err != nil || got != "1\t# メモ" {
		t.Errorf("notes を読む: %q, %v", got, err)
	}
	if got, err := run(t, read, "path", "app.go"); err != nil || got != "1\tpackage app" {
		t.Errorf("folder を省くとメイン: %q, %v", got, err)
	}
	if got, err := run(t, list, "folder", "Notes"); err != nil || got != "memo.md" {
		t.Errorf("大文字と小文字は区別しない: %q, %v", got, err)
	}
	// 絶対パスは、どのフォルダの中かを見て振り分ける。folder と食い違えばエラー
	if got, err := run(t, read, "path", filepath.Join(notes, "memo.md")); err != nil || got != "1\t# メモ" {
		t.Errorf("notes の中の絶対パス: %q, %v", got, err)
	}
	if _, err := run(t, read, "folder", "app", "path", filepath.Join(notes, "memo.md")); err == nil || !strings.Contains(err.Error(), "食い違") {
		t.Errorf("folder と絶対パスの食い違い: %v", err)
	}
	if _, err := run(t, read, "folder", "docs", "path", "a.md"); err == nil || !strings.Contains(err.Error(), "app・notes") {
		t.Errorf("無いフォルダ: %v", err)
	}

	// 読むだけのフォルダは、確認のカードを作る前に断る(Preview がエラー)
	for _, c := range []struct {
		tool Tool
		args []string
	}{
		{write, []string{"folder", "notes", "path", "new.md", "content", "x"}},
		{edit, []string{"folder", "notes", "path", "memo.md", "old_string", "メモ", "new_string", "めも"}},
		{write, []string{"path", filepath.Join(notes, "new.md"), "content", "x"}}, // 絶対パスで notes を指しても
	} {
		_, err := c.tool.(Confirmable).Preview(argsFor(t, c.tool, c.args...))
		if err == nil || !strings.Contains(err.Error(), "notes は読むだけのフォルダなので、書き換えられません") {
			t.Errorf("%s %v: %v", c.tool.Def().Name, c.args, err)
		}
		if _, err := run(t, c.tool, c.args...); err == nil {
			t.Errorf("%s %v: 書けてしまった", c.tool.Def().Name, c.args)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(notes, "memo.md")); string(b) != "# メモ\n" {
		t.Errorf("読むだけのフォルダが書き換わった: %q", b)
	}
	// メインには書ける。フォルダが複数なら、メインでも確認のカードにフォルダの名前を出す
	pv, err := write.(Confirmable).Preview(argsFor(t, write, "path", "new.go", "content", "package app\n"))
	if err != nil || pv.Folder != "app" || pv.Path != "new.go" {
		t.Errorf("メインへの書き込み: %+v, %v", pv, err)
	}
	if _, err := run(t, write, "path", "new.go", "content", "package app\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(main, "new.go")); err != nil {
		t.Errorf("メインに書けていない: %v", err)
	}
}

// フォルダが複数なら、確認のカードにフォルダの名前を出す(メインでも)。1つなら出さない
func TestFolderOnCard(t *testing.T) {
	main, other := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(main, "b.txt"), []byte("old\n"), 0o644)
	tools := FilesIn([]Folder{{Name: "app", Path: main, Main: true, Writable: true}, {Name: "lib", Path: other, Writable: true}})
	write, edit := Find(tools, "write_file"), Find(tools, "edit_file")
	pv, err := write.(Confirmable).Preview(argsFor(t, write, "folder", "lib", "path", "a.txt", "content", "x"))
	if err != nil || pv.Folder != "lib" || pv.Path != "a.txt" {
		t.Errorf("カード: %+v, %v", pv, err)
	}
	pv, err = edit.(Confirmable).Preview(argsFor(t, edit, "path", "b.txt", "old_string", "old", "new_string", "new"))
	if err != nil || pv.Folder != "app" {
		t.Errorf("メインの書き換えのカード: %+v, %v", pv, err)
	}
	one := Find(Files(main), "edit_file")
	pv, err = one.(Confirmable).Preview(argsFor(t, one, "path", "b.txt", "old_string", "old", "new_string", "new"))
	if err != nil || pv.Folder != "" {
		t.Errorf("フォルダが1つのカード: %+v, %v", pv, err)
	}
}

// フォルダが1つなら、ツールの定義は Files と同じ(folder の引数を出さない)。複数なら名前の enum を足す(完了の条件 6)
func TestFolderDefs(t *testing.T) {
	dir := t.TempDir()
	single := FilesIn([]Folder{{Name: "x", Path: dir, Main: true}})
	for i, tl := range Files(dir) {
		a, b := tl.Def(), single[i].Def()
		if a.Name != b.Name || a.Description != b.Description || string(a.Parameters) != string(b.Parameters) {
			t.Errorf("%s: フォルダ1つの定義が変わった", a.Name)
		}
		// 引数の名前(キー)で見る。説明の文の「folders」には合わせない
		if strings.Contains(string(b.Parameters), `"folder":`) {
			t.Errorf("%s: フォルダ1つで folder がある", b.Name)
		}
	}
	_, _, multi := twoFolders(t)
	for _, tl := range multi {
		var schema struct {
			Properties map[string]struct {
				Enum []string `json:"enum"`
			} `json:"properties"`
			Required []string `json:"required"`
		}
		if err := json.Unmarshal(tl.Def().Parameters, &schema); err != nil {
			t.Fatalf("%s: %v", tl.Def().Name, err)
		}
		if f, ok := schema.Properties["folder"]; !ok || !slices.Equal(f.Enum, []string{"app", "notes"}) || slices.Contains(schema.Required, "folder") {
			t.Errorf("%s: folder の引数 %+v(必須 %v)", tl.Def().Name, f, schema.Required)
		}
	}
}

// argsFor は、名前と値を交互に並べた引数を、ツール tl の引数にする(run と同じ形)。
func argsFor(t *testing.T, tl Tool, kv ...string) Args {
	t.Helper()
	var args []Arg
	for i := 0; i+1 < len(kv); i += 2 {
		args = append(args, Arg{kv[i], kv[i+1]})
	}
	a, err := CheckArgs(tl.Def(), args)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// 4B のつまずき(_docs/tuning.md)への添え書き:folder の付け忘れと、フォルダの名前をパスの先頭に付けた書き方
func TestFolderHints(t *testing.T) {
	main, _, tools := twoFolders(t)
	read, list, write := Find(tools, "read_file"), Find(tools, "list_files"), Find(tools, "write_file")

	if _, err := run(t, read, "path", "memo.md"); err == nil || !strings.Contains(err.Error(), "memo.md はフォルダ notes の中にあります。folder に notes を書いてください") {
		t.Errorf("folder の付け忘れ: %v", err)
	}
	for _, c := range []struct {
		tool Tool
		args []string
	}{
		{read, []string{"path", "notes/memo.md"}},
		{list, []string{"path", "Notes/sub"}},
		{write, []string{"path", "notes/new.md", "content", "x"}}, // メインの中に notes/ を作らない
	} {
		if _, err := run(t, c.tool, c.args...); err == nil || !strings.Contains(err.Error(), "はフォルダの名前です。パスの先頭に付けず、folder に notes") {
			t.Errorf("%s %v: %v", c.tool.Def().Name, c.args, err)
		}
	}
	if _, err := os.Stat(filepath.Join(main, "notes")); err == nil {
		t.Error("メインの中に notes を作ってしまった")
	}
	// 書き換えるツールでは、読むだけのフォルダを案内しない
	edit := Find(tools, "edit_file")
	if _, err := run(t, edit, "path", "memo.md", "old_string", "メモ", "new_string", "めも"); err == nil || strings.Contains(err.Error(), "folder に notes") {
		t.Errorf("edit_file の添え書き: %v", err)
	}
	// メインの中に本当に同じ名前のサブフォルダがあれば、そのまま使う
	os.MkdirAll(filepath.Join(main, "notes"), 0o755)
	os.WriteFile(filepath.Join(main, "notes", "x.md"), []byte("x\n"), 0o644)
	if got, err := run(t, read, "path", "notes/x.md"); err != nil || got != "1\tx" {
		t.Errorf("メインのサブフォルダ: %q, %v", got, err)
	}
}

// run_command も folder でカレントを選び、省けばメイン。読むだけのフォルダでも動かせ、確認のカードにそう出す(2026-11-02)
func TestRunCommandFolder(t *testing.T) {
	main, notes, _ := twoFolders(t)
	tl := RunCommandIn([]Folder{
		{Name: "app", Path: main, Main: true, Writable: true},
		{Name: "notes", Path: notes},
	})
	list := byOS("dir /b", "ls")

	if got, err := run(t, tl, "command", list); err != nil || !strings.Contains(got, "app.go") {
		t.Errorf("folder を省くとメイン: %q, %v", got, err)
	}
	if got, err := run(t, tl, "folder", "Notes", "command", list); err != nil || !strings.Contains(got, "memo.md") || strings.Contains(got, "app.go") {
		t.Errorf("notes で動く: %q, %v", got, err)
	}
	if _, err := run(t, tl, "folder", "docs", "command", list); err == nil || !strings.Contains(err.Error(), "app・notes") {
		t.Errorf("無いフォルダ: %v", err)
	}

	// 確認のカード:フォルダの名前(メインでも)。読むだけのフォルダならそう書く
	pv, err := tl.(Confirmable).Preview(argsFor(t, tl, "folder", "notes", "command", "git log"))
	if err != nil || pv.Folder != "notes" || pv.Note != "コマンド(読むだけのフォルダ)" || pv.Command != "git log" {
		t.Errorf("読むだけのフォルダの確認: %+v, %v", pv, err)
	}
	pv, err = tl.(Confirmable).Preview(argsFor(t, tl, "command", "go test"))
	if err != nil || pv.Folder != "app" || pv.Note != "コマンド" {
		t.Errorf("メインの確認: %+v, %v", pv, err)
	}

	// folder を渡さずに、先頭の cd でフォルダを選んだら、そのフォルダをカレントにする(cmd.exe の /d 無しの cd は別のドライブへ移らないため。2026-11-03)。
	// コマンドは書き換えない。folder を書いていても cd の行き先が勝つ(コマンドがそこで動くつもりで書かれているため)
	cd := `cd "` + notes + `" && ` + list
	pv, err = tl.(Confirmable).Preview(argsFor(t, tl, "command", cd))
	if err != nil || pv.Folder != "notes" || pv.Note != "コマンド(読むだけのフォルダ)" || pv.Command != cd {
		t.Errorf("cd で選ぶ確認: %+v, %v", pv, err)
	}
	if got, err := run(t, tl, "folder", "app", "command", "cd /d "+notes+" & "+list); err != nil || !strings.Contains(got, "memo.md") {
		t.Errorf("cd で notes を選ぶ: %q, %v", got, err)
	}

	// 定義:複数なら folder(名前の enum)がある。1つなら RunCommand と同じ(folder を出さない)
	if d := tl.Def(); !strings.Contains(string(d.Parameters), `"folder": {"type": "string", "enum": ["app","notes"]`) || !strings.Contains(d.Description, "a cd does not carry over to the next call") {
		t.Errorf("複数のときの定義: %+v", d)
	}
	one := RunCommandIn(Single(main)).Def()
	if want := RunCommand(main).Def(); string(one.Parameters) != string(want.Parameters) || one.Description != want.Description || strings.Contains(string(one.Parameters), "folder") {
		t.Errorf("1つのときの定義が変わった: %+v", one)
	}
}
