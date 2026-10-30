package tool

import (
	"slices"
	"testing"
)

// argDef は、いろいろな型の引数を持つツールの定義。
var argDef = Def{Name: "t", Parameters: []byte(`{"type": "object", "properties": {
	"s": {"type": "string"}, "i": {"type": "integer"}, "n": {"type": "number"}, "b": {"type": "boolean"},
	"a": {"type": "array"}, "o": {"type": "object"}, "h": {"type": ["integer", "null"]}, "x": {"anyOf": [{"type": "string"}]}}}`)}

func TestArgTypes(t *testing.T) {
	types := ArgTypes(argDef)
	want := map[string]string{"s": "string", "i": "integer", "n": "number", "b": "boolean", "a": "array", "o": "object", "h": "integer"}
	if len(types) != len(want) {
		t.Fatalf("型: %v", types)
	}
	for k, v := range want {
		if types[k] != v {
			t.Errorf("%s の型: %q, want %q", k, types[k], v)
		}
	}
	if ArgTypesFor([]Def{argDef}, "t")["i"] != "integer" || ArgTypesFor([]Def{argDef}, "u") != nil {
		t.Error("ArgTypesFor は名前で探す。無ければ nil")
	}
}

// 文字で書かれた値(Qwen3.5)は、型に合えばそのまま、合わなければ JSON の文字列にして持つ。ArgText で書いた文字に戻る
func TestArgFromText(t *testing.T) {
	cases := []struct{ typ, text, want string }{
		{"string", "5", "5"},
		{"string", `"q"`, `"q"`},
		{"", "True", "True"}, // 型を決めていない引数は文字列
		{"integer", "12", "12"},
		{"integer", "-3", "-3"},
		{"integer", " 5", `" 5"`},
		{"integer", "5.0", `"5.0"`},
		{"integer", "05", `"05"`},
		{"number", "1.5e3", "1.5e3"},
		{"number", `"5"`, `"\"5\""`},
		{"boolean", "true", "true"},
		{"boolean", "True", `"True"`},
		{"array", `["a", 1]`, `["a", 1]`},
		{"array", `{"a": 1}`, `"{\"a\": 1}"`},
		{"object", `{"a": 1}`, `{"a": 1}`},
		{"object", "a\nb", `"a\nb"`},
	}
	for _, c := range cases {
		got := ArgFromText(c.typ, c.text)
		if got != c.want {
			t.Errorf("ArgFromText(%q, %q) = %q, want %q", c.typ, c.text, got, c.want)
		}
		if back := ArgText(c.typ, got); back != c.text {
			t.Errorf("ArgText(%q, %q) = %q, want %q(書いた文字に戻る)", c.typ, got, back, c.text)
		}
	}
	// 0026 より前に保存した値(型のある引数でも、モデルが書いたままの文字)は、そのまま書く
	if got := ArgText("boolean", "True"); got != "True" {
		t.Errorf("前の形の値: %q", got)
	}
}

// arguments の JSON と tool.Arg の形を行き来しても、変わらないこと。文字列の引数だけ中身にする
func TestArgsJSON(t *testing.T) {
	types := ArgTypes(argDef)
	in := `{"s":"a\"b","i":5,"b":true,"a":["x",1],"o":{"k":null},"n":"5","x":7,"s2":3}`
	args, err := ArgsFromJSON(in, types)
	if err != nil {
		t.Fatal(err)
	}
	want := []Arg{{"s", `a"b`}, {"i", "5"}, {"b", "true"}, {"a", `["x",1]`}, {"o", `{"k":null}`}, {"n", `"5"`}, {"x", "7"}, {"s2", "3"}}
	if !slices.Equal(args, want) {
		t.Fatalf("読んだ引数: %q", args)
	}
	if got := ArgsJSON(args, types); got != `{"s":"a\"b","i":5,"b":true,"a":["x",1],"o":{"k":null},"n":"5","x":"7","s2":"3"}` {
		t.Errorf("書いた arguments: %s", got)
	}
	// 0026 より前に保存した、型のある引数の JSON でない値は、JSON の文字列にする
	old := []Arg{{"b", "True"}, {"i", " 5"}}
	got := ArgsJSON(old, types)
	if got != `{"b":"True","i":" 5"}` {
		t.Errorf("前の形の値: %s", got)
	}
	if back, _ := ArgsFromJSON(got, types); !slices.Equal(back, []Arg{{"b", `"True"`}, {"i", `" 5"`}}) || ArgText("integer", back[1].Value) != " 5" {
		t.Errorf("読み戻すと JSON の文字列になり、書く文字は前と同じ: %q", back)
	}
	for _, bad := range []string{`[1]`, `{"a":1} x`, `{"a":}`} {
		if _, err := ArgsFromJSON(bad, types); err == nil {
			t.Errorf("%s はエラーになるはずです", bad)
		}
	}
}

// CheckArgs は、型に合わない文字を書いた値(JSON の文字列で持っている)も、今までどおり大目に見て変換する
func TestCheckArgsWrapped(t *testing.T) {
	got, err := CheckArgs(argDef, []Arg{{"i", `" 5"`}, {"b", `"True"`}, {"n", "1.5"}, {"s", `"q"`}})
	if err != nil {
		t.Fatal(err)
	}
	if got["i"] != 5 || got["b"] != true || got["n"] != 1.5 || got["s"] != `"q"` {
		t.Errorf("変換した値: %#v", got)
	}
	if _, err := CheckArgs(argDef, []Arg{{"i", `"5.0"`}}); err == nil {
		t.Error("整数でない値はエラー")
	}
}
