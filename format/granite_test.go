package format

import (
	"reflect"
	"strings"
	"testing"

	"github.com/secondarykey/aibow/tool"
)

// GGUF の中のテンプレート(Jinja)と、空白や改行の1文字まで同じ文字列になること。
// 4.0 は Hugging Face の granite-4.0-micro のテンプレートで作った期待値(4.1 との違いは既定のシステムの文だけ。0029 の決定事項 9)
func TestGranite4PromptMatchesTemplate(t *testing.T) {
	testPromptMatchesTemplate(t, "granite4", granite4{})
	testPromptMatchesTemplate(t, "granite40", granite4{defaultSystem: granite4DefaultSystem})
}

// 書式はテンプレートの印で選ぶ。同じ granite の種類でも、3.x と 4.2 は選ばない(0029 の決定事項 2)
func TestGranite4ChosenByTemplate(t *testing.T) {
	const v41 = `{{- '<|start_of_role|>system<|end_of_role|>' }}{{- '<tool_call>\n{"name": "' }}`
	if f, ok := ForModel("granite", v41).(granite4); !ok || f.defaultSystem != "" {
		t.Errorf("4.1 のテンプレート: %#v", f)
	}
	v40 := v41 + `{%- set g4_default_system_message = '` + granite4DefaultSystem + `' %}`
	if f, ok := ForModel("granite", v40).(granite4); !ok || f.defaultSystem != granite4DefaultSystem {
		t.Errorf("4.0 のテンプレート: %#v", f)
	}
	for name, tmpl := range map[string]string{
		"3.x":         `{{- '<|start_of_role|>available_tools<|end_of_role|>' }}{{- '<|tool_call|>' }}`,
		"4.2(ChatML)": `{{- '<|im_start|>system\n' }}{{- '<tool_call>\n<function=' }}`,
		"テンプレートが無い":   "",
	} {
		if f := ForModel("granite", tmpl); f != nil {
			t.Errorf("%s: 選ばないはず(%s)", name, f.Name())
		}
	}
	if Thinks(granite4{}) {
		t.Error("Granite 4 は思考しない書式")
	}
	if s := Supported(); !strings.Contains(s, "Granite 4(granite。テンプレートに <|start_of_role|> と <tool_call> があるもの)") {
		t.Errorf("対応している書式の文: %s", s)
	}
}

// granite4Parse は pieces を順に届いたものとして Granite 4 の読み取りに通す。
func granite4Parse(tools []tool.Def, pieces ...string) (content string, out Output, stopped bool) {
	q := granite4{}.NewParser(Prompt{Tools: tools})
	for _, p := range pieces {
		th, c := q.Push(p)
		if th != "" {
			panic("思考は出ない: " + th)
		}
		content += c
		stopped = stopped || q.Stop()
	}
	_, c := q.Flush()
	return content + c, q.Result(), stopped
}

// 呼び出しの無い返事は全部が答え(思考は無い)。文の中・コードブロックの中の <tool_call> は呼び出しではない
func TestGranite4ParserContent(t *testing.T) {
	for _, text := range []string{
		"東京は晴れです。",
		"<think>は書式の文字ではないのでそのまま",
		"書き方は `<tool_call>` で囲みます。",
		"例:\n```\n<tool_call>\n{\"name\": \"x\", \"arguments\": {}}\n</tool_call>\n```\n以上です。",
	} {
		for _, pieces := range [][]string{{text}, runes(text)} {
			ct, out, _ := granite4Parse(nil, pieces...)
			if ct != text || out.Content != text || len(out.ToolCalls) != 0 {
				t.Errorf("%q: 画面 %q / 結果 %+v", text, ct, out)
			}
		}
	}
}

