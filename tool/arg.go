package tool

// arg.go: 引数の値の持ち方(0026 の決定事項 1)。
//
// 書式によって、モデルが引数の値を書く形は違う。Qwen3.5 は値をいつも文字で書き(<parameter=n>\n5\n</parameter>)、
// OpenAI 互換の口の arguments は JSON(数は数)、Gemma 4 は型のある独自の書き方をする。
// 会話に保存し、別の書式で書き直せるように、aibow の中では次の1つの形にそろえる(Arg.Value):
//
//	文字列の引数・型を決めていない引数 … 文字列の中身そのまま
//	数・真偽・配列・オブジェクトの引数 … JSON の文字(5 / true / ["a", "b"])
//
// どちらの形かは値だけでは決まらず、ツールの定義(JSON Schema の type)を見て決める。
// 書式は、読むときは定義の型を見てこの形にし(ArgFromText・ArgFromJSON)、書くときはこの形から自分の書き方に直す(ArgText・ArgJSON)。
//
// 型のある引数にモデルが型に合わない文字を書いたとき(整数の引数に " 5" や 5.0、真偽の引数に True)は、
// 書いた文字を JSON の文字列にして持つ(" 5" → "\" 5\"")。直さずに持つので、同じ書式に書き戻すと1文字も変わらず
// (KV キャッシュを使い回せる)、実行の前の CheckArgs は中身の文字を今までどおり大目に見て変換する(" 5" → 5、True → true)。
//
// この形にする前(0026 より前)に保存した会話は、型のある引数でもモデルが書いた文字のまま持っている(True など)。
// JSON の文字列でない値は書いたままの文字として扱うので、そのまま読める。

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// ArgTypes は、ツールの定義の引数ごとの型(JSON Schema の properties の type)を返す。
// type が ["integer", "null"] のような配列なら null でない最初の型。型を決めていない引数(anyOf など)は入れない。
func ArgTypes(def Def) map[string]string {
	var schema struct {
		Properties map[string]struct {
			Type schemaType `json:"type"`
		} `json:"properties"`
	}
	json.Unmarshal(def.Parameters, &schema) // 読めない定義は「型が分からない」として扱う(CheckArgs がエラーにする)
	types := map[string]string{}
	for name, p := range schema.Properties {
		if p.Type != "" {
			types[name] = string(p.Type)
		}
	}
	return types
}

// ArgTypesFor は、定義の一覧から name のツールを探して、その引数の型を返す。無ければ nil(どの引数も型が分からない)。
func ArgTypesFor(defs []Def, name string) map[string]string {
	for _, d := range defs {
		if d.Name == name {
			return ArgTypes(d)
		}
	}
	return nil
}

// typed は、値を JSON の文字で持つ型か。文字列と、型を決めていない・知らない型の引数は、文字列の中身のまま持つ
// (CheckArgs も、この5つの型だけを変換する)。
func typed(typ string) bool {
	switch typ {
	case "integer", "number", "boolean", "array", "object":
		return true
	}
	return false
}

// ArgFromText は、値を文字で書く書式(Qwen3.5)が読み取った値を、Arg.Value の形にする。
// 型のある引数で、書いた文字がそのまま型に合う JSON なら、その文字のまま。合わなければ JSON の文字列にする。
func ArgFromText(typ, text string) string {
	if !typed(typ) || isJSONOf(typ, text) {
		return text
	}
	return jsonString(text)
}

// ArgText は ArgFromText の逆。Arg.Value を、値を文字で書く書式に書く文字にする。
// 型のある引数の JSON の文字列(型に合わない文字を書いていたもの)は、中身の文字に戻す。それ以外は Value のまま。
func ArgText(typ, value string) string {
	if typed(typ) {
		var s string
		if json.Unmarshal([]byte(value), &s) == nil {
			return s
		}
	}
	return value
}

// ArgFromJSON は、JSON で届いた値1つ(OpenAI 互換の arguments・MCP の arguments)を、Arg.Value の形にする。
// 型のある引数は JSON の文字のまま(文字列で届いたら、JSON の文字列のまま)。それ以外は、文字列なら中身、ほかは JSON の文字。
func ArgFromJSON(typ string, raw json.RawMessage) string {
	if !typed(typ) {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
	}
	return string(raw)
}

// ArgJSON は ArgFromJSON の逆。Arg.Value を JSON の値の文字にする。
// 型のある引数で JSON として読める値はそのまま。それ以外(文字列の引数・0026 より前に保存した True や " 5" など)は JSON の文字列にする
// (前後の空白は JSON の中では消えてしまうので、空白のある値も文字列にする。読み戻すと書いた文字に戻る)。
func ArgJSON(typ, value string) string {
	if typed(typ) && value == strings.TrimSpace(value) && json.Valid([]byte(value)) {
		return value
	}
	return jsonString(value)
}

// ArgsFromJSON は arguments(JSON のオブジェクトの文字列)を、書いた順の引数にする。types は ArgTypes の型。
//
// map に読むと順番がなくなる(プロンプトに書き戻す順が変わり、モデルが書いたものと違ってしまう)ので、
// json.Decoder で1つずつ読む。
func ArgsFromJSON(arguments string, types map[string]string) ([]Arg, error) {
	args := []Arg{}
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
		args = append(args, Arg{Name: name, Value: ArgFromJSON(types[name], raw)})
	}
	if _, err := dec.Token(); err != nil { // 閉じの }
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("JSON のオブジェクトの後ろに余分なものがあります: %q", arguments)
	}
	return args, nil
}

// ArgsJSON は ArgsFromJSON の逆。引数を arguments(JSON のオブジェクトの文字列)にする。書いた順のまま並べる。
func ArgsJSON(args []Arg, types map[string]string) string {
	var b strings.Builder
	b.WriteString("{")
	for i, a := range args {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(jsonString(a.Name))
		b.WriteString(":")
		b.WriteString(ArgJSON(types[a.Name], a.Value))
	}
	b.WriteString("}")
	return b.String()
}

// isJSONOf は、text が前後に空白の無い、typ の型の JSON の値かを返す。
// 整数は、JSON の数で、小数点も指数も無いもの(CheckArgs が整数として受け取れるもの)。
func isJSONOf(typ, text string) bool {
	if text != strings.TrimSpace(text) || !json.Valid([]byte(text)) {
		return false
	}
	switch typ {
	case "integer":
		_, err := strconv.Atoi(text)
		return err == nil
	case "number":
		return text[0] == '-' || '0' <= text[0] && text[0] <= '9' // JSON として読めて、この文字で始まるのは数だけ
	case "boolean":
		return text == "true" || text == "false"
	case "array":
		return strings.HasPrefix(text, "[")
	case "object":
		return strings.HasPrefix(text, "{")
	}
	return false
}

func jsonString(s string) string {
	b, _ := json.Marshal(s) // 文字列の Marshal は失敗しない
	return string(b)
}
