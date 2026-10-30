package format

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/secondarykey/aibow/tool"
)

// parse は pieces を順に届いたものとして Qwen3.5 の読み取りに通す(ツールの定義なし)。
// 返すのは、生成中に画面へ出した思考と答え(streamed)と、終わったあとの結果(Result)。
func parse(think bool, pieces ...string) (thinking, content string, out Output, stopped bool) {
	return parseWith(nil, think, pieces...)
}

// parseWith は parse の、モデルに知らせたツールの定義があるとき(引数の値を定義の型を見て tool.Arg の形にする)。
func parseWith(tools []tool.Def, think bool, pieces ...string) (thinking, content string, out Output, stopped bool) {
	p := (qwen35{}).NewParser(think, tools)
	for _, piece := range pieces {
		th, c := p.Push(piece)
		thinking += th
		content += c
		if p.Stop() {
			stopped = true
			break
		}
	}
	th, c := p.Flush()
	return thinking + th, content + c, p.Result(), stopped
}

// runes は文字列を1文字ずつに分ける(1トークンずつ届く様子のまね。タグがどこで分かれても読めるかを見る)。
func runes(s string) []string {
	var out []string
	for _, r := range s {
		out = append(out, string(r))
	}
	return out
}

func TestQwen35ParserSplitsThinking(t *testing.T) {
	cases := []struct {
		name         string
		think        bool
		pieces       []string
		wantThinking string
		wantContent  string
	}{
		{"タグが1回で届く", true, []string{"考え中", "</think>", "\n\n", "答え"}, "考え中", "答え"},
		{"タグが分かれて届く", true, []string{"考え", "中</th", "ink>\n\n答", "え"}, "考え中", "答え"},
		{"タグに似た文字列", true, []string{"a</b", "x</think>答え"}, "a</bx", "答え"},
		{"思考なし", false, []string{"答", "え"}, "", "答え"},
		{"思考の途中で終了", true, []string{"考え", "中</thi"}, "考え中</thi", ""},
		{"<tool_call> に似た文字列", false, []string{"a <tool", "_cal と <tool_call"}, "", "a <tool_cal と <tool_call"},
	}
	for _, c := range cases {
		th, ct, out, _ := parse(c.think, c.pieces...)
		if th != c.wantThinking || ct != c.wantContent {
			t.Errorf("%s: got (%q, %q), want (%q, %q)", c.name, th, ct, c.wantThinking, c.wantContent)
		}
		if out.Thinking != th || out.Content != ct || len(out.ToolCalls) != 0 {
			t.Errorf("%s: Result が画面に出した内容と違います: %+v", c.name, out)
		}
	}
}

// 答えの後ろの呼び出し(複数・複数行の値)を読み取り、呼び出しの部分は画面に出さないこと
func TestQwen35ParserReadsToolCalls(t *testing.T) {
	text := "時刻を見てから計算する\n</think>\n\n調べます。\n\n" +
		"<tool_call>\n<function=get_current_time>\n</function>\n</tool_call>\n" +
		"<tool_call>\n<function=calculate>\n<parameter=expression>\n(1 +\n  2) * 3\n</parameter>\n" +
		"<parameter=note>\n\n</parameter>\n</function>\n</tool_call>"
	want := []ToolCall{
		toolCall("get_current_time"),
		toolCall("calculate", "expression", "(1 +\n  2) * 3", "note", ""),
	}
	// まとめて届いても、1文字ずつ届いても同じ
	for _, pieces := range [][]string{{text}, runes(text)} {
		th, ct, out, stopped := parse(true, pieces...)
		if th != "時刻を見てから計算する\n" || out.Thinking != th {
			t.Errorf("思考: %q", th)
		}
		if strings.Contains(ct, "<tool") || strings.TrimSpace(ct) != "調べます。" || out.Content != "調べます。" {
			t.Errorf("答え: 画面 %q / 結果 %q", ct, out.Content)
		}
		if !reflect.DeepEqual(out.ToolCalls, want) || stopped {
			t.Errorf("呼び出し: %+v(止めた: %v)", out.ToolCalls, stopped)
		}
	}
}

