package aibow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func split(think bool, pieces ...string) (thinking, content string) {
	s := newThinkSplitter(think)
	for _, p := range pieces {
		th, c := s.Push(p)
		thinking += th
		content += c
	}
	th, c := s.Flush()
	return thinking + th, content + c
}

func TestThinkSplitter(t *testing.T) {
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
	}
	for _, c := range cases {
		th, ct := split(c.think, c.pieces...)
		if th != c.wantThinking || ct != c.wantContent {
			t.Errorf("%s: got (%q, %q), want (%q, %q)", c.name, th, ct, c.wantThinking, c.wantContent)
		}
	}
}

// templateCases は testdata/qwen35/cases.json。期待値(<name>.txt)は、同じ会話を公式テンプレートに
// jinja2 で通して作ったもの(testdata/qwen35/gen_expected.py)。
type templateCases struct {
	Tools []struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"tools"`
	Cases []struct {
		Name     string `json:"name"`
		Think    bool   `json:"think"`
		Tools    bool   `json:"tools"`
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
	b, err := os.ReadFile("testdata/qwen35/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var spec templateCases
	if err := json.Unmarshal(b, &spec); err != nil {
		t.Fatal(err)
	}
	var tools []ToolDef
	for _, td := range spec.Tools {
		tools = append(tools, ToolDef{Name: td.Name, Description: td.Description, Parameters: td.Parameters})
	}

	for _, c := range spec.Cases {
		p := Prompt{Think: c.Think}
		if c.Tools {
			p.Tools = tools
		}
		for _, m := range c.Messages {
			msg := Message{Role: m.Role, Content: m.Content, Thinking: m.Thinking}
			for _, tc := range m.ToolCalls {
				call := ToolCall{Name: tc.Name}
				for _, a := range tc.Args {
					call.Args = append(call.Args, ToolArg{Name: a[0], Value: a[1]})
				}
				msg.ToolCalls = append(msg.ToolCalls, call)
			}
			p.Messages = append(p.Messages, msg)
		}

		want, err := os.ReadFile(filepath.Join("testdata", "qwen35", c.Name+".txt"))
		if err != nil {
			t.Fatal(err)
		}
		if got := (qwen35{}).BuildPrompt(p); got != string(want) {
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
