package aibow

// format_qwen35.go: Qwen3.5 の書式(format.go の Format の実装)。
//
// Qwen3.5 の公式テンプレート(GGUF の tokenizer.chat_template に入っている Jinja テンプレート)を、
// このアプリで使う部分だけ Go で書き直したもの。

import "strings"

// qwen35 は Qwen3.5 の書式。GGUF の general.architecture が "qwen35" のモデルに使う。
type qwen35 struct{}

func (qwen35) Name() string { return "Qwen3.5" }

func (qwen35) BuildPrompt(p Prompt) string { return buildPrompt(p.Messages, p.Think) }

func (qwen35) NewParser(think bool) OutputParser {
	return &qwen35Parser{split: newThinkSplitter(think)}
}

// qwen35Parser は生成した文字列を思考と答えに振り分け、全体も覚えておく(Result 用)。
type qwen35Parser struct {
	split             *thinkSplitter
	thinking, content strings.Builder
}

func (q *qwen35Parser) Push(text string) (thinking, content string) {
	return q.keep(q.split.Push(text))
}

func (q *qwen35Parser) Flush() (thinking, content string) {
	return q.keep(q.split.Flush())
}

func (q *qwen35Parser) keep(thinking, content string) (string, string) {
	q.thinking.WriteString(thinking)
	q.content.WriteString(content)
	return thinking, content
}

func (q *qwen35Parser) Stop() bool { return false }

func (q *qwen35Parser) Result() Output {
	return Output{Thinking: q.thinking.String(), Content: q.content.String()}
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
