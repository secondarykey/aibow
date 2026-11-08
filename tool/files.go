package tool

// files.go: 作業フォルダの中のファイルを扱うツールの共通部分。
//
// ファイルのツールは、作業フォルダの外に出られないことを「仕組みで」守る。
// Go の os.Root は、決めたフォルダの下しか開けないファイル操作の口で、
// ../ や、フォルダの外を指すシンボリックリンクをたどろうとするとエラーになる。
// 文字列でパスを調べるだけだと、シンボリックリンクや Windows の書き方(\\?\ など)の抜け道を塞ぎきれないので、
// 開く処理そのものに任せる。
//
// (コマンドのツールはこうはいかない。コマンドは何でもできるので、仕組みでは縛れず、実行前の確認が唯一の守りになる)
//
// 作業フォルダは会話ごとの設定なので、ツールは作業フォルダを持った値として、会話ごとに作る(Files)。
// Tool の interface は作業フォルダを知らなくてよい。
//
// ワークスペース(0035)では、フォルダが複数になる(FilesIn)。モデルにはツールの引数 folder でフォルダを選ばせ、省けばメイン。
// 名前は JSON Schema の enum で並べる(パスの先頭に名前を付ける形は、メインの中のサブフォルダと区別できないので採らない)。
// フォルダが1つのときは folder の引数を出さない(定義は Files と同じ。4B のプロンプトを増やさない)。
// 読むだけのフォルダには、書き換えるツール(write_file・edit_file)を向けられない(確認のカードを出す前に断る)。

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// Folder は、ファイルのツールが扱うフォルダ1つ(ワークスペースのフォルダ。0035)。
type Folder struct {
	Name     string // モデルが引数 folder で呼ぶ名前(フォルダが複数のときだけ使う)
	Path     string // 絶対パス
	Main     bool   // メイン(folder を省いたときのフォルダ)
	Writable bool   // 書けるか(false なら読むだけ。write_file・edit_file を受け付けない)
	// Description は、フォルダの中身の短い説明(ユーザーが書く。環境の説明でフォルダの行の末尾に付ける。ツールの動きには使わない)
	Description string
}

// Files は、作業フォルダ dir の中を扱うツール。dir は絶対パス。
func Files(dir string) []Tool {
	return FilesIn(Single(dir))
}

// Single は、作業フォルダ dir 1つ(メイン・書く)のフォルダの組。dir が空なら空。
func Single(dir string) []Folder {
	if dir == "" {
		return nil
	}
	return []Folder{{Name: filepath.Base(dir), Path: dir, Main: true, Writable: true}}
}

// FolderMarks は、フォルダの印(main・writable / read-only・git)を、環境の説明に書く形にする(英語。モデルが読む)。
// git は呼ぶ側が調べて渡す(tool はリポジトリかを見ない)。
func FolderMarks(f Folder, git bool) string {
	var marks []string
	if f.Main {
		marks = append(marks, "main")
	}
	if f.Writable {
		marks = append(marks, "writable")
	} else {
		marks = append(marks, "read-only")
	}
	if git {
		marks = append(marks, "git")
	}
	return strings.Join(marks, ", ")
}

// FolderLine は、環境の説明に並べるフォルダ1行(「名前 (印): パス — 説明」。説明が無ければパスまで)。
// 説明はユーザーの言葉のまま(日本語でも)付ける。ユーザーが名前と違う言葉でフォルダを呼んだとき、モデルが当てる手がかりにする(2026-11-03)
func FolderLine(f Folder, git bool) string {
	line := fmt.Sprintf("%s (%s): %s", f.Name, FolderMarks(f, git), f.Path)
	if d := strings.Join(strings.Fields(f.Description), " "); d != "" {
		line += " — " + d
	}
	return line
}

// FilesIn は、フォルダの組 folders の中を扱うツール。メインはちょうど1つにしておく(無ければ先頭をメインにする)。
// フォルダが1つなら、定義は Files と同じ(folder の引数を出さない)。
// search_files(0037)は読むだけなので、資料のフォルダ(0024)・aibow-mcp でも、ほかのフォルダのツールと一緒に出る。
func FilesIn(folders []Folder) []Tool {
	set := folderSet(folders)
	return []Tool{listFiles{set}, readFile{set}, searchFiles{set}, writeFile{set}, editFile{set}}
}

