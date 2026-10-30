package format

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/secondarykey/aibow/tool"
)

// ToolCall の JSON が、tool パッケージに切り出す前と同じ平らな形であること。
// 保存済みの会話(messages の tool_calls / tool_result)をそのまま読めるようにするため
func TestToolCallJSONIsFlat(t *testing.T) {
	cases := map[string]ToolCall{
		`{"name":"calculate","args":[{"name":"expression","value":"7/2"}]}`: calc("7/2"),
		`{"name":"","args":null,"raw":"calculate(1+1)","error":"崩れています"}`:   {Raw: "calculate(1+1)", Error: "崩れています"},
	}
	for want, c := range cases {
		b, err := json.Marshal(c)
		if err != nil || string(b) != want {
			t.Errorf("書き出し: %s, %v(期待: %s)", b, err, want)
		}
		var back ToolCall
		if err := json.Unmarshal([]byte(want), &back); err != nil || !reflect.DeepEqual(back, c) {
			t.Errorf("読み込み: %+v, %v(期待: %+v)", back, err, c)
		}
	}
	// tool.Call だけを読む側(ステップ4・5 の MCP など)からも、名前と引数が読めること
	var call tool.Call
	if err := json.Unmarshal([]byte(`{"name":"calculate","args":[],"raw":"x","error":"y"}`), &call); err != nil || call.Name != "calculate" {
		t.Errorf("tool.Call として: %+v, %v", call, err)
	}
}

// toolCall はモデルが書いた(書式から読み取った)呼び出しを作る。kv は引数の名前と値を交互に並べる。
func toolCall(name string, kv ...string) ToolCall {
	args := []tool.Arg{}
	for i := 0; i+1 < len(kv); i += 2 {
		args = append(args, tool.Arg{Name: kv[i], Value: kv[i+1]})
	}
	return ToolCall{Call: tool.Call{Name: name, Args: args}}
}

func calc(expr string) ToolCall {
	return toolCall("calculate", "expression", expr)
}

// 書式に未対応のエラーは errors.Is で見分けられる(メーターを出さないだけにするため)
func TestUnsupportedIs(t *testing.T) {
	err := fmt.Errorf("数えられません: %w", Unsupported("gemma4"))
	if !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), "gemma4") {
		t.Errorf("見分けられない: %v", err)
	}
}

// 種類の名前だけで決まらない書式は、チャットテンプレートの印で選ぶ。印が無ければ選ばない(0027 の決定事項 2)
func TestForModelTemplateMark(t *testing.T) {
	saved := formats
	t.Cleanup(func() { formats = saved })
	formats = append(slices.Clone(saved), formatEntry{arch: "testllama", mark: "<|start_header_id|>", make: func(string) Format { return gemma4{} }})

	if f := ForModel("testllama", "{{ '<|start_header_id|>' + role }}"); f == nil {
		t.Error("印のあるテンプレートなら選ぶ")
	}
	for name, tmpl := range map[string]string{"ChatML(Hermes 3 など)": "{{ '<|im_start|>' + role }}", "テンプレートが無い": ""} {
		if f := ForModel("testllama", tmpl); f != nil {
			t.Errorf("%s: 選ばないはず(%s)", name, f.Name())
		}
	}
	if f := ForModel("qwen35", ""); f == nil || f.Name() != "Qwen3.5" {
		t.Error("印の要らない書式は、テンプレートによらず選ぶ")
	}
	if s := Supported(); !strings.Contains(s, "testllama。テンプレートに <|start_header_id|> があるもの") {
		t.Errorf("対応している書式の文: %s", s)
	}
}
