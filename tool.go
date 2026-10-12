package aibow

// tool.go: ツール(モデルが「使いたい」と書いたら、アプリが代わりに実行するもの)。
//
// ツール呼び出しの正体は「決まった書式の文字列」。モデルは呼び出しを書いて止まるだけで、
// 実行しているのはアプリ。結果を返して、もう一度生成させる、を繰り返すのがエージェント(agent.go)。
//
// ツールは書式(モデルの系統)に依存しない。定義(名前・説明・引数の JSON Schema)を書式がプロンプトに書き、
// モデルが書いた呼び出しを書式が ToolCall に読み取る。ここではそれを受けて実行するだけ。
// ステップ3(ファイル操作)・ステップ4(MCP のツール)も、同じ Tool の形で足す。

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// Tool はモデルが使えるツール1つ。
type Tool interface {
	Def() ToolDef
	// Run はツールを実行して、モデルに返す結果の文を返す。
	// error を返すと「エラー: …」としてモデルに返る(送信は止めない。モデル自身に立て直させる)
	Run(ctx context.Context, args map[string]string) (string, error)
}

// ToolDef はモデルに知らせるツールの定義。書式(format.go)がシステムプロンプトに入れる。
type ToolDef struct {
	Name        string          `json:"name"`        // 英語の snake_case
	Description string          `json:"description"` // 英語(テンプレートの指示文が英語のため)
	Parameters  json.RawMessage `json:"parameters"`  // 引数の JSON Schema。書いた順のままモデルに見せる
}

// ToolCall はモデルが書いたツール呼び出し1つ。書式に依存しない形にしたもの。
//
// 書式が崩れて読み取れなかった呼び出しは、Raw にモデルが書いた生の文字列、Error に理由を持つ。
// 実行はせず、エラーの結果としてモデルに返して立て直させる。次のプロンプトには Raw をそのまま入れ、
// モデルが自分の書いたものを見られるようにする。
type ToolCall struct {
	Name  string    `json:"name"`
	Args  []ToolArg `json:"args"` // 書いた順(プロンプトに書き戻すときも同じ順にするため)
	Raw   string    `json:"raw,omitempty"`
	Error string    `json:"error,omitempty"`
}

// ToolArg は引数1つ。値は文字列のまま持つ(Qwen3.5 の書式は値を文字列で書くため。
// 数や真偽値の引数が要るツール(ステップ4 の MCP)を足すときに、JSON Schema の型で変換する)。
type ToolArg struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// BuiltinTools は aibow に組み込みのツール。どれも何も書き換えない。
func BuiltinTools() []Tool {
	return []Tool{currentTime{}, calculator{}}
}

// toolDefs はツールの定義の一覧(モデルに知らせる用)。
func toolDefs(tools []Tool) []ToolDef {
	defs := make([]ToolDef, len(tools))
	for i, t := range tools {
		defs[i] = t.Def()
	}
	return defs
}

// findTool は名前でツールを探す。
func findTool(tools []Tool, name string) Tool {
	for _, t := range tools {
		if t.Def().Name == name {
			return t
		}
	}
	return nil
}

// toolArgs は呼び出しの引数を、定義(JSON Schema の properties と required)と照らし合わせて map にする。
// 足りない・知らない・重なった引数はエラー。小さいモデルは引数の名前を間違えやすいので、
// 何が使えるかをエラーの文に入れて、次の呼び出しで直せるようにする。
func toolArgs(def ToolDef, args []ToolArg) (map[string]string, error) {
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal(def.Parameters, &schema); err != nil {
		return nil, fmt.Errorf("ツール %s の定義が読めません: %w", def.Name, err)
	}
	known := make([]string, 0, len(schema.Properties))
	for k := range schema.Properties {
		known = append(known, k)
	}
	sort.Strings(known)

	out := map[string]string{}
	for _, a := range args {
		if _, ok := schema.Properties[a.Name]; !ok {
			return nil, fmt.Errorf("引数 %s はありません(使える引数: %s)", a.Name, listOrNone(known))
		}
		if _, dup := out[a.Name]; dup {
			return nil, fmt.Errorf("引数 %s が2回書かれています", a.Name)
		}
		out[a.Name] = a.Value
	}
	for _, r := range schema.Required {
		if _, ok := out[r]; !ok {
			return nil, fmt.Errorf("必須の引数 %s がありません", r)
		}
	}
	return out, nil
}

func listOrNone(names []string) string {
	if len(names) == 0 {
		return "なし"
	}
	return strings.Join(names, ", ")
}

// toolNames はツールの名前の一覧(知らないツールを呼ばれたときのエラーに使う)。
func toolNames(tools []Tool) string {
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Def().Name
	}
	return listOrNone(slices.Sorted(slices.Values(names)))
}
