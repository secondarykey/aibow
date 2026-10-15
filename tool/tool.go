// Package tool は、モデルが使えるツール(モデルが「使いたい」と書いたら、アプリが代わりに実行するもの)。
//
// ツール呼び出しの正体は「決まった書式の文字列」。モデルは呼び出しを書いて止まるだけで、
// 実行しているのはアプリ。結果を返して、もう一度生成させる、を繰り返すのがエージェント(ルートの agent.go)。
//
// このパッケージは LLM・書式・会話・保存のどれも import しない。依存の向きは次のとおり:
//
//	書式(format/qwen35.go)── ツールの定義(Def)をプロンプトに書き、呼び出しを Call に読み取る ──→ tool
//	会話の仕組み(agent.go)── 呼び出しを受けて実行し、回数・時間の上限や結果の保存を受け持つ ──→ tool
//	tool ── 何も知らない(定義を出して、引数を受けて実行するだけ)
//
// こうしておくと、ステップ5の MCP サーバー(ツールだけを外に出すプログラム)が、
// LLM(yzma)や会話の仕組みを抱えずにこのパッケージだけを使える。
// ステップ3(ファイル操作)・ステップ4(MCP のツール)も、同じ Tool の形で足す。
package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Tool はモデルが使えるツール1つ。
type Tool interface {
	Def() Def
	// Run はツールを実行して、モデルに返す結果の文を返す。args は CheckArgs で定義と照らし合わせ、型を変換したもの。
	// error を返すと「エラー: …」としてモデルに返る(送信は止めない。モデル自身に立て直させる)
	Run(ctx context.Context, args Args) (string, error)
}

// TimeLimiter は、実行の時間の上限を自分で決めるツール(run_command は 120 秒)。
// 決めないツールは、会話の仕組みの既定(10 秒)。上限を過ぎると ctx が止まるので、ツールは後片付け(子プロセスを止める、など)をして返す
type TimeLimiter interface {
	TimeLimit() time.Duration
}

// Remote は、aibow の外のプログラム(MCP のサーバー)が実行するツール。
// モデルから見ると組み込みのツールと違いは無い(定義を見て呼ぶだけ)。違うのは、実行がどこで起きるかだけ。
// Server はそのサーバーの名前(settings.json の mcpServers に書いた名前。画面とログに出す)。
type Remote interface {
	Tool
	Server() string
}

// Def はモデルに知らせるツールの定義。書式がシステムプロンプトに入れる。
type Def struct {
	Name        string          `json:"name"`        // 英語の snake_case
	Description string          `json:"description"` // 英語(テンプレートの指示文が英語のため)
	Parameters  json.RawMessage `json:"parameters"`  // 引数の JSON Schema。書いた順のままモデルに見せる
}

// Call はツールの呼び出し1つ(名前と引数だけ)。
//
// モデルが書いたままの文字列や、書式が崩れて読めなかった理由はここに持たない。それは書式の話なので、
// 書式の側(ルートの ToolCall)がこれを埋め込んで足している。
type Call struct {
	Name string `json:"name"`
	Args []Arg  `json:"args"` // 書いた順(プロンプトに書き戻すときも同じ順にするため)
}

// Arg はモデルが書いた引数1つ。値は文字列のまま持つ(Qwen3.5 の書式は値を文字列で書くため)。
// 数の引数は、実行する前に CheckArgs が JSON Schema の型を見て変換する。
// 呼び出しを保存・プロンプトに書き戻すときは、モデルが書いたままの文字列を使う。
type Arg struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Args は実行するときの引数。CheckArgs が定義と照らし合わせ、JSON Schema の型に変換したもの
// (type が integer なら int、number なら float64、boolean なら bool、array / object なら JSON を読んだ値([]any / map[string]any)、
// ほかは string)。省略された引数は入っていない。
type Args map[string]any

// String は文字列の引数を返す。省略されていたら "" 。
func (a Args) String(name string) string {
	s, _ := a[name].(string)
	return s
}

// Int は整数の引数を返す。省略されていたら def。
func (a Args) Int(name string, def int) int {
	if n, ok := a[name].(int); ok {
		return n
	}
	return def
}

// Builtin は aibow に組み込みのツール。どれも何も書き換えない。
func Builtin() []Tool {
	return []Tool{currentTime{}, calculator{}}
}

// Defs はツールの定義の一覧(モデルに知らせる用)。
func Defs(tools []Tool) []Def {
	defs := make([]Def, len(tools))
	for i, t := range tools {
		defs[i] = t.Def()
	}
	return defs
}

