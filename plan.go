package aibow

// plan.go: compact で捨てた・要約した部分にあった作業の予定(update_plan)を、モデルに渡し続ける(0037 の段3。決定事項 13)。
//
// 予定は別に保存しない。モデルが書いた update_plan の呼び出しの引数が履歴に残っているので、それを読む(tool.PlanFromCall)。
// ふだんは呼び出しそのものがプロンプトに入っているので、何も足さない。
// 困るのは compact で、その呼び出しを含む古い発言をモデルに渡さなくなったとき。長い作業ほど compact が起きやすく、
// そこで予定まで消えると、予定を書かせた意味が無くなる。そこで、渡さない部分(history[:first])の最後の予定を、
// システムの文の最後(要約があればその後ろ。truncate なら残した発言の前)に足す。
//
// 足すのは「渡さない部分の最後の予定」で、「会話全体の最後の予定」ではない。
// 渡す部分でモデルが予定を書き直したら、それは履歴の中に見えていて、足した予定より後ろにあるので、新しいほうだと分かる。
// 会話全体の最後の予定にすると、モデルが予定を書き直すたびにシステムの文が変わり、KV キャッシュがその先頭から使えなくなる。
// 渡さない部分が変わる(compact が進む)までは同じ文なので、毎回の送信で文が変わることはない。

import (
	"strings"

	"github.com/secondarykey/aibow/format"
	"github.com/secondarykey/aibow/tool"
)

// lastPlan は、history の中で最後に成功した update_plan の予定。無ければ nil。
// エラーになった呼び出し(in_progress が2つ、など)は、モデルが書き直しているはずなので見ない。
// MCP のサーバーの同じ名前のツール(aibow-mcp をつないだときの aibow__update_plan など)は、名前が違うので入らない。
func lastPlan(history []StoredMessage) []tool.PlanItem {
	for i := len(history) - 1; i >= 0; i-- {
		r := history[i].ToolResult
		if r == nil || r.Error || r.Server != "" || r.Call.Name != tool.PlanToolName {
			continue
		}
		if items, ok := tool.PlanFromCall(r.Call.Call); ok {
			return items
		}
	}
	return nil
}

// planNote は、モデルに渡す予定の文(英語。ツールの指示文に合わせる)。予定が無ければ空。
// 項目の印は update_plan の status の値そのまま(モデルが次に書き直すときに、そのまま写せるように)。
func planNote(items []tool.PlanItem) string {
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Current plan (from update_plan in the earlier part of this conversation, which is no longer shown):")
	for _, it := range items {
		b.WriteString("\n- [" + it.Status + "] " + it.Text)
	}
	return b.String()
}

// withPlan は、モデルに渡す発言の並び msgs のシステムの文の最後に note を足す(システムの文が無ければ作る)。
func withPlan(msgs []format.Message, note string) []format.Message {
	if note == "" {
		return msgs
	}
	if len(msgs) > 0 && msgs[0].Role == "system" {
		msgs[0].Content += "\n\n" + note
		return msgs
	}
	return append([]format.Message{{Role: "system", Content: note}}, msgs...)
}
