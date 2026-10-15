package engine

// stream.go: 生成したトークンを、文字として完成した単位で取り出す部分。

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
//
// モデルは UTF-8 として正しくないバイト列を出すこともある(小さいモデルや、重みがランダムなモデルで起きやすい)。
// そのまま保存すると、次の送信でプロンプトに入ったときに llama.cpp のトークン化が止まる(プロセスごと落ちる)ことがあるので、
// 文字にならないバイトは U+FFFD(�)に置き換えて返す。
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

	out := strings.ToValidUTF8(string(u.pending[:cut]), replacementChar)
	u.pending = append(u.pending[:0], u.pending[cut:]...)
	return out
}

// Flush は残っているバイトを全部返す(生成が終わったとき用)。書きかけのまま終わった文字は U+FFFD にする。
func (u *utf8Buffer) Flush() string {
	out := strings.ToValidUTF8(string(u.pending), replacementChar)
	u.pending = u.pending[:0]
	return out
}

// replacementChar は文字にならないバイトの代わりに入れる文字(U+FFFD)。
const replacementChar = "\uFFFD"
