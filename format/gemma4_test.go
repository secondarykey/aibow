package format

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/secondarykey/aibow/tool"
)

// GGUF の中のテンプレート(Jinja)と、空白や改行の1文字まで同じ文字列になること(bos は除く。0026 の決定事項 25)
func TestGemma4PromptMatchesTemplate(t *testing.T) {
	testPromptMatchesTemplate(t, "gemma4", gemma4{})
}

// gemma4Parse は pieces を順に届いたものとして Gemma 4 の読み取りに通す。p は生成のときに渡したプロンプト。
func gemma4Parse(p Prompt, pieces ...string) (thinking, content string, out Output, stopped bool) {
	q := gemma4{}.NewParser(p)
	for _, piece := range pieces {
		th, c := q.Push(piece)
		thinking += th
		content += c
		if q.Stop() {
			stopped = true
			break
		}
	}
	th, c := q.Flush()
	return thinking + th, content + c, q.Result(), stopped
}

var gemma4Tools = []tool.Def{typedTool, {Name: "calculate", Parameters: []byte(`{"type": "object", "properties": {"expression": {"type": "string"}}}`)}}

// 思考(<|channel>thought … <channel|>)と答えを分け、呼び出しの部分は画面に出さないこと。<|tool_response> で止める
func TestGemma4ParserThinkingAndCalls(t *testing.T) {
	text := "<|channel>thought\n計算しよう\n<channel|>計算します。" +
		`<|tool_call>call:calculate{expression:<|"|>1234 * 5678<|"|>}<tool_call|>` +
		`<|tool_call>call:read_file{offset:12,ok:true,path:<|"|>a.go<|"|>,paths:[<|"|>b.go<|"|>,<|"|>c, d<|"|>]}<tool_call|>` +
		"<|tool_response>response:…"
	want := []ToolCall{
		toolCall("calculate", "expression", "1234 * 5678"),
		toolCall("read_file", "offset", "12", "ok", "true", "path", "a.go", "paths", `["b.go", "c, d"]`),
	}
	p := Prompt{Think: true, Tools: gemma4Tools, Messages: []Message{{Role: "user", Content: "q"}}}
	for _, pieces := range [][]string{{text}, runes(text)} {
		th, ct, out, stopped := gemma4Parse(p, pieces...)
		if th != "計算しよう\n" || out.Thinking != "計算しよう" {
			t.Errorf("思考: 画面 %q / 結果 %q", th, out.Thinking)
		}
		if ct != "計算します。" || out.Content != "計算します。" {
			t.Errorf("答え: 画面 %q / 結果 %q", ct, out.Content)
		}
		if !reflect.DeepEqual(out.ToolCalls, want) || !stopped {
			t.Errorf("呼び出し: %+v(止めた: %v)", out.ToolCalls, stopped)
		}
	}
}

// ツールの結果の後ろで思考 ON なら、プロンプトが <|channel>thought まで書いてあるので、思考の途中から読む
func TestGemma4ParserStartsInThoughtAfterToolResponse(t *testing.T) {
	p := Prompt{Think: true, Messages: []Message{
		{Role: "user", Content: "q"},
		{Role: "assistant", ToolCalls: []ToolCall{toolCall("calculate", "expression", "1+1")}},
		{Role: "tool", Content: "2", Name: "calculate"},
	}}
	if got := (gemma4{}).BuildPrompt(p); !strings.HasSuffix(got, "<tool_response|><|channel>thought\n") {
		t.Fatalf("プロンプトの終わり: %q", got)
	}
	th, ct, out, _ := gemma4Parse(p, runes("答えは 2\n<channel|>2 です")...)
	if th != "答えは 2\n" || ct != "2 です" || out.Thinking != "答えは 2" || out.Content != "2 です" {
		t.Errorf("思考 %q / 答え %q / 結果 %+v", th, ct, out)
	}
	// 思考 OFF なら答えから
	p.Think = false
	_, ct, _, _ = gemma4Parse(p, "2 です")
	if ct != "2 です" {
		t.Errorf("思考 OFF: %q", ct)
	}
}

// 生成した呼び出しを書き戻して読むと、元と同じになること(書き方と読み方が対になっている。キーは名前の順に並ぶ)
func TestGemma4CallsRoundTrip(t *testing.T) {
	calls := []ToolCall{
		toolCall("read_file", "offset", "12", "options", `{"n": 2, "sort": "名前"}`, "path", "a <b> & {c}", "paths", `["x", 1.5, null, true, [{"k": "v"}]]`),
		toolCall("calculate", "expression", "1 +\n2"),
		toolCall("read_file", "offset", `" 5"`, "path", "d.go"), // 型に合わない値は文字列で書かれ、読むと同じ形に戻る
	}
	var b strings.Builder
	writeGemma4Calls(&b, calls, gemma4Tools)
	_, _, out, _ := gemma4Parse(Prompt{Tools: gemma4Tools}, b.String())
	if !reflect.DeepEqual(out.ToolCalls, calls) {
		t.Errorf("読み戻した呼び出し:\n%+v\n%s", out.ToolCalls, b.String())
	}
}

