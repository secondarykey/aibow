package client

// log.go: MCP のやり取りとサーバーのエラー出力を、1行ずつログに出す部分(学習用)。

import (
	"bytes"
	"strings"
	"sync"
	"unicode/utf8"
)

// logMaxChars はログの1行の長さの上限。ツールの定義の一覧やファイルの中身は長いので、先頭だけを出す
const logMaxChars = 400

// lineLogger は、書かれたものを1行ずつに分けてログに出す io.Writer。
//
// SDK の LoggingTransport は「write: {JSON}」(aibow → サーバー)と「read: {JSON}」(サーバー → aibow)の形で書いてくるので、
// 向きが分かる矢印に置き換える。サーバーのエラー出力(stderr)は、そのまま出す。
type lineLogger struct {
	logf   func(format string, args ...any)
	prefix string

	mu   sync.Mutex
	buf  []byte
	tail string // 最後に出した行(起動に失敗したときに、理由として画面のお知らせに添える)
}

// last は最後の行を返す。改行で終わっていない書きかけの行があれば、そちらを返す。
func (l *lineLogger) last() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if s := strings.TrimSpace(string(l.buf)); s != "" {
		return shorten(s)
	}
	return l.tail
}

func (l *lineLogger) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf = append(l.buf, p...)
	for {
		i := bytes.IndexByte(l.buf, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimRight(string(l.buf[:i]), "\r")
		l.buf = l.buf[i+1:]
		if line != "" {
			l.logf("%s%s", l.prefix, shorten(arrow(line)))
			l.tail = shorten(strings.TrimSpace(line))
		}
	}
	return len(p), nil
}

// arrow は LoggingTransport の書き方を、向きの矢印にする。
func arrow(line string) string {
	if s, ok := strings.CutPrefix(line, "write: "); ok {
		return "→ " + s
	}
	if s, ok := strings.CutPrefix(line, "read: "); ok {
		return "← " + s
	}
	return line
}

func shorten(s string) string {
	if utf8.RuneCountInString(s) <= logMaxChars {
		return s
	}
	return string([]rune(s)[:logMaxChars]) + "…"
}
