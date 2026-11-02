package tool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// glob の合わせ方(0037 の完了の条件 2)。/ を含まなければ名前だけ、含めば作業フォルダからの相対パスと比べる
func TestGlobMatch(t *testing.T) {
	cases := []struct {
		pattern string
		yes, no []string
	}{
		{"*.go", []string{"main.go", "cmd/aibow/main.go", "a/b/c/x_test.go"}, []string{"go.mod", "web/app.ts", "main.go/x"}},
		{"*_test.go", []string{"x_test.go", "tool/search_test.go"}, []string{"tool/search.go", "test.go"}},
		{"**/*.go", []string{"main.go", "a/b.go", "a/b/c.go"}, []string{"go.mod", "a/b.ts"}},
		{"**/*_test.go", []string{"x_test.go", "tool/search_test.go"}, []string{"tool/search.go"}},
		{"cmd/**/main.go", []string{"cmd/main.go", "cmd/aibow/main.go", "cmd/a/b/main.go"}, []string{"main.go", "x/cmd/main.go", "cmd/aibow/app.go"}},
		{"tool/*.go", []string{"tool/list.go"}, []string{"tool/x/list.go", "list.go"}},
		{"cmd/**", []string{"cmd/a.go", "cmd/x/y.txt"}, []string{"a.go"}},
		{"./tool/*.go", []string{"tool/list.go"}, nil}, // ./ は省く
		// \ は区切りに直さず、path.Match のとおり「次の記号をそのまま」(正規表現の癖の *\.go・[ を含む名前)
		{`*\.go`, []string{"main.go", "a/b.go"}, []string{"main_go"}},
		{`app/\[id\]/*.tsx`, []string{"app/[id]/page.tsx"}, []string{"app/i/page.tsx"}},
		{`cmd\**\main.go`, nil, []string{"cmd/aibow/main.go"}},
		{"[ab].txt", []string{"a.txt", "x/b.txt"}, []string{"c.txt"}},
	}
	for _, c := range cases {
		g, err := compileGlob(c.pattern)
		if err != nil {
			t.Errorf("%s: %v", c.pattern, err)
			continue
		}
		for _, p := range c.yes {
			if !g.match(p) {
				t.Errorf("%s は %s に合うはず", c.pattern, p)
			}
		}
		for _, p := range c.no {
			if g.match(p) {
				t.Errorf("%s は %s に合わないはず", c.pattern, p)
			}
		}
	}
	for _, bad := range []string{"[", "a/[x", "  "} {
		if _, err := compileGlob(bad); err == nil {
			t.Errorf("%q はエラーのはず", bad)
		}
	}
}

func TestListFilesPattern(t *testing.T) {
	dir := workDir(t, map[string]string{
		"main.go": "", "go.mod": "", "tool/list.go": "", "tool/list_test.go": "", "cmd/aibow/main.go": "", "cmd/main.go": "",
		".git/x.go": "", "web/node_modules/m/i.go": "", ".claude/w/a.go": "", "empty/": "",
	})
	tl := listFiles{one(dir)}
	cases := map[string]string{
		// 浅い順・名前の順は pattern が無いときと同じ。フォルダは並べない。.git・node_modules・隠しフォルダの中は見ない
		"*.go":           "main.go\ncmd/main.go\ntool/list.go\ntool/list_test.go\ncmd/aibow/main.go",
		"**/*_test.go":   "tool/list_test.go",
		"cmd/**/main.go": "cmd/main.go\ncmd/aibow/main.go",
		"*.ts":           "(pattern *.ts に合うファイルはありません)",
	}
	for pattern, want := range cases {
		if got, err := run(t, tl, "pattern", pattern); err != nil || got != want {
			t.Errorf("%s:\n%s\n%v\n期待:\n%s", pattern, got, err, want)
		}
	}
	// path と一緒に使うと、その下だけ。パスはいつも作業フォルダから
	if got, err := run(t, tl, "path", "cmd", "pattern", "*.go"); err != nil || got != "cmd/main.go\ncmd/aibow/main.go" {
		t.Errorf("path=cmd: %q, %v", got, err)
	}
	// 隠しフォルダも path に指定すれば探す
	if got, err := run(t, tl, "path", ".claude", "pattern", "*.go"); err != nil || got != ".claude/w/a.go" {
		t.Errorf("path=.claude: %q, %v", got, err)
	}
	if _, err := run(t, tl, "pattern", "[x"); err == nil || !strings.Contains(err.Error(), "読めません") {
		t.Errorf("読めないパターン: %v", err)
	}
}

