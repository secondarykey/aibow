package tool

// search.go: 作業フォルダの中のファイルから、正規表現に合う行を探すツール(search_files。grep に当たる。0037)。
//
// コードを探す手段が read_file だけだと、ファイルを丸ごと読んで会話の上限を使ってしまう。
// run_command で探そうにも、Windows の cmd.exe には grep が無く(findstr はある)、毎回の実行に確認が挟まる。
// 一致した行だけを「パス:行番号: 行」で返せば、どこを read_file で読めばよいかが少ない文字数で分かる
// (Claude Code に Grep のツールがあるのと同じ理由)。
//
// 正規表現は Go の regexp(RE2 の書き方)。後方参照などは無いが、どんな入力でも行の長さに比例した時間で終わる。
//
// たどり方は list_files と同じ(walk。浅い順・名前の順。.git と node_modules は飛ばし、隠しフォルダには入らない)。
// 読めないファイル(バイナリ・UTF-8 でない・大きすぎる)は黙って飛ばし、数だけを最後に書く(決定事項 6)。
// 一つずつエラーを並べると、画像の多いフォルダでは結果がそれで埋まるため。
//
// 結果は上限(件数と文字数)で切るが、数えるのは最後まで続ける。全部で何件あるかが分かれば、
// モデルは「path か glob で絞る」か「この中から選ぶ」かを決められる。
// 読むだけで、作業フォルダ(os.Root)の外には出られないので、実行前の確認は要らない(list_files・read_file と同じ)。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	searchMax      = 100     // 返す一致の件数の上限
	searchMaxRunes = 8000    // 返す文字数の上限(read_file・list_files と同じ)
	searchLineMax  = 300     // 1つの一致の行を切る文字数(一覧として読むものなので、read_file の 1,000 より短く)
	searchMaxBytes = 1 << 20 // 探すファイルの大きさの上限。これより大きいものは飛ばす(生成されたファイル・データであることが多い)
)

type searchFiles struct {
	folders folderSet // 作業フォルダ(ワークスペースのフォルダの組)
}

func (t searchFiles) Def() Def {
	return t.folders.withFolder(Def{
		Name: "search_files",
		Description: "Search text files in the working directory for lines matching a regular expression (Go RE2 syntax). " +
			"Returns matching lines as path:line: text, at most 100. Use read_file with offset to see the lines around a match. " +
			"Skips .git, node_modules, hidden folders (unless given as path) and binary files. " +
			"It searches inside files; to find files by name, use list_files with pattern.",
		Parameters: json.RawMessage(`{"type": "object", "properties": {` +
			`"pattern": {"type": "string", "description": "Regular expression to search for, such as func main or TODO\\(.*\\)."}, ` +
			`"path": {"type": "string", "description": "Folder or file to search, relative to the working directory. Omit to search the whole working directory."}, ` +
			`"glob": {"type": "string", "description": "Only search files matching this glob, such as *.go or **/*_test.go. Use / between folders, even on Windows."}, ` +
			`"ignore_case": {"type": "boolean", "description": "Ignore upper and lower case. Default false."}}, "required": ["pattern"]}`),
	})
}

func (t searchFiles) Run(ctx context.Context, args Args) (string, error) {
	re, err := searchRegexp(args.String("pattern"), args.Bool("ignore_case"))
	if err != nil {
		return "", err
	}
	var g *globPattern
	if s := args.String("glob"); s != "" {
		pg, err := compileGlob(s)
		if err != nil {
			return "", err
		}
		g = &pg
	}
	tg, err := t.folders.resolve(args)
	if err != nil {
		return "", err
	}
	p := tg.path
	root, err := openRoot(tg.dir())
	if err != nil {
		return "", err
	}
	defer root.Close()

	st, err := root.Stat(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("%w%s", openError(p, err), t.folders.elsewhere(tg, false))
		}
		return "", openError(p, err)
	}

	s := searcher{re: re}
	if !st.IsDir() {
		// ファイルを名指ししたときは glob を見ない(書いた本人が選んだファイルなので)
		s.file(root, p)
		return s.result(), nil
	}
	err = walk(ctx, root.FS(), p, func(name string, d fs.DirEntry) bool {
		if d.IsDir() {
			// list_files と同じ:.git と node_modules は飛ばし、隠しフォルダには入らない(path に指定したフォルダの中は見る)
			return !listSkip[d.Name()] && !strings.HasPrefix(d.Name(), ".")
		}
		if g == nil || g.match(name) {
			s.file(root, name)
		}
		return false
	}, func() bool { return ctx.Err() != nil })
	if err == nil {
		err = ctx.Err() // 途中で止まった(walk は stop で抜けるとエラーを返さない)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		// 数え終わる前の結果は「全部で N 件」が書けないので返さず、範囲を絞らせる
		return "", errors.New("時間内に探しきれませんでした。path か glob で、探す範囲を絞ってください")
	}
	if err != nil {
		return "", err
	}
	return s.result(), nil
}

