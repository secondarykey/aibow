package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 画面で変えた鍵だけを差し替え、ほかの鍵は並びも値も残す。書く前の中身は .bak に残る(0061 の決定事項 8)
func TestSaveServerFileKeepsOtherKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.json")
	before := "{\n  \"port\": 8090,\n  \"parallel\": 1,\n  \"model\": \"qwen3.5:4b\"\n}\n"
	os.WriteFile(path, []byte(before), 0o644)

	two, ctx := 2, 65536
	if err := SaveServerFile(path, ServerEdit{Parallel: &two, Ctx: &ctx}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	want := "{\n  \"port\": 8090,\n  \"parallel\": 2,\n  \"model\": \"qwen3.5:4b\",\n  \"ctx\": 65536\n}\n"
	if string(data) != want {
		t.Errorf("書いた中身:\n%s\nほしい中身:\n%s", data, want)
	}
	if bak, _ := os.ReadFile(path + ".bak"); string(bak) != before {
		t.Errorf(".bak は書く前の中身のはず: %s", bak)
	}
	// 書いたファイルは起動のときと同じ読み方で読める
	f, found, err := LoadServerFile(path)
	if err != nil || !found || f.Parallel != 2 || f.Ctx != 65536 || f.Port != 8090 || f.Model != "qwen3.5:4b" {
		t.Errorf("読み直し: %+v %v %v", f, found, err)
	}
	// 一時ファイルは残らない
	if m, _ := filepath.Glob(filepath.Join(dir, "*.tmp")); len(m) > 0 {
		t.Errorf("一時ファイルが残っている: %v", m)
	}
}

// ワークスペースは全部を差し替え、空なら鍵を消す。フォルダが無ければ書かない(0065)
func TestSaveServerFileWorkspaces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.json")
	os.WriteFile(path, []byte(`{"port": 8090}`), 0o644)
	docs := filepath.Join(dir, "docs")
	os.Mkdir(docs, 0o755)

	ws := []ServerWorkspace{{Name: "社内規程", Folders: []WorkspaceFolder{{Path: docs, Name: "docs", Main: true, Writable: true}}}}
	if err := SaveServerFile(path, ServerEdit{SetWorkspaces: true, Workspaces: ws}); err != nil {
		t.Fatal(err)
	}
	f, _, err := LoadServerFile(path)
	if err != nil || len(f.Workspaces) != 1 || f.Workspaces[0].Name != "社内規程" || f.Workspaces[0].Folders[0].Path != docs || f.Port != 8090 {
		t.Errorf("足したあと: %+v %v", f, err)
	}
	if err := SaveServerFile(path, ServerEdit{SetWorkspaces: true}); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); strings.Contains(string(data), "workspaces") {
		t.Errorf("空なら workspaces の鍵を消すはず: %s", data)
	}
	bad := []ServerWorkspace{{Name: "無い", Folders: []WorkspaceFolder{{Path: filepath.Join(dir, "none"), Name: "none", Main: true}}}}
	err = SaveServerFile(path, ServerEdit{SetWorkspaces: true, Workspaces: bad})
	if err == nil || !strings.Contains(err.Error(), "ありません") {
		t.Errorf("無いフォルダは書かないはず: %v", err)
	}
}

// 前の形の folders は、管理の画面で最初に保存したときに workspaces に書き直して消す。ほかの鍵はそのまま(0065 の決定事項 10)
func TestSaveServerFileMigratesFolders(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.json")
	docs := filepath.Join(dir, "docs")
	os.Mkdir(docs, 0o755)
	raw, _ := json.Marshal(map[string]string{"docs": docs})
	before := `{"port": 8090, "folders": ` + string(raw) + `, "model": "qwen3.5:4b"}`
	os.WriteFile(path, []byte(before), 0o644)

	f, _, err := LoadServerFile(path)
	if err != nil {
		t.Fatal(err)
	}
	list, migrated := f.ServerWorkspaces()
	if !migrated {
		t.Fatal("folders から移したはず")
	}
	if err := SaveServerFile(path, ServerEdit{SetWorkspaces: true, Workspaces: list}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), `"folders": {`) || !strings.Contains(string(data), `"workspaces"`) || !strings.Contains(string(data), `"model"`) {
		t.Errorf("folders を消して workspaces を書き、ほかの鍵は残すはず: %s", data)
	}
	if bak, _ := os.ReadFile(path + ".bak"); string(bak) != before {
		t.Errorf(".bak は書く前の中身のはず: %s", bak)
	}
	f, _, err = LoadServerFile(path)
	if list2, m := f.ServerWorkspaces(); err != nil || m || len(list2) != 1 || list2[0].Name != "docs" || !list2[0].Folders[0].Writable {
		t.Errorf("読み直し: %+v %v %v", list2, m, err)
	}
}

// 壊れたファイル(JSON として読めない・知らない欄がある)には書かない。値の範囲の外も書かない
func TestSaveServerFileRefusesBroken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.json")
	one := 1
	for _, broken := range []string{`{"port": 8090,`, `{"prot": 8090}`, `[1]`} {
		os.WriteFile(path, []byte(broken), 0o644)
		err := SaveServerFile(path, ServerEdit{Parallel: &one})
		if err == nil || !strings.Contains(err.Error(), "直してから") {
			t.Errorf("%s: 断るはず: %v", broken, err)
		}
		if data, _ := os.ReadFile(path); string(data) != broken {
			t.Errorf("%s: 書き換えてはいけない: %s", broken, data)
		}
	}
	os.WriteFile(path, []byte(`{}`), 0o644)
	zero := 0
	if err := SaveServerFile(path, ServerEdit{Parallel: &zero}); err == nil {
		t.Error("並列数 0 は断るはず")
	}
}

// ファイルが無ければ、書いた項目だけで作る(.bak は作らない。0061 の決定事項 7)
func TestSaveServerFileCreates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "aibow", "server.json")
	two := 2
	if err := SaveServerFile(path, ServerEdit{Parallel: &two}); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != "{\n  \"parallel\": 2\n}\n" {
		t.Errorf("作った中身: %s", data)
	}
	if _, err := os.Stat(path + ".bak"); err == nil {
		t.Error("無かったファイルの .bak は作らない")
	}
}
