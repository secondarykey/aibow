package format

// Package format は、チャットテンプレート(書式)。会話を、モデルが学習時に見てきた形の文字列にし、
// 生成した文字列を思考・答え・ツール呼び出しに振り分ける。
//
// LLM は「メッセージの配列」を理解するわけではなく、ただの文字列の続きを予測しているだけ。
// 会話をどんな文字列にすれば「アシスタントの返事」を書いてくれるかは、モデルが学習時に見てきた書式で決まる。
// 書式(会話の区切り方・思考の書き方・ツールの説明と呼び出しの書き方)はモデルの系統ごとに違う。
// 同じ Qwen でも、Qwen3 と Qwen3.5 ではツール呼び出しの書き方が違う。
//
// 一方、会話の保存・compact・エージェントループ・ツールの中身は、モデルに関係なく共通にできる。
// そこで、系統ごとに違う部分を Format にまとめ、エンジン(engine パッケージ)だけがそれを使う:
//
//	会話の仕組み ─ Prompt(発言・ツール・思考の ON/OFF。構造のまま)→ Engine ─ Format.BuildPrompt → 文字列 → llama.cpp
//	会話の仕組み ← Output(思考・答え・ツール呼び出し。構造のまま)← Engine ← Format.NewParser ← 生成した文字列
//
// 会話の仕組み(ルートの aibow)は、<|im_start|> や <think> や <tool_call> のような書式の文字列を一切扱わない。
// このパッケージは llama.cpp も会話の保存も知らない(使うのは tool だけ)。書式だけを取り出して試せる。
// 今ある実装は Qwen3.5 用(qwen35.go)だけ。

import (
	"errors"
	"fmt"
	"strings"

	"github.com/secondarykey/aibow/tool"
)

// Message は会話の1件。role は "system" / "user" / "assistant" / "tool"。
//
// Thinking と ToolCalls は assistant の返事だけが持つ。どれをプロンプトに入れるかは書式が決める
// (Qwen3.5 は、最後のユーザーの発言より後の返事 = ツールのループの途中の返事だけ、思考も入れる)。
// tool は Content にツールの結果を持つ。
type Message struct {
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	Thinking  string     `json:"thinking,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

// Prompt はモデルに渡す内容を、書式を当てる前の「構造」のまま持つ。
type Prompt struct {
	Messages []Message
	Tools    []tool.Def // モデルに知らせるツール。空ならツールの説明を入れない
	Think    bool       // 思考させるか
}

// Output は1回の生成の結果を、書式を外した「構造」にしたもの。
//
// 生成の記録(トークン数・速さ・終了の理由)は書式の話ではないので、ここには持たない
// (エンジンが engine.GenerateStats として別に返す)。
type Output struct {
	Thinking  string
	Content   string     // 答え。ツール呼び出しの部分は含まない
	ToolCalls []ToolCall // モデルが書いたツール呼び出し(書いた順)
}

// ToolCall はモデルが書いたツール呼び出し1つ。書式に依存しない形にしたもの。
//
// 名前と引数(tool.Call)に、書式の崩れの情報を足している。崩れは書式の話なので tool パッケージには持たせない。
// 書式が崩れて読み取れなかった呼び出しは、Raw にモデルが書いた生の文字列、Error に理由を持つ。
// 実行はせず、エラーの結果としてモデルに返して立て直させる。次のプロンプトには Raw をそのまま入れ、
// モデルが自分の書いたものを見られるようにする。
//
// tool.Call を埋め込んでいるので、JSON は {"name", "args", "raw", "error"} の平らな形になる
// (tool パッケージに切り出す前と同じ形。保存済みの会話をそのまま読める)。
type ToolCall struct {
	tool.Call
	Raw   string `json:"raw,omitempty"`
	Error string `json:"error,omitempty"`
}

// Format はモデルの系統ごとの書式。
type Format interface {
	// Name は書式の名前(画面とログに出す)
	Name() string
	// BuildPrompt は会話を、モデルが学習時に見てきた書式の1本の文字列にする。
	// 最後は「アシスタントの返事の書き出し」で終える(モデルはその続きを書く)
	BuildPrompt(p Prompt) string
	// NewParser は、生成した文字列を思考・答え・ツール呼び出しに振り分けるものを作る(生成1回ごとに1つ)。
	// tools はモデルに知らせたツール(呼び出しの引数を、定義の型を見て tool.Arg の形にするため)
	NewParser(think bool, tools []tool.Def) OutputParser
}

// OutputParser は、生成中に少しずつ届く文字列を振り分ける。
//
// タグ(</think> など)は何トークンかに分かれて届くことがあるので、タグの書きかけかもしれない末尾は
// 次が届くまで持っておく。
type OutputParser interface {
	// Push は新しく届いた文字列を振り分け、画面に出してよい思考と答えを返す
	Push(text string) (thinking, content string)
	// Flush は持っていた残りを全部返す(生成が終わったとき用)
	Flush() (thinking, content string)
	// Stop は、ここで生成を止めるべきかを返す(例: ツールの結果をモデルが自分で書き始めた)
	Stop() bool
	// Result は生成全体を振り分けた結果を返す(Flush の後に呼ぶ)
	Result() Output
}

// formats は、モデルの種類(GGUF の general.architecture)ごとの書式。
var formats = map[string]func() Format{
	"qwen35": func() Format { return qwen35{} },
}

// For はモデルの種類に合う書式を返す。未対応なら nil。
func For(arch string) Format {
	if f, ok := formats[arch]; ok {
		return f()
	}
	return nil
}

// ErrUnsupported は、書式に対応していない種類のモデルのエラー(errors.Is で見分ける)。
// メーター(トークン数)は数えられないだけで、会話を開くのは止めない(画面は「メーターなし」にする)ために見分ける。
var ErrUnsupported = errors.New("未対応の書式")

// Unsupported は、書式に対応していない種類のモデルで生成しようとしたときのエラー。errors.Is(err, ErrUnsupported) になる。
func Unsupported(arch string) error {
	return unsupportedError{arch}
}

type unsupportedError struct{ arch string }

func (e unsupportedError) Error() string {
	return fmt.Sprintf("このモデルの書式(種類: %s)には未対応です。対応しているのは Qwen3.5(qwen35)だけです。"+
		"書式が合わないまま会話すると、モデルが会話の区切りを理解できずにおかしな返事になるため、生成しません", e.arch)
}

func (e unsupportedError) Is(target error) bool { return target == ErrUnsupported }

// partialSuffix は、s の末尾が tag の先頭の何文字分と一致しているかを返す。
// 例: partialSuffix("考えた</th", "</think>") == 4
//
// OutputParser が、タグの書きかけかもしれない末尾を持っておくのに使う。
func partialSuffix(s, tag string) int {
	for n := min(len(tag)-1, len(s)); n > 0; n-- {
		if strings.HasSuffix(s, tag[:n]) {
			return n
		}
	}
	return 0
}