// 答えの後ろの呼び出しは画面に出さず、引数は定義の型を見て tool.Arg の形にする。2つ並べてもよい
func TestGranite4ParserCalls(t *testing.T) {
	tools := []tool.Def{{Name: "read_file", Parameters: []byte(`{"type": "object", "properties": {"path": {"type": "string"}, "offset": {"type": "integer"}, "all": {"type": "boolean"}}}`)}}
	text := "読みます。\n<tool_call>\n{\"name\": \"read_file\", \"arguments\": {\"path\": \"a.go\", \"offset\": 12, \"all\": true}}\n</tool_call>\n" +
		"<tool_call>\n{\"name\": \"get_current_time\", \"arguments\": {}}\n</tool_call>"
	want := []ToolCall{
		{Call: tool.Call{Name: "read_file", Args: []tool.Arg{{Name: "path", Value: "a.go"}, {Name: "offset", Value: "12"}, {Name: "all", Value: "true"}}}},
		{Call: tool.Call{Name: "get_current_time", Args: []tool.Arg{}}},
	}
	for _, pieces := range [][]string{{text}, runes(text)} {
		ct, out, _ := granite4Parse(tools, pieces...)
		if ct != "読みます。\n" || out.Content != "読みます。" || !reflect.DeepEqual(out.ToolCalls, want) {
			t.Errorf("画面 %q / 結果 %+v", ct, out)
		}
	}
}

// 読み取った呼び出しを書き戻すと、モデルが書いた文字と同じになる(次の送信で KV キャッシュを使い回せる)
func TestGranite4CallRoundTrip(t *testing.T) {
	tools := []tool.Def{{Name: "read_file", Parameters: []byte(`{"type": "object", "properties": {"path": {"type": "string"}, "offset": {"type": "integer"}, "paths": {"type": "array"}, "all": {"type": "boolean"}}}`)}}
	reply := "読みます。\n<tool_call>\n{\"name\": \"read_file\", \"arguments\": {\"path\": \"日本語 <a&b>.go\", \"offset\": 12, \"paths\": [\"b.go\", \"c.go\"], \"all\": false}}\n</tool_call>"
	_, out, _ := granite4Parse(tools, reply)
	p := Prompt{Tools: tools, Messages: []Message{{Role: "user", Content: "q"}, {Role: "assistant", Content: out.Content, ToolCalls: out.ToolCalls}}}
	if got := (granite4{}).BuildPrompt(p); !strings.Contains(got, "<|start_of_role|>assistant<|end_of_role|>"+reply+"<|end_of_text|>\n") {
		t.Errorf("書き戻すと変わる:\n%s", got)
	}
}

// 呼び出しの後ろでモデルが結果を書き始めたら止め、書きかけの結果は捨てる
func TestGranite4ParserStopsAtToolResponse(t *testing.T) {
	text := "<tool_call>\n{\"name\": \"calculate\", \"arguments\": {\"expression\": \"1+1\"}}\n</tool_call>\n<tool_response>\n2"
	_, out, stopped := granite4Parse(nil, runes(text)...)
	if !stopped || len(out.ToolCalls) != 1 || out.ToolCalls[0].Name != "calculate" || out.ToolCalls[0].Raw != "" {
		t.Errorf("止まった %v / 結果 %+v", stopped, out)
	}
}

// 崩れた呼び出しは、モデルが書いたままと理由・正しい書き方を持つ(実行せずにエラーとして返す)。書き戻すと書いたままになる
func TestGranite4ParserBrokenCalls(t *testing.T) {
	for reason, text := range map[string]string{
		"JSON として読めません":         "<tool_call>\n{\"name\": \"calculate\", \"arguments\": {\"expression\": }\n</tool_call>",
		"\"name\" がありません":       "<tool_call>\n{\"arguments\": {}}\n</tool_call>",
		"JSON のオブジェクトではありません":   "<tool_call>\n{\"name\": \"calculate\", \"arguments\": [1]}\n</tool_call>",
		"</tool_call> で閉じていません": "<tool_call>\n{\"name\": \"calculate\", \"arguments\": {\"expression\": \"1+1\"}}",
	} {
		_, out, _ := granite4Parse(nil, text)
		if len(out.ToolCalls) != 1 || out.ToolCalls[0].Raw != text || !strings.Contains(out.ToolCalls[0].Error, reason) ||
			!strings.Contains(out.ToolCalls[0].Error, granite4CallHelp) {
			t.Errorf("%s: %+v", reason, out.ToolCalls)
			continue
		}
		p := Prompt{Messages: []Message{{Role: "user", Content: "q"}, {Role: "assistant", ToolCalls: out.ToolCalls}, {Role: "tool", Content: "エラー"}}}
		if got := (granite4{}).BuildPrompt(p); !strings.Contains(got, "<|end_of_role|>"+text+"<|end_of_text|>\n") {
			t.Errorf("%s: 書いたままに戻らない:\n%s", reason, got)
		}
	}
}
