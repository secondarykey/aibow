package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// newRepo は、テスト用の一時リポジトリを git init で作り、files を最初のコミットにする。git が無ければテストを飛ばす。
func newRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	if err := Available(); err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	ctx := context.Background()
	mustRun(t, dir, "init", "-q", "-b", "main")
	// テストの PC の設定(改行の変換・署名など)に左右されないよう、このリポジトリだけで決める
	mustRun(t, dir, "config", "user.name", "aibow test")
	mustRun(t, dir, "config", "user.email", "test@example.com")
	mustRun(t, dir, "config", "core.autocrlf", "false")
	mustRun(t, dir, "config", "commit.gpgsign", "false")
	for name, body := range files {
		write(t, filepath.Join(dir, name), body)
	}
	mustRun(t, dir, "add", "-A")
	if _, err := run(ctx, dir, nil, "commit", "-q", "-m", "first"); err != nil {
		t.Fatal(err)
	}
	return dir
}

func mustRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := run(context.Background(), dir, nil, args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// addWorktree は、本体の .aibow/worktrees/test に worktree を作る(アプリと同じ置き場所・同じ exclude)。
func addWorktree(t *testing.T, repo string) (wt, base string) {
	t.Helper()
	ctx := context.Background()
	if err := Exclude(ctx, repo, "/.aibow/"); err != nil {
		t.Fatal(err)
	}
	wt = filepath.Join(repo, ".aibow", "worktrees", "test")
	base, err := Add(ctx, repo, wt, "aibow/test")
	if err != nil {
		t.Fatal(err)
	}
	return wt, base
}

// 作る → 書き換える → 差分 → 選んで当てる → 取り込み済み → 消す、の流れ。
// 本体は worktree の書き換えで変わらず、git status にも .aibow/ が出ず、当てたあとも索引は変わらない(0039 の完了の条件 1・2・7・8)。
func TestWorktreeFlow(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, map[string]string{"a.txt": "one\ntwo\n", "b.txt": "bee\n", "keep.txt": "keep\n"})
	wt, base := addWorktree(t, repo)

	if out := mustRun(t, repo, "status", "--porcelain"); out != "" {
		t.Fatalf("worktree を作ったら、本体の git status に出た: %q", out)
	}
	if again := Exclude(ctx, repo, "/.aibow/"); again != nil {
		t.Fatal(again)
	}
	exclude := read(t, filepath.Join(repo, ".git", "info", "exclude"))
	if strings.Count(exclude, "/.aibow/") != 1 {
		t.Fatalf("exclude の行が重なった: %q", exclude)
	}

	write(t, filepath.Join(wt, "a.txt"), "one\nTWO\n")
	write(t, filepath.Join(wt, "dir", "c.txt"), "new\n")
	if err := os.Remove(filepath.Join(wt, "b.txt")); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(repo, "a.txt")) != "one\ntwo\n" {
		t.Fatal("worktree の書き換えで本体が変わった")
	}

	changes, err := Changes(ctx, wt, base)
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, c := range changes {
		got = append(got, c.Status+" "+c.Path)
	}
	want := []string{"modified a.txt", "deleted b.txt", "added dir/c.txt"}
	if !slices.Equal(got, want) {
		t.Fatalf("変わったファイル = %v, want %v", got, want)
	}
	done, err := Applied(ctx, repo, changes)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(done, true) {
		t.Fatalf("まだ何も取り込んでいないのに取り込み済み: %v", done)
	}

	// 本体のコミットしていない変更(別の行)があっても当たる
	write(t, filepath.Join(repo, "keep.txt"), "keep\nmine\n")
	mustRun(t, repo, "add", "keep.txt")
	indexBefore := mustRun(t, repo, "diff", "--cached", "--name-only")

	patch, err := Diff(ctx, wt, base, true, "a.txt", "dir/c.txt")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(patch, "b.txt") {
		t.Fatalf("選んでいないファイルの差分が入った:\n%s", patch)
	}
	if err := Apply(ctx, repo, []byte(patch)); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(repo, "a.txt")) != "one\nTWO\n" || read(t, filepath.Join(repo, "dir", "c.txt")) != "new\n" {
		t.Fatal("選んだファイルが本体に入っていない")
	}
	if _, err := os.Stat(filepath.Join(repo, "b.txt")); err != nil {
		t.Fatal("選んでいない b.txt まで消えた")
	}
	if after := mustRun(t, repo, "diff", "--cached", "--name-only"); after != indexBefore {
		t.Fatalf("当てたら本体の索引が変わった: %q → %q", indexBefore, after)
	}
	done, err = Applied(ctx, repo, changes)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(done, []bool{true, false, true}) {
		t.Fatalf("取り込み済み = %v, want [true false true]", done)
	}

	if err := Remove(ctx, repo, wt, "aibow/test"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("worktree のフォルダが残った: %v", err)
	}
	if ok, err := BranchExists(ctx, repo, "aibow/test"); err != nil || ok {
		t.Fatalf("ブランチが残った: %v %v", ok, err)
	}
}