// MainFolder は、フォルダの組のメイン(メインの印が無ければ先頭。空なら空)。
func MainFolder(folders []Folder) Folder {
	for _, f := range folders {
		if f.Main {
			return f
		}
	}
	if len(folders) > 0 {
		return folders[0]
	}
	return Folder{}
}

// folderSet は、ファイルのツールが扱うフォルダの組。
type folderSet []Folder

// multi は、フォルダが複数か(folder の引数を出すか)。
func (set folderSet) multi() bool { return len(set) > 1 }

// target は、モデルが選んだフォルダと、その中の相対パス(/ 区切り)。
type target struct {
	folder Folder
	path   string
}

// dir はフォルダの絶対パス。
func (t target) dir() string { return t.folder.Path }

// resolve は、引数 folder と path から、扱うフォルダとその中の相対パスを決める。
//
//   - folder を省いたら、メイン。ただし path がほかのフォルダの中を指す絶対パスなら、そのフォルダ
//     (4B は絶対パスで書きがちで、今も作業フォルダの中の絶対パスを受け付けているため)
//   - folder を書いたら、その名前のフォルダ(大文字と小文字は区別しない)。絶対パスがそのフォルダの外を指せばエラー(食い違い)
func (set folderSet) resolve(args Args) (target, error) {
	p := strings.TrimSpace(args.String("path"))
	name := strings.TrimSpace(args.String("folder"))
	f, err := set.pick(name, p)
	if err != nil {
		return target{}, err
	}
	rel, err := localPath(f.Path, p)
	if err != nil {
		if name != "" && set.multi() && filepath.IsAbs(p) {
			return target{}, fmt.Errorf("%s はフォルダ %s の外です(folder と path が食い違っています。folder を省くと、絶対パスからフォルダを選びます)", p, f.Name)
		}
		return target{}, err
	}
	tg := target{folder: f, path: rel}
	if err := set.prefixed(tg); err != nil {
		return target{}, err
	}
	return tg, nil
}

// prefixed は、パスの先頭にほかのフォルダの名前を付けた書き方(spec/greeting.md)を、直し方の分かるエラーにする。
//
// Qwen3.5 4B で試したら、folder を使わずに、フォルダの名前をパスの先頭に付けて書くことがあった(0035 の完了の条件 7。_docs/tuning.md)。
// その形はメインの中のサブフォルダと見分けられないので受け付けない(決定事項 3)。メインの中にその名前のものが無いときだけ、
// folder と path に分けて書くよう返す(あるなら、メインの中のサブフォルダとしてそのまま扱う)。
// write_file で、メインの中に同じ名前のフォルダを新しく作ってしまうのも、これで防ぐ。
//
// 選んだフォルダ自身の名前を先頭に付けた書き方(aibow/agent.go)も同じように返す。フォルダが1つでも、4B はこう書いた
// (環境の説明の「"aibow" refers to the working directory itself.」は効かなかった。_docs/tuning.md の「コーディング向けのツールと fetch_url を 4B で試す」)。
func (set folderSet) prefixed(tg target) error {
	first, rest, ok := strings.Cut(tg.path, "/")
	if !ok || rest == "" {
		return nil
	}
	if strings.EqualFold(tg.folder.Name, first) {
		if _, err := os.Stat(filepath.Join(tg.folder.Path, filepath.FromSlash(first))); err == nil {
			return nil // 同じ名前のサブフォルダが本当にある
		}
		return fmt.Errorf("%s は、このフォルダそのものの名前です。パスの先頭に付けず、path に %s と書いてください", first, rest)
	}
	if !set.multi() {
		return nil
	}
	for _, f := range set {
		if f.Path == tg.folder.Path || !strings.EqualFold(f.Name, first) {
			continue
		}
		if _, err := os.Stat(filepath.Join(tg.folder.Path, filepath.FromSlash(first))); err == nil {
			return nil // メインの中に本当にある(サブフォルダ)
		}
		return fmt.Errorf("%s はフォルダの名前です。パスの先頭に付けず、folder に %s、path に %s と分けて書いてください", first, f.Name, rest)
	}
	return nil
}

