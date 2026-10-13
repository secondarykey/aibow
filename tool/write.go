package tool

// write.go: ファイルを丸ごと書くツール(write_file)と、文字列を置き換えるツール(edit_file)。どちらも実行前に確認する。
//
// ファイルの直し方は2つある:
//
//	write_file : 中身を全部書く。新しいファイルを作るときはこれしかない。小さな修正でもファイル全体を生成するので、
//	             長いファイルほどトークン数と時間がかかり、触っていない行を書き間違える(消してしまう)おそれもある
//	edit_file  : 「古い文字列 → 新しい文字列」だけを書く。生成する量が少なく、触らない行は変わらない。
//	             その代わり、古い文字列をファイルの中身と1文字違わず書けないと失敗する(小さいモデルは行番号や字下げを間違えやすい)
//
// 古い文字列がファイルの中に1か所だけのときだけ置き換える。2か所以上あると、どれを直したいのか分からないため。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strings"
)

type writeFile struct {
	dir string // 作業フォルダ(絶対パス)
}

func (writeFile) Def() Def {
	return Def{
		Name: "write_file",
		Description: "Create a new file, or overwrite an existing file, in the working directory with the given content. " +
			"Missing parent folders are created. The user is asked to approve before it runs. Prefer edit_file for small changes to an existing file.",
		Parameters: json.RawMessage(`{"type": "object", "properties": {` +
			`"path": {"type": "string", "description": "File path relative to the working directory."}, ` +
			`"content": {"type": "string", "description": "The full content of the file. Do not include line numbers."}}, "required": ["path", "content"]}`),
	}
}

func (t writeFile) Preview(args Args) (Preview, error) {
	p, before, exists, err := t.current(args)
	if err != nil {
		return Preview{}, err
	}
	note := "新しく作る"
	if exists {
		note = "上書き"
	}
	return Preview{Path: p, Note: note, Diff: unifiedDiff(p, before, args.String("content"))}, nil
}

func (t writeFile) Run(ctx context.Context, args Args) (string, error) {
	p, _, exists, err := t.current(args)
	if err != nil {
		return "", err
	}
	content := args.String("content")
	if err := writeText(t.dir, p, content); err != nil {
		return "", err
	}
	done := "新しく作りました"
	if exists {
		done = "上書きしました"
	}
	return fmt.Sprintf("%s を%s(%d 行)", p, done, lineCount(content)), nil
}

// current は書く先のパスと、今の中身(無ければ exists=false)を返す。
func (t writeFile) current(args Args) (p, text string, exists bool, err error) {
	p, err = filePath(t.dir, args.String("path"))
	if err != nil {
		return "", "", false, err
	}
	root, err := openRoot(t.dir)
	if err != nil {
		return "", "", false, err
	}
	defer root.Close()
	text, exists, err = readText(root, p)
	return p, text, exists, err
}

type editFile struct {
	dir string // 作業フォルダ(絶対パス)
}

func (editFile) Def() Def {
	return Def{
		Name: "edit_file",
		Description: "Replace text in an existing file in the working directory. old_string must appear exactly once in the file; " +
			"include enough surrounding lines to make it unique. Copy old_string exactly from the file, without the line numbers that read_file adds. " +
			"The user is asked to approve before it runs.",
		Parameters: json.RawMessage(`{"type": "object", "properties": {` +
			`"path": {"type": "string", "description": "File path relative to the working directory."}, ` +
			`"old_string": {"type": "string", "description": "The exact text to replace."}, ` +
			`"new_string": {"type": "string", "description": "The text to replace it with."}}, "required": ["path", "old_string", "new_string"]}`),
	}
}

func (t editFile) Preview(args Args) (Preview, error) {
	e, err := t.plan(args)
	if err != nil {
		return Preview{}, err
	}
	return Preview{Path: e.path, Note: "書き換え", Diff: unifiedDiff(e.path, e.before, e.after)}, nil
}

