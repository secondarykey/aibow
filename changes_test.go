package aibow

import (
	"slices"
	"strings"
	"testing"
)

// 保存のログに書く「変えた項目」が、画面の言葉で並び、内部の形(`{` や `[`)が出ないこと(0053 の完了の条件 6)。
func TestProfileChanges(t *testing.T) {
	old := Profile{Name: "開発用", Values: ProfileValues{Model: "a", System: "x", Compact: CompactSummary, HistoryTokens: 8192, ReplyTokens: 4096}}
	next := old
	next.Values.Model = "b"
	next.Values.System = "y"
	next.Rules.Off = ProfileOff{Builtin: true, Tools: []string{"run_command"}}
	next.Rules.Workspace = "docs"

	got := ProfileChanges(old, next)
	want := []string{"モデル", "システムプロンプト", "使えないツール(run_command・組み込み を追加)", "サーバーのワークスペース(なし → docs)"}
	if !slices.Equal(got, want) {
		t.Fatalf("変えた項目 = %q, want %q", got, want)
	}
	line := ChangedItems(got)
	if strings.ContainsAny(line, "{[") {
		t.Errorf("内部の形が出ている: %s", line)
	}

	// 外したほうも書く。名前を変えたら元の名前を添える
	back := next
	back.Name = "開発"
	back.Rules.Off = ProfileOff{Tools: []string{"write_file"}}
	got = ProfileChanges(next, back)
	want = []string{"名前(「開発用」から)", "使えないツール(write_file を追加、run_command・組み込み を外す)"}
	if !slices.Equal(got, want) {
		t.Fatalf("変えた項目 = %q, want %q", got, want)
	}

	if got := ChangedItems(ProfileChanges(old, old)); got != "変えた項目なし" {
		t.Errorf("同じプロファイル: %q", got)
	}
}

func TestSettingsChanges(t *testing.T) {
	old := ConversationSettings{Model: "a", Temperature: 0.7, Compact: CompactSummary}
	next := old
	next.Temperature = 0.2
	next.MCPServers = []string{"blender"}
	next.Workspace = "ws" // ワークスペースは会話の設定の画面の「保存」に入らないので数えない
	got := SettingsChanges(old, next)
	want := []string{"temperature", "使うツールのグループ"}
	if !slices.Equal(got, want) {
		t.Fatalf("変えた項目 = %q, want %q", got, want)
	}
}
