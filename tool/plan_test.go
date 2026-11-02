package tool

import (
	"slices"
	"strings"
	"testing"
)

func TestUpdatePlan(t *testing.T) {
	tl := UpdatePlan()
	items := `[{"text": "main.go を読む", "status": "done"}, {"text": "直す", "status": "in_progress"}, {"text": "テストする", "status": "pending"}]`
	if got, err := run(t, tl, "items", items); err != nil || got != "予定を覚えました(3 件中 1 件済み)" {
		t.Errorf("結果: %q, %v", got, err)
	}
	if got, err := run(t, tl, "items", "[]"); err != nil || got != "予定を空にしました" {
		t.Errorf("空: %q, %v", got, err)
	}
	// 間違いは、直し方の分かるエラー(モデルが次の呼び出しで直す)
	for in, want := range map[string]string{
		`[{"text": "a", "status": "in_progress"}, {"text": "b", "status": "in_progress"}]`: "1つだけにしてください",
		`[{"text": "a", "status": "completed"}]`:                                           "pending・in_progress・done",
		`[{"text": " ", "status": "done"}]`:                                                "text が空",
		`["a", "b"]`:                                                                       `{"text": "…", "status": "pending"}`,
		`"a"`:                                                                              "配列",
	} {
		if got, err := run(t, tl, "items", in); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %q, %v(期待: %s)", in, got, err, want)
		}
	}
}

// 履歴に残った呼び出し(書式から読んだ形。値は JSON の文字)から予定を読む
func TestPlanFromCall(t *testing.T) {
	c := Call{Name: PlanToolName, Args: []Arg{{Name: "items", Value: `[{"text": "読む", "status": "done"}, {"text": "直す", "status": "pending"}]`}}}
	items, ok := PlanFromCall(c)
	if !ok || !slices.Equal(items, []PlanItem{{"読む", PlanDone}, {"直す", PlanPending}}) {
		t.Errorf("予定: %+v, %v", items, ok)
	}
	for _, bad := range []Call{
		{Name: "read_file", Args: c.Args},
		{Name: PlanToolName, Args: []Arg{{Name: "items", Value: `[{"text": "a", "status": "x"}]`}}},
		{Name: PlanToolName},
	} {
		if items, ok := PlanFromCall(bad); ok {
			t.Errorf("%+v: 読めないはず: %+v", bad, items)
		}
	}
}
