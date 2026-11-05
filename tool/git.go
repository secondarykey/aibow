package tool

// git.go: 読むだけの Git のツール(git_status・git_diff。0041 の段2)。
//
// 直したあとに「何を変えたか」をモデル自身が見直す道具。run_command で git diff を打つこともできるが、
// 確認のカードが出る・シェルごとの書き方(cmd.exe の引用符など)でつまずく・出力が長いと会話の上限を食う。
// 読むだけのツールなら確認は要らず(list_files・search_files と同じ)、出力の上限もここで決められる。
//
// Git の操作は git パッケージが行い、ここは引数と結果の形だけを受け持つ(tool → git の向き。0039)。
// モデルには Git を書き換える操作(commit・push・branch)はさせない(0039 と同じ)。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/secondarykey/aibow/git"
)

const gitMaxRunes = 8000 // 返す文字数の上限(read_file・search_files と同じ)

// gitAvailable は、git の exe が使えるか(テストで「git が無い」を作るために変数にしている)
var gitAvailable = git.Available

// GitIn は、フォルダの組 folders の読むだけの Git のツール。
// どれかのフォルダが Git のリポジトリで、git の exe が見つかるときだけ出す(0039 の worktree と同じ見分け方)。出さないときは nil。
// フォルダが複数なら、ほかのフォルダのツールと同じく引数 folder で選ぶ(リポジトリでないフォルダを選んだら、そう返す)。
func GitIn(folders []Folder) []Tool {
	set := folderSet(folders)
	if !set.anyRepo() || gitAvailable() != nil {
		return nil
	}
	return []Tool{gitStatus{set}, gitDiff{set}}
}

// GitTools は、設定の画面に並べる用の Git のツール(定義はフォルダによらない。リポジトリかどうかも見ない)。
func GitTools() []Tool {
	set := folderSet(Single("."))
	return []Tool{gitStatus{set}, gitDiff{set}}
}

func (set folderSet) anyRepo() bool {
	for _, f := range set {
		if git.IsRepo(f.Path) {
			return true
		}
	}
	return false
}

// repo は、引数 folder・path で選んだフォルダとその中のパス(ファイルのツールと同じ決め方。resolve)。リポジトリでなければエラー。
func (set folderSet) repo(args Args) (target, error) {
	tg, err := set.resolve(args)
	if err != nil {
		return target{}, err
	}
	if !git.IsRepo(tg.dir()) {
		if set.multi() {
			return target{}, fmt.Errorf("フォルダ %s は Git のリポジトリではありません(Environment で git の印があるフォルダを folder に選んでください)", tg.folder.Name)
		}
		return target{}, errors.New("作業フォルダは Git のリポジトリではありません")
	}
	return tg, nil
}

type gitStatus struct {
	folders folderSet
}

func (t gitStatus) Def() Def {
	return t.folders.withFolder(Def{
		Name: "git_status",
		Description: "List files changed in the working directory since the last commit (HEAD): added, modified, deleted, " +
			"and untracked (not in Git yet). Ignored files are not listed.",
		Parameters: json.RawMessage(`{"type": "object", "properties": {}, "required": []}`),
	})
}

func (t gitStatus) Run(ctx context.Context, args Args) (string, error) {
	tg, err := t.folders.repo(args)
	if err != nil {
		return "", err
	}
	list, err := git.Status(ctx, tg.dir())
	if err != nil {
		return "", err
	}
	if len(list) == 0 {
		return "(変わったファイルはありません。HEAD のままです)", nil
	}
	// 「modified: tool/glob.go」の形。porcelain の XY は小さいモデルには読みにくいので、言葉にする
	var b strings.Builder
	for i, e := range list {
		line := e.Status + ": " + e.Path + "\n"
		if utf8.RuneCountInString(b.String())+utf8.RuneCountInString(line) > gitMaxRunes {
			fmt.Fprintf(&b, "(変わったファイルは全部で %d 件あり、%d 件までを並べました)\n", len(list), i)
			break
		}
		b.WriteString(line)
	}
	return strings.TrimSuffix(b.String(), "\n"), nil
}

type gitDiff struct {
	folders folderSet
}

func (t gitDiff) Def() Def {
	return t.folders.withFolder(Def{
		Name: "git_diff",
		Description: "Show the changes in the working directory since the last commit (HEAD), as a unified diff. " +
			"Untracked files (not in Git yet) are not included; use git_status to see them and read_file to read them.",
		Parameters: json.RawMessage(`{"type": "object", "properties": {` +
			`"path": {"type": "string", "description": "File or folder to show, relative to the working directory. Omit to show all changes."}}, "required": []}`),
	})
}

func (t gitDiff) Run(ctx context.Context, args Args) (string, error) {
	tg, err := t.folders.repo(args)
	if err != nil {
		return "", err
	}
	var paths []string
	if tg.path != "." {
		paths = append(paths, tg.path)
	}
	out, err := git.WorkDiff(ctx, tg.dir(), paths...)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(out) == "" {
		if len(paths) > 0 {
			return fmt.Sprintf("(%s は HEAD から変わっていません。Git に入っていないファイルは git_status で見てください)", paths[0]), nil
		}
		return "(HEAD から変わったところはありません。Git に入っていないファイルは git_status で見てください)", nil
	}
	return cutDiff(out, len(paths) > 0), nil
}

// cutDiff は、差分を gitMaxRunes 文字までにする。切ったら、どのファイルが入りきらなかったかと、path で絞ることを添える。
// 行の途中では切らない(差分は行ごとに意味があるため)。
func cutDiff(out string, narrowed bool) string {
	if utf8.RuneCountInString(out) <= gitMaxRunes {
		return strings.TrimSuffix(out, "\n")
	}
	var b strings.Builder
	n := 0
	var rest []string // 入りきらなかったファイル
	cut := false
	for line := range strings.Lines(out) {
		if cut {
			if name, ok := diffFile(line); ok {
				rest = append(rest, name)
			}
			continue
		}
		if l := utf8.RuneCountInString(line); n+l > gitMaxRunes {
			cut = true
			continue
		} else {
			n += l
		}
		b.WriteString(line)
	}
	s := strings.TrimSuffix(b.String(), "\n") + "\n(差分が長いので、ここで切りました"
	if len(rest) > 0 {
		s += "。このあとのファイル: " + strings.Join(rest, ", ")
	}
	if !narrowed {
		s += "。path でファイルを絞ってください"
	}
	return s + ")"
}

// diffFile は、「diff --git a/x b/x」の行から、ファイルのパスを取り出す。
func diffFile(line string) (string, bool) {
	rest, ok := strings.CutPrefix(strings.TrimRight(line, "\r\n"), "diff --git a/")
	if !ok {
		return "", false
	}
	if _, b, ok := strings.CutLast(rest, " b/"); ok {
		return b, true
	}
	return rest, true
}
