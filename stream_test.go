package main

import (
	"strings"
	"testing"
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

func split(think bool, pieces ...string) (thinking, content string) {
	s := newThinkSplitter(think)
	for _, p := range pieces {
		th, c := s.Push(p)
		thinking += th
		content += c
	}
	th, c := s.Flush()
	return thinking + th, content + c
}

func TestThinkSplitter(t *testing.T) {
	cases := []struct {
		name         string
		think        bool
		pieces       []string
		wantThinking string
		wantContent  string
	}{
		{"タグが1回で届く", true, []string{"考え中", "</think>", "\n\n", "答え"}, "考え中", "答え"},
		{"タグが分かれて届く", true, []string{"考え", "中</th", "ink>\n\n答", "え"}, "考え中", "答え"},
		{"タグに似た文字列", true, []string{"a</b", "x</think>答え"}, "a</bx", "答え"},
		{"思考なし", false, []string{"答", "え"}, "", "答え"},
		{"思考の途中で終了", true, []string{"考え", "中</thi"}, "考え中</thi", ""},
	}
	for _, c := range cases {
		th, ct := split(c.think, c.pieces...)
		if th != c.wantThinking || ct != c.wantContent {
			t.Errorf("%s: got (%q, %q), want (%q, %q)", c.name, th, ct, c.wantThinking, c.wantContent)
		}
	}
}

func TestBuildPrompt(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "S"},
		{Role: "user", Content: "U1"},
		{Role: "assistant", Content: "A1"},
		{Role: "user", Content: "U2"},
	}
	want := "<|im_start|>system\nS<|im_end|>\n" +
		"<|im_start|>user\nU1<|im_end|>\n" +
		"<|im_start|>assistant\nA1<|im_end|>\n" +
		"<|im_start|>user\nU2<|im_end|>\n" +
		"<|im_start|>assistant\n<think>\n"
	if got := buildPrompt(msgs, true); got != want {
		t.Errorf("think=true:\n%s", got)
	}
	if got := buildPrompt(msgs, false); !strings.HasSuffix(got, "<|im_start|>assistant\n<think>\n\n</think>\n\n") {
		t.Errorf("think=false:\n%s", got)
	}
}
