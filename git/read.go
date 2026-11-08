package git

// read.go: 読むだけの Git の操作(0041 の段2)。モデルに見せるツール(tool の git_status・git_diff)が中で呼ぶ。
//
// worktree.go の Changes・Diff は、worktree の中を全部ステージしてから(add -A)比べる。worktree の索引はアプリのものなので構わないが、
// ここはユーザーの本体のフォルダでも呼ばれるので、索引を変えるコマンドは使わない(status と diff だけ)。
// そのため Git に入っていないファイル(untracked)は、差分(WorkDiff)には出ない。状態(Status)には「Git に入っていない」として出る。

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Untracked は、Git に入っていないファイル(StatusEntry.Status。足した・変えた・消したは worktree.go の Added・Modified・Deleted)。
const Untracked = "untracked"

// IsRepo は、フォルダ path がリポジトリ(.git がある)か。worktree では .git がファイルなので、ある・なしだけを見る(0039 の決定事項 2)。
// git の exe は呼ばない(ツールの一覧を決めるたびに呼ばれるため)。
func IsRepo(path string) bool {
	_, err := os.Stat(filepath.Join(path, ".git"))
	return err == nil
}

// StatusEntry は、作業ツリーで HEAD から変わっているファイル1つ。
type StatusEntry struct {
	Path   string // リポジトリの中の相対パス(/ 区切り)。Git に入っていないフォルダは、末尾が /
	Status string // added / modified / deleted / untracked
}

// Status は、フォルダ dir の作業ツリーの変わったファイルの一覧(git status --porcelain の並び。無視しているファイルは入らない)。
//
// porcelain の1件は「XY パス」で、X は索引(ステージ)、Y は作業ツリーの変わり方。モデルにはステージの区別は要らないので、
// どちらかが D なら消した・A なら足した・?? なら Git に入っていない・ほかは変えた、の4つにまとめる。
// 名前の変更は、消したものと足したものの2つにする(--no-renames。差分の読み方と同じ)。
func Status(ctx context.Context, dir string) ([]StatusEntry, error) {
	out, err := run(ctx, dir, nil, "status", "--porcelain=v1", "-z", "--no-renames")
	if err != nil {
		return nil, err
	}
	var list []StatusEntry
	for rec := range strings.SplitSeq(out, "\x00") {
		if len(rec) < 4 {
			continue
		}
		xy, path := rec[:2], rec[3:]
		e := StatusEntry{Path: path, Status: Modified}
		switch {
		case xy == "??":
			e.Status = Untracked
		case strings.Contains(xy, "D"):
			e.Status = Deleted
		case strings.Contains(xy, "A"):
			e.Status = Added
		}
		list = append(list, e)
	}
	return list, nil
}

// ErrNoCommit は、まだコミットが無いリポジトリで HEAD との差分を求めたときのエラー。
var ErrNoCommit = errors.New("このリポジトリにはまだコミットがないので、HEAD との差分はありません(git_status で変わったファイルを見てください)")

// WorkDiff は、フォルダ dir の作業ツリーと HEAD との差分(ステージしたものも、していないものも含む)。paths を渡せばそこだけ。
// Git に入っていないファイルは含まない(索引を変えないため。上の説明)。
func WorkDiff(ctx context.Context, dir string, paths ...string) (string, error) {
	if _, err := run(ctx, dir, nil, "rev-parse", "--verify", "--quiet", "HEAD^{commit}"); err != nil {
		var ge *Error
		if errors.As(err, &ge) && ge.Stderr == "" {
			return "", ErrNoCommit
		}
		return "", err
	}
	args := append([]string{"diff", "--no-renames", "--no-ext-diff", "HEAD", "--"}, paths...)
	return run(ctx, dir, nil, args...)
}