func TestSearchFiles(t *testing.T) {
	dir := workDir(t, map[string]string{
		"main.go":           "package main\n\nfunc main() {\n\trun()\n}\n",
		"tool/run.go":       "package tool\r\n\r\n// Run は実行する\r\nfunc Run() {}\r\n",
		"tool/run_test.go":  "package tool\n\nfunc TestRun(t *testing.T) {}\n",
		"docs/memo.md":      "# メモ\nfunc の説明\n",
		".git/config":       "func x",
		"node_modules/m.js": "func y",
		".claude/w/a.go":    "func hidden() {}\n",
		"img.png":           "\x89PNG\x00\x00func",
		"sjis.txt":          "\x82\xa0func",
		"big/large.txt":     strings.Repeat("x", searchMaxBytes) + "\nfunc",
		"long.txt":          "func " + strings.Repeat("あ", 400),
	})
	tl := searchFiles{one(dir)}

	// 浅い順・名前の順。パス:行番号: 行。CRLF の CR は見せない。読めないファイルは数だけ
	got, err := run(t, tl, "pattern", `^func`)
	want := strings.Join([]string{
		"long.txt:1: func " + strings.Repeat("あ", searchLineMax-5) + "…",
		"main.go:3: func main() {",
		"docs/memo.md:2: func の説明",
		"tool/run.go:4: func Run() {}",
		"tool/run_test.go:3: func TestRun(t *testing.T) {}",
		"(バイナリ・UTF-8 でない・1 MB を超えるファイル 3 件は、探さずに飛ばしました)",
	}, "\n")
	if err != nil || got != want {
		t.Errorf("全体:\n%s\n%v\n期待:\n%s", got, err, want)
	}

	// glob で絞る・path で絞る・ファイルを名指しする・大文字と小文字
	checks := []struct {
		args []string
		want string
	}{
		{[]string{"pattern", "func", "glob", "*_test.go"}, "tool/run_test.go:3: func TestRun(t *testing.T) {}"},
		{[]string{"pattern", "func", "path", "tool", "glob", "**/run.go"}, "tool/run.go:4: func Run() {}"},
		{[]string{"pattern", "package", "path", "main.go", "glob", "*.md"}, "main.go:1: package main"}, // ファイルを名指しすれば glob は見ない
		{[]string{"pattern", "run\\(", "path", "main.go"}, "main.go:4: \trun()"},
		{[]string{"pattern", "^run", "path", "tool", "ignore_case", "true", "glob", "*.go"}, "(見つかりませんでした)"},
		{[]string{"pattern", "// run", "path", "tool", "ignore_case", "true"}, "tool/run.go:3: // Run は実行する"},
		{[]string{"pattern", "hidden", "path", ".claude"}, ".claude/w/a.go:1: func hidden() {}"}, // 隠しフォルダは path に指定したときだけ
		{[]string{"pattern", "nothing", "path", "docs"}, "(見つかりませんでした)"},
		{[]string{"pattern", "func", "glob", `*\.go`}, "main.go:3: func main() {\ntool/run.go:4: func Run() {}\ntool/run_test.go:3: func TestRun(t *testing.T) {}"},
	}
	for _, c := range checks {
		if got, err := run(t, tl, c.args...); err != nil || got != c.want {
			t.Errorf("%v:\n%q\n%v\n期待: %q", c.args, got, err, c.want)
		}
	}

	// エラー:読めない正規表現・読めない glob・無い場所・外
	for _, c := range [][]string{
		{"pattern", "run("},
		{"pattern", ""},
		{"pattern", "x", "glob", "[a"},
		{"pattern", "x", "path", "nothing"},
		{"pattern", "x", "path", "../outside"},
		{"pattern", "secret", "path", filepath.Join(filepath.Dir(dir), "outside")},
	} {
		if got, err := run(t, tl, c...); err == nil {
			t.Errorf("%v: %q はエラーのはず", c, got)
		}
	}
}

