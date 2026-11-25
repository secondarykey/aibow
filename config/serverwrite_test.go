package config

import (
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

// 資料のフォルダは全部を差し替え、空なら鍵を消す。フォルダが無ければ書かない
func TestSaveServerFileFolders(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.json")
	os.WriteFile(path, []byte(`{"port": 8090}`), 0o644)
	docs := filepath.Join(dir, "docs")
	os.Mkdir(docs, 0o755)

	if err := SaveServerFile(path, ServerEdit{SetFolders: true, Folders: map[string]string{"社内規程": docs}}); err != nil {
		t.Fatal(err)
	}
	if f, _, err := LoadServerFile(path); err != nil || f.Folders["社内規程"] != docs || f.Port != 8090 {
		t.Errorf("足したあと: %+v %v", f, err)
	}
	if err := SaveServerFile(path, ServerEdit{SetFolders: true}); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); strings.Contains(string(data), "folders") {
		t.Errorf("空なら folders の鍵を消すはず: %s", data)
	}
	err := SaveServerFile(path, ServerEdit{SetFolders: true, Folders: map[string]string{"無い": filepath.Join(dir, "none")}})
	if err == nil || !strings.Contains(err.Error(), "フォルダがありません") {
		t.Errorf("無いフォルダは書かないはず: %v", err)
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
