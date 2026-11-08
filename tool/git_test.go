package tool

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitRepo は、files を最初のコミットにした一時のリポジトリを作る。git が無ければテストを飛ばす。
func gitRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git がありません")
	}
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", "main")
	gitRun(t, dir, "config", "user.name", "aibow test")
	gitRun(t, dir, "config", "user.email", "test@example.com")
	gitRun(t, dir, "config", "core.autocrlf", "false")
	gitRun(t, dir, "config", "commit.gpgsign", "false")
	for name, body := range files {
		writeTestFile(t, filepath.Join(dir, name), body)
	}
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "first")
	return dir
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func writeTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runGit(t *testing.T, tools []Tool, name string, args Args) (string, error) {
	t.Helper()
	g := Find(tools, name)
	if g == nil {
		t.Fatalf("%s がありません: %s", name, Names(tools))
	}
	return g.Run(context.Background(), args)
}

// Git のツールは、リポジトリで git があるときだけ出る(0041 の完了の条件 4)
func TestGitInOnlyForRepos(t *testing.T) {
	if got := GitIn(Single(t.TempDir())); got != nil {
		t.Errorf("Git でないフォルダ: %s", Names(got))
	}
	repo := gitRepo(t, map[string]string{"a.go": "package a\n"})
	if got := Names(GitIn(Single(repo))); got != "git_diff, git_status" {
		t.Errorf("リポジトリ: %s", got)
	}
	saved := gitAvailable
	gitAvailable = func() error { return errors.New("git が見つかりません") }
	t.Cleanup(func() { gitAvailable = saved })
	if got := GitIn(Single(repo)); got != nil {
		t.Errorf("git が無いとき: %s", Names(got))
	}
}

// 状態は「変わり方: パス」の行。差分は HEAD との差で、path で絞れる。フォルダの名前を先頭に付けたパスはファイルのツールと同じく直し方を返す
func TestGitStatusAndDiff(t *testing.T) {
	repo := gitRepo(t, map[string]string{"a.go": "package a\n", "b.go": "package b\n"})
	tools := GitIn(Single(repo))

	if out, err := runGit(t, tools, "git_status", Args{}); err != nil || !strings.Contains(out, "変わったファイルはありません") {
		t.Errorf("変わっていないとき: %q, %v", out, err)
	}
	if out, err := runGit(t, tools, "git_diff", Args{}); err != nil || !strings.Contains(out, "変わったところはありません") {
		t.Errorf("変わっていないときの差分: %q, %v", out, err)
	}

	writeTestFile(t, filepath.Join(repo, "a.go"), "package a\n\nfunc A() {}\n")
	writeTestFile(t, filepath.Join(repo, "b.go"), "package b\n\nfunc B() {}\n")
	writeTestFile(t, filepath.Join(repo, "memo.txt"), "メモ\n")
	out, err := runGit(t, tools, "git_status", Args{})
	if err != nil || out != "modified: a.go\nmodified: b.go\nuntracked: memo.txt" {
		t.Errorf("状態: %q, %v", out, err)
	}
	out, err = runGit(t, tools, "git_diff", Args{"path": "a.go"})
	if err != nil || !strings.Contains(out, "+func A() {}") || strings.Contains(out, "func B") {
		t.Errorf("a.go の差分: %q, %v", out, err)
	}
	// 作業フォルダの中の絶対パスも受け付ける(4B は絶対パスで書きがち)
	if out, err := runGit(t, tools, "git_diff", Args{"path": filepath.Join(repo, "b.go")}); err != nil || !strings.Contains(out, "+func B() {}") {
		t.Errorf("絶対パスの差分: %q, %v", out, err)
	}
	if _, err := runGit(t, tools, "git_diff", Args{"path": "../x.go"}); err == nil {
		t.Error("作業フォルダの外を受け付けました")
	}
	if out, err := runGit(t, tools, "git_diff", Args{"path": "memo.txt"}); err != nil || !strings.Contains(out, "変わっていません") {
		t.Errorf("Git に入っていないファイル: %q, %v", out, err)
	}
}

// 差分は 8,000 文字で切り、入りきらなかったファイルと「path で絞って」を添える(決定事項 11)
func TestGitDiffCut(t *testing.T) {
	big := strings.Repeat("line\n", 3000)
	repo := gitRepo(t, map[string]string{"big.txt": "", "z.go": "package z\n"})
	writeTestFile(t, filepath.Join(repo, "big.txt"), big)
	writeTestFile(t, filepath.Join(repo, "z.go"), "package z\n\nfunc Z() {}\n")
	tools := GitIn(Single(repo))

	out, err := runGit(t, tools, "git_diff", Args{})
	if err != nil {
		t.Fatal(err)
	}
	if n := len([]rune(out)); n > gitMaxRunes+200 {
		t.Errorf("%d 文字返しました", n)
	}
	if !strings.Contains(out, "このあとのファイル: z.go") || !strings.Contains(out, "path でファイルを絞ってください") {
		t.Errorf("切ったことの添え書き:\n%s", out[len(out)-300:])
	}
	// 絞ったうえで長いときは、絞れとは書かない
	out, _ = runGit(t, tools, "git_diff", Args{"path": "big.txt"})
	if !strings.Contains(out, "ここで切りました") || strings.Contains(out, "path でファイルを絞って") {
		t.Errorf("絞ったときの添え書き:\n%s", out[len(out)-200:])
	}
}

// フォルダが複数なら folder で選ぶ。リポジトリでないフォルダを選んだら、そう返す。どれもリポジトリでなければ出さない
func TestGitInFolders(t *testing.T) {
	repo := gitRepo(t, map[string]string{"a.go": "package a\n"})
	docs := t.TempDir()
	folders := []Folder{{Name: "app", Path: repo, Main: true, Writable: true}, {Name: "docs", Path: docs}}
	tools := GitIn(folders)
	if !strings.Contains(string(Find(tools, "git_diff").Def().Parameters), `"folder"`) {
		t.Errorf("folder の引数がありません: %s", Find(tools, "git_diff").Def().Parameters)
	}
	if _, err := runGit(t, tools, "git_status", Args{"folder": "docs"}); err == nil || !strings.Contains(err.Error(), "docs は Git のリポジトリではありません") {
		t.Errorf("リポジトリでないフォルダ: %v", err)
	}
	if out, err := runGit(t, tools, "git_status", Args{"folder": "app"}); err != nil || !strings.Contains(out, "変わったファイルはありません") {
		t.Errorf("app: %q, %v", out, err)
	}
	if got := GitIn([]Folder{{Name: "docs", Path: docs, Main: true}, {Name: "other", Path: t.TempDir()}}); got != nil {
		t.Errorf("どれもリポジトリでないとき: %s", Names(got))
	}
}
