package format

// parts.go: 2つ以上の書式で使う部品(0026 の決定事項 12)。
//
// Qwen3.5 だけのときは qwen35.go の中にあったもの。Gemma 4 を書いて、どちらも同じものを使うと分かったところだけを出した
// (先に汎用の枠を作らない。使う相手が現れたら分ける)。
//
//	partialSuffix / partialSuffixAny … タグの書きかけかもしれない末尾を持っておく(タグは何トークンかに分かれて届く)
//	insideCodeFence                  … コードブロックの中に書かれたタグを、呼び出しと見ないため
//	brokenCall                       … 書式が崩れた呼び出し(正しい書き方の例は書式ごとに違う)
//	toolCallParser / toolCallBlocks  … 行の頭の <tool_call> から後ろを呼び出しの部分として取っておく(Qwen3.5・Granite 4。0029)
//	toolDefJSON                      … ツールの定義の1行の JSON(Qwen3.5・Granite 4)

import (
	"strings"
	"unicode"

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

// toolDefJSON はツールの定義を、テンプレートの tojson と同じ1行の JSON にする({"name": …, "description": …, "parameters": …})。
// Qwen3.5 と Granite 4 は、これを {"type": "function", "function": …} で包んで1行ずつ並べる。
func toolDefJSON(t tool.Def) string {
	return `{"name": ` + pyJSON(jsonString(t.Name)) + `, "description": ` + pyJSON(jsonString(t.Description)) +
		`, "parameters": ` + pyJSON(t.Parameters) + `}`
}

// ---- <tool_call> で囲んだ呼び出しの読み取り(Qwen3.5・Granite 4) --------------------------
//
// 答えの中に <tool_call> が出てきたら、そこから先は「呼び出しの部分」として取っておき、生成が終わってから読み取る。
// タグは何トークンかに分かれて届くことがあるので、"<tool_call>" の書きかけかもしれない末尾は、次が届くまで出さずに持っておく
// (思考の振り分けと同じ考え方)。<tool_call> の中身の書き方は書式ごとに違う(Qwen3.5 は XML 風、Granite 4 は JSON)ので、
// 読み取りは parse に任せる。
//
// ただし、呼び出しと見るのは「行の頭」にあって「コードブロック(``` / ~~~)の外」にある <tool_call> だけ。
// テンプレートは呼び出しを必ず行の頭から書く。文の中の `<tool_call>` は、仕組みを説明している答えなどで書かれたもので、
// 呼び出しではない。これも呼び出しと見ると、答えがそこで切れ、「読み取れない呼び出し」のエラーを返されたモデルが
// 同じ答えを書き直し続ける(2026-10-16 に Qwen3.5 で起きた)。
const (
	toolCallStart     = "<tool_call>"
	toolCallEnd       = "</tool_call>"
	toolResponseStart = "<tool_response>"
)

// toolCallParser は生成した文字列を思考・答え・呼び出しの部分に振り分け、全体も覚えておく(Result 用)。
type toolCallParser struct {
	split *thinkSplitter // 思考しない書式(Granite 4)は newThinkSplitter(false) で、全部が答えになる
	tools []tool.Def     // 引数の値を tool.Arg の形にするときに、型を見る
	// parse は呼び出しの部分(<tool_call> から後ろ。モデルが書き始めた結果は除いたもの)から、呼び出しを全部読み取る
	parse             func(calls string, tools []tool.Def) []ToolCall
	thinking, content strings.Builder
	held              string          // 答えのうち、"<tool_call>" の書きかけかもしれないので保留している文字列
	calls             strings.Builder // "<tool_call>" から後ろ(呼び出しの部分)。空なら、まだ呼び出しは出ていない
}

func (q *toolCallParser) Push(text string) (thinking, content string) {
	thinking, content = q.split.Push(text)
	return q.keep(thinking, q.pushContent(content, false))
}

func (q *toolCallParser) Flush() (thinking, content string) {
	thinking, content = q.split.Flush()
	return q.keep(thinking, q.pushContent(content, true))
}

// pushContent は答えの文字列から、"<tool_call>" より前の部分だけを返す。後ろは calls に取っておく。
func (q *toolCallParser) pushContent(text string, last bool) string {
	if q.calls.Len() > 0 {
		q.calls.WriteString(text)
		return ""
	}
	q.held += text
	out := ""
	for {
		i := strings.Index(q.held, toolCallStart)
		if i < 0 {
			break
		}
		if isCallPosition(q.content.String() + out + q.held[:i]) {
			out += q.held[:i]
			q.calls.WriteString(q.held[i:])
			q.held = ""
			return out
		}
		// 文の中・コードブロックの中の <tool_call> は、答えの文字としてそのまま出す
		n := i + len(toolCallStart)
		out += q.held[:n]
		q.held = q.held[n:]
	}
	keep := 0
	if !last {
		keep = partialSuffix(q.held, toolCallStart)
	}
	out += q.held[:len(q.held)-keep]
	q.held = q.held[len(q.held)-keep:]
	return out
}

// isCallPosition は、答えの before の続きに出てきた <tool_call> を、呼び出しの始まりと見るかを返す。
// 行の頭(答えの最初か、改行の直後)で、コードブロックの外にあるときだけ呼び出しと見る。
func isCallPosition(before string) bool {
	if before != "" && !strings.HasSuffix(before, "\n") {
		return false
	}
	return !insideCodeFence(before)
}

func (q *toolCallParser) keep(thinking, content string) (string, string) {
	q.thinking.WriteString(thinking)
	q.content.WriteString(content)
	return thinking, content
}

// Stop は、呼び出しの後ろでモデルが <tool_response> を書き始めたら true を返す。
// 結果を書くのはアプリの役目。モデルに続きを書かせると、ありもしない結果を想像して答えてしまう。
// (<tool_response> は発言の終わりのトークンではないので、llama.cpp は止めない。0029 の段1)
func (q *toolCallParser) Stop() bool {
	return strings.Contains(q.calls.String(), toolResponseStart)
}

func (q *toolCallParser) Result() Output {
	out := Output{Thinking: q.thinking.String(), Content: q.content.String()}
	if q.calls.Len() > 0 {
		// 呼び出しの前の空行は書式の区切りなので、答えからは落とす
		out.Content = strings.TrimRightFunc(out.Content, unicode.IsSpace)
		calls := q.calls.String()
		// モデルが自分で書き始めた結果は捨てる(Stop で止めたときに、書きかけで残っている)
		if i := strings.Index(calls, toolResponseStart); i >= 0 {
			calls = calls[:i]
		}
		out.ToolCalls = q.parse(calls, q.tools)
	}
	return out
}

// toolCallBlocks は呼び出しの部分から、<tool_call>…</tool_call> を書いた順に切り出す。
// 間や後ろに書かれた文は捨てる。閉じないまま終わった呼び出しは open に返す(返事の上限・停止など)。
func toolCallBlocks(s string) (blocks []string, open string) {
	for {
		i := strings.Index(s, toolCallStart)
		if i < 0 {
			return blocks, ""
		}
		s = s[i:]
		end := strings.Index(s, toolCallEnd)
		if end < 0 {
			return blocks, strings.TrimRightFunc(s, unicode.IsSpace)
		}
		blocks = append(blocks, s[:end+len(toolCallEnd)])
		s = s[end+len(toolCallEnd):]
	}
}