// 文の中・コードブロックの中に書かれた <tool_call> は呼び出しと見ず、答えの文字のまま出すこと。
// 仕組みを説明する答えで `<tool_call>` と書かれ、そこから後ろが「読み取れない呼び出し」になって、
// モデルが同じ答えを書き直し続けたことがある(2026-10-16)
func TestQwen35ParserIgnoresToolCallInText(t *testing.T) {
	cases := []struct{ name, text string }{
		{"文の中(インラインのコード)", "## aibow\n\n- **ツール呼び出し**: エージェントループの実装(`<tool_call>` を読み取る)\n\n" +
			"### ロードマップ\n| ステップ | 完了度 |\n|---|---|\n| 1. チャット | 完了 |"},
		{"文の中(囲みなし)", "モデルは <tool_call> と書いてツールを呼ぶ。"},
		{"コードブロックの中", "書き方:\n\n```\n<tool_call>\n<function=calculate>\n</function>\n</tool_call>\n```\n\n以上です。"},
		{"~~~ のコードブロックの中", "~~~xml\n<tool_call>\n~~~\n"},
	}
	for _, c := range cases {
		for _, pieces := range [][]string{{c.text}, runes(c.text)} {
			_, ct, out, stopped := parse(false, pieces...)
			if ct != c.text || out.Content != c.text || len(out.ToolCalls) != 0 || stopped {
				t.Errorf("%s: 画面 %q / 結果 %+v(止めた: %v)", c.name, ct, out, stopped)
			}
		}
	}

	// 文の中で触れたあとに、行の頭から書いた本物の呼び出しは読み取る
	text := "`<tool_call>` で呼びます。計算します。\n\n<tool_call>\n<function=calculate>\n<parameter=expression>\n1+1\n</parameter>\n</function>\n</tool_call>"
	for _, pieces := range [][]string{{text}, runes(text)} {
		_, ct, out, _ := parse(false, pieces...)
		want := []ToolCall{calc("1+1")}
		if out.Content != "`<tool_call>` で呼びます。計算します。" || !reflect.DeepEqual(out.ToolCalls, want) || strings.Contains(ct, "<function") {
			t.Errorf("触れたあとの呼び出し: 画面 %q / 結果 %+v", ct, out)
		}
	}
}

// プロンプトに書き戻した呼び出しを読むと、元と同じになること(書き方と読み方が対になっている)
func TestQwen35CallsRoundTrip(t *testing.T) {
	calls := []ToolCall{
		toolCall("calculate", "expression", "1+2"),
		toolCall("write_file", "path", "a.go", "content", "package a\n\n\tfunc f() {}\n"),
	}
	var b strings.Builder
	writeQwen35Calls(&b, calls, true, nil)
	_, _, out, _ := parse(false, "答え"+b.String())
	if !reflect.DeepEqual(out.ToolCalls, calls) || out.Content != "答え" {
		t.Errorf("読み戻した呼び出し: %+v / 答え %q", out.ToolCalls, out.Content)
	}
}

// typedTool は、文字列でない引数を持つツールの定義(引数の値の持ち方を確かめる)。
var typedTool = tool.Def{Name: "read_file", Parameters: []byte(`{"type": "object", "properties": {
	"path": {"type": "string"}, "offset": {"type": "integer"}, "ok": {"type": "boolean"}, "paths": {"type": "array"}}}`)}

// Qwen3.5 は値をいつも文字で書く。型のある引数は、型に合う文字ならそのまま JSON の値として持ち、
// 合わない文字(" 5"・True・5.0)は書いた文字を JSON の文字列にして持つ。どちらも書き戻すと書いた文字に戻る(0026 の決定事項 1)
func TestQwen35TypedArgs(t *testing.T) {
	text := "<tool_call>\n<function=read_file>\n" +
		"<parameter=path>\n5\n</parameter>\n" + // 文字列の引数は、数に見えても中身のまま
		"<parameter=offset>\n12\n</parameter>\n" +
		"<parameter=ok>\nTrue\n</parameter>\n" +
		"<parameter=paths>\n[\"a.go\", \"b.go\"]\n</parameter>\n" +
		"<parameter=offset>\n 5\n</parameter>\n" +
		"</function>\n</tool_call>"
	_, _, out, _ := parseWith([]tool.Def{typedTool}, false, text)
	want := toolCall("read_file", "path", "5", "offset", "12", "ok", `"True"`, "paths", `["a.go", "b.go"]`, "offset", `" 5"`)
	if len(out.ToolCalls) != 1 || !reflect.DeepEqual(out.ToolCalls[0], want) {
		t.Fatalf("読み取った引数: %+v", out.ToolCalls)
	}

	var b strings.Builder
	writeQwen35Calls(&b, out.ToolCalls, false, []tool.Def{typedTool})
	if b.String() != text {
		t.Errorf("書き戻すと書いた文字に戻る:\n%s", b.String())
	}

	// ツールの定義が無い(知らないツール)ときは、どの引数も文字列として持つ
	_, _, out, _ = parse(false, text)
	if got := out.ToolCalls[0].Args[2].Value; got != "True" {
		t.Errorf("定義が無いときの値: %q", got)
	}
}

