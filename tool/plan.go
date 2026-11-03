package tool

// plan.go: 作業の予定を書くツール(update_plan。Claude Code の TodoWrite に当たる。0037 の段3)。
//
// 小さなモデルは、何手もかかる作業の途中で「何をしていたか」「次に何をするか」を見失いやすい。
// 最初に予定をチェックリストとして書かせ、進むたびに書き直させると、自分の書いた予定が履歴に残り、それを見て次の手を選べる。
//
// このツールは何も実行しない。予定はモデル自身の呼び出しの引数として履歴に残るので、結果では繰り返さず短く返す(決定事項 12)。
// 「今の予定」は、会話の仕組みが履歴の最後の update_plan の呼び出しから読む(PlanFromCall。保存の形は変えない。決定事項 13)。
//
// 引数は毎回、予定の全部(決定事項 11)。「2番目を done に」のような差分の指示は、小さなモデルが番号を取り違えやすいため。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// PlanToolName は update_plan の名前(会話の仕組みが履歴から予定を探すときに使う)。
const PlanToolName = "update_plan"

// 予定の項目の状態。
const (
	PlanPending    = "pending"
	PlanInProgress = "in_progress"
	PlanDone       = "done"
)

// PlanItem は予定の項目1つ。
type PlanItem struct {
	Text   string `json:"text"`
	Status string `json:"status"` // pending / in_progress / done
}

// UpdatePlan は予定を書くツール。作業フォルダのある会話でだけ出す(決定事項 14。呼ぶ側が決める)。
func UpdatePlan() Tool { return updatePlan{} }

type updatePlan struct{}

func (updatePlan) Def() Def {
	return Def{
		Name: PlanToolName,
		Description: "Write your plan for a task that takes several steps, as a checklist, and update it as you work. " +
			"Always pass the whole list. Mark the step you are working on in_progress (at most one) and finished steps done.",
		Parameters: json.RawMessage(`{"type": "object", "properties": {"items": {"type": "array", "description": "All steps of the plan, in order.", ` +
			`"items": {"type": "object", "properties": {"text": {"type": "string", "description": "What to do in this step."}, ` +
			`"status": {"type": "string", "enum": ["pending", "in_progress", "done"]}}, "required": ["text", "status"]}}}, "required": ["items"]}`),
	}
}

func (updatePlan) Run(ctx context.Context, args Args) (string, error) {
	items, err := planItems(args["items"])
	if err != nil {
		return "", err
	}
	if len(items) == 0 {
		return "予定を空にしました", nil
	}
	done := 0
	for _, it := range items {
		if it.Status == PlanDone {
			done++
		}
	}
	return fmt.Sprintf("予定を覚えました(%d 件中 %d 件済み)", len(items), done), nil
}

// PlanFromCall は、履歴に残った update_plan の呼び出しから予定を読む。読めなければ ok=false
// (書き損じた呼び出しはエラーの結果になっているので、ふつうは呼ぶ側が成功した呼び出しだけを渡す)。
func PlanFromCall(c Call) (items []PlanItem, ok bool) {
	if c.Name != PlanToolName {
		return nil, false
	}
	args, err := CheckArgs(updatePlan{}.Def(), c.Args)
	if err != nil {
		return nil, false
	}
	items, err = planItems(args["items"])
	return items, err == nil
}

// planItems は、引数 items(CheckArgs が JSON の配列を読んだ値)を予定の項目にする。
// 形の間違いは、次の呼び出しで直せるよう、正しい書き方を添えたエラーにする。
func planItems(v any) ([]PlanItem, error) {
	list, ok := v.([]any)
	if !ok {
		return nil, errors.New(`items は配列で書いてください(例: [{"text": "main.go を読む", "status": "done"}, {"text": "直す", "status": "in_progress"}])`)
	}
	items := make([]PlanItem, 0, len(list))
	working := 0
	for i, x := range list {
		m, ok := x.(map[string]any)
		if !ok {
			return nil, fmt.Errorf(`items の %d 番目は {"text": "…", "status": "pending"} の形で書いてください`, i+1)
		}
		text, _ := m["text"].(string)
		status, _ := m["status"].(string)
		text = strings.TrimSpace(text)
		if text == "" {
			return nil, fmt.Errorf("items の %d 番目の text が空です", i+1)
		}
		switch status {
		case PlanPending, PlanDone:
		case PlanInProgress:
			working++
		default:
			return nil, fmt.Errorf("items の %d 番目の status %q は使えません。pending・in_progress・done のどれかにしてください", i+1, status)
		}
		items = append(items, PlanItem{Text: text, Status: status})
	}
	if working > 1 {
		return nil, fmt.Errorf("in_progress が %d 件あります。今取りかかっている1つだけにしてください", working)
	}
	return items, nil
}
