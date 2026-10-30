package format

import (
	"reflect"
	"strings"
	"testing"

	"github.com/secondarykey/aibow/tool"
)

// GGUF の中のテンプレート(Jinja)と、空白や改行の1文字まで同じ文字列になること
func TestGranite42PromptMatchesTemplate(t *testing.T) {
	testPromptMatchesTemplate(t, "granite42", granite42{})
}

// 4.2 は ChatML の区切りと Qwen3.5 と同じ呼び出しの印で選ぶ。思考する書式(0029 の決定事項 7)
func TestGranite42ChosenByTemplate(t *testing.T) {
	const v42 = `{{- '<|im_start|>system\n' }}{{- '<tool_call>\n<function=' ~ tool_call.name }}`
	if _, ok := ForModel("granite", v42).(granite42); !ok {
		t.Errorf("4.2 のテンプレート: %#v", ForModel("granite", v42))
	}
	if ForModel("qwen35", v42).Name() != "Qwen3.5" {
		t.Error("qwen35 の種類は Qwen3.5 のまま")
	}
	if !Thinks(granite42{}) {
		t.Error("Granite 4.2 は思考する書式")
	}
}

// granite42Parse は pieces を順に届いたものとして Granite 4.2 の読み取りに通す。
func granite42Parse(think bool, tools []tool.Def, pieces ...string) (thinking, content string, out Output) {
	q := granite42{}.NewParser(Prompt{Think: think, Tools: tools})
	for _, p := range pieces {
		th, c := q.Push(p)
		thinking += th
		content += c
	}
	th, c := q.Flush()
	return thinking + th, content + c, q.Result()
}

// 思考・答え・呼び出しに振り分ける(Qwen3.5 と同じ形)。思考の後ろの改行1つは思考に入れない
func TestGranite42Parser(t *testing.T) {
	tools := []tool.Def{{Name: "read_file", Parameters: []byte(`{"type": "object", "properties": {"path": {"type": "string"}, "all": {"type": "boolean"}}}`)}}
	text := "読むファイルを決める。\n</think>\n読みます。\n<tool_call>\n<function=read_file>\n<parameter=path>\na.go\n</parameter>\n<parameter=all>\nTrue\n</parameter>\n</function>\n</tool_call>\n"
	want := []ToolCall{{Call: tool.Call{Name: "read_file", Args: []tool.Arg{{Name: "path", Value: "a.go"}, {Name: "all", Value: "true"}}}}}
	for _, pieces := range [][]string{{text}, runes(text)} {
		_, ct, out := granite42Parse(true, tools, pieces...)
		if out.Thinking != "読むファイルを決める。" || ct != "読みます。\n" || out.Content != "読みます。" || !reflect.DeepEqual(out.ToolCalls, want) {
			t.Errorf("画面 %q / 結果 %+v", ct, out)
		}
	}
	// 思考を切ったときは、全部が答え
	if _, ct, out := granite42Parse(false, nil, "こんにちは"); ct != "こんにちは" || out.Content != "こんにちは" || out.Thinking != "" {
		t.Errorf("思考なし: %q / %+v", ct, out)
	}
}

// ループの途中の返事は、読み取ったものを書き戻すとモデルが書いた文字と同じになる(次の送信で KV キャッシュを使い回せる)
func TestGranite42ReplyRoundTrip(t *testing.T) {
	tools := []tool.Def{{Name: "calculate", Parameters: []byte(`{"type": "object", "properties": {"expression": {"type": "string"}}}`)}}
	for _, think := range []bool{true, false} {
		reply := "計算する。\n</think>\n計算します。\n<tool_call>\n<function=calculate>\n<parameter=expression>\n1+1\n</parameter>\n</function>\n</tool_call>\n"
		if !think {
			reply = "計算します。\n<tool_call>\n<function=calculate>\n<parameter=expression>\n1+1\n</parameter>\n</function>\n</tool_call>\n"
		}
		p := Prompt{Think: think, Tools: tools, Messages: []Message{{Role: "user", Content: "1+1"}}}
		before := granite42{}.BuildPrompt(p)
		_, _, out := granite42Parse(think, tools, reply)
		p.Messages = append(p.Messages, Message{Role: "assistant", Thinking: out.Thinking, Content: out.Content, ToolCalls: out.ToolCalls}, Message{Role: "tool", Content: "2"})
		if got := (granite42{}).BuildPrompt(p); !strings.HasPrefix(got, before+reply+"<|im_end|>\n") {
			t.Errorf("思考 %v: 書き戻すと変わる\n--- 前のプロンプト + 返事\n%s\n--- 書き戻し\n%s", think, before+reply, got)
		}
	}
}

// 呼び出しの後ろでモデルが結果を書き始めたら止める
func TestGranite42ParserStopsAtToolResponse(t *testing.T) {
	q := granite42{}.NewParser(Prompt{})
	q.Push("<tool_call>\n<function=x>\n</function>\n</tool_call>\n<tool_res")
	if q.Stop() {
		t.Error("まだ書きかけ")
	}
	q.Push("ponse>\n")
	if !q.Stop() {
		t.Error("<tool_response> で止める")
	}
}
