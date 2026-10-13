package tool

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// workDir は作業フォルダを作り、files(相対パス → 中身。末尾が / ならフォルダ)を置く。
// 外を指すパスのテストのため、作業フォルダは一時フォルダの下の work にし、隣に outside を作る。
func workDir(t *testing.T, files map[string]string) string {
	t.Helper()
	base := t.TempDir()
	dir := filepath.Join(base, "work")
	for name, body := range map[string]string{"work/": "", "outside/secret.txt": "secret"} {
		write(t, base, name, body)
	}
	for name, body := range files {
		write(t, dir, name, body)
	}
	return dir
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	if strings.HasSuffix(name, "/") {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// run はツールを、モデルが書いた形(文字列の引数)から CheckArgs を通して実行する
func run(t *testing.T, tl Tool, kv ...string) (string, error) {
	t.Helper()
	var args []Arg
	for i := 0; i+1 < len(kv); i += 2 {
		args = append(args, Arg{kv[i], kv[i+1]})
	}
	a, err := CheckArgs(tl.Def(), args)
	if err != nil {
		return "", err
	}
	return tl.Run(context.Background(), a)
}

func TestLocalPath(t *testing.T) {
	dir := workDir(t, nil)
	ok := map[string]string{
		"":                                ".",
		"main.go":                         "main.go",
		"./src/a.go":                      "src/a.go",
		"src/../b.go":                     "b.go",     // 中で戻るだけなら外に出ていない
		filepath.Join(dir, "src", "a.go"): "src/a.go", // 作業フォルダの中を指す絶対パスは受け付ける
		dir:                               ".",
	}
	for in, want := range ok {
		if got, err := localPath(dir, in); err != nil || got != want {
			t.Errorf("localPath(%q) = %q, %v(期待: %q)", in, got, err, want)
		}
	}
	bad := []string{"..", "../outside/secret.txt", "src/../../x", filepath.Join(filepath.Dir(dir), "outside", "secret.txt")}
	for _, in := range bad {
		if got, err := localPath(dir, in); err == nil || !strings.Contains(err.Error(), "作業フォルダの外") {
			t.Errorf("localPath(%q) = %q, %v(外はエラーのはず)", in, got, err)
		}
	}
}

// 文字列の確かめをすり抜ける、外を指すシンボリックリンクも os.Root が止めること
func TestFilesCannotFollowSymlinkOutside(t *testing.T) {
	dir := workDir(t, map[string]string{"in.txt": "inside"})
	outside := filepath.Join(filepath.Dir(dir), "outside")
	if err := os.Symlink(outside, filepath.Join(dir, "out")); err != nil {
		t.Skipf("シンボリックリンクを作れない環境です(Windows は開発者モードか管理者が要る): %v", err)
	}
	if err := os.Symlink(filepath.Join("..", "outside", "secret.txt"), filepath.Join(dir, "rel.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("in.txt", filepath.Join(dir, "ok.txt")); err != nil {
		t.Fatal(err)
	}
	checkLinkOutside(t, dir, "out/secret.txt", "rel.txt")
	if got, err := run(t, readFile{dir}, "path", "ok.txt"); err != nil || got != "1\tinside" {
		t.Errorf("中を指すリンクは読める: %q, %v", got, err)
	}
}

// Windows のジャンクション(mklink /J)。シンボリックリンクと違って特別な権限なしで作れるので、
// Windows でフォルダの外を指すリンクといえばまずこれ。os.Root はこれも止める
func TestFilesCannotFollowJunctionOutside(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("ジャンクションは Windows だけ")
	}
	dir := workDir(t, nil)
	outside := filepath.Join(filepath.Dir(dir), "outside")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(dir, "out"), outside).CombinedOutput(); err != nil {
		t.Fatalf("ジャンクションを作れません: %v %s", err, out)
	}
	checkLinkOutside(t, dir, "out/secret.txt")
}

// checkLinkOutside は、外を指すリンク out(フォルダ)と paths を、読むことも並べることもできないのを確かめる
func checkLinkOutside(t *testing.T, dir string, paths ...string) {
	t.Helper()
	read, list := readFile{dir}, listFiles{dir}
	for _, p := range paths {
		if got, err := run(t, read, "path", p); err == nil || !strings.Contains(err.Error(), "作業フォルダの外") {
			t.Errorf("read_file %s = %q, %v(外を指すリンクはエラーのはず)", p, got, err)
		}
	}
	if got, err := run(t, list, "path", "out"); err == nil {
		t.Errorf("list_files out = %q(外を指すリンクの中は並べない)", got)
	}
	// 全体の一覧にはリンクの名前だけが出る(たどらない)
	if got, err := run(t, list); err != nil || strings.Contains(got, "secret") {
		t.Errorf("list_files: %q, %v", got, err)
	}
}

func TestListFiles(t *testing.T) {
	dir := workDir(t, map[string]string{
		"main.go": "", "go.mod": "", "b/z.txt": "", "b/a/x.txt": "", "a.txt": "", "empty/": "",
		".git/HEAD": "", "web/node_modules/m/i.js": "", "web/app.ts": "", ".gitignore": "",
		".claude/worktrees/x/a.go": "", ".vscode/settings.json": "", ".env": "",
	})
	tl := listFiles{dir}
	// 浅い順(直下を全部 → 1つ下を全部 → …)。それぞれの中は名前の順。
	// .git と node_modules は名前も出さない。隠しフォルダは名前だけ出して中に入らない。隠しファイルは出す
	want := strings.Join([]string{
		".claude/ (中は省略)", ".env", ".gitignore", ".vscode/ (中は省略)", "a.txt", "b/", "empty/", "go.mod", "main.go", "web/",
		"b/a/", "b/z.txt", "web/app.ts",
		"b/a/x.txt",
		"(「中は省略」は隠しフォルダです。中を見るには path に指定してください)"}, "\n")
	if got, err := run(t, tl); err != nil || got != want {
		t.Errorf("全体:\n%s\n%v\n期待:\n%s", got, err, want)
	}
	// 隠しフォルダも、path に指定すれば中を並べる
	if got, err := run(t, tl, "path", ".claude"); err != nil || got != ".claude/worktrees/\n.claude/worktrees/x/\n.claude/worktrees/x/a.go" {
		t.Errorf(".claude: %q, %v", got, err)
	}
	// 下のフォルダを指定しても、パスは作業フォルダから
	if got, err := run(t, tl, "path", "b/"); err != nil || got != "b/a/\nb/z.txt\nb/a/x.txt" {
		t.Errorf("b: %q, %v", got, err)
	}
	if got, err := run(t, tl, "path", "empty"); err != nil || !strings.Contains(got, "空のフォルダ") {
		t.Errorf("空: %q, %v", got, err)
	}
	for _, p := range []string{"main.go", "nothing", "../outside"} {
		if got, err := run(t, tl, "path", p); err == nil {
			t.Errorf("%s: %q はエラーのはず", p, got)
		}
	}
}

func TestListFilesLimit(t *testing.T) {
	files := map[string]string{}
	for i := range listMax + 1 {
		files[fmt.Sprintf("f%04d.txt", i)] = ""
	}
	got, err := run(t, listFiles{workDir(t, files)})
	lines := strings.Split(got, "\n")
	if err != nil || len(lines) != listMax+1 || lines[listMax-1] != fmt.Sprintf("f%04d.txt", listMax-1) || !strings.Contains(lines[listMax], "切りました") {
		t.Errorf("%d 行(最後: %q), %v", len(lines), lines[len(lines)-1], err)
	}
	// ちょうど上限なら、切った旨は書かない
	delete(files, fmt.Sprintf("f%04d.txt", listMax))
	if got, _ := run(t, listFiles{workDir(t, files)}); strings.Contains(got, "切りました") {
		t.Error("ちょうど上限で「切りました」が出ています")
	}

	// 件数が少なくても、パスが長ければ文字数で切る(深い場所のファイルはパスが長い)
	files = map[string]string{}
	for i := range 100 {
		files[fmt.Sprintf("%s%03d.txt", strings.Repeat("x", 92), i)] = "" // 改行を足して 100 文字
	}
	got, err = run(t, listFiles{workDir(t, files)})
	lines = strings.Split(got, "\n")
	if err != nil || len(lines) != listMaxRunes/100+1 || !strings.Contains(lines[len(lines)-1], "切りました") {
		t.Errorf("文字数: %d 行(最後: %q), %v", len(lines), lines[len(lines)-1], err)
	}
}

func TestReadFile(t *testing.T) {
	var long []string
	for i := 1; i <= 450; i++ {
		long = append(long, fmt.Sprintf("line %d", i))
	}
	dir := workDir(t, map[string]string{
		"main.go":  "package main\n\nfunc main() {}\n",
		"crlf.txt": "a\r\nb\r\n",
		"long.txt": strings.Join(long, "\n"),
		"wide.txt": strings.Repeat("あ", readMaxRunes+100) + "\nnext",
		"many.txt": strings.Repeat(strings.Repeat("x", 99)+"\n", 150), // 行番号とタブを足すと約 104 文字 × 150 行で、8,000 文字を超える
		"empty.go": "",
		"bin.dat":  "PNG\x00\x01",
		"sjis.txt": "\x82\xa0", // Shift_JIS の「あ」
		"sub/":     "",
	})
	tl := readFile{dir}

	if got, err := run(t, tl, "path", "main.go"); err != nil || got != "1\tpackage main\n2\t\n3\tfunc main() {}" {
		t.Errorf("main.go: %q, %v", got, err)
	}
	if got, err := run(t, tl, "path", "crlf.txt"); err != nil || got != "1\ta\n2\tb" {
		t.Errorf("CRLF: %q, %v", got, err)
	}
	if got, err := run(t, tl, "path", filepath.Join(dir, "main.go"), "offset", "3"); err != nil || got != "3\tfunc main() {}" {
		t.Errorf("絶対パスと offset: %q, %v", got, err)
	}

	// 長いファイルは 200 行ずつ。続きの読み方を書き添え、モデルがそのとおりに読める
	got, err := run(t, tl, "path", "long.txt")
	if err != nil || strings.Count(got, "\n") != readMaxLines || !strings.HasSuffix(got, "(全 450 行のうち 1〜200 行目です。続きは offset=201 で読めます)") {
		t.Errorf("1回目: 末尾 %q, %v", got[max(0, len(got)-80):], err)
	}
	got, err = run(t, tl, "path", "long.txt", "offset", " 401 ", "limit", "500") // 空白や上限を超える limit も受け付ける
	if err != nil || !strings.HasPrefix(got, "401\tline 401\n") || !strings.HasSuffix(got, "450\tline 450") {
		t.Errorf("最後: %q, %v", got, err)
	}
	if got, err := run(t, tl, "path", "long.txt", "offset", "10", "limit", "2"); err != nil || got != "10\tline 10\n11\tline 11\n(全 450 行のうち 10〜11 行目です。続きは offset=12 で読めます)" {
		t.Errorf("limit: %q, %v", got, err)
	}

	// 8,000 文字で切る。1行だけで超えるなら、その行を切って次へ進めるようにする
	got, err = run(t, tl, "path", "many.txt")
	if err != nil || len(got) > readMaxRunes+200 || !strings.Contains(got, "続きは offset=78 で読めます") {
		t.Errorf("文字数: %d 文字 / 末尾 %q, %v", len(got), got[max(0, len(got)-80):], err)
	}
	got, err = run(t, tl, "path", "wide.txt")
	if err != nil || !strings.Contains(got, "1 行目は長いため、途中で切りました") || !strings.Contains(got, "続きは offset=2 で読めます") {
		t.Errorf("長い行: 末尾 %q, %v", got[max(0, len(got)-120):], err)
	}

	if got, err := run(t, tl, "path", "empty.go"); err != nil || !strings.Contains(got, "空のファイル") {
		t.Errorf("空: %q, %v", got, err)
	}
	for _, c := range [][]string{
		{"path", "bin.dat"}, {"path", "sjis.txt"}, {"path", "sub"}, {"path", "nothing.go"}, {"path", "../outside/secret.txt"},
		{"path", "main.go", "offset", "4"}, {"path", "main.go", "offset", "0"}, {"path", "main.go", "limit", "0"},
		{"path", "main.go", "offset", "two"}, {},
	} {
		if got, err := run(t, tl, c...); err == nil {
			t.Errorf("%v: %q はエラーのはず", c, got)
		}
	}
}

// 作業フォルダが消えていたら、ツールはエラーを返す(アプリは止まらない)
func TestFilesWorkDirGone(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "gone")
	for _, tl := range Files(dir) {
		if _, err := run(t, tl, "path", "a.txt"); err == nil || !strings.Contains(err.Error(), "作業フォルダ") {
			t.Errorf("%s: %v", tl.Def().Name, err)
		}
	}
}

func TestCheckArgsConvertsNumbers(t *testing.T) {
	def := readFile{}.Def()
	got, err := CheckArgs(def, []Arg{{"path", "a"}, {"offset", "\n12\n"}})
	if err != nil || got["offset"] != 12 || got.Int("limit", 200) != 200 || got.String("path") != "a" {
		t.Errorf("got %#v, %v", got, err)
	}
	if _, err := CheckArgs(def, []Arg{{"path", "a"}, {"limit", "1.5"}}); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Errorf("整数でない: %v", err)
	}
}
