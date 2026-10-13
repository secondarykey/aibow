package tool

// read.go: 作業フォルダの中のファイルを読むツール(read_file)。
//
// 読んだ中身はそのままプロンプトに入る。長いファイルを丸ごと渡すと、それだけで会話の上限を使い切ってしまうので、
// 1回に読む量に上限を付け、続きは offset を指定して読ませる(モデルが自分でページをめくる)。
//
// 行番号を付けるのは、モデルが「何行目あたり」を言えるようにするため、
// 続きの読み方(offset)を決めやすくするため。行番号はファイルの中身ではないので、
// 書き換えのツール(3-2)では行番号を含めずに書かせる。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	readMaxLines = 200      // 1回に読む行数の上限(limit の既定と最大)
	readMaxRunes = 8000     // 1回に返す文字数の上限(行番号を含む)
	readMaxBytes = 10 << 20 // 読むファイルの大きさの上限。これより大きいファイルはテキストとして読ませない
)

type readFile struct {
	dir string // 作業フォルダ(絶対パス)
}

func (readFile) Def() Def {
	return Def{
		Name: "read_file",
		Description: "Read a text file in the working directory. Each line is prefixed with its line number and a tab. " +
			"Reads at most 200 lines at a time; use offset to read the rest.",
		Parameters: json.RawMessage(`{"type": "object", "properties": {` +
			`"path": {"type": "string", "description": "File path relative to the working directory."}, ` +
			`"offset": {"type": "integer", "description": "Line number to start reading from (1-based). Default 1."}, ` +
			`"limit": {"type": "integer", "description": "Number of lines to read. Default and maximum 200."}}, "required": ["path"]}`),
	}
}

func (t readFile) Run(ctx context.Context, args Args) (string, error) {
	p, err := localPath(t.dir, args.String("path"))
	if err != nil {
		return "", err
	}
	data, err := t.read(p)
	if err != nil {
		return "", err
	}
	return readLines(p, data, args.Int("offset", 1), args.Int("limit", readMaxLines))
}

// read はファイルを丸ごと読む。テキストとして読めないもの(フォルダ・大きすぎる・バイナリ・UTF-8 でない)はエラー。
func (t readFile) read(p string) ([]byte, error) {
	root, err := openRoot(t.dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()

	st, err := root.Stat(p)
	switch {
	case err != nil:
		return nil, openError(p, err)
	case st.IsDir():
		return nil, fmt.Errorf("%s はフォルダです。中を見るには list_files を使ってください", p)
	case st.Size() > readMaxBytes:
		return nil, fmt.Errorf("%s は大きすぎるため読めません(%d MB まで)", p, readMaxBytes>>20)
	}
	data, err := root.ReadFile(p)
	if err != nil {
		return nil, openError(p, err)
	}
	// NUL はテキストのファイルにはまず出てこないので、バイナリ(画像・実行ファイルなど)の目印にする
	if bytes.IndexByte(data, 0) >= 0 {
		return nil, fmt.Errorf("%s はバイナリのファイルのため読めません", p)
	}
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("%s は UTF-8 のテキストではないため読めません(Shift_JIS などは扱えません)", p)
	}
	return data, nil
}

// readLines は、offset 行目から limit 行(と readMaxRunes 文字)までを、行番号を付けて返す。
// 続きがあれば、続きの読み方を書き添える。
func readLines(p string, data []byte, offset, limit int) (string, error) {
	text := strings.TrimSuffix(string(data), "\n")
	if text == "" {
		return fmt.Sprintf("(%s は空のファイルです)", p), nil
	}
	lines := strings.Split(text, "\n")
	total := len(lines)
	if offset < 1 || offset > total {
		return "", fmt.Errorf("%s は %d 行です。offset は 1〜%d で指定してください", p, total, total)
	}
	if limit < 1 {
		return "", fmt.Errorf("limit は 1 以上で指定してください(最大 %d)", readMaxLines)
	}
	limit = min(limit, readMaxLines)

	var b strings.Builder
	runes := 0
	next := offset // 次に読む行
	cut := false   // 1行が長すぎて、途中で切ったか
	for next < offset+limit && next <= total {
		// 改行が CRLF のファイルも、行の中身としては CR を見せない
		line := fmt.Sprintf("%d\t%s\n", next, strings.TrimSuffix(lines[next-1], "\r"))
		n := utf8.RuneCountInString(line)
		if runes+n > readMaxRunes {
			if next == offset {
				// 最初の1行だけで上限を超える(圧縮された JS など)。何も返せないと進めないので、行を切って返す
				line = string([]rune(line)[:readMaxRunes]) + "\n"
				b.WriteString(line)
				cut, next = true, next+1
			}
			break
		}
		b.WriteString(line)
		runes += n
		next++
	}

	if cut {
		fmt.Fprintf(&b, "(%d 行目は長いため、途中で切りました)\n", next-1)
	}
	if next <= total {
		fmt.Fprintf(&b, "(全 %d 行のうち %d〜%d 行目です。続きは offset=%d で読めます)", total, offset, next-1, next)
	}
	return strings.TrimSuffix(b.String(), "\n"), nil
}
