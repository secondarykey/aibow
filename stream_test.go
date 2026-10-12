package aibow

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// 日本語の1文字が2つのトークンに分かれて届いても、文字化けせずに出てくること
func TestUTF8BufferSplitRune(t *testing.T) {
	var u utf8Buffer
	b := []byte("あい") // 6バイト
	got := u.Push(b[:2]) + u.Push(b[2:4]) + u.Push(b[4:])
	if got != "あい" {
		t.Fatalf("got %q", got)
	}
	if u.Push(b[:1]) != "" {
		t.Fatal("書きかけのバイトを出してはいけない")
	}
	if u.Flush() == "" {
		t.Fatal("Flush で残りが出ること")
	}
}

// モデルが UTF-8 として正しくないバイトを出しても、正しい文字列だけを返すこと
// (保存した発言が次のプロンプトに入ったとき、llama.cpp のトークン化で落ちないように)
func TestUTF8BufferReplacesInvalidBytes(t *testing.T) {
	var u utf8Buffer
	got := u.Push([]byte{'a', 0x80, 'b'}) + // 先頭になれないバイト
		u.Push([]byte{0xF7, 0xBF, 0xBF, 0xBF}) + // U+10FFFF を超える4バイト
		u.Push([]byte("い")[:2]) + u.Flush() // 書きかけのまま終わった文字
	if !utf8.ValidString(got) || !strings.HasPrefix(got, "a\uFFFDb") || !strings.HasSuffix(got, "\uFFFD") {
		t.Fatalf("got %q", got)
	}
}