// elsewhere は、フォルダの中に見つからなかったパスが、ほかのフォルダにあるときの添え書き(無ければ空)。
// 4B は list_files(folder=spec) の結果を見たあと、次の read_file で folder を付け忘れることがあった(_docs/tuning.md)。
// 書き換えるツール(write が true)では、読むだけのフォルダを案内しない(書けないフォルダへ誘うことになるため)。
func (set folderSet) elsewhere(tg target, write bool) string {
	if !set.multi() {
		return ""
	}
	for _, f := range set {
		if f.Path == tg.folder.Path || write && !f.Writable {
			continue
		}
		if _, err := os.Stat(filepath.Join(f.Path, filepath.FromSlash(tg.path))); err == nil {
			return fmt.Sprintf("(%s はフォルダ %s の中にあります。folder に %s を書いてください)", tg.path, f.Name, f.Name)
		}
	}
	return ""
}

// pick は、引数 folder の名前と path から、扱うフォルダを選ぶ。
func (set folderSet) pick(name, p string) (Folder, error) {
	if name != "" && set.multi() {
		for _, f := range set {
			if strings.EqualFold(f.Name, name) {
				return f, nil
			}
		}
		return Folder{}, fmt.Errorf("フォルダ %s はありません。folder には %s のどれかを書くか、省いてメインのフォルダを使ってください", name, strings.Join(set.names(), "・"))
	}
	main := MainFolder(set)
	if !set.multi() {
		return main, nil
	}
	if f, ok := set.containing(p); ok {
		return f, nil
	}
	return main, nil // 相対パスか、どこにも入らない。メインの外の絶対パスはエラーにする(localPath)
}

// containing は、絶対パス p を含むフォルダ(入れ子なら深いほう)。p が相対パスか、どこにも入らなければ false。
func (set folderSet) containing(p string) (Folder, bool) {
	if !filepath.IsAbs(p) {
		return Folder{}, false
	}
	best := Folder{}
	for _, f := range set {
		if rel, err := filepath.Rel(f.Path, p); err == nil && filepath.IsLocal(rel) {
			if len(f.Path) > len(best.Path) {
				best = f
			}
		}
	}
	return best, best.Path != ""
}

func (set folderSet) names() []string {
	var names []string
	for _, f := range set {
		names = append(names, f.Name)
	}
	return names
}

// writable は、書き換えるツールでフォルダを書けるかを確かめる(読むだけのフォルダならエラー。確認のカードを出す前に呼ぶ)。
//
// 文では、ほかのフォルダに写しを作らず、書けないことをユーザーに伝えて止まるよう促す。
// Qwen3.5 4B で試したら、読むだけと知ると、書けるフォルダ(メイン)に同じ内容のファイルを新しく作ることがあった(4回のうち2回。_docs/tuning.md)。
// 読むだけにしたのはユーザーなので、どうするかはユーザーに聞く(2026-11-02、ユーザーと決めた)。
func (t target) writable() error {
	if !t.folder.Writable {
		return fmt.Errorf("%s は読むだけのフォルダなので、書き換えられません。ほかのフォルダに写しを作らず、書き換えられないことをユーザーに伝えて、ここで止まってください", t.folder.Name)
	}
	return nil
}

// cardFolder は、確認のカードに出すフォルダの名前(フォルダが複数なら、メインでも出す。1つなら空)。
//
// 以前はメイン以外のときだけ出していた。でもそれだと、モデルが「前の呼び出しで cd /d B したから B にいる」と思い込んで
// folder を付けずに git commit を書いたとき、カードにフォルダが出ず、メイン(A)で動くことに気づきにくい
// (run_command は呼ぶたびに新しいシェルで始まり、cd は持ち越されない)。ファイルのツールも同じ理由でそろえる(2026-11-02、ユーザーと決めた)。
func (set folderSet) cardFolder(t target) string {
	if !set.multi() {
		return ""
	}
	return t.folder.Name
}

