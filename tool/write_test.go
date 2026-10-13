package tool

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// preview はツールの確認の内容を、モデルが書いた形(文字列の引数)から作る
func preview(t *testing.T, tl Confirmable, kv ...string) (Preview, error) {
	t.Helper()
	var args []Arg
	for i := 0; i+1 < len(kv); i += 2 {
		args = append(args, Arg{kv[i], kv[i+1]})
	}
	a, err := CheckArgs(tl.Def(), args)
	if err != nil {
		return Preview{}, err
	}
	return tl.Preview(a)
}

func readBack(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const greet = "package greet\n\n// Hello は name へのあいさつを返す。\nfunc Hello(name string) string {\n\treturn \"Hello, \" + name + \"!\"\n}\n"

func TestWriteFile(t *testing.T) {
	dir := workDir(t, map[string]string{"greet.go": greet, "sub/": ""})
	tl := writeFile{dir}

	// 新しく作る(親のフォルダが無ければ作る)。差分は全部 + の行
	pv, err := preview(t, tl, "path", "pkg/new/x.go", "content", "package x\n")
	if err != nil || pv.Path != "pkg/new/x.go" || pv.Note != "新しく作る" || !strings.Contains(pv.Diff, "--- /dev/null\n+++ b/pkg/new/x.go\n") || !strings.Contains(pv.Diff, "+package x\n") {
		t.Errorf("新しく作る確認: %+v, %v", pv, err)
	}
	if got, err := run(t, tl, "path", "pkg/new/x.go", "content", "package x\n"); err != nil || got != "pkg/new/x.go を新しく作りました(1 行)" {
		t.Errorf("新しく作る: %q, %v", got, err)
	}
	if got := readBack(t, dir, "pkg/new/x.go"); got != "package x\n" {
		t.Errorf("書いた中身: %q", got)
	}

	// 上書き。差分は書き換えた行だけが -/+ になる
	after := strings.Replace(greet, "Hello, ", "こんにちは、", 1)
	pv, err = preview(t, tl, "path", "greet.go", "content", after)
	if err != nil || pv.Note != "上書き" || !strings.Contains(pv.Diff, "-\treturn \"Hello, \" + name + \"!\"\n+\treturn \"こんにちは、\" + name + \"!\"\n") || strings.Contains(pv.Diff, "-package") {
		t.Errorf("上書きの確認: %+v, %v", pv, err)
	}
	if got, err := run(t, tl, "path", "greet.go", "content", after); err != nil || !strings.Contains(got, "上書きしました") || readBack(t, dir, "greet.go") != after {
		t.Errorf("上書き: %q, %v", got, err)
	}

	for _, p := range []string{"", ".", "sub", "../outside/secret.txt"} {
		if _, err := preview(t, tl, "path", p, "content", "x"); err == nil {
			t.Errorf("path %q はエラーのはず", p)
		}
	}
	if got := readBack(t, filepath.Dir(dir), "outside/secret.txt"); got != "secret" {
		t.Errorf("作業フォルダの外が書き換わっています: %q", got)
	}
}

func TestEditFile(t *testing.T) {
	dir := workDir(t, map[string]string{
		"greet.go": greet,
		"twice.go": "x := 1\nx := 1\n",
		"crlf.txt": "a\r\nb\r\nc\r\n",
	})
	tl := editFile{dir}

	pv, err := preview(t, tl, "path", "greet.go", "old_string", "\treturn \"Hello, \"", "new_string", "\treturn \"こんにちは、\"")
	if err != nil || pv.Note != "書き換え" || !strings.Contains(pv.Diff, "@@ -2,5 +2,5 @@") || !strings.Contains(pv.Diff, "+\treturn \"こんにちは、\" + name + \"!\"\n") {
		t.Errorf("確認: %+v, %v", pv, err)
	}
	if readBack(t, dir, "greet.go") != greet {
		t.Error("確認を作っただけで書き換わっています")
	}
	got, err := run(t, tl, "path", "greet.go", "old_string", "\treturn \"Hello, \"", "new_string", "\treturn \"こんにちは、\"")
	if err != nil || got != "greet.go の 5 行目からを書き換えました" || readBack(t, dir, "greet.go") != strings.Replace(greet, "Hello, ", "こんにちは、", 1) {
		t.Errorf("書き換え: %q, %v", got, err)
	}

	// CRLF のファイルも、LF で書いた複数行の old_string で書き換えられ、改行は CRLF のまま
	if _, err := run(t, tl, "path", "crlf.txt", "old_string", "a\nb", "new_string", "A\nB"); err != nil || readBack(t, dir, "crlf.txt") != "A\r\nB\r\nc\r\n" {
		t.Errorf("CRLF: %q, %v", readBack(t, dir, "crlf.txt"), err)
	}

	errs := map[string][]string{
		"見つかりません":    {"path", "greet.go", "old_string", "1\tpackage greet", "new_string", "x"}, // 行番号を付けたまま
		"2 か所":       {"path", "twice.go", "old_string", "x := 1", "new_string", "x := 2"},
		"空です":        {"path", "greet.go", "old_string", "", "new_string", "x"},
		"同じです":       {"path", "greet.go", "old_string", "package", "new_string", "package"},
		"write_file": {"path", "nothing.go", "old_string", "a", "new_string", "b"}, // 無いファイルは write_file で作る
	}
	for want, kv := range errs {
		if _, err := preview(t, tl, kv...); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q を含むエラーのはず: %v", want, err)
		}
	}
	if readBack(t, dir, "twice.go") != "x := 1\nx := 1\n" {
		t.Error("2 か所あるときに書き換わっています")
	}
}

// 確認を待つ間にファイルが変わったら、実行するときにもう一度確かめる(古い中身で書き戻さない)
func TestEditFileChangedWhileWaiting(t *testing.T) {
	dir := workDir(t, map[string]string{"a.txt": "one\n"})
	tl := editFile{dir}
	if _, err := preview(t, tl, "path", "a.txt", "old_string", "one", "new_string", "two"); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "a.txt", "ONE\n") // 確認を待っている間に、ほかで書き換えられた
	if _, err := run(t, tl, "path", "a.txt", "old_string", "one", "new_string", "two"); err == nil || readBack(t, dir, "a.txt") != "ONE\n" {
		t.Errorf("変わったファイルを書き換えています: %q, %v", readBack(t, dir, "a.txt"), err)
	}
}

// 大きいファイルを丸ごと書き直すときは、差分を作らない(時間がかかるため)
func TestDiffTooLarge(t *testing.T) {
	big := strings.Repeat("a\n", diffMaxLines)
	if d := unifiedDiff("x", big, strings.ReplaceAll(big, "a", "b")); d != "" {
		t.Errorf("差分を作っています(%d 文字)", len(d))
	}
	// 共通の行が1つも無いと、pkg/diff は「消す」と「足す」を別の @@ に分けて書く(@@ -1,1 +0,0 @@ と @@ -0,0 +1,1 @@)。
	// 行の中身は正しいので、そのまま使う
	if d := unifiedDiff("x", "a\n", "b\n"); !strings.Contains(d, "-a\n") || !strings.Contains(d, "+b\n") {
		t.Errorf("小さい差分: %q", d)
	}
}