// 上限(件数・文字数)で切っても、全部の件数とファイルの数を書く(完了の条件 1)
func TestSearchFilesLimit(t *testing.T) {
	files := map[string]string{}
	for i := range 30 {
		files[fmt.Sprintf("f%02d.txt", i)] = strings.Repeat("hit\n", 5) // 30 ファイル × 5 行 = 150 件
	}
	got, err := run(t, searchFiles{one(workDir(t, files))}, "pattern", "hit")
	lines := strings.Split(got, "\n")
	if err != nil || len(lines) != searchMax+1 || lines[0] != "f00.txt:1: hit" ||
		lines[searchMax] != "(一致は全部で 150 件(30 ファイル)あり、100 件までを並べました。path か glob で絞ってください)" {
		t.Errorf("件数: %d 行(最後: %q), %v", len(lines), lines[len(lines)-1], err)
	}

	// 件数が少なくても、文字数で切る
	files = map[string]string{"a.txt": strings.Repeat("hit "+strings.Repeat("x", 280)+"\n", 50)}
	got, err = run(t, searchFiles{one(workDir(t, files))}, "pattern", "hit")
	lines = strings.Split(got, "\n")
	if err != nil || len(lines) > 30 || !strings.Contains(lines[len(lines)-1], "全部で 50 件(1 ファイル)") {
		t.Errorf("文字数: %d 行(最後: %q), %v", len(lines), lines[len(lines)-1], err)
	}
}

// 時間切れは、途中までの結果ではなく、絞る案内を返す
func TestSearchFilesTimeout(t *testing.T) {
	dir := workDir(t, map[string]string{"a.txt": "x"})
	tl := searchFiles{one(dir)}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if _, err := tl.Run(ctx, argsFor(t, tl, "pattern", "x")); err == nil || !strings.Contains(err.Error(), "絞って") {
		t.Errorf("時間切れ: %v", err)
	}
}

// フォルダが複数なら folder を受け付ける(完了の条件 4)
func TestSearchFilesFolder(t *testing.T) {
	main, notes := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(main, "app.go"), []byte("package app\n"), 0o644)
	os.WriteFile(filepath.Join(notes, "memo.md"), []byte("# メモ\npackage の話\n"), 0o644)
	tl := searchFiles{folderSet{{Name: "app", Path: main, Main: true, Writable: true}, {Name: "notes", Path: notes}}}
	if !strings.Contains(string(tl.Def().Parameters), `"folder":`) {
		t.Error("フォルダが複数なのに folder の引数が無い")
	}
	if got, err := run(t, tl, "pattern", "package", "folder", "notes"); err != nil || got != "memo.md:2: package の話" {
		t.Errorf("notes: %q, %v", got, err)
	}
	if got, err := run(t, tl, "pattern", "package"); err != nil || got != "app.go:1: package app" {
		t.Errorf("メイン: %q, %v", got, err)
	}
	if strings.Contains(string(searchFiles{one(main)}.Def().Parameters), `"folder":`) {
		t.Error("フォルダが1つなのに folder の引数がある")
	}
}

// search_files があれば、Windows の環境の説明で findstr ではなく search_files を案内する(決定事項 8)
func TestShellNoteFor(t *testing.T) {
	if runtime.GOOS != "windows" {
		if ShellNoteFor(true) != ShellNote || ShellNoteFor(false) != ShellNote {
			t.Errorf("/bin/sh は書き分けない: %q %q", ShellNoteFor(true), ShellNoteFor(false))
		}
		return
	}
	if got := ShellNoteFor(true); !strings.Contains(got, "use search_files instead of findstr") || strings.Contains(got, "use dir, type, findstr") {
		t.Errorf("search_files あり: %q", got)
	}
	if ShellNoteFor(false) != ShellNote {
		t.Errorf("search_files なし: %q", ShellNoteFor(false))
	}
}
