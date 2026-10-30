package format

// llama3.go: Llama 3(3.1・3.2)の書式(format.go の Format の実装。0027)。
//
// Ollama の llama3.2:3b の GGUF の tokenizer.chat_template(Meta のテンプレート)を、aibow が渡す会話の形で通る部分だけ Go で書き直したもの。
// 空白や改行の1文字まで同じになるよう、テンプレートを jinja2 で通した文字列と比べてテストしている(testdata/llama3/)。
//
//	<|start_header_id|>system<|end_header_id|>
//
//	Environment: ipython                          ← ツールがあるとき
//	Cutting Knowledge Date: December 2023
//	Today Date: 30 Oct 2026                       ← 3.2 は今日の日付(3.1 は 26 Jul 2024)
//
//	あなたは…<|eot_id|><|start_header_id|>user<|end_header_id|>
//
//	Given the following functions, …              ← ツールの説明は、最初の発言の頭に入れる
//	{
//	    "type": "function",
//	    …
//	}
//
//	1234 × 5678 は?<|eot_id|><|start_header_id|>assistant<|end_header_id|>
//
//	{"name": "calculate", "parameters": {"expression": "1234 * 5678"}}<|eot_id|><|start_header_id|>ipython<|end_header_id|>
//
//	"7006652"<|eot_id|><|start_header_id|>assistant<|end_header_id|>
//
// Qwen3.5・Gemma 4 と比べると:
//
//   - **思考が無い**。思考の ON / OFF は無視する(画面は「思考する」を押せなくする。0027 の決定事項 4)
//   - **呼び出しに印が無い**。返事の全体を JSON にして呼ぶ。答えか呼び出しかは、中身で見分ける
//   - 1回の返事に呼び出しは1つだけで、呼び出しのある返事の答えの文は書かない。aibow の会話はそうとは限らない(ほかのモデルの会話を引き継いだ)ので、
//     分けて書く(splitForLlama3。0027 の決定事項 7・8)
//   - ツールの結果は ipython の役の発言。テンプレートは結果の文字を JSON の文字列にして書く(Jinja では文字列も「繰り返せるもの」なので tojson を通る)
//   - 呼び出しの後ろは <|eot_id|>(発言の終わり)なので、止める印は要らない(llama.cpp が止める)
//
// bos(<|begin_of_text|>)は書かない。llama.cpp がトークン化で1つ足す(0026 の決定事項 25・0027 の段1)。

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"time"
	"unicode"

	"github.com/secondarykey/aibow/tool"
)

// llama3 は Llama 3 の書式。GGUF の種類が llama で、チャットテンプレートに <|start_header_id|> があるモデルに使う(0027 の決定事項 2)。
type llama3 struct {
	// dated は、テンプレートが今日の日付を書くか(strftime_now を使う。3.2)。書かなければ 26 Jul 2024 に決め打ち(3.1)。0027 の決定事項 3
	dated bool
}

// newLlama3 は、GGUF のテンプレートを見て Llama 3 の書式を作る。
func newLlama3(template string) Format {
	return llama3{dated: strings.Contains(template, "strftime_now")}
}

// llama3Now は今日(テストで差し替える)。
var llama3Now = time.Now

func (llama3) Name() string { return "Llama 3" }

// NoThinking は思考しない書式の印(画面の「思考する」を押せなくする。0027 の決定事項 4)。
func (llama3) NoThinking() bool { return true }

// テンプレートの英語の文(訳さない。モデルが学習で見た文のため)。文と文の間に空白が無いのもテンプレートのまま
const (
	llama3Format    = `Respond in the format {"name": function name, "parameters": dictionary of argument name and its value}.` + "Do not use variables.\n\n"
	llama3ToolsUser = "Given the following functions, please respond with a JSON for a function call " +
		"with its proper arguments that best answers the given prompt.\n\n" + llama3Format
	llama3ToolsSystem = "You have access to the following functions. To call a function, please respond with JSON for a function call." + llama3Format
)