func (t editFile) Run(ctx context.Context, args Args) (string, error) {
	e, err := t.plan(args) // 確認を待つ間にファイルが変わっているかもしれないので、もう一度確かめる
	if err != nil {
		return "", err
	}
	if err := writeText(t.dir, e.path, e.after); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s の %d 行目からを書き換えました", e.path, e.line), nil
}

// edit は edit_file で書き換える内容。
type edit struct {
	path          string
	before, after string // 書き換える前と後の、ファイルの中身全体
	line          int    // 書き換える場所の行番号(1 始まり)
}

// plan は、引数とファイルの今の中身から、書き換えた後の中身を作る。置き換える場所が1か所に決まらなければエラー。
func (t editFile) plan(args Args) (edit, error) {
	p, err := filePath(t.dir, args.String("path"))
	if err != nil {
		return edit{}, err
	}
	root, err := openRoot(t.dir)
	if err != nil {
		return edit{}, err
	}
	defer root.Close()
	before, exists, err := readText(root, p)
	switch {
	case err != nil:
		return edit{}, err
	case !exists:
		return edit{}, fmt.Errorf("%s は見つかりません。新しく作るときは write_file を使ってください", p)
	}

	old, repl := args.String("old_string"), args.String("new_string")
	switch {
	case old == "":
		return edit{}, errors.New("old_string が空です。置き換える文字列を、ファイルの中身のとおりに書いてください")
	case old == repl:
		return edit{}, errors.New("old_string と new_string が同じです")
	}
	// 改行が CRLF のファイル(Windows で作ったものなど)。read_file は CR を見せないので、モデルが書く文字列の改行は LF になる。
	// ファイルの書き方に合わせてから探す(合わせないと、複数行の old_string がいつまでも見つからない)
	if strings.Contains(before, "\r\n") && !strings.Contains(old, "\r\n") {
		old = strings.ReplaceAll(old, "\n", "\r\n")
		repl = strings.ReplaceAll(repl, "\n", "\r\n")
	}
	switch n := strings.Count(before, old); n {
	case 0:
		return edit{}, fmt.Errorf("old_string が %s の中に見つかりません(read_file で今の中身を確かめてください。行番号と、その後ろのタブは含めません)", p)
	case 1:
	default:
		return edit{}, fmt.Errorf("old_string が %s の中に %d か所あります。前後の行を含めて、1か所に決まるようにしてください", p, n)
	}
	i := strings.Index(before, old)
	return edit{
		path:   p,
		before: before,
		after:  before[:i] + repl + before[i+len(old):],
		line:   strings.Count(before[:i], "\n") + 1,
	}, nil
}

// filePath は、書き換えるツールの path を確かめる(作業フォルダの中で、作業フォルダそのものではないこと)。
func filePath(dir, p string) (string, error) {
	p, err := localPath(dir, p)
	if err != nil {
		return "", err
	}
	if p == "." {
		return "", errors.New("path にファイルのパスを指定してください")
	}
	return p, nil
}

// writeText はファイルを書く。親のフォルダが無ければ作る。すでにあるファイルは、ファイルの属性(パーミッション)を引き継ぐ。
func writeText(dir, p, text string) error {
	root, err := openRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	if parent := path.Dir(p); parent != "." {
		if err := root.MkdirAll(parent, 0o755); err != nil {
			return fmt.Errorf("フォルダ %s を作れません: %w", parent, err)
		}
	}
	perm := os.FileMode(0o644)
	if st, err := root.Stat(p); err == nil {
		perm = st.Mode().Perm()
	} else if !errors.Is(err, fs.ErrNotExist) {
		return openError(p, err)
	}
	if err := root.WriteFile(p, []byte(text), perm); err != nil {
		return fmt.Errorf("%s を書けません: %w", p, err)
	}
	return nil
}

// lineCount は文字列の行数(最後の改行の後ろは数えない)。
func lineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(strings.TrimSuffix(s, "\n"), "\n") + 1
}
