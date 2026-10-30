package format

// granite.go: IBM の Granite 4(4.0・4.1)の書式(format.go の Format の実装。0029)。
//
// Ollama の granite4.1:3b の GGUF の tokenizer.chat_template(Hugging Face の ibm-granite/granite-4.1-3b と同じもの)を、
// aibow が渡す会話の形で通る部分だけ Go で書き直したもの。空白や改行の1文字まで同じになるよう、テンプレートを jinja2 で通した文字列と
// 比べてテストしている(testdata/granite4/。4.0 は testdata/granite40/)。
//
//	<|start_of_role|>system<|end_of_role|>あなたは…
//
//	You are a helpful assistant with access to the following tools. …   ← ツールがあるとき
//	<tools>
//	{"type": "function", "function": {"name": "calculate", …}}
//	</tools>
//
//	For each tool call, return a json object …<|end_of_text|>
//	<|start_of_role|>user<|end_of_role|>1234 × 5678 は?<|end_of_text|>
//	<|start_of_role|>assistant<|end_of_role|>計算します。
//	<tool_call>
//	{"name": "calculate", "arguments": {"expression": "1234 * 5678"}}
//	</tool_call><|end_of_text|>
//	<|start_of_role|>user<|end_of_role|>
//	<tool_response>
//	7006652
//	</tool_response><|end_of_text|>
//	<|start_of_role|>assistant<|end_of_role|>
//
// ほかの書式と比べると:
//
//   - 呼び出しは <tool_call> で JSON を囲む(Qwen3.5 と同じ印で、中身は Llama 3 と同じ JSON。Hermes 3・Qwen2.5 もこの流儀)。
//     読み取りの振り分けは Qwen3.5 と共通(parts.go の toolCallParser)で、中身の読み方だけが違う
//   - **思考が無い**(Llama 3 と同じ。0029 の決定事項 6)
//   - 続けて返った結果は、1つの user の発言の中に <tool_response> を並べる(Qwen3.5 と同じ)。結果の名前は書かない
//   - 答えの文も結果も、テンプレートは前後の空白を削らずにそのまま書く(Qwen3.5・Llama 3 は削る)
//   - 4.0 は、システムの発言が無いときに既定の文を書く(4.1 との違いはこれだけ。0029 の決定事項 9)
//   - 呼び出しの後ろでモデルが <tool_response> を書き始めたら止める。<tool_response> は発言の終わりのトークンではないので、
//     llama.cpp は止めない(0029 の段1)
//
// bos は書かない。どちらの版も、テンプレートは bos を書かず、llama.cpp も足さない(0029 の段1)。
//
// 同じ granite の種類でも、3.x は呼び出しの書き方が違い(<|tool_call|>)、4.2 は区切りが ChatML で呼び出しが Qwen3.5 と同じ形なので、
// テンプレートに <|start_of_role|> と <tool_call> の両方があるときだけこの書式を選ぶ(0029 の決定事項 2)。

import (
	"encoding/json"
	"strings"

	"github.com/secondarykey/aibow/tool"
)

// granite4 は Granite 4(4.0・4.1)の書式。
type granite4 struct {
	// defaultSystem は、システムの発言が無いときに書く文(4.0 のテンプレートの g4_default_system_message)。4.1 は空
	defaultSystem string
}

// テンプレートの英語の文(訳さない。モデルが学習で見た文のため)
const (
	granite4DefaultSystem = "You are a helpful assistant. Please ensure responses are professional, accurate, and safe."
	granite4ToolsIntro    = "You are a helpful assistant with access to the following tools. You may call one or more tools to assist with the user query.\n\n" +
		"You are provided with function signatures within <tools></tools> XML tags:\n<tools>"
	granite4ToolsGuide = "\n</tools>\n\nFor each tool call, return a json object with function name and arguments within <tool_call></tool_call> XML tags:\n" +
		"<tool_call>\n{\"name\": <function-name>, \"arguments\": <args-json-object>}\n</tool_call>. " +
		"If a tool does not exist in the provided list of tools, notify the user that you do not have the ability to fulfill the request."
)

// newGranite4 は、GGUF のテンプレートを見て Granite 4 の書式を作る。テンプレートに既定のシステムの文があれば 4.0。
func newGranite4(template string) Format {
	if strings.Contains(template, granite4DefaultSystem) {
		return granite4{defaultSystem: granite4DefaultSystem}
	}
	return granite4{}
}

func (granite4) Name() string { return "Granite 4" }

// NoThinking は思考しない書式の印(画面の「思考する」を押せなくする。0027 の決定事項 18)。
func (granite4) NoThinking() bool { return true }

