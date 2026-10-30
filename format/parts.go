package format

// parts.go: 2つ以上の書式で使う部品(0026 の決定事項 12)。
//
// Qwen3.5 だけのときは qwen35.go の中にあったもの。Gemma 4 を書いて、どちらも同じものを使うと分かったところだけを出した
// (先に汎用の枠を作らない。使う相手が現れたら分ける)。
//
//	partialSuffix / partialSuffixAny … タグの書きかけかもしれない末尾を持っておく(タグは何トークンかに分かれて届く)
//	insideCodeFence                  … コードブロックの中に書かれたタグを、呼び出しと見ないため
//	brokenCall                       … 書式が崩れた呼び出し(正しい書き方の例は書式ごとに違う)

import (
	"strings"

	"github.com/secondarykey/aibow/tool"
)

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

// partialSuffixAny は、partialSuffix の、待っているタグが複数あるとき(いちばん長く一致するもの)。
// Gemma 4 の答えの中では、思考の始まり(<|channel>)と呼び出しの始まり(<|tool_call>)の両方を待つ。
func partialSuffixAny(s string, tags ...string) int {
	n := 0
	for _, t := range tags {
		n = max(n, partialSuffix(s, t))
	}
	return n
}

// insideCodeFence は、text の終わりがコードブロック(``` か ~~~ の行で囲んだところ)の中かを返す。
// 囲みの行(字下げ 3 文字まで)が出るたびに、中と外が入れ替わる。
//
// 仕組みを説明する答えでは、呼び出しのタグがコードブロックの中に書かれることがある。それを呼び出しと見ると、
// 答えがそこで切れ、「読み取れない呼び出し」のエラーを返されたモデルが同じ答えを書き直し続ける(2026-10-16 に Qwen3.5 で起きた)。
func insideCodeFence(text string) bool {
	inside := false
	for line := range strings.SplitSeq(text, "\n") {
		t := strings.TrimLeft(line, " ")
		if len(line)-len(t) <= 3 && (strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~")) {
			inside = !inside
		}
	}
	return inside
}

// brokenCall は読み取れなかった呼び出し。name は読み取れた関数名(読めなければ空)、help はその書式の正しい書き方の例。
// 実行はせず、エラーの結果としてモデルに返して立て直させる。次のプロンプトには raw(モデルが書いたまま)を入れる。
func brokenCall(raw, name, reason, help string) ToolCall {
	return ToolCall{Call: tool.Call{Name: name}, Raw: raw, Error: "書式が崩れていて呼び出しを読み取れません(" + reason + ")。\n" + help}
}
