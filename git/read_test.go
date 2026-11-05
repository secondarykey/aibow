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

// 状態は、足した(ステージした)・変えた・消した・Git に入っていないの4つ。差分は HEAD との差で、Git に入っていないものは入らない。
// どちらも索引を変えない(本体のフォルダでも呼ばれるため。0041 の段2)
func TestStatusAndWorkDiff(t *testing.T) {
	repo := newRepo(t, map[string]string{"a.go": "package a\n", "b.go": "package b\n", "c.go": "package c\n"})
	ctx := context.Background()
	write(t, filepath.Join(repo, "a.go"), "package a\n\nfunc A() {}\n")
	if err := os.Remove(filepath.Join(repo, "b.go")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(repo, "new.go"), "package a\n")
	mustRun(t, repo, "add", "new.go")
	write(t, filepath.Join(repo, "memo.txt"), "メモ\n")

	list, err := Status(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	want := []StatusEntry{{"a.go", Modified}, {"b.go", Deleted}, {"new.go", Added}, {"memo.txt", Untracked}}
	for _, w := range want {
		if !slices.Contains(list, w) {
			t.Errorf("状態に %+v がありません: %+v", w, list)
		}
	}
	if len(list) != len(want) {
		t.Errorf("状態: %+v", list)
	}

	diff, err := WorkDiff(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"+func A() {}", "deleted file mode", "b/new.go"} {
		if !strings.Contains(diff, s) {
			t.Errorf("差分に %q がありません:\n%s", s, diff)
		}
	}
	if strings.Contains(diff, "memo.txt") {
		t.Errorf("Git に入っていないファイルが差分に出ました:\n%s", diff)
	}
	// path で絞る
	if d, _ := WorkDiff(ctx, repo, "a.go"); !strings.Contains(d, "a.go") || strings.Contains(d, "new.go") {
		t.Errorf("a.go だけの差分:\n%s", d)
	}
	// 索引は変わらない(memo.txt はステージされず、a.go の変更もステージされない)
	if staged := mustRun(t, repo, "diff", "--cached", "--name-only"); strings.TrimSpace(staged) != "new.go" {
		t.Errorf("索引が変わりました: %q", staged)
	}
}

// worktree の中では、worktree を作ったときのコミット(HEAD)との差分になる(会話で変えたものだけ。0041 の決定事項 13)。
// 本体のコミットしていない変更は出ない
func TestWorkDiffInWorktree(t *testing.T) {
	repo := newRepo(t, map[string]string{"a.go": "package a\n"})
	write(t, filepath.Join(repo, "a.go"), "package a // 本体の変更\n")
	wt, _ := addWorktree(t, repo)
	write(t, filepath.Join(wt, "b.go"), "package a\n")
	write(t, filepath.Join(wt, "a.go"), "package a // worktree の変更\n")
	ctx := context.Background()

	diff, err := WorkDiff(ctx, wt)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, "worktree の変更") || strings.Contains(diff, "本体の変更") {
		t.Errorf("worktree の差分:\n%s", diff)
	}
	list, err := Status(ctx, wt)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(list, []StatusEntry{{"a.go", Modified}, {"b.go", Untracked}}) {
		t.Errorf("worktree の状態: %+v", list)
	}
}

// まだコミットが無いリポジトリでは、差分は分かる文のエラー(状態は出せる)
func TestWorkDiffWithoutCommit(t *testing.T) {
	if err := Available(); err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	mustRun(t, dir, "init", "-q")
	write(t, filepath.Join(dir, "a.go"), "package a\n")
	if _, err := WorkDiff(context.Background(), dir); !errors.Is(err, ErrNoCommit) {
		t.Errorf("err = %v", err)
	}
	if list, err := Status(context.Background(), dir); err != nil || !slices.Equal(list, []StatusEntry{{"a.go", Untracked}}) {
		t.Errorf("状態: %+v, %v", list, err)
	}
}

func TestIsRepo(t *testing.T) {
	if IsRepo(t.TempDir()) {
		t.Error("空のフォルダをリポジトリと見ました")
	}
}
