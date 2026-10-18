package api

// tools.go: ツール呼び出しの変換(6-2)。
//
// OpenAI の API では、ツールの定義を JSON Schema で渡し(tools)、呼び出しが JSON で返る(tool_calls)ように見える。
// 実際は、サーバーがツールの説明をプロンプトに書き、モデルが書いた <tool_call> を読み取って JSON にしているだけ
// (aibow がステップ 2-3 から自分でやってきたこと)。ここはその「JSON ⇄ 書式の形」の変換をする:
//
//	行き: tools               → tool.Def(書式がシステムプロンプトに書く)
//	      履歴の tool_calls    → format.ToolCall(引数は JSON の値 → 文字列)
//	      tool の役の発言     → format.Message{Role: "tool"}
//	帰り: format.ToolCall     → tool_calls(引数は文字列 → JSON の値。tools の JSON Schema の型を見る)
//
// 引数の型の違い:Qwen3.5 の書式は値をいつも文字列で書く(<parameter=n>\n5\n</parameter>)。OpenAI の arguments は JSON(数は数)。
// 文字列でない値は、モデルが書いた文字をそのまま JSON の値として arguments に入れ、戻ってきたときもその文字のまま書式に戻す。
// こうすると往復で1文字も変わらないので、ツールの結果を足したリクエストで、前回の生成の分まで KV キャッシュを使い回せる
// (書き戻したプロンプトがモデルの書いた文字と違うと、Qwen3.5 は途中から巻き戻せないので全部を読み直す)。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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

// fromRequestCalls は履歴の assistant の tool_calls を、書式の形にする。
func fromRequestCalls(calls []toolCall) ([]format.ToolCall, error) {
	out := make([]format.ToolCall, 0, len(calls))
	for i, c := range calls {
		if c.Type != "" && c.Type != "function" {
			return nil, fmt.Errorf("tool_calls[%d]: 種類 %q には対応していません(function だけです)", i, c.Type)
		}
		args, err := argsFromJSON(c.Function.Arguments)
		if err != nil {
			return nil, fmt.Errorf("tool_calls[%d](%s)の arguments: %w", i, c.Function.Name, err)
		}
		out = append(out, format.ToolCall{Call: tool.Call{Name: c.Function.Name, Args: args}})
	}
	return out, nil
}

// argsFromJSON は arguments(JSON のオブジェクトの文字列)を、書いた順の引数にする。
// 文字列の値は中身の文字列に、それ以外(数・真偽値・配列・オブジェクト・null)は JSON の文字のまま値にする。
//
// map に読むと順番がなくなる(プロンプトに書き戻す順が変わり、モデルが書いたものと違ってしまう)ので、
// json.Decoder で1つずつ読む。
func argsFromJSON(arguments string) ([]tool.Arg, error) {
	args := []tool.Arg{}
	if strings.TrimSpace(arguments) == "" {
		return args, nil
	}
	dec := json.NewDecoder(strings.NewReader(arguments))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, fmt.Errorf("JSON のオブジェクトではありません: %q", arguments)
	}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, err
		}
		name, _ := t.(string) // オブジェクトの中のこの位置は、いつもキー(文字列)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		value := string(raw)
		var s string
		if json.Unmarshal(raw, &s) == nil {
			value = s
		}
		args = append(args, tool.Arg{Name: name, Value: value})
	}
	if _, err := dec.Token(); err != nil { // 閉じの }
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("JSON のオブジェクトの後ろに余分なものがあります: %q", arguments)
	}
	return args, nil
}

// toResponseCalls は、モデルが書いた呼び出しを返事の形にする(0012 の 6-2 でユーザーと決めた。2026-10-19)。
//
//   - 読み取れた呼び出しは tool_calls にする。tools の定義に合わない(知らない名前・引数の間違い・型が合わない)ものも、そのまま返す。
//     クライアントのツールの検証でエラーになり、それがツールの結果としてモデルに返るので、aibow 自身のループと同じくモデルが直せる。合わない理由は Warn のログに出す
//   - 書式が崩れて読み取れなかった呼び出しは、モデルが書いたままを答えの文字列(extra)として返す。
//     クライアントには崩れた形が見え、Cline などは「ツールを使っていない」とモデルに言い返して立て直させる
func toResponseCalls(calls []format.ToolCall, defs []tool.Def) (out []toolCall, extra string) {
	var broken []string
	for _, c := range calls {
		if c.Error != "" {
			log.Warnf("書式が崩れた呼び出しを、答えの文字列として返します: %s", firstLine(c.Error))
			broken = append(broken, c.Raw)
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
		tc.Function.Arguments = argumentsJSON(c.Args, def)
		out = append(out, tc)
	}
	return out, strings.Join(broken, "\n")
}

// argumentsJSON は引数を arguments(JSON のオブジェクトの文字列)にする。書いた順のまま並べる。
//
// 値の型は tools の JSON Schema を見て決める(tool.CheckArgs と同じ考え方)。文字列でない型で、モデルが書いた文字が
// その型の JSON として読めるなら、書いた文字をそのまま値にする(往復で変わらないように)。読めなければ文字列のまま。
func argumentsJSON(args []tool.Arg, def *tool.Def) string {
	types := map[string]string{}
	if def != nil {
		types = propertyTypes(def.Parameters)
	}
	var b strings.Builder
	b.WriteString("{")
	for i, a := range args {
		if i > 0 {
			b.WriteString(",")
		}
		b.Write(jsonString(a.Name))
		b.WriteString(":")
		if raw, ok := typedValue(types[a.Name], a.Value); ok {
			b.WriteString(raw)
		} else {
			b.Write(jsonString(a.Value))
		}
	}
	b.WriteString("}")
	return b.String()
}

// typedValue は、文字列で書かれた値を typ の JSON の値として書けるなら、その文字を返す。
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

// propertyTypes は JSON Schema の properties の、引数ごとの type を返す。
// type が ["string", "null"] のような配列なら null でない最初の型(tool の schemaType と同じ扱い)。型を決めていない引数は入れない。
func propertyTypes(params json.RawMessage) map[string]string {
	var schema struct {
		Properties map[string]struct {
			Type json.RawMessage `json:"type"`
		} `json:"properties"`
	}
	json.Unmarshal(params, &schema)
	types := map[string]string{}
	for name, p := range schema.Properties {
		var one string
		var many []string
		switch {
		case json.Unmarshal(p.Type, &one) == nil:
			types[name] = one
		case json.Unmarshal(p.Type, &many) == nil:
			for _, m := range many {
				if m != "null" {
					types[name] = m
					break
				}
			}
		}
	}
	return types
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

func jsonString(s string) []byte {
	b, _ := json.Marshal(s) // 文字列の Marshal は失敗しない
	return b
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
