package aibow

import (
	"encoding/json"
	"reflect"
	"testing"

	"aibow/tool"
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