// ShellNoteFor は、環境の説明に書く run_command のシェル。search_files も使えるなら(search が true)、
// ファイルの中を探すのは run_command(Windows の findstr)ではなく search_files だと書く(0037 の決定事項 8)。
func ShellNoteFor(search bool) string {
	if search {
		return shellNoteSearch
	}
	return ShellNote
}

// searchRegexp は、モデルが書いた正規表現を読み取る。読めなければ、直し方の分かるエラー。
func searchRegexp(pattern string, ignoreCase bool) (*regexp.Regexp, error) {
	if pattern == "" {
		return nil, errors.New("pattern に探す文字(正規表現)を書いてください")
	}
	expr := pattern
	if ignoreCase {
		expr = "(?i)" + expr
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		// よくあるのは ( や [ を文字として探そうとして、閉じていないもの
		return nil, fmt.Errorf("pattern %q を正規表現として読めません(Go の RE2 の書き方です。( [ . * などの記号そのものを探すときは \\( のように \\ を前に付けます): %v", pattern, err)
	}
	return re, nil
}

// searcher は search_files の結果を、上限を数えながら組み立てる。上限を超えても数えるのは続ける。
type searcher struct {
	re *regexp.Regexp

	b       strings.Builder
	shown   int  // 書いた一致の数
	runes   int  // 書いた文字数
	full    bool // 上限に達して、それより後を書いていない
	total   int  // 一致の数(全部)
	files   int  // 一致のあったファイルの数
	skipped int  // 読めないので飛ばしたファイルの数
}

// file は、ファイル name(作業フォルダからの相対パス)の中を探す。
func (s *searcher) file(root *os.Root, name string) {
	text, ok := searchText(root, name)
	if !ok {
		s.skipped++
		return
	}
	found := false
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if !s.re.MatchString(line) {
			continue
		}
		found = true
		s.total++
		s.add(fmt.Sprintf("%s:%d: %s", name, i+1, cutLine(line, searchLineMax)))
	}
	if found {
		s.files++
	}
}

// add は一致の1行を、上限に収まるなら書く。
func (s *searcher) add(line string) {
	if s.full {
		return
	}
	n := utf8.RuneCountInString(line) + 1 // 改行の分
	if s.shown == searchMax || s.runes+n > searchMaxRunes {
		s.full = true
		return
	}
	s.b.WriteString(line + "\n")
	s.shown, s.runes = s.shown+1, s.runes+n
}

// result は書いた一致に、切ったこと・飛ばしたファイルの説明を添えて返す。
func (s *searcher) result() string {
	if s.total == 0 {
		s.b.WriteString("(見つかりませんでした)\n")
	}
	if s.full {
		fmt.Fprintf(&s.b, "(一致は全部で %d 件(%d ファイル)あり、%d 件までを並べました。path か glob で絞ってください)\n", s.total, s.files, s.shown)
	}
	if s.skipped > 0 {
		fmt.Fprintf(&s.b, "(バイナリ・UTF-8 でない・%d MB を超えるファイル %d 件は、探さずに飛ばしました)\n", searchMaxBytes>>20, s.skipped)
	}
	return strings.TrimSuffix(s.b.String(), "\n")
}

// searchText は、探すファイルを UTF-8 のテキストとして読む。探さないもの(大きすぎる・バイナリ・UTF-8 でない・開けない)は ok=false。
// readText と同じ見分け方だが、上限は小さく、理由はモデルに返さない(飛ばした数だけを書く)。
func searchText(root *os.Root, name string) (string, bool) {
	st, err := root.Stat(name)
	if err != nil || !st.Mode().IsRegular() || st.Size() > searchMaxBytes {
		return "", false
	}
	data, err := root.ReadFile(name)
	if err != nil || bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		return "", false
	}
	return string(data), true
}

// cutLine は、max 文字より長い行を切り、切ったことが分かるように … を付ける。
func cutLine(line string, max int) string {
	if utf8.RuneCountInString(line) <= max {
		return line
	}
	return string([]rune(line)[:max]) + "…"
}
