package format

// granite42.go: IBM の Granite 4.2 の書式(format.go の Format の実装。0029 の段3)。
//
// Ollama の granite4.2:3b の GGUF の tokenizer.chat_template(Hugging Face の ibm-granite/granite-4.2-3b と同じもの)を、
// aibow が渡す会話の形で通る部分だけ Go で書き直したもの。テンプレートを jinja2 で通した文字列と比べてテストしている(testdata/granite42/)。
//
// 同じ Granite でも 4.0・4.1(granite.go)とは別の書式で、区切りは ChatML、ツールの説明と呼び出しは Qwen3.5 と同じ形:
//
//	<|im_start|>system
//	あなたは…                                ← システムの文が先(Qwen3.5 はツールの説明が先)
//
//	# Tools …(Qwen3.5 と同じ文。ツールの定義の行は {"type": "function", …} で包まない)<|im_end|>
//	<|im_start|>user
//	1234 × 5678 は?<|im_end|>
//	<|im_start|>assistant
//	<think>
//	計算ツールを使おう。
//	</think>
//	<tool_call>
//	<function=calculate>
//	<parameter=expression>
//	1234 * 5678
//	</parameter>
//	</function>
//	</tool_call>
//	<|im_end|>                               ← 呼び出しの後ろに改行を置いてから閉じる
//	<|im_start|>user
//	<tool_response>
//	7006652
//	</tool_response>
//	<|im_end|>
//	<|im_start|>assistant
//	<think>
//
// Qwen3.5 と比べると:
//
//   - システムの発言はいつも作る(システムの文もツールも無ければ中身は空)
//   - 空の思考は <think></think>(改行なし)。思考を切ると、返事の書き出しが <think></think> になる
//   - 最後のユーザーの発言より前の返事は、思考を <think></think> に置き換える(テンプレートの truncate_history_thinking)。
//     思考を消すのは Qwen3.5 と同じだが、空の思考の印を残す
//   - 発言の中身・思考・ツールの結果は、テンプレートが削らないところは削らずに書く(返事の全体は前後の空白を削る)
//
// bos は書かない(テンプレートも書かず、llama.cpp も足さない。0029 の段1)。
// テンプレートの low_effort(思考を短くする)は使わない。

import (
	"strings"

	"github.com/secondarykey/aibow/tool"
)

// granite42 は Granite 4.2 の書式。GGUF の種類が granite で、テンプレートに <|im_start|> と <function= があるモデルに使う(0029 の決定事項 7)。
type granite42 struct{}

func (granite42) Name() string { return "Granite 4.2" }

// granite42EmptyThink は空の思考の印(改行を挟まない)。
const granite42EmptyThink = "<think></think>"

// BuildPrompt は会話を Granite 4.2 の書式の1本の文字列にする。
func (granite42) BuildPrompt(p Prompt) string {
	var b strings.Builder
	msgs := p.Messages

	// 1. システムの発言(いつも作る)。システムの文が先で、ツールの説明が後
	system := ""
	if len(msgs) > 0 && msgs[0].Role == "system" {
		system = msgs[0].Content
		msgs = msgs[1:]
	}
	b.WriteString("<|im_start|>system\n" + system)
	if len(p.Tools) > 0 {
		if system != "" {
			b.WriteString("\n\n")
		}
		b.WriteString(qwen35ToolsIntro)
		for _, t := range p.Tools {
			b.WriteString("\n" + toolDefJSON(t))
		}
		b.WriteString(qwen35ToolsGuide)
	}
	b.WriteString("<|im_end|>\n")

	// 2. 発言。最後のユーザーの発言より前の返事は、思考を空の印に置き換える
	lastQuery := -1
	for i, m := range msgs {
		if m.Role == "user" {
			lastQuery = i
		}
	}
	for i, m := range msgs {
		switch m.Role {
		case "assistant":
			writeGranite42Reply(&b, m, i < lastQuery, p.Tools)
		case "tool":
			// ツールの結果は user の発言の形で返す。続けて複数あるときは、1つの user の中に並べる
			if i > 0 && msgs[i-1].Role != "tool" {
				b.WriteString("<|im_start|>user\n")
			}
			b.WriteString("<tool_response>\n" + m.Content + "\n</tool_response>\n")
			if i == len(msgs)-1 || msgs[i+1].Role != "tool" {
				b.WriteString("<|im_end|>\n")
			}
		default: // user と、2つ目からのシステムの発言
			b.WriteString("<|im_start|>" + m.Role + "\n" + m.Content + "<|im_end|>\n")
		}
	}

	// 3. 返事の書き出し。思考 ON なら「<think>\n」まで、OFF なら空の思考まで書いておく(Qwen3.5 と同じ考え方)
	b.WriteString("<|im_start|>assistant\n")
	if p.Think {
		b.WriteString("<think>\n")
	} else {
		b.WriteString(granite42EmptyThink)
	}
	return b.String()
}

