package api

// tools.go: ツール呼び出しの変換(6-2)。
//
// OpenAI の API では、ツールの定義を JSON Schema で渡し(tools)、呼び出しが JSON で返る(tool_calls)ように見える。
// 実際は、サーバーがツールの説明をプロンプトに書き、モデルが書いた <tool_call> を読み取って JSON にしているだけ
// (aibow がステップ 2-3 から自分でやってきたこと)。ここはその「JSON ⇄ 書式の形」の変換をする:
//
//	行き: tools               → tool.Def(書式がシステムプロンプトに書く)
//	      履歴の tool_calls    → format.ToolCall(引数は JSON → tool.Arg の形。tool.ArgsFromJSON)
//	      tool の役の発言     → format.Message{Role: "tool"}
//	帰り: format.ToolCall     → tool_calls(引数は tool.Arg の形 → JSON。tools の JSON Schema の型を見る)
//
// 引数の値は、aibow の中では書式によらない形(文字列の引数は中身、数などの引数は JSON の文字。tool/arg.go)で持つので、
// arguments の JSON とは型を見て行き来するだけ。Qwen3.5 が数の引数に書いた "5" は、そのまま JSON の 5 になる。
// モデルが型に合わない文字(" 5" や True)を書いたときは、tool.Arg は書いた文字を JSON の文字列で持っている。
// aibow のクライアントにはそれをそのまま返し(戻ってきたときに、書いた文字のまま書式に戻るように。往復で1文字も変わらないと、
// ツールの結果を足したリクエストで前回の生成の分まで KV キャッシュを使い回せる)、Cline などには型の値に直して返す。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"uuid"

	"github.com/secondarykey/aibow/format"
	"github.com/secondarykey/aibow/log"
	"github.com/secondarykey/aibow/tool"
)

// requestTool はリクエストの tools の1つ。
type requestTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"` // 書いた順のままモデルに見せるので、map にしない
	} `json:"function"`
}

// toolCall はツール呼び出し1つ。返事の tool_calls と、履歴の assistant の tool_calls の両方で使う。
type toolCall struct {
	Index    *int   `json:"index,omitempty"` // ストリーミングの delta だけで使う(何番目の呼び出しか)
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"` // JSON のオブジェクトを文字列にしたもの
	} `json:"function"`
}

// emptyParameters は parameters を書いていないツールの定義(引数なし)。
var emptyParameters = json.RawMessage(`{"type": "object", "properties": {}}`)

// toDefs はリクエストの tools を tool.Def にする。
func toDefs(tools []requestTool) ([]tool.Def, error) {
	defs := make([]tool.Def, 0, len(tools))
	for i, t := range tools {
		if t.Type != "function" {
			return nil, fmt.Errorf("tools[%d]: 種類 %q には対応していません(function だけです)", i, t.Type)
		}
		if t.Function.Name == "" {
			return nil, fmt.Errorf("tools[%d]: function.name がありません", i)
		}
		params := t.Function.Parameters
		if len(bytes.TrimSpace(params)) == 0 || string(bytes.TrimSpace(params)) == "null" {
			params = emptyParameters
		}
		defs = append(defs, tool.Def{Name: t.Function.Name, Description: t.Function.Description, Parameters: params})
	}
	return defs, nil
}

// useTools は tool_choice を見て、モデルにツールを知らせるかを決める。
//
// "none" ならツールを知らせない。"auto"(書かれていないときも)なら知らせる。
// "required" や特定のツールの指定は、書式の上では強制できない(モデルが呼ぶかは、モデルが決める)ので、"auto" と同じにしてログに残す。
func useTools(choice json.RawMessage) bool {
	s := strings.TrimSpace(string(choice))
	switch s {
	case "", "null", `"auto"`:
		return true
	case `"none"`:
		return false
	}
	log.Infof("tool_choice %s は強制できないので、auto と同じに扱います(呼ぶかはモデルが決めます)", s)
	return true
}

// fromRequestCalls は履歴の assistant の tool_calls を、書式の形にする。defs はモデルに知らせるツール(引数の型を見る)。
func fromRequestCalls(calls []toolCall, defs []tool.Def) ([]format.ToolCall, error) {
	out := make([]format.ToolCall, 0, len(calls))
	for i, c := range calls {
		if c.Type != "" && c.Type != "function" {
			return nil, fmt.Errorf("tool_calls[%d]: 種類 %q には対応していません(function だけです)", i, c.Type)
		}
		args, err := tool.ArgsFromJSON(c.Function.Arguments, tool.ArgTypesFor(defs, c.Function.Name))
		if err != nil {
			return nil, fmt.Errorf("tool_calls[%d](%s)の arguments: %w", i, c.Function.Name, err)
		}
		out = append(out, format.ToolCall{Call: tool.Call{Name: c.Function.Name, Args: args}})
	}
	return out, nil
}

// mergeBroken は、履歴の assistant の読み取れた呼び出し(tool_calls)に、書式が崩れた呼び出し(aibow 欄)を元の並びで混ぜる(0015)。
// 崩れた呼び出しは Raw(モデルが書いたまま)で書式に戻るので、プロンプトが前回モデルの書いた文字と同じになる。
func mergeBroken(calls []format.ToolCall, broken []brokenCall) ([]format.ToolCall, error) {
	if len(broken) == 0 {
		return calls, nil
	}
	n := len(calls) + len(broken)
	out := make([]format.ToolCall, n)
	placed := make([]bool, n)
	for _, b := range broken {
		if b.Index < 0 || b.Index >= n || placed[b.Index] {
			return nil, fmt.Errorf("aibow.broken_tool_calls の index %d が呼び出しの並び(全部で %d 個)に合いません", b.Index, n)
		}
		if b.Raw == "" {
			return nil, fmt.Errorf("aibow.broken_tool_calls の index %d に raw がありません", b.Index)
		}
		out[b.Index] = format.ToolCall{Call: tool.Call{Name: b.Name}, Raw: b.Raw, Error: b.Error}
		placed[b.Index] = true
	}
	j := 0
	for i := range out {
		if !placed[i] {
			out[i] = calls[j]
			j++
		}
	}
	return out, nil
}

