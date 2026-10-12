package aibow

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestCalculate(t *testing.T) {
	ok := map[string]string{
		"1234 * 5678":       "7006652",
		"7/2":               "3.5", // Go の規則のままだと 3 になる
		"(1 + 2) * -3":      "-9",
		"1/3":               "0.3333333333333333",
		"0.1 + 0.2":         "0.3", // 定数式は有理数で計算するので誤差が出ない
		"2 * (3 + 4) / 7":   "2",
		"010 + 1":           "11", // 先頭の 0 は 8 進数ではなく 10 進数として読む
		"123456789 * 1e15":  "123456789000000000000000",
		"1 / 3000000000000": "3.3333333333333334e-13", // 有理数から一番近い float64(float64 どうしの割り算とは最後の桁が違う)
	}
	for in, want := range ok {
		if got, err := calculate(in); err != nil || got != want {
			t.Errorf("calculate(%q) = %q, %v(期待: %q)", in, got, err, want)
		}
	}
	bad := []string{"", "1 / 0", "2 ^ 3", "7 % 2", "1234 × 5678", "len(\"abc\")", "x + 1", "0x10", "1 << 3", "\"a\" + \"b\""}
	for _, in := range bad {
		if got, err := calculate(in); err == nil {
			t.Errorf("calculate(%q) = %q はエラーになるはずです", in, got)
		}
	}
}

func TestCurrentTime(t *testing.T) {
	at := time.Date(2026, 10, 13, 6, 4, 5, 0, time.UTC)
	tool := currentTime{now: func() time.Time { return at }}
	got, err := tool.Run(context.Background(), map[string]string{"timezone": "Asia/Tokyo"})
	if err != nil || got != "2026-10-13 15:04:05 Tuesday (Asia/Tokyo, UTC+09:00)" {
		t.Errorf("got %q, %v", got, err)
	}
	if got, err := tool.Run(context.Background(), map[string]string{}); err != nil || !strings.Contains(got, "(local time, UTC") {
		t.Errorf("省略時は PC のタイムゾーン: %q, %v", got, err)
	}
	if _, err := tool.Run(context.Background(), map[string]string{"timezone": "Tokyo"}); err == nil {
		t.Error("IANA の名前でなければエラー")
	}
}

func TestToolArgs(t *testing.T) {
	def := calculator{}.Def()
	if got, err := toolArgs(def, []ToolArg{{"expression", "1+1"}}); err != nil || got["expression"] != "1+1" {
		t.Errorf("got %v, %v", got, err)
	}
	for name, args := range map[string][]ToolArg{
		"必須が無い":  nil,
		"知らない引数": {{"expression", "1"}, {"expr", "1"}},
		"重なった引数": {{"expression", "1"}, {"expression", "2"}},
	} {
		if _, err := toolArgs(def, args); err == nil {
			t.Errorf("%s: エラーになるはずです", name)
		}
	}
	if _, err := toolArgs(currentTime{}.Def(), nil); err != nil {
		t.Errorf("省略できる引数だけなら、無くてもよい: %v", err)
	}
}

// 組み込みのツールの定義が、モデルに見せる形(Python の json.dumps と同じ見た目)で書けること
func TestBuiltinToolDefsAreValidJSON(t *testing.T) {
	for _, tool := range BuiltinTools() {
		d := tool.Def()
		if got := pyJSON(d.Parameters); got != string(d.Parameters) {
			t.Errorf("%s の JSON Schema は、書き直さなくてもモデルに見せる形にしておく:\n%s\n%s", d.Name, d.Parameters, got)
		}
		if _, err := toolArgs(d, nil); err != nil && !strings.Contains(err.Error(), "必須") {
			t.Errorf("%s: %v", d.Name, err)
		}
	}
}