// BuildPrompt は会話を Granite 4 の書式の1本の文字列にする(bos は書かない)。
func (g granite4) BuildPrompt(p Prompt) string {
	var b strings.Builder
	msgs := p.Messages

	// 1. システムの発言。システムの文とツールの説明のどちらかがあれば作る(4.0 はどちらも無くても既定の文で作る)
	hasSystem := len(msgs) > 0 && msgs[0].Role == "system"
	system := ""
	if hasSystem {
		system = msgs[0].Content
		msgs = msgs[1:]
	}
	if len(p.Tools) > 0 {
		if hasSystem {
			system += "\n\n"
		}
		system += granite4Tools(p.Tools)
	}
	if system == "" {
		system = g.defaultSystem
	}
	if system != "" {
		b.WriteString("<|start_of_role|>system<|end_of_role|>" + system + "<|end_of_text|>\n")
	}

	// 2. 発言。思考は書かない(この書式には無い)
	for i, m := range msgs {
		switch m.Role {
		case "assistant":
			b.WriteString("<|start_of_role|>assistant<|end_of_role|>" + m.Content)
			for j, c := range m.ToolCalls {
				if j > 0 || m.Content != "" {
					b.WriteString("\n")
				}
				writeGranite4Call(&b, c, p.Tools)
			}
			b.WriteString("<|end_of_text|>\n")
		case "tool":
			// ツールの結果は user の発言の形で返す。続けて複数あるときは、1つの user の中に並べる
			if i == 0 || msgs[i-1].Role != "tool" {
				b.WriteString("<|start_of_role|>user<|end_of_role|>")
			}
			b.WriteString("\n<tool_response>\n" + m.Content + "\n</tool_response>")
			if i == len(msgs)-1 || msgs[i+1].Role != "tool" {
				b.WriteString("<|end_of_text|>\n")
			}
		default: // user と、2つ目からのシステムの発言
			b.WriteString("<|start_of_role|>" + m.Role + "<|end_of_role|>" + m.Content + "<|end_of_text|>\n")
		}
	}

	// 3. 返事の書き出し
	b.WriteString("<|start_of_role|>assistant<|end_of_role|>")
	return b.String()
}

// granite4Tools はシステムの発言に入れるツールの説明(ツールの定義は1つにつき1行の JSON。Qwen3.5 と同じ形)。
func granite4Tools(tools []tool.Def) string {
	var b strings.Builder
	b.WriteString(granite4ToolsIntro)
	for _, t := range tools {
		b.WriteString("\n" + `{"type": "function", "function": ` + toolDefJSON(t) + `}`)
	}
	b.WriteString(granite4ToolsGuide)
	return b.String()
}

// writeGranite4Call は呼び出しを1つ書く。<tool_call>\n{"name": "名前", "arguments": {引数}}\n</tool_call>
// (引数は書いた順。テンプレートの tojson なので、真偽は JSON の true)。
// 書式が崩れて読み取れなかった呼び出しは、モデルが書いたままを入れる(ほかの書式と同じ)。
func writeGranite4Call(b *strings.Builder, c ToolCall, tools []tool.Def) {
	if c.Raw != "" {
		b.WriteString(c.Raw)
		return
	}
	types := tool.ArgTypesFor(tools, c.Name)
	b.WriteString(toolCallStart + "\n" + `{"name": "` + c.Name + `", "arguments": {`)
	for i, a := range c.Args {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(pyJSONString(a.Name) + ": " + pyJSON([]byte(tool.ArgJSON(types[a.Name], a.Value))))
	}
	b.WriteString("}}\n" + toolCallEnd)
}

// ---- 出力の読み取り -----------------------------------------------------------------
//
// モデルが書く返事は、答えの文の後ろに、行の頭から呼び出しを並べたもの:
//
//	計算します。                                                     ← 答え(画面に出す)
//	<tool_call>                                                      ← ここから先は答えの欄に出さない
//	{"name": "calculate", "arguments": {"expression": "1234 * 5678"}}
//	</tool_call>
//
// 振り分けは Qwen3.5 と同じ(parts.go の toolCallParser)。思考は無いので、全部を答えとして受け取る。

// granite4CallHelp は、書式が崩れていたときにモデルに返すエラーに添える、正しい書き方。
const granite4CallHelp = "正しい書式: <tool_call>\n{\"name\": \"関数名\", \"arguments\": {\"引数名\": \"値\"}}\n</tool_call>"

func (granite4) NewParser(p Prompt) OutputParser {
	return &toolCallParser{split: newThinkSplitter(false), tools: p.Tools, parse: parseGranite4Calls}
}

// parseGranite4Calls は「呼び出しの部分」(<tool_call> から後ろ)から、呼び出しを全部読み取る。
func parseGranite4Calls(s string, tools []tool.Def) []ToolCall {
	blocks, open := toolCallBlocks(s)
	var calls []ToolCall
	for _, block := range blocks {
		calls = append(calls, parseGranite4Call(block, tools))
	}
	if open != "" {
		// 閉じないまま終わった(返事の上限・停止など)
		calls = append(calls, brokenCall(open, "", "</tool_call> で閉じていません", granite4CallHelp))
	}
	return calls
}

// parseGranite4Call は <tool_call>…</tool_call> を1つ読み取る。中身は {"name": "名前", "arguments": {引数}} の JSON。
// 引数はツールの定義の型を見て tool.Arg の形にする(Llama 3 と同じ。0026 の決定事項 1)。
func parseGranite4Call(block string, tools []tool.Def) ToolCall {
	inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(block, toolCallStart), toolCallEnd))
	var obj struct {
		Name      *string         `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal([]byte(inner), &obj); err != nil {
		return brokenCall(block, "", "<tool_call> の中が JSON として読めません: "+err.Error(), granite4CallHelp)
	}
	if obj.Name == nil || *obj.Name == "" {
		return brokenCall(block, "", "\"name\" がありません", granite4CallHelp)
	}
	params := strings.TrimSpace(string(obj.Arguments))
	if params == "" || params == "null" {
		params = "{}"
	}
	args, err := tool.ArgsFromJSON(params, tool.ArgTypesFor(tools, *obj.Name))
	if err != nil {
		return brokenCall(block, *obj.Name, "\"arguments\" が JSON のオブジェクトではありません", granite4CallHelp)
	}
	return ToolCall{Call: tool.Call{Name: *obj.Name, Args: args}}
}
