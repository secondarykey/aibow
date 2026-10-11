package main

import "strings"

// Message は会話の1件。role は "system" / "user" / "assistant"。
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// buildPrompt は会話履歴を、モデルが学習時に見てきた書式(チャットテンプレート)の
// 1本の文字列に組み立てる。
//
// LLM は「メッセージの配列」を理解するわけではなく、ただの文字列の続きを予測しているだけ。
// Qwen は ChatML と呼ばれる次の書式で学習されているので、それに合わせて並べる:
//
//	<|im_start|>system
//	あなたは…<|im_end|>
//	<|im_start|>user
//	こんにちは<|im_end|>
//	<|im_start|>assistant
//	<think>
//
// 最後を「assistant の発言の書き出し」で終えておくと、モデルはその続き、
// つまりアシスタントの返事を書き始める。
//
// <|im_start|> や <|im_end|> は普通の文字列ではなく、語彙の中の特別な1トークン。
// トークン化のときに parseSpecial=true にすることで、特別なトークンとして扱われる。
//
// 書式は Qwen3.5 の公式テンプレート(GGUF の tokenizer.chat_template に入っている
// Jinja テンプレート)を、このアプリで使う部分だけ Go で書き直したもの。
func buildPrompt(messages []Message, think bool) string {
	var b strings.Builder
	for _, m := range messages {
		b.WriteString("<|im_start|>")
		b.WriteString(m.Role)
		b.WriteString("\n")
		// 過去のアシスタントの返事は「答え」だけを入れる(思考は入れない)。
		// 公式テンプレートも、最後のユーザー発言より前の思考は捨てている。
		b.WriteString(m.Content)
		b.WriteString("<|im_end|>\n")
	}

	b.WriteString("<|im_start|>assistant\n")
	if think {
		// 「<think>」まで書いておくと、モデルは考えるところから書き始める。
		// 考え終わると自分で </think> を書き、続けて答えを書く。
		b.WriteString("<think>\n")
	} else {
		// 空の思考を先に書いてしまうと、モデルは「もう考え終わった」と見なして
		// すぐに答えを書き始める。思考の ON/OFF の正体はこれだけ。
		b.WriteString("<think>\n\n</think>\n\n")
	}
	return b.String()
}