// Find は名前でツールを探す。無ければ nil。
func Find(tools []Tool, name string) Tool {
	for _, t := range tools {
		if t.Def().Name == name {
			return t
		}
	}
	return nil
}

// Names はツールの名前の一覧を「a, b」の形で返す(知らないツールを呼ばれたときのエラーに使う)。
func Names(tools []Tool) string {
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Def().Name
	}
	return listOrNone(slices.Sorted(slices.Values(names)))
}

// CheckArgs は呼び出しの引数を、定義(JSON Schema の properties と required)と照らし合わせて Args にする。
// 足りない・知らない・重なった引数はエラー。小さいモデルは引数の名前を間違えやすいので、
// 何が使えるかをエラーの文に入れて、次の呼び出しで直せるようにする。
//
// Qwen3.5 の書式では値がすべて文字列で届くので、文字列でない引数(数・真偽値・配列・オブジェクト)はここで変換する。
// ツールの側は、受け取った値の型を信じてよい。
func CheckArgs(def Def, args []Arg) (Args, error) {
	var schema struct {
		Properties map[string]struct {
			Type schemaType `json:"type"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(def.Parameters, &schema); err != nil {
		return nil, fmt.Errorf("ツール %s の定義が読めません: %w", def.Name, err)
	}
	known := make([]string, 0, len(schema.Properties))
	for k := range schema.Properties {
		known = append(known, k)
	}
	sort.Strings(known)

	out := Args{}
	for _, a := range args {
		prop, ok := schema.Properties[a.Name]
		if !ok {
			return nil, fmt.Errorf("引数 %s はありません(使える引数: %s)", a.Name, listOrNone(known))
		}
		if _, dup := out[a.Name]; dup {
			return nil, fmt.Errorf("引数 %s が2回書かれています", a.Name)
		}
		v, err := convert(string(prop.Type), a.Value)
		if err != nil {
			return nil, fmt.Errorf("引数 %s: %w", a.Name, err)
		}
		out[a.Name] = v
	}
	for _, r := range schema.Required {
		if _, ok := out[r]; !ok {
			return nil, fmt.Errorf("必須の引数 %s がありません", r)
		}
	}
	return out, nil
}

// schemaType は JSON Schema の type。ふつうは "string" のような文字列だが、
// MCP のサーバーの定義には ["string", "null"](null も許す)のような配列もあるので、null でない最初の型を使う。
// 型を決めていない(anyOf などで書いた)引数は "" になり、文字列のまま渡す。
type schemaType string

func (t *schemaType) UnmarshalJSON(b []byte) error {
	var one string
	if json.Unmarshal(b, &one) == nil {
		*t = schemaType(one)
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return err
	}
	for _, m := range many {
		if m != "null" {
			*t = schemaType(m)
			return nil
		}
	}
	return nil
}

// convert は文字列で届いた値を、JSON Schema の型に変換する。型を決めていない・文字列の引数はそのまま。
//
// 文字列でない値の書き方は、Qwen3.5 の公式テンプレートが会話の履歴に呼び出しを書き戻すときの書き方に合わせる
// (モデルは学習でその形を見ているので、同じ形で書いてくる):
//
//	配列・オブジェクト … JSON(テンプレートの tojson)
//	それ以外           … 文字列にしたもの(テンプレートは Python で動くので、真偽値は True / False になる)
func convert(typ, value string) (any, error) {
	v := strings.TrimSpace(value)
	switch typ {
	case "integer":
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("整数で書いてください(%q)", value)
		}
		return n, nil
	case "number":
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return nil, fmt.Errorf("数で書いてください(%q)", value)
		}
		return f, nil
	case "boolean":
		switch strings.ToLower(v) {
		case "true":
			return true, nil
		case "false":
			return false, nil
		}
		return nil, fmt.Errorf("true か false で書いてください(%q)", value)
	case "array", "object":
		var x any
		err := json.Unmarshal([]byte(v), &x)
		if _, isArray := x.([]any); err == nil && typ == "array" && isArray {
			return x, nil
		}
		if _, isObject := x.(map[string]any); err == nil && typ == "object" && isObject {
			return x, nil
		}
		if typ == "array" {
			return nil, fmt.Errorf(`JSON の配列で書いてください(例: ["a", "b"])。書かれた値: %q`, value)
		}
		return nil, fmt.Errorf(`JSON のオブジェクトで書いてください(例: {"key": "value"})。書かれた値: %q`, value)
	}
	return value, nil
}

func listOrNone(names []string) string {
	if len(names) == 0 {
		return "なし"
	}
	return strings.Join(names, ", ")
}