// 本体の同じ行が変わっているときは、何も変えずに失敗し、当たらないファイルが分かる(0039 の完了の条件 7)。
func TestApplyConflict(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, map[string]string{"a.txt": "one\ntwo\n"})
	wt, base := addWorktree(t, repo)
	write(t, filepath.Join(wt, "a.txt"), "one\nTWO\n")
	write(t, filepath.Join(wt, "new.txt"), "new\n")
	write(t, filepath.Join(repo, "a.txt"), "one\nzwei\n") // 本体の同じ行を変えておく

	patch, err := Diff(ctx, wt, base, true)
	if err != nil {
		t.Fatal(err)
	}
	err = Apply(ctx, repo, []byte(patch))
	var ae *ApplyError
	if !errors.As(err, &ae) {
		t.Fatalf("当たらないのに ApplyError でない: %v", err)
	}
	if !slices.Equal(ae.Files, []string{"a.txt"}) {
		t.Fatalf("当たらないファイル = %v, want [a.txt](出力: %s)", ae.Files, ae.Output)
	}
	if _, err := os.Stat(filepath.Join(repo, "new.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("当たらないのに、ほかのファイルだけ入った(全部当たるか、何も変えないか、のはず)")
	}
	if read(t, filepath.Join(repo, "a.txt")) != "one\nzwei\n" {
		t.Fatal("当たらないのに本体のファイルが変わった")
	}
}

// 本体のコミットしていない変更の数(作るときの知らせ。0039 の完了の条件 3)
func TestUncommitted(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, map[string]string{"a.txt": "a\n", "b.txt": "b\n"})
	if n, err := Uncommitted(ctx, repo); err != nil || n != 0 {
		t.Fatalf("Uncommitted = %d, %v, want 0", n, err)
	}
	write(t, filepath.Join(repo, "a.txt"), "changed\n")
	write(t, filepath.Join(repo, "untracked.txt"), "u\n")
	if n, err := Uncommitted(ctx, repo); err != nil || n != 2 {
		t.Fatalf("Uncommitted = %d, %v, want 2", n, err)
	}
}

// まだコミットが無いリポジトリでは作れない(理由が分かるエラー)
func TestAddWithoutCommit(t *testing.T) {
	if err := Available(); err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	mustRun(t, dir, "init", "-q")
	_, err := Add(context.Background(), dir, filepath.Join(dir, ".aibow", "worktrees", "x"), "aibow/x")
	if err == nil || !strings.Contains(err.Error(), "コミットが無い") {
		t.Fatalf("err = %v", err)
	}
}

// フォルダを手で消したあとでも、Git の記録とブランチを片付けられる
func TestRemoveMissingFolder(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, map[string]string{"a.txt": "a\n"})
	wt, _ := addWorktree(t, repo)
	if err := os.RemoveAll(wt); err != nil {
		t.Fatal(err)
	}
	if err := Remove(ctx, repo, wt, "aibow/test"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := BranchExists(ctx, repo, "aibow/test"); ok {
		t.Fatal("ブランチが残った")
	}
	if out := mustRun(t, repo, "worktree", "list", "--porcelain"); strings.Contains(out, "aibow/test") {
		t.Fatalf("Git の記録が残った: %s", out)
	}
}

// Windows の改行の変換(core.autocrlf=true)でも、取り込んだファイルは取り込み済みになる
// (本体には CRLF で書かれ、worktree には LF のままでも、ハッシュは変換してから計算するので同じ)
func TestAppliedWithAutoCRLF(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, map[string]string{"a.txt": "one\ntwo\n"})
	mustRun(t, repo, "config", "core.autocrlf", "true")
	wt, base := addWorktree(t, repo)
	write(t, filepath.Join(wt, "a.txt"), "one\nTWO\n")
	write(t, filepath.Join(wt, "new.txt"), "new\n")
	changes, err := Changes(ctx, wt, base)
	if err != nil {
		t.Fatal(err)
	}
	patch, err := Diff(ctx, wt, base, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, repo, []byte(patch)); err != nil {
		t.Fatal(err)
	}
	done, err := Applied(ctx, repo, changes)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(done, []bool{true, true}) {
		t.Fatalf("取り込み済み = %v, want [true true](本体の a.txt: %q)", done, read(t, filepath.Join(repo, "a.txt")))
	}
}

// git が PATH に無いときは、理由の分かるエラー(画面は切り替えを灰色にして、これを出す。0039 の完了の条件 4)
func TestAvailableWithoutGit(t *testing.T) {
	found.mu.Lock()
	saved := found.path
	found.path = ""
	found.mu.Unlock()
	t.Cleanup(func() {
		found.mu.Lock()
		found.path = saved
		found.mu.Unlock()
	})
	t.Setenv("PATH", "")
	if err := Available(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Available = %v, want ErrNotFound", err)
	}
}
