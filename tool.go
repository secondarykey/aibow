package aibow

// tool.go: ツール(モデルが「使いたい」と書いたら、アプリが代わりに実行するもの)。
//
// ツール呼び出しの正体は「決まった書式の文字列」。モデルは呼び出しを書いて止まるだけで、
// 実行しているのはアプリ。結果を返して、もう一度生成させる、を繰り返すのがエージェント(conversation.go)。

import "encoding/json"

// ToolDef はモデルに知らせるツールの定義。書式(format.go)がシステムプロンプトに入れる。
type ToolDef struct {
	Name        string          // 英語の snake_case
	Description string          // 英語(テンプレートの指示文が英語のため)
	Parameters  json.RawMessage // 引数の JSON Schema。書いた順のままモデルに見せる
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

// ToolArg は引数1つ。値は文字列のまま持つ(Qwen3.5 の書式は値を文字列で書くため)。
type ToolArg struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}