// toResponseCalls は、モデルが書いた呼び出しを返事の形にする(0012 の 6-2 でユーザーと決めた。2026-10-19)。
//
//   - 読み取れた呼び出しは tool_calls にする。tools の定義に合わない(知らない名前・引数の間違い・型が合わない)ものも、そのまま返す。
//     クライアントのツールの検証でエラーになり、それがツールの結果としてモデルに返るので、aibow 自身のループと同じくモデルが直せる。合わない理由は Warn のログに出す
//   - 書式が崩れて読み取れなかった呼び出しは broken に入れる(全体の中の順番つき)。
//     Cline などには、呼ぶ側がモデルの書いたままを答えの文字列に足して返す(joinBroken。0012 の形)。
//     クライアントには崩れた形が見え、Cline などは「ツールを使っていない」とモデルに言い返して立て直させる。
//     aibow のクライアントには aibow 欄で分けて返す(自分のループでエラーとしてモデルに返して直させるため。0015)
//
// exact は aibow のクライアントのとき。モデルが型に合わない文字を書いた値を、直さずに返す(argumentsJSON)。
func toResponseCalls(calls []format.ToolCall, defs []tool.Def, exact bool) (out []toolCall, broken []brokenCall) {
	for i, c := range calls {
		if c.Error != "" {
			log.Warnf("書式が崩れた呼び出しを返します: %s", firstLine(c.Error))
			broken = append(broken, brokenCall{Index: i, Name: c.Name, Raw: c.Raw, Error: c.Error})
			continue
		}
		def := findDef(defs, c.Name)
		switch {
		case def == nil:
			log.Warnf("tools に無いツール %s の呼び出しを、そのまま返します(使えるのは %s)", c.Name, defNames(defs))
		default:
			if _, err := tool.CheckArgs(*def, c.Args); err != nil {
				log.Warnf("ツール %s の引数が定義と合いませんが、そのまま返します: %v", c.Name, err)
			}
		}
		tc := toolCall{ID: "call_" + strings.ReplaceAll(uuid.NewV7().String(), "-", ""), Type: "function"}
		tc.Function.Name = c.Name
		tc.Function.Arguments = argumentsJSON(c.Args, def, exact)
		out = append(out, tc)
	}
	return out, broken
}

// joinBroken は、崩れた呼び出しのモデルが書いたままを、答えに足す文字列にする(0012 の形。aibow のクライアントでないとき)。
func joinBroken(broken []brokenCall) string {
	raws := make([]string, len(broken))
	for i, b := range broken {
		raws[i] = b.Raw
	}
	return strings.Join(raws, "\n")
}

// argumentsJSON は引数を arguments(JSON のオブジェクトの文字列)にする。書いた順のまま並べる。
//
// 値は tool.Arg の形から、tools の JSON Schema の型を見て JSON の値にする(tool.ArgJSON)。
// exact でなければ、モデルが型に合わない文字を書いた値(JSON の文字列で持っている)も、前後の空白を除いたり True を true に
// 直したりして読めるなら型の値にする(Cline などのツールの検証を通るように)。aibow のクライアントは返ってきた値を書式に戻すので、
// 1文字でも変わると KV キャッシュを使い回せなくなるため、直さない(0015 の 8)。
func argumentsJSON(args []tool.Arg, def *tool.Def, exact bool) string {
	var types map[string]string
	if def != nil {
		types = tool.ArgTypes(*def)
	}
	if exact {
		return tool.ArgsJSON(args, types)
	}
	fixed := make([]tool.Arg, len(args))
	for i, a := range args {
		fixed[i] = a
		if raw, ok := typedValue(types[a.Name], tool.ArgText(types[a.Name], a.Value)); ok {
			fixed[i].Value = raw
		}
	}
	return tool.ArgsJSON(fixed, types)
}

// typedValue は、文字で書かれた値を typ の JSON の値として書けるなら、その文字を返す(大目に見る。tool.CheckArgs と同じ考え方)。
func typedValue(typ, value string) (string, bool) {
	v := strings.TrimSpace(value)
	switch typ {
	case "integer", "number":
		var n json.Number
		if json.Unmarshal([]byte(v), &n) == nil {
			return v, true
		}
	case "boolean":
		// 公式テンプレートは Python で動くので、履歴には True / False と書かれる。モデルがそれをまねても読めるようにする
		if l := strings.ToLower(v); l == "true" || l == "false" {
			return l, true
		}
	case "array", "object":
		var x any
		if json.Unmarshal([]byte(v), &x) == nil {
			_, isArray := x.([]any)
			_, isObject := x.(map[string]any)
			if (typ == "array" && isArray) || (typ == "object" && isObject) {
				return v, true
			}
		}
	}
	return "", false
}

func findDef(defs []tool.Def, name string) *tool.Def {
	for i := range defs {
		if defs[i].Name == name {
			return &defs[i]
		}
	}
	return nil
}

func defNames(defs []tool.Def) string {
	if len(defs) == 0 {
		return "なし"
	}
	names := make([]string, len(defs))
	for i, d := range defs {
		names[i] = d.Name
	}
	return strings.Join(names, ", ")
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