// BuildPrompt は会話を Llama 3 の書式の1本の文字列にする(bos は書かない)。
func (l llama3) BuildPrompt(p Prompt) string {
	var b strings.Builder
	msgs := splitForLlama3(p.Messages)

	// 1. システムの発言(いつも作る)。日付と、ツールがあれば ipython の印
	system := ""
	if len(msgs) > 0 && msgs[0].Role == "system" {
		system = strings.TrimSpace(msgs[0].Content)
		msgs = msgs[1:]
	}
	// ツールの説明は最初の発言の頭に入れる(テンプレートの既定)。発言が無いとき(ツールの説明だけを数える)はシステムに入れる(決定事項 5)
	toolsInUser := len(msgs) > 0
	b.WriteString("<|start_header_id|>system<|end_header_id|>\n\n")
	if len(p.Tools) > 0 {
		b.WriteString("Environment: ipython\n")
	}
	b.WriteString("Cutting Knowledge Date: December 2023\n")
	b.WriteString("Today Date: " + l.today() + "\n\n")
	if len(p.Tools) > 0 && !toolsInUser {
		b.WriteString(llama3ToolsSystem)
		writeLlama3Tools(&b, p.Tools)
	}
	b.WriteString(system + "<|eot_id|>")

	// 2. ツールの説明と最初の発言(役によらず user として書く。テンプレートのまま)
	if len(p.Tools) > 0 && toolsInUser {
		b.WriteString("<|start_header_id|>user<|end_header_id|>\n\n" + llama3ToolsUser)
		writeLlama3Tools(&b, p.Tools)
		b.WriteString(strings.TrimSpace(msgs[0].Content) + "<|eot_id|>")
		msgs = msgs[1:]
	}

	// 3. 発言。思考は書かない(この書式には無い)
	for _, m := range msgs {
		switch {
		case m.Role == "tool":
			b.WriteString("<|start_header_id|>ipython<|end_header_id|>\n\n" + pyJSONString(m.Content) + "<|eot_id|>")
		case len(m.ToolCalls) > 0:
			b.WriteString("<|start_header_id|>assistant<|end_header_id|>\n\n")
			writeLlama3Call(&b, m.ToolCalls[0], p.Tools)
			b.WriteString("<|eot_id|>")
		default:
			b.WriteString("<|start_header_id|>" + m.Role + "<|end_header_id|>\n\n" + strings.TrimSpace(m.Content) + "<|eot_id|>")
		}
	}

	// 4. 返事の書き出し
	b.WriteString("<|start_header_id|>assistant<|end_header_id|>\n\n")
	return b.String()
}

// today はシステムの発言に書く日付(テンプレートの date_string)。
func (l llama3) today() string {
	if l.dated {
		return llama3Now().Format("02 Jan 2006") // strftime の "%d %b %Y"
	}
	return "26 Jul 2024"
}

// splitForLlama3 は、Llama 3 に渡せる形に返事を分ける(0027 の決定事項 7・8)。
//
// Llama 3 は1回の返事に呼び出し1つで、呼び出しのある返事の答えの文を書かない。そうでない返事(ほかのモデルの会話を引き継いだ・モデルが2つ書いた)は、
// 答えの文があれば先に答えだけの返事にし、呼び出しは1つずつ「呼び出し → その結果」の組に並べ直す:
//
//	assistant(答え・呼び出し A・B)、tool(A の結果)、tool(B の結果)
//	→ assistant(答え)、assistant(呼び出し A)、tool(A の結果)、assistant(呼び出し B)、tool(B の結果)
//
// 期待値のスクリプト(testdata/gen_expected.py の split_for_llama3)も同じ決まりで分けてからテンプレートに渡す。
func splitForLlama3(msgs []Message) []Message {
	var out []Message
	for i := 0; i < len(msgs); {
		m := msgs[i]
		if m.Role != "assistant" || len(m.ToolCalls) == 0 {
			out = append(out, m)
			i++
			continue
		}
		j := i + 1
		for j < len(msgs) && msgs[j].Role == "tool" {
			j++
		}
		results := msgs[i+1 : j]
		if strings.TrimSpace(m.Content) != "" {
			out = append(out, Message{Role: "assistant", Content: m.Content})
		}
		for k, c := range m.ToolCalls {
			out = append(out, Message{Role: "assistant", ToolCalls: []ToolCall{c}})
			if k < len(results) {
				out = append(out, results[k])
			}
		}
		if len(results) > len(m.ToolCalls) {
			out = append(out, results[len(m.ToolCalls):]...)
		}
		i = j
	}
	return out
}

