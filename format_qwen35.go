package aibow

// format_qwen35.go: Qwen3.5 の書式(format.go の Format の実装)。
//
// Qwen3.5 の公式テンプレート(GGUF の tokenizer.chat_template に入っている Jinja テンプレート)を、
// このアプリで使う部分だけ Go で書き直したもの。空白や改行の1文字まで同じになるよう、
// テンプレートを jinja2 で実際に通した文字列と比べてテストしている(testdata/qwen35/)。

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
)

// qwen35 は Qwen3.5 の書式。GGUF の general.architecture が "qwen35" のモデルに使う。
type qwen35 struct{}

func (qwen35) Name() string { return "Qwen3.5" }

// BuildPrompt は会話を Qwen3.5 の書式の1本の文字列にする。
//
// Qwen は ChatML と呼ばれる次の書式で学習されているので、それに合わせて並べる:
//
//	<|im_start|>system
//	# Tools …(ツールを使うときだけ。ツールの定義と呼び出し方の説明)
//
//	あなたは…<|im_end|>
//	<|im_start|>user
//	1234 × 5678 は?<|im_end|>
//	<|im_start|>assistant
//	<think>
//	計算ツールを使おう。
//	</think>
//
//	<tool_call>
//	<function=calculate>
//	<parameter=expression>
//	1234 * 5678
//	</parameter>
//	</function>
//	</tool_call><|im_end|>
//	<|im_start|>user
//	<tool_response>
//	7006652
//	</tool_response><|im_end|>
//	<|im_start|>assistant
//	<think>
//
// 最後を「assistant の発言の書き出し」で終えておくと、モデルはその続き、
// つまりアシスタントの返事を書き始める。
//
// <|im_start|> や <|im_end|> は普通の文字列ではなく、語彙の中の特別な1トークン。
// トークン化のときに parseSpecial=true にすることで、特別なトークンとして扱われる。
func (qwen35) BuildPrompt(p Prompt) string {
	var b strings.Builder
	msgs := p.Messages

	// 1. システムプロンプト。ツールを使うときは、その先頭にツールの説明を置く
	hasSystem := len(msgs) > 0 && msgs[0].Role == "system"
	system := ""
	if hasSystem {
		system = strings.TrimSpace(msgs[0].Content)
		msgs = msgs[1:]
	}
	if len(p.Tools) > 0 {
		b.WriteString("<|im_start|>system\n")
		writeQwen35Tools(&b, p.Tools)
		if system != "" {
			b.WriteString("\n\n" + system)
		}
		b.WriteString("<|im_end|>\n")
	} else if hasSystem {
		b.WriteString("<|im_start|>system\n" + system + "<|im_end|>\n")
	}

	// 2. 発言。最後のユーザーの質問より後の返事(= ツールのループの途中の返事)だけ、思考も残す。
	// それより前の返事は答えだけにする(思考は長く、終わった質問の思考はもう役に立たないため)。
	// ループの途中の思考を残すのは、「何のためにツールを呼んだか」を覚えたまま続きを書けるようにするため
	lastQuery := -1
	for i, m := range msgs {
		if m.Role == "user" {
			lastQuery = i
		}
	}
	for i, m := range msgs {
		content := strings.TrimSpace(m.Content)
		switch m.Role {
		case "user":
			b.WriteString("<|im_start|>user\n" + content + "<|im_end|>\n")
		case "assistant":
			b.WriteString("<|im_start|>assistant\n")
			if i > lastQuery {
				b.WriteString("<think>\n" + strings.TrimSpace(m.Thinking) + "\n</think>\n\n")
			}
			b.WriteString(content)
			writeQwen35Calls(&b, m.ToolCalls, content != "")
			b.WriteString("<|im_end|>\n")
		case "tool":
			// ツールの結果は user の発言の形で返す。続けて複数あるときは、1つの user の中に並べる
			if i == 0 || msgs[i-1].Role != "tool" {
				b.WriteString("<|im_start|>user")
			}
			b.WriteString("\n<tool_response>\n" + content + "\n</tool_response>")
			if i == len(msgs)-1 || msgs[i+1].Role != "tool" {
				b.WriteString("<|im_end|>\n")
			}
		}
	}

	// 3. 返事の書き出し
	b.WriteString("<|im_start|>assistant\n")
	if p.Think {
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

// qwen35ToolsIntro と qwen35ToolsGuide は、公式テンプレートのツールの説明の文(英語のまま)。
// モデルはこの文と一緒に学習しているので、訳したり言い換えたりしない。
const (
	qwen35ToolsIntro = "# Tools\n\nYou have access to the following functions:\n\n<tools>"
	qwen35ToolsGuide = "\n</tools>" +
		"\n\nIf you choose to call a function ONLY reply in the following format with NO suffix:\n\n" +
		"<tool_call>\n<function=example_function_name>\n<parameter=example_parameter_1>\nvalue_1\n</parameter>\n" +
		"<parameter=example_parameter_2>\nThis is the value for the second parameter\nthat can span\nmultiple lines\n</parameter>\n" +
		"</function>\n</tool_call>\n\n" +
		"<IMPORTANT>\nReminder:\n" +
		"- Function calls MUST follow the specified format: an inner <function=...></function> block must be nested within <tool_call></tool_call> XML tags\n" +
		"- Required parameters MUST be specified\n" +
		"- You may provide optional reasoning for your function call in natural language BEFORE the function call, but NOT after\n" +
		"- If there is no function call available, answer the question like normal with your current knowledge and do not tell the user about function calls\n" +
		"</IMPORTANT>"
)

// writeQwen35Tools はツールの説明を書く。ツールの定義は1つにつき1行の JSON。
func writeQwen35Tools(b *strings.Builder, tools []ToolDef) {
	b.WriteString(qwen35ToolsIntro)
	for _, t := range tools {
		b.WriteString("\n")
		b.WriteString(`{"type": "function", "function": {"name": ` + pyJSON(jsonString(t.Name)) +
			`, "description": ` + pyJSON(jsonString(t.Description)) +
			`, "parameters": ` + pyJSON(t.Parameters) + `}}`)
	}
	b.WriteString(qwen35ToolsGuide)
}

// writeQwen35Calls は返事の中のツール呼び出しを書く。afterContent は、呼び出しの前に答えの文があるか。
//
//	答えの文
//
//	<tool_call>
//	<function=名前>
//	<parameter=引数の名前>
//	値(複数行でもよい)
//	</parameter>
//	</function>
//	</tool_call>
//	<tool_call>
//	…(2つ目)
func writeQwen35Calls(b *strings.Builder, calls []ToolCall, afterContent bool) {
	for i, c := range calls {
		switch {
		case i > 0:
			b.WriteString("\n")
		case afterContent:
			b.WriteString("\n\n")
		}
		if c.Raw != "" {
			// 書式が崩れて読み取れなかった呼び出しは、モデルが書いたままを入れる。
			// 結果の「エラー: …」と並べて、モデルが自分の間違いを見られるようにする
			b.WriteString(c.Raw)
			continue
		}
		b.WriteString("<tool_call>\n<function=" + c.Name + ">\n")
		for _, a := range c.Args {
			b.WriteString("<parameter=" + a.Name + ">\n" + a.Value + "\n</parameter>\n")
		}
		b.WriteString("</function>\n</tool_call>")
	}
}

// jsonString は文字列を JSON の文字列にする。
func jsonString(s string) []byte {
	b, _ := json.Marshal(s) // 文字列の Marshal は失敗しない
	return b
}

// pyJSON は JSON を、Python の json.dumps(ensure_ascii=False)と同じ見た目に書き直す。
//
// テンプレートの tojson は、学習に使われた HuggingFace transformers では json.dumps で、
// 区切りが ", " と ": "(空白あり)、キーは書いた順のまま、日本語や < > & はそのまま書く。
// Go の json.Marshal は区切りに空白を入れず、map のキーを並べ替え、< > & を < などにするので、
// モデルが学習で見た形と変わってしまう。そこで、トークンを1つずつ読んで書き直す。
//
// 書いた順を保つため、ツールの JSON Schema は map ではなく JSON の文字列(json.RawMessage)で持っている。
// 読めない JSON は、そのまま返す(ツールの定義はコードに書いた定数なので、テストで確かめている)。
func pyJSON(raw []byte) string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber() // 数は書いたままの文字列で受け取る(1.0 が 1 にならないように)

	var b strings.Builder
	type frame struct {
		object bool // {} の中か([] の中なら false)
		n      int  // ここまでに書いた要素の数(object ではキーと値を別に数える)
	}
	var stack []frame
	// sep は、次に書く要素の前の区切りを書く
	sep := func() {
		if len(stack) == 0 {
			return
		}
		f := &stack[len(stack)-1]
		switch {
		case f.object && f.n%2 == 1:
			b.WriteString(": ") // キーと値の間
		case f.n > 0:
			b.WriteString(", ") // 要素と要素の間
		}
		f.n++
	}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return string(raw)
		}
		switch v := tok.(type) {
		case json.Delim:
			switch v {
			case '{', '[':
				sep()
				stack = append(stack, frame{object: v == '{'})
			case '}', ']':
				stack = stack[:len(stack)-1]
			}
			b.WriteString(v.String())
		case string:
			sep()
			var s bytes.Buffer
			enc := json.NewEncoder(&s)
			enc.SetEscapeHTML(false)
			enc.Encode(v)
			b.WriteString(strings.TrimSuffix(s.String(), "\n"))
		case json.Number:
			sep()
			b.WriteString(v.String())
		case bool:
			sep()
			if v {
				b.WriteString("true")
			} else {
				b.WriteString("false")
			}
		case nil:
			sep()
			b.WriteString("null")
		}
	}
	return b.String()
}

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