// withFolder は、フォルダが複数のとき、ツールの定義に引数 folder(名前の enum)を足す。1つなら def のまま。
// path の説明の「作業フォルダ」は、フォルダが複数でも「選んだフォルダ」と読めるように変えない(定義を短く保つ)。
//
// JSON として読み直さず文字列で差し込むのは、定義の書き方(キーの順番・「, 」「: 」の区切り)をそのまま残すため。
// 定義の文字列はそのままモデルに見せる形にしてある(format の qwen35_test)。encoding/json で書き戻すとキーが並べ替わり、区切りも詰まる。
// 引数の無いツール(git_status)は properties が空の {} なので、後ろに区切りのカンマを付けると壊れた JSON になる。
// 以前はそれで MCP の SDK の AddTool が panic した(フォルダが複数のワークスペースで会話を始められなかった。2026-11-08)。
func (set folderSet) withFolder(def Def) Def {
	if !set.multi() {
		return def
	}
	names, _ := json.Marshal(set.names())
	prop := `"folder": {"type": "string", "enum": ` + string(names) + `, "description": "Folder to use (see Environment). Omit for the main folder."}`
	s := string(def.Parameters)
	if strings.Contains(s, `"properties": {}`) {
		s = strings.Replace(s, `"properties": {}`, `"properties": {`+prop+`}`, 1)
	} else {
		s = strings.Replace(s, `"properties": {`, `"properties": {`+prop+`, `, 1)
	}
	def.Parameters = json.RawMessage(s)
	return def
}

// textMaxBytes は、テキストとして扱うファイルの大きさの上限。これより大きいファイルは読ませも書き換えさせもしない
const textMaxBytes = 10 << 20

// readText は、作業フォルダの中のファイルを UTF-8 のテキストとして丸ごと読む。無ければ exists=false(エラーではない。新しく作るときのため)。
// テキストとして扱えないもの(フォルダ・大きすぎる・バイナリ・UTF-8 でない)はエラー。
func readText(root *os.Root, p string) (text string, exists bool, err error) {
	st, err := root.Stat(p)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", false, nil
	case err != nil:
		return "", false, openError(p, err)
	case st.IsDir():
		return "", true, fmt.Errorf("%s はフォルダです。中を見るには list_files を使ってください", p)
	case st.Size() > textMaxBytes:
		return "", true, fmt.Errorf("%s は大きすぎるため扱えません(%d MB まで)", p, textMaxBytes>>20)
	}
	data, err := root.ReadFile(p)
	if err != nil {
		return "", true, openError(p, err)
	}
	// NUL はテキストのファイルにはまず出てこないので、バイナリ(画像・実行ファイルなど)の目印にする
	if bytes.IndexByte(data, 0) >= 0 {
		return "", true, fmt.Errorf("%s はバイナリのファイルのため扱えません", p)
	}
	if !utf8.Valid(data) {
		return "", true, fmt.Errorf("%s は UTF-8 のテキストではないため扱えません(Shift_JIS などは扱えません)", p)
	}
	return string(data), true, nil
}

// openRoot は作業フォルダを os.Root として開く。開くのは呼び出しごと(会話の途中でフォルダが消えても気づけるように)。
func openRoot(dir string) (*os.Root, error) {
	r, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("作業フォルダ %s を開けません(消えたか、名前が変わった可能性があります): %w", dir, err)
	}
	return r, nil
}

// localPath は、モデルが書いたパスを作業フォルダからの相対パス(/ 区切り)にする。空なら "."(作業フォルダそのもの)。
//
// 4B のモデルは絶対パスで書きがちなので、作業フォルダの中を指す絶対パスも受け付ける。
// 外を指すパス(../ や、外の絶対パス)は、開く前にここで分かりやすいエラーにする。
// ここをすり抜けても(シンボリックリンクなど)、os.Root が開くときに止める。
func localPath(dir, p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" || p == "." {
		return ".", nil
	}
	if filepath.IsAbs(p) {
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return "", outside(p)
		}
		p = rel
	}
	if !filepath.IsLocal(p) {
		return "", outside(p)
	}
	return filepath.ToSlash(filepath.Clean(p)), nil
}

func outside(p string) error {
	return fmt.Errorf("%s は作業フォルダの外です。作業フォルダからの相対パスで指定してください", p)
}

// openError は、os.Root で開けなかったときの理由を、モデルが次にどうすればよいか分かる文にする。
func openError(p string, err error) error {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("%s は見つかりません。list_files で、ある場所を確かめてください", p)
	case strings.Contains(err.Error(), "escapes"):
		// os.Root の「外に出ようとした」エラー(作業フォルダの外を指すシンボリックリンクなど)。
		// このエラーの値は外に公開されていないので、文で見分ける
		return fmt.Errorf("%s は作業フォルダの外を指しているため、開けません", p)
	}
	return fmt.Errorf("%s を開けません: %w", p, err)
}