// writeLlama3Tools はツールの定義を、字下げ 4 の JSON で1つずつ書く(テンプレートの tojson(indent=4))。
func writeLlama3Tools(b *strings.Builder, tools []tool.Def) {
	for _, t := range tools {
		params := bytes.TrimSpace(t.Parameters)
		if len(params) == 0 {
			params = []byte("null")
		}
		raw := `{"type": "function", "function": {"name": ` + pyJSONString(t.Name) + `, "description": ` + pyJSONString(t.Description) +
			`, "parameters": ` + string(params) + `}}`
		b.WriteString(pyJSONIndent([]byte(raw), 4) + "\n\n")
	}
}

// writeLlama3Call は呼び出しを1つ書く。{"name": "名前", "parameters": {引数}}(引数は書いた順。テンプレートの tojson)。
// 書式が崩れて読み取れなかった呼び出しは、モデルが書いたままを入れる(Qwen3.5・Gemma 4 と同じ)。
func writeLlama3Call(b *strings.Builder, c ToolCall, tools []tool.Def) {
	if c.Raw != "" {
		b.WriteString(c.Raw)
		return
	}
	types := tool.ArgTypesFor(tools, c.Name)
	b.WriteString(`{"name": "` + c.Name + `", "parameters": {`)
	for i, a := range c.Args {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(pyJSONString(a.Name) + ": " + pyJSON([]byte(tool.ArgJSON(types[a.Name], a.Value))))
	}
	b.WriteString("}}")
}