// 呼び出しの後ろでモデルが結果を自分で書き始めたら止め、その部分は捨てること
func TestQwen35ParserStopsAtToolResponse(t *testing.T) {
	text := "<tool_call>\n<function=calculate>\n<parameter=expression>\n7/2\n</parameter>\n</function>\n</tool_call>\n" +
		"<tool_response>\n3.5\n</tool_response>\n答えは 3.5 です"
	_, ct, out, stopped := parse(false, runes(text)...)
	if !stopped {
		t.Fatal("<tool_response> で止まっていません")
	}
	if ct != "" || len(out.ToolCalls) != 1 || out.ToolCalls[0].Error != "" || out.ToolCalls[0].Args[0].Value != "7/2" {
		t.Errorf("答え %q / 呼び出し %+v", ct, out.ToolCalls)
	}
}

// 書式が崩れた呼び出しは、エラーと生の文字列を持たせる(実行はしない。モデルに立て直させる)
func TestQwen35ParserBrokenCalls(t *testing.T) {
	cases := []struct {
		name, text, wantName, wantRaw string
	}{
		{"Qwen3 の JSON の書き方", "<tool_call>\n{\"name\": \"calculate\", \"arguments\": {\"expression\": \"1+1\"}}\n</tool_call>", "",
			"<tool_call>\n{\"name\": \"calculate\", \"arguments\": {\"expression\": \"1+1\"}}\n</tool_call>"},
		{"閉じないまま終わった", "<tool_call>\n<function=calculate>\n<parameter=expression>\n1+\n", "",
			"<tool_call>\n<function=calculate>\n<parameter=expression>\n1+"},
		{"</function> が無い", "<tool_call>\n<function=calculate>\n<parameter=expression>\n1\n</parameter>\n</tool_call>", "calculate",
			"<tool_call>\n<function=calculate>\n<parameter=expression>\n1\n</parameter>\n</tool_call>"},
		{"</parameter> が無い", "<tool_call>\n<function=calculate>\n<parameter=expression>\n1\n</function>\n</tool_call>", "calculate",
			"<tool_call>\n<function=calculate>\n<parameter=expression>\n1\n</function>\n</tool_call>"},
		{"関数名が空", "<tool_call>\n<function=>\n</function>\n</tool_call>", "", "<tool_call>\n<function=>\n</function>\n</tool_call>"},
	}
	for _, c := range cases {
		_, _, out, _ := parse(false, c.text)
		if len(out.ToolCalls) != 1 {
			t.Errorf("%s: 呼び出しの数 %+v", c.name, out.ToolCalls)
			continue
		}
		got := out.ToolCalls[0]
		if got.Error == "" || got.Raw != c.wantRaw || got.Name != c.wantName || !strings.Contains(got.Error, "正しい書式") {
			t.Errorf("%s: %+v", c.name, got)
		}
	}
}

