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

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// Files は、作業フォルダ dir の中を扱うツール。dir は絶対パス。
func Files(dir string) []Tool {
	return []Tool{listFiles{dir}, readFile{dir}, writeFile{dir}, editFile{dir}}
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