// writeGranite42Reply は返事を1つ書く。old は、最後のユーザーの発言より前の返事か(思考を空の印に置き換える)。
//
// テンプレートは、思考と答えを1本の文字列(<think>\n思考\n</think>\n答え。思考が無ければ <think></think>答え)にしてから、
// 古い返事なら </think> より後ろだけを残して頭に <think></think> を付け直す。同じ手順で書く(答えの文に <think> を書いた返事も同じになるように)。
func writeGranite42Reply(b *strings.Builder, m Message, old bool, tools []tool.Def) {
	text := m.Content
	switch {
	case strings.TrimSpace(m.Thinking) != "":
		text = "<think>\n" + m.Thinking + "\n</think>\n" + m.Content
	case !strings.Contains(text, "<think>") && !strings.Contains(text, thinkEnd):
		text = granite42EmptyThink + text
	}

	b.WriteString("<|im_start|>assistant\n")
	if len(m.ToolCalls) == 0 {
		if old && strings.Contains(text, "<think>") && strings.Contains(text, thinkEnd) {
			text = granite42EmptyThink + afterLast(text, thinkEnd)
		}
		b.WriteString(strings.TrimSpace(text) + "<|im_end|>\n")
		return
	}

	if old {
		if strings.Contains(text, thinkEnd) {
			text = afterLast(text, thinkEnd)
		} else if i := strings.Index(text, "<think>"); i >= 0 {
			text = text[:i] // 閉じていない思考は捨てる
		}
		text = granite42EmptyThink + strings.TrimSpace(text)
	}
	b.WriteString(strings.TrimSpace(text) + "\n")
	for _, c := range m.ToolCalls {
		if c.Raw != "" {
			// 書式が崩れて読み取れなかった呼び出しは、モデルが書いたままを入れる(ほかの書式と同じ)
			b.WriteString(c.Raw + "\n")
			continue
		}
		types := tool.ArgTypesFor(tools, c.Name)
		b.WriteString("<tool_call>\n<function=" + c.Name + ">\n")
		for _, a := range c.Args {
			b.WriteString("<parameter=" + a.Name + ">\n" + qwen35ArgText(types[a.Name], a.Value) + "\n</parameter>\n")
		}
		b.WriteString("</function>\n</tool_call>\n")
	}
	b.WriteString("<|im_end|>\n")
}

// afterLast は、s の中の最後の sep より後ろを返す(Python の s.split(sep)[-1])。
func afterLast(s, sep string) string {
	return s[strings.LastIndex(s, sep)+len(sep):]
}

// ---- 出力の読み取り -----------------------------------------------------------------
//
// 返事の形は Qwen3.5 と同じ(思考 → </think> → 答え → 行の頭の <tool_call>。中身も Qwen3.5 と同じ XML 風)なので、
// Qwen3.5 と同じ部品で読む(parts.go の toolCallParser と parseQwen35Calls)。
// 違うのは思考の終わりの改行だけ:モデルは「思考\n</think>」と書き、テンプレートは思考を削らずに「<think>\n思考\n</think>」と書き戻すので、
// 思考の後ろの改行1つは思考に入れない(書き戻したときにモデルが書いた文字と同じになり、KV キャッシュを使い回せる。Gemma 4 と同じ)。

func (granite42) NewParser(p Prompt) OutputParser {
	return &granite42Parser{toolCallParser{split: newThinkSplitter(p.Think), tools: p.Tools, parse: parseQwen35Calls}}
}

type granite42Parser struct {
	toolCallParser
}

func (q *granite42Parser) Result() Output {
	out := q.toolCallParser.Result()
	out.Thinking = strings.TrimSuffix(out.Thinking, "\n")
	return out
}
