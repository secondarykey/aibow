package format

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/secondarykey/aibow/tool"
)

// GGUF の中のテンプレート(Jinja)と、空白や改行の1文字まで同じ文字列になること(bos は除く。日付は期待値のスクリプトと同じ 2026-10-30)
func TestLlama3PromptMatchesTemplate(t *testing.T) {
	saved := llama3Now
	t.Cleanup(func() { llama3Now = saved })
	llama3Now = func() time.Time { return time.Date(2026, 10, 30, 9, 0, 0, 0, time.Local) }
	testPromptMatchesTemplate(t, "llama3", llama3{dated: true})
}

// 書式はテンプレートの印で選び、日付の決め方もテンプレートで決まる(3.2 は今日・3.1 は 26 Jul 2024。0027 の決定事項 2・3)
func TestLlama3ChosenByTemplate(t *testing.T) {
	if f := ForModel("llama", `{{- '<|start_header_id|>' }}{%- set date_string = strftime_now("%d %b %Y") %}`); f == nil || !f.(llama3).dated {
		t.Errorf("3.2 のテンプレート: %#v", f)
	}
	f := ForModel("llama", `{{- '<|start_header_id|>' }}{%- set date_string = "26 Jul 2024" %}`)
	if f == nil || f.(llama3).dated || !strings.Contains(f.BuildPrompt(Prompt{}), "Today Date: 26 Jul 2024\n") {
		t.Errorf("3.1 のテンプレート: %#v", f)
	}
	if ForModel("llama", "{{ '<|im_start|>' }}") != nil {
		t.Error("ChatML のテンプレート(Hermes 3 など)は選ばない")
	}
	if Thinks(f) || !Thinks(qwen35{}) || !Thinks(gemma4{}) {
		t.Error("Llama 3 は思考しない書式")
	}
}

// llama3Parse は pieces を順に届いたものとして Llama 3 の読み取りに通す。
func llama3Parse(tools []tool.Def, pieces ...string) (content string, out Output) {
	q := llama3{}.NewParser(Prompt{Tools: tools})
	for _, p := range pieces {
		_, c := q.Push(p)
		content += c
	}
	_, c := q.Flush()
	return content + c, q.Result()
}

func TestLlama3ParserContent(t *testing.T) {
	for _, text := range []string{"\n\n東京は晴れです。", "```json\n{\"name\": \"x\"}\n```", `{"city": "東京"} のように書きます`} {
		for _, pieces := range [][]string{{text}, runes(text)} {
			ct, out := llama3Parse(nil, pieces...)
			want := strings.TrimSpace(text)
			if ct != want || out.Content != want || len(out.ToolCalls) != 0 {
				t.Errorf("%q: 画面 %q / 結果 %+v", text, ct, out)
			}
		}
	}
}

// 返事の全体が JSON なら呼び出し。画面には出さない。引数は定義の型を見て tool.Arg の形にする
func TestLlama3ParserCalls(t *testing.T) {
	cases := []struct {
		name, text string
		want       []ToolCall
	}{
		{"parameters", `{"name": "read_file", "parameters": {"path": "a.go", "offset": 12, "ok": true}}`,
			[]ToolCall{toolCall("read_file", "path", "a.go", "offset", "12", "ok", "true")}},
		{"arguments と python_tag", "<|python_tag|>{\"name\": \"calculate\", \"arguments\": {\"expression\": \"1+1\"}}",
			[]ToolCall{toolCall("calculate", "expression", "1+1")}},
		{"2つ(; 区切り)", `{"name": "calculate", "parameters": {"expression": "1+1"}}; {"name": "get_current_time", "parameters": {}}`,
			[]ToolCall{toolCall("calculate", "expression", "1+1"), toolCall("get_current_time")}},
		{"型に合わない値", `{"name": "read_file", "parameters": {"path": "a.go", "offset": "5"}}`,
			[]ToolCall{toolCall("read_file", "path", "a.go", "offset", `"5"`)}},
	}
	tools := []tool.Def{typedTool, {Name: "calculate", Parameters: []byte(`{"type": "object", "properties": {"expression": {"type": "string"}}}`)},
		{Name: "get_current_time", Parameters: []byte(`{"type": "object", "properties": {}}`)}}
	for _, c := range cases {
		for _, pieces := range [][]string{{c.text}, runes(c.text)} {
			ct, out := llama3Parse(tools, pieces...)
			if ct != "" || out.Content != "" || !reflect.DeepEqual(out.ToolCalls, c.want) {
				t.Errorf("%s: 画面 %q / 結果 %+v", c.name, ct, out.ToolCalls)
			}
		}
	}
}

// 呼び出しのつもりで崩れた JSON は、エラーと生の文字列を持たせる
func TestLlama3ParserBrokenCall(t *testing.T) {
	text := `{"name": "calculate", "parameters": {"expression": "1+1"`
	_, out := llama3Parse(nil, text)
	if len(out.ToolCalls) != 1 || out.ToolCalls[0].Raw != text || !strings.Contains(out.ToolCalls[0].Error, "正しい書式") {
		t.Errorf("%+v", out)
	}
}

// 書いた呼び出しを読むと、元と同じになる(書き方と読み方が対になっている)
func TestLlama3CallsRoundTrip(t *testing.T) {
	calls := []ToolCall{
		toolCall("read_file", "path", "a <b> & \"c\"", "offset", "12", "paths", `["x", "y"]`, "ok", "false"),
		toolCall("read_file", "offset", `" 5"`, "path", "日本語\n2行目"),
	}
	for _, c := range calls {
		var b strings.Builder
		writeLlama3Call(&b, c, []tool.Def{typedTool})
		if _, out := llama3Parse([]tool.Def{typedTool}, b.String()); !reflect.DeepEqual(out.ToolCalls, []ToolCall{c}) {
			t.Errorf("読み戻した呼び出し:\n%+v\n%s", out.ToolCalls, b.String())
		}
	}
}

// Python の json.dumps(indent=4) と同じ見た目になること
func TestPyJSONIndent(t *testing.T) {
	in := `{"a": [], "b": {}, "c": [1, 1.0, "x<y"], "d": {"e": null, "f": true}}`
	want := "{\n    \"a\": [],\n    \"b\": {},\n    \"c\": [\n        1,\n        1.0,\n        \"x<y\"\n    ],\n    \"d\": {\n        \"e\": null,\n        \"f\": true\n    }\n}"
	if got := pyJSONIndent([]byte(in), 4); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}