// pyJSONIndent は JSON を、Python の json.dumps(indent=n, ensure_ascii=False) と同じ見た目に書き直す
// (キーは書いた順・区切りは "," と ": "・空の配列と辞書は [] と {}・数は Python の str())。
func pyJSONIndent(raw []byte, n int) string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var b strings.Builder
	type frame struct {
		object bool
		n      int // ここまでに書いた要素の数(object ではキーと値を別に数える)
	}
	var stack []frame
	newline := func() { b.WriteString("\n" + strings.Repeat(" ", n*len(stack))) }
	sep := func() { // 次に書く要素の前の区切り
		if len(stack) == 0 {
			return
		}
		f := &stack[len(stack)-1]
		switch {
		case f.object && f.n%2 == 1:
			b.WriteString(": ")
		case f.n > 0:
			b.WriteString(",")
			newline()
		default:
			newline()
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
				if !dec.More() { // 空の配列・辞書は1行で書く
					dec.Token()
					b.WriteString(map[json.Delim]string{'{': "{}", '[': "[]"}[v])
					continue
				}
				b.WriteString(v.String())
				stack = append(stack, frame{object: v == '{'})
			case '}', ']':
				stack = stack[:len(stack)-1]
				newline()
				b.WriteString(v.String())
			}
		case string:
			sep()
			b.WriteString(pyJSONString(v))
		case json.Number:
			sep()
			b.WriteString(pyNumber(v))
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

// ---- 出力の読み取り -----------------------------------------------------------------
//
// Llama 3 の返事は、答えの文か、全体が JSON の呼び出しのどちらか:
//
//	東京の天気は…                                                   ← 答え
//	{"name": "calculate", "parameters": {"expression": "1+1"}}       ← 呼び出し(印が無い)
//
// 返事の頭(空白を除いた最初の文字)が { か <|python_tag|> なら、呼び出しかもしれないので画面に出さずに持っておき、
// 終わってから JSON として読む。読めれば呼び出し、"name" があるのに読めなければ崩れた呼び出し、それ以外は答えとして出す(0027 の決定事項 6)。
// コードブロックで始まる返事(```)は、頭が { ではないので答えのまま。

// llama3CallHelp は、書式が崩れていたときにモデルに返すエラーに添える、正しい書き方。
const llama3CallHelp = `正しい書式: {"name": "関数名", "parameters": {"引数名": "値"}}(返事の全体を、この JSON 1つだけにする)`

const llama3PythonTag = "<|python_tag|>"

func (llama3) NewParser(p Prompt) OutputParser {
	return &llama3Parser{tools: p.Tools}
}

// llama3Parser は生成した文字列を、答えと呼び出しに振り分ける。
type llama3Parser struct {
	tools   []tool.Def
	all     strings.Builder // 届いた全部
	held    bool            // 頭が { か <|python_tag|> なので、画面に出さずに持っている
	decided bool            // 頭の文字を見て、持つかを決めたか
	content strings.Builder // 画面に出した答え
}

func (q *llama3Parser) Push(text string) (thinking, content string) {
	q.all.WriteString(text)
	if q.held {
		return "", ""
	}
	if !q.decided {
		head := strings.TrimLeftFunc(q.all.String(), unicode.IsSpace)
		switch {
		case head == "", strings.HasPrefix(llama3PythonTag, head) && head != llama3PythonTag:
			return "", "" // まだ頭が決まらない(空白だけ・<|python_tag|> の書きかけ)
		case strings.HasPrefix(head, "{"), strings.HasPrefix(head, llama3PythonTag):
			q.held, q.decided = true, true
			return "", ""
		}
		q.decided = true
		text = head // 頭の空白は出さない
	}
	q.content.WriteString(text)
	return "", text
}

func (q *llama3Parser) Flush() (thinking, content string) {
	if !q.decided || q.held {
		// 持っていたものは、呼び出しとして読めなければ答えとして出す
		if _, ok := q.calls(); ok {
			return "", ""
		}
		rest := strings.TrimSpace(q.all.String())
		q.content.Reset()
		q.content.WriteString(rest)
		q.held, q.decided = false, true
		return "", rest
	}
	return "", ""
}

// Stop は使わない(呼び出しの後ろは <|eot_id|> で、llama.cpp が止める)。
func (q *llama3Parser) Stop() bool { return false }

func (q *llama3Parser) Result() Output {
	if calls, ok := q.calls(); ok {
		return Output{ToolCalls: calls}
	}
	return Output{Content: strings.TrimRightFunc(q.content.String(), unicode.IsSpace)}
}

// calls は、返事の全体を呼び出しとして読む。呼び出しでなければ ok は false。
// JSON のオブジェクトが ; や空白で区切られて続くときは、それぞれを呼び出しにする(Llama 3.2 はまれに2つ書く)。
func (q *llama3Parser) calls() ([]ToolCall, bool) {
	text := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(q.all.String()), llama3PythonTag))
	if !strings.HasPrefix(text, "{") {
		return nil, false
	}
	raw := strings.TrimSpace(q.all.String())
	var calls []ToolCall
	for rest := text; rest != ""; {
		var obj struct {
			Name       *string         `json:"name"`
			Parameters json.RawMessage `json:"parameters"`
			Arguments  json.RawMessage `json:"arguments"`
		}
		dec := json.NewDecoder(strings.NewReader(rest))
		err := dec.Decode(&obj)
		switch {
		case err != nil && strings.Contains(text, `"name"`):
			// 呼び出しのつもりで崩れた(閉じていない・JSON でない など)
			return []ToolCall{brokenCall(raw, "", "JSON として読めません: "+err.Error(), llama3CallHelp)}, true
		case err != nil, obj.Name == nil, obj.Parameters == nil && obj.Arguments == nil:
			return nil, false // 答えとして JSON を書いた
		}
		params := obj.Parameters
		if params == nil {
			params = obj.Arguments
		}
		if string(bytes.TrimSpace(params)) == "null" {
			params = []byte("{}")
		}
		args, err := tool.ArgsFromJSON(string(params), tool.ArgTypesFor(q.tools, *obj.Name))
		if err != nil {
			return []ToolCall{brokenCall(raw, *obj.Name, "parameters が JSON のオブジェクトではありません", llama3CallHelp)}, true
		}
		calls = append(calls, ToolCall{Call: tool.Call{Name: *obj.Name, Args: args}})
		// 次の呼び出しとの区切り(; や空白)を読み飛ばす
		rest = strings.TrimLeft(rest[dec.InputOffset():], " \t\r\n;")
	}
	return calls, len(calls) > 0
}