// templateCases は testdata/cases.json(書式をまたいで共通の会話)。期待値(<書式の名前>/<name>.txt)は、
// 同じ会話を GGUF の中のテンプレートに jinja2 で通して作ったもの(testdata/gen_expected.py)。
type templateCases struct {
	Tools []struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"tools"`
	Cases []struct {
		Name     string   `json:"name"`
		Formats  []string `json:"formats"` // 空なら全部の書式
		Think    bool     `json:"think"`
		Tools    []string `json:"tools"` // 知らせるツールの名前
		Messages []struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			Thinking  string `json:"thinking"`
			ToolCalls []struct {
				Name string      `json:"name"`
				Args [][2]string `json:"args"`
			} `json:"tool_calls"`
		} `json:"messages"`
	} `json:"cases"`
}

// 公式テンプレート(Jinja)と、空白や改行の1文字まで同じ文字列になること
func TestQwen35PromptMatchesTemplate(t *testing.T) {
	testPromptMatchesTemplate(t, "qwen35", qwen35{})
}

// testPromptMatchesTemplate は、testdata/cases.json の会話を書式 f で組み立て、testdata/<name>/ の期待値と比べる。
// 期待値の引数の値は tool.Arg の形で書いてある(書式が型を見て書き直す)。
func testPromptMatchesTemplate(t *testing.T, name string, f Format) {
	b, err := os.ReadFile("testdata/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var spec templateCases
	if err := json.Unmarshal(b, &spec); err != nil {
		t.Fatal(err)
	}

	for _, c := range spec.Cases {
		if len(c.Formats) > 0 && !slices.Contains(c.Formats, name) {
			continue
		}
		p := Prompt{Think: c.Think}
		for _, td := range spec.Tools {
			if slices.Contains(c.Tools, td.Name) {
				p.Tools = append(p.Tools, tool.Def{Name: td.Name, Description: td.Description, Parameters: td.Parameters})
			}
		}
		for _, m := range c.Messages {
			msg := Message{Role: m.Role, Content: m.Content, Thinking: m.Thinking}
			for _, tc := range m.ToolCalls {
				call := ToolCall{Call: tool.Call{Name: tc.Name}}
				for _, a := range tc.Args {
					call.Args = append(call.Args, tool.Arg{Name: a[0], Value: a[1]})
				}
				msg.ToolCalls = append(msg.ToolCalls, call)
			}
			p.Messages = append(p.Messages, msg)
		}

		want, err := os.ReadFile(filepath.Join("testdata", name, c.Name+".txt"))
		if err != nil {
			t.Fatal(err)
		}
		if got := f.BuildPrompt(p); got != string(want) {
			t.Errorf("%s: テンプレートと違います\n--- got\n%s\n--- want\n%s", c.Name, got, want)
		}
	}
}

// 読み取れなかった呼び出しは、モデルが書いたままプロンプトに戻すこと
func TestQwen35PromptKeepsBrokenCallAsWritten(t *testing.T) {
	raw := "<tool_call>\n{\"name\": \"calculate\"}\n</tool_call>"
	p := Prompt{Messages: []Message{
		{Role: "user", Content: "q"},
		{Role: "assistant", Content: "a", ToolCalls: []ToolCall{{Raw: raw, Error: "x"}}},
		{Role: "tool", Content: "エラー: x"},
	}}
	if got := (qwen35{}).BuildPrompt(p); !strings.Contains(got, "a\n\n"+raw+"<|im_end|>\n<|im_start|>user\n<tool_response>\nエラー: x\n</tool_response>") {
		t.Errorf("崩れた呼び出しの書き戻し:\n%s", got)
	}
}

// 組み込みのツールとファイルのツールの定義が、モデルに見せる形(Python の json.dumps と同じ見た目)で書けること
func TestBuiltinToolDefsAreValidJSON(t *testing.T) {
	for _, tl := range append(tool.Builtin(), tool.Files("dir")...) {
		d := tl.Def()
		if got := pyJSON(d.Parameters); got != string(d.Parameters) {
			t.Errorf("%s の JSON Schema は、書き直さなくてもモデルに見せる形にしておく:\n%s\n%s", d.Name, d.Parameters, got)
		}
	}
}

// Python の json.dumps(ensure_ascii=False)と同じ見た目になること
func TestPyJSON(t *testing.T) {
	cases := map[string]string{
		`{"b":1,"a":[1.0,true,null,"x<y&z"],"c":{}}`: `{"b": 1, "a": [1.0, true, null, "x<y&z"], "c": {}}`,
		`{"s":"日本語\n\"q\""}`:                         `{"s": "日本語\n\"q\""}`,
		`[]`:                                         `[]`,
	}
	for in, want := range cases {
		if got := pyJSON([]byte(in)); got != want {
			t.Errorf("pyJSON(%s) = %s, want %s", in, got, want)
		}
	}
}
