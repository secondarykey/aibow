package main

import (
	"strings"
	"unicode/utf8"
)

// ---- UTF-8 の切れ目をまたぐトークン対策 -------------------------------------------
//
// トークンは「文字」ではなく「バイト列のかたまり」。日本語の1文字(UTF-8 で3バイト)が
// 2つのトークンに分かれることがよくある。そのまま画面に送ると文字化けするので、
// 文字として完成した部分だけを取り出し、途中のバイトは次のトークンまで持ち越す。
type utf8Buffer struct {
	pending []byte
}

// Push はトークン1個分のバイト列を受け取り、文字として完成した部分を返す。
func (u *utf8Buffer) Push(piece []byte) string {
	u.pending = append(u.pending, piece...)

	// 末尾から見て、書きかけの文字があればその手前までを返す
	cut := len(u.pending)
	for i := 1; i <= utf8.UTFMax && i <= len(u.pending); i++ {
		start := len(u.pending) - i
		if utf8.RuneStart(u.pending[start]) {
			if !utf8.FullRune(u.pending[start:]) {
				cut = start // この文字はまだ途中
			}
			break
		}
	}

	out := string(u.pending[:cut])
	u.pending = append(u.pending[:0], u.pending[cut:]...)
	return out
}

// Flush は残っているバイトを全部返す(生成が終わったとき用)。
func (u *utf8Buffer) Flush() string {
	out := string(u.pending)
	u.pending = u.pending[:0]
	return out
}

// ---- 思考と答えの切り分け -----------------------------------------------------------
//
// モデルが書くのは、思考も答えも区別のない1本の文字列:
//
//	ユーザーは挨拶している。…(思考)…
//	</think>
//
//	こんにちは!(答え)
//
// プロンプトの最後に "<think>\n" を置いているので、出力は思考から始まる。
// "</think>" が出てきたら、そこから先が答え。
//
// "</think>" が2回に分かれて届くこともあるので("</th" + "ink>")、
// タグの書きかけかもしれない末尾は、次が届くまで出さずに持っておく。
const thinkEnd = "</think>"

type thinkSplitter struct {
	inThink    bool   // 今、思考の部分を受け取っているか
	held       string // タグの書きかけかもしれないので保留している文字列
	gotContent bool   // 答えの文字をすでに出したか
}

func newThinkSplitter(think bool) *thinkSplitter {
	return &thinkSplitter{inThink: think}
}

// Push は新しく届いた文字列を、思考と答えに振り分けて返す。
func (s *thinkSplitter) Push(text string) (thinking, content string) {
	s.held += text

	if s.inThink {
		if i := strings.Index(s.held, thinkEnd); i >= 0 {
			thinking = s.held[:i]
			rest := s.held[i+len(thinkEnd):]
			s.held = ""
			s.inThink = false
			content = s.content(rest)
			return thinking, content
		}
		// 末尾が "</think>" の書き出しと一致する分だけ保留する
		keep := partialSuffix(s.held, thinkEnd)
		thinking = s.held[:len(s.held)-keep]
		s.held = s.held[len(s.held)-keep:]
		return thinking, ""
	}

	text, s.held = s.held, ""
	return "", s.content(text)
}

// Flush は保留中の文字列を全部出す(生成が終わったとき用)。
func (s *thinkSplitter) Flush() (thinking, content string) {
	rest := s.held
	s.held = ""
	if s.inThink {
		return rest, ""
	}
	return "", s.content(rest)
}

// content は答えの部分を整える。"</think>" の直後の改行は表示に不要なので落とす。
func (s *thinkSplitter) content(text string) string {
	if !s.gotContent {
		text = strings.TrimLeft(text, "\n")
		if text == "" {
			return ""
		}
		s.gotContent = true
	}
	return text
}

// partialSuffix は、s の末尾が tag の先頭の何文字分と一致しているかを返す。
// 例: partialSuffix("考えた</th", "</think>") == 4
func partialSuffix(s, tag string) int {
	for n := min(len(tag)-1, len(s)); n > 0; n-- {
		if strings.HasSuffix(s, tag[:n]) {
			return n
		}
	}
	return 0
}