// コードブロックの中の <|tool_call> は呼び出しと見ない。文の途中の呼び出しは読む(Gemma 4 は行の頭とは限らない)
func TestGemma4ParserCodeFence(t *testing.T) {
	text := "書き方:\n\n```\n<|tool_call>call:calculate{}<tool_call|>\n```\n\n以上です。"
	_, ct, out, stopped := gemma4Parse(Prompt{}, runes(text)...)
	if ct != text || out.Content != text || len(out.ToolCalls) != 0 || stopped {
		t.Errorf("コードブロック: 画面 %q / 結果 %+v", ct, out)
	}
	_, ct, out, _ = gemma4Parse(Prompt{Tools: gemma4Tools}, `計算します<|tool_call>call:calculate{expression:<|"|>1+1<|"|>}<tool_call|>`)
	if ct != "計算します" || len(out.ToolCalls) != 1 || out.ToolCalls[0].Error != "" {
		t.Errorf("文の続きの呼び出し: 画面 %q / 結果 %+v", ct, out)
	}
}

// 書式が崩れた呼び出しは、エラーと生の文字列を持たせる
func TestGemma4ParserBrokenCalls(t *testing.T) {
	cases := []struct{ name, text, wantName string }{
		{"閉じていない", `<|tool_call>call:calculate{expression:<|"|>1+`, ""},
		{"call: が無い", `<|tool_call>calculate{}<tool_call|>`, ""},
		{"{ が無い", `<|tool_call>call:calculate<tool_call|>`, ""},
		{"文字列が囲まれていない", `<|tool_call>call:calculate{expression:1+1}<tool_call|>`, "calculate"},
		{"文字列が閉じていない", `<|tool_call>call:calculate{expression:<|"|>1+1}<tool_call|>`, "calculate"},
		{"JSON で書いた", `<|tool_call>call:calculate{"expression": "1+1"}<tool_call|>`, "calculate"},
		{"} の後ろに文", `<|tool_call>call:calculate{} x<tool_call|>`, "calculate"},
	}
	for _, c := range cases {
		_, _, out, _ := gemma4Parse(Prompt{Tools: gemma4Tools}, c.text)
		if len(out.ToolCalls) != 1 {
			t.Errorf("%s: 呼び出しの数 %+v", c.name, out.ToolCalls)
			continue
		}
		got := out.ToolCalls[0]
		if got.Error == "" || got.Raw != c.text || got.Name != c.wantName || !strings.Contains(got.Error, "正しい書式") {
			t.Errorf("%s: %+v", c.name, got)
		}
	}
}

// ツールの説明の、テンプレートの癖も写すこと(enum・入れ子の配列・配列の type・properties の無い object・nullable)
func TestGemma4Declaration(t *testing.T) {
	d := tool.Def{Name: "f", Description: "d", Parameters: []byte(`{"type": "object", "properties": {
		"mode": {"type": "string", "enum": ["a", "b"]},
		"Zeta": {"type": "array", "items": {"type": "array", "items": {"type": "string"}}},
		"opt": {"type": ["string", "null"], "nullable": true},
		"o": {"type": "object", "properties": {"k": {"type": "integer"}}, "required": ["k"]}
	}}`)}
	want := `declaration:f{description:<|"|>d<|"|>,parameters:{properties:{` +
		`mode:{enum:[<|"|>a<|"|>,<|"|>b<|"|>],type:<|"|>STRING<|"|>},` +
		`o:{properties:{k:{type:<|"|>INTEGER<|"|>}},required:[<|"|>k<|"|>],type:<|"|>OBJECT<|"|>},` +
		`opt:{nullable:true,type:<|"|>['STRING', 'NULL']<|"|>},` +
		`Zeta:{items:{items:{<|"|>type<|"|>:<|"|>string<|"|>},type:<|"|>ARRAY<|"|>},type:<|"|>ARRAY<|"|>}` +
		`},type:<|"|>OBJECT<|"|>}}`
	if got := gemma4Declaration(d); got != want {
		t.Errorf("ツールの説明:\n got  %s\n want %s", got, want)
	}
}

// 数は Python の str() と同じ見た目で書く(テンプレートは json.loads した値を書く)
func TestPyNumber(t *testing.T) {
	cases := map[string]string{
		"12": "12", "-0": "0", "0.5": "0.5", "1.0": "1.0", "1e3": "1000.0", "1e16": "1e+16", "1.5e-5": "1.5e-05",
		"0.0001": "0.0001", "123456789012345678": "123456789012345678", "-2.50": "-2.5", "1E2": "100.0",
	}
	for in, want := range cases {
		if got := pyNumber(json.Number(in)); got != want {
			t.Errorf("pyNumber(%s) = %s, want %s", in, got, want)
		}
	}
}
