package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// settings.json に足したばかりの MCP のサーバーは、新しい会話で最初から使わない。
// 一度見たサーバーは、settings.json から消して戻しても前の扱いのまま。
// この仕組みより前の tools.json(seen が無い)なら、今あるサーバーの扱いは変えない
func TestNoteServers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tools.json")
	p, _ := LoadToolPrefs(path)
	if added, err := p.NoteServers(nil); err != nil || added != nil {
		t.Fatalf("サーバーが無ければ何もしない: %v %v", added, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("サーバーが無ければ tools.json を作らない: %v", err)
	}
	added, err := p.NoteServers([]string{"filesystem"})
	if err != nil || !slices.Equal(added, []string{"filesystem"}) || p.GroupDefault("filesystem") {
		t.Fatalf("足したばかりのサーバーは最初から使わない: %v %v", added, err)
	}
	p.update(func(f *toolsFile) { f.NotDefault.Servers = nil }, "") // 使う人がチェックした(プロファイルより前の画面)

	again, _ := LoadToolPrefs(path)
	added, _ = again.NoteServers([]string{"Blender", "filesystem"})
	if !slices.Equal(added, []string{"Blender"}) || !again.GroupDefault("filesystem") || again.GroupDefault("Blender") {
		t.Errorf("見たサーバーはそのまま、足したサーバーだけ使わない: %v", added)
	}
	again.NoteServers([]string{"Blender"}) // filesystem を settings.json から消した
	if added, _ := again.NoteServers([]string{"Blender", "filesystem"}); len(added) != 0 || !again.GroupDefault("filesystem") {
		t.Errorf("消して戻しても前の扱いのまま: %v", added)
	}

	// この仕組みより前のファイル: 今あるサーバーは全部見たことにする(最初から使っていたものを急に外さない)
	old := filepath.Join(t.TempDir(), "tools.json")
	os.WriteFile(old, []byte(`{"notDefault":{"servers":["Blender"]}}`), 0o644)
	q, err := LoadToolPrefs(old)
	if err != nil {
		t.Fatal(err)
	}
	if added, _ := q.NoteServers([]string{"Blender", "filesystem"}); len(added) != 0 || !q.GroupDefault("filesystem") || q.GroupDefault("Blender") {
		t.Errorf("前のファイルは扱いを変えない: %v", added)
	}
	q2, _ := LoadToolPrefs(old)
	if added, _ := q2.NoteServers([]string{"Blender", "filesystem", "fetch"}); !slices.Equal(added, []string{"fetch"}) || q2.GroupDefault("fetch") {
		t.Errorf("次の起動からは、足したサーバーを使わない: %v", added)
	}
}
