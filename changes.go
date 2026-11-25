package aibow

// changes.go: 保存したときにログに書く「変えた項目」(0053 の決定事項 21〜24)。
//
// 確定式の画面(プロファイル・会話の設定)は、編集して「保存」を押したときに1回だけ保存する。そのときのログは1行で、
// 「何を変えたか」を画面と同じ言葉で並べる(「プロファイル「開発用」を保存しました(変えた項目: モデル・system)」)。
// 古い値と新しい値を比べて項目の名前にするのは、ここ1か所だけにする(ui も aibow-server も同じ言葉で出すため)。
//
// ログは「後から追いたい出来事」の記録なので、値の中身は書かない。ただし、ほかの人に効く権限に関わる項目
// (使えないツール・サーバーのワークスペース)だけは、何を足して何を外したかを添える(誰がいつ禁止したかを追えるように)。
// 構造体やスライスを %v でそのまま出さない(`{Builtin:false ...}` は読む人には分からない。決定事項 24)。

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// builtinLabel は、組み込みのツールのグループの画面の名前(画面の BUILTIN と同じ)。
const builtinLabel = "組み込み"

// SettingsChanges は、会話の設定を old から next に変えたときに、変えた項目の名前(画面と同じ言葉)を画面の並びで返す。
// 何も変えていなければ空。ワークスペース・worktree は入力欄の上で切り替えるもので、会話の設定の画面の「保存」には入らないので見ない。
func SettingsChanges(old, next ConversationSettings) []string {
	old, next = old.normalized(), next.normalized()
	var out []string
	for _, it := range fixItems {
		if !it.same(old, next) {
			out = append(out, it.label)
		}
	}
	if !maps.Equal(old.ToolPolicies, next.ToolPolicies) {
		out = append(out, "ツール")
	}
	return out
}

// ProfileChanges は、プロファイルを old から next に変えたときに、変えた項目を画面の言葉で返す(名前・値・決まり)。
// 使えないツールとサーバーのワークスペースは、何を変えたかを括弧で添える。
func ProfileChanges(old, next Profile) []string {
	var out []string
	if old.Name != next.Name {
		out = append(out, fmt.Sprintf("名前(「%s」から)", old.Name))
	}
	out = append(out, SettingsChanges(old.Values.settings(), next.Values.settings())...)
	if !sameSet(old.Rules.Fixed, next.Rules.Fixed) {
		out = append(out, "固定")
	}
	if d := Diff(offNames(old.Rules.Off), offNames(next.Rules.Off)); d != "" {
		out = append(out, "使えないツール("+d+")")
	}
	if old.Rules.Workspace != next.Rules.Workspace {
		out = append(out, fmt.Sprintf("サーバーのワークスペース(%s → %s)", cmp.Or(old.Rules.Workspace, "なし"), cmp.Or(next.Rules.Workspace, "なし")))
	}
	return out
}

// offNames は、使えないツールを画面の名前の並びにする(グループは「組み込み」・サーバーの名前、ツールはモデルに見せる名前)。
func offNames(o ProfileOff) []string {
	var names []string
	if o.Builtin {
		names = append(names, builtinLabel)
	}
	names = append(names, o.Servers...)
	return append(names, o.Tools...)
}

// Diff は、名前の集まりを old から next にしたときの差を「a・b を追加、c を外す」の形で返す(同じなら空)。
// 使えないツール・確認しないツールのように、足したものと外したものを読めればよい項目に使う。
func Diff(old, next []string) string {
	var added, removed []string
	for _, n := range next {
		if !slices.Contains(old, n) {
			added = append(added, n)
		}
	}
	for _, n := range old {
		if !slices.Contains(next, n) {
			removed = append(removed, n)
		}
	}
	slices.Sort(added)
	slices.Sort(removed)
	var parts []string
	if len(added) > 0 {
		parts = append(parts, strings.Join(added, "・")+" を追加")
	}
	if len(removed) > 0 {
		parts = append(parts, strings.Join(removed, "・")+" を外す")
	}
	return strings.Join(parts, "、")
}

// ChangedItems は、変えた項目の並びをログの括弧の中の形にする(「変えた項目: モデル・system」)。何も変えていなければ「変えた項目なし」。
func ChangedItems(items []string) string {
	if len(items) == 0 {
		return "変えた項目なし"
	}
	return "変えた項目: " + strings.Join(items, "・")
}
