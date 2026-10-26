package tool

// read.go: 作業フォルダの中のファイルを読むツール(read_file)。
//
// 読んだ中身はそのままプロンプトに入る。長いファイルを丸ごと渡すと、それだけで会話の上限を使い切ってしまうので、
// 1回に読む量に上限を付け、続きは offset を指定して読ませる(モデルが自分でページをめくる)。
//
// 行番号を付けるのは、モデルが「何行目あたり」を言えるようにするため、
// 続きの読み方(offset)を決めやすくするため。行番号はファイルの中身ではないので、
// 書き換えのツール(3-2)では行番号を含めずに書かせる。
//
// 長い行(1行が数千文字の Markdown の表など)は、readWrap 文字ごとに分けて、別の行として番号を付ける(0025 の決定事項 5)。
// 分けないと、会話の上限に1行も入らないときに、その行の続きを読む手段が無い(offset は行の単位なので)。
// 分けた行の番号は、エディタの行番号とずれる。同じファイルなら毎回同じに分かれるので、offset で続きを読むのには困らない。

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	readMaxLines = 200  // 1回に読む行数の上限(limit の既定と最大)
	readMaxRunes = 8000 // 1回に返す文字数の上限(行番号を含む)
	// readWrap は、長い行を分ける文字数。会話の上限 8,192 で残りが 2,000 トークンほどでも、1つの行が入る大きさ(0025 の決定事項 7)
	readWrap = 1000
)

type readFile struct {
	dir string // 作業フォルダ(絶対パス)
}

func (readFile) Def() Def {
	return Def{
		Name: "read_file",
		Description: "Read a text file in the working directory. Each line is prefixed with its line number and a tab. " +
			"Reads at most 200 lines at a time; use offset to read the rest. Lines longer than 1000 characters are split into several numbered lines.",
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
	root, err := openRoot(t.dir)
	if err != nil {
		return "", err
	}
	defer root.Close()
	text, exists, err := readText(root, p)
	switch {
	case err != nil:
		return "", err
	case !exists:
		return "", fmt.Errorf("%s は見つかりません。list_files で、ある場所を確かめてください", p)
	}
	return readLines(p, text, args.Int("offset", 1), args.Int("limit", readMaxLines))
}

// readLines は、offset 行目から limit 行(と readMaxRunes 文字)までを、行番号を付けて返す。
// 続きがあれば、続きの読み方を書き添える。
func readLines(p, text string, offset, limit int) (string, error) {
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return fmt.Sprintf("(%s は空のファイルです)", p), nil
	}
	lines := splitLong(strings.Split(text, "\n"), readWrap)
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
	for next < offset+limit && next <= total {
		line := fmt.Sprintf("%d\t%s\n", next, lines[next-1])
		n := utf8.RuneCountInString(line)
		// 行は readWrap 文字までに分けてあるので、最初の1行はいつも入る(何も返せずに進めない、にならない)
		if runes+n > readMaxRunes && next > offset {
			break
		}
		b.WriteString(line)
		runes += n
		next++
	}

	if next <= total {
		fmt.Fprintf(&b, "(全 %d 行のうち %d〜%d 行目です。続きは offset=%d で読めます)", total, offset, next-1, next)
	}
	return strings.TrimSuffix(b.String(), "\n"), nil
}

// splitLong は、wrap 文字より長い行を wrap 文字ごとに分ける。改行が CRLF のファイルも、行の中身としては CR を見せない。
func splitLong(lines []string, wrap int) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		r := []rune(strings.TrimSuffix(l, "\r"))
		for len(r) > wrap {
			out = append(out, string(r[:wrap]))
			r = r[wrap:]
		}
		out = append(out, string(r))
	}
	return out
}
