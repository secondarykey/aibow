package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// logLevel の書き間違いはエラー(書ける値は log.LevelNames。trace も書ける。0052)
func TestServerFileLogLevel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.json")
	os.WriteFile(path, []byte(`{"logLevel": "verbose"}`), 0o644)
	if _, _, err := LoadServerFile(path); err == nil || !strings.Contains(err.Error(), "logLevel") {
		t.Errorf("書き間違い: %v", err)
	}
	for _, name := range []string{"notice", "trace"} {
		os.WriteFile(path, []byte(`{"logLevel": "`+name+`"}`), 0o644)
		if f, found, err := LoadServerFile(path); err != nil || !found || f.LogLevel != name {
			t.Errorf("%s: %+v %v %v", name, f, found, err)
		}
	}
}

// 無ければ found が false で、エラーにしない。知らない欄はエラー
func TestServerFileMissingAndUnknown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.json")
	if _, found, err := LoadServerFile(path); found || err != nil {
		t.Errorf("無いとき: %v %v", found, err)
	}
	// 知らない欄の誤り(encoding/json の unknown field)は行が分からないので、欄の名前を疑うよう伝える
	os.WriteFile(path, []byte(`{"prot": 8089}`), 0o644)
	if _, _, err := LoadServerFile(path); err == nil || !strings.Contains(err.Error(), "欄の名前") || !strings.Contains(err.Error(), "prot") {
		t.Errorf("知らない欄は、欄の名前の分かるエラーのはず: %v", err)
	}
}

// server.json の mcpServers は、アプリの settings.json と同じ形で読み、同じように確かめる(0024 の決定事項 23)
func TestServerFileMCPServers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.json")
	os.WriteFile(path, []byte(`{"mcpServers": {"fetch": {"command": "uvx", "args": ["mcp-server-fetch"]}}}`), 0o644)
	file, _, err := LoadServerFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if f := file.MCPServers["fetch"]; f.Command != "uvx" || len(f.Args) != 1 {
		t.Errorf("mcpServers: %+v", file.MCPServers)
	}
	os.WriteFile(path, []byte(`{"mcpServers": {"broken": {}}}`), 0o644)
	if _, _, err := LoadServerFile(path); err == nil || !strings.Contains(err.Error(), "broken") {
		t.Errorf("書き間違えたサーバーは、名前の分かるエラーのはず: %v", err)
	}
}

// 前の形の folders は、名前と絶対パスで、フォルダがあること(0024 の決定事項 29)。
// 読むと、同じ名前のワークスペース・フォルダ1つ(メイン・書ける)として扱う(0065 の決定事項 10)
func TestServerFileFolders(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.json")
	docs := filepath.ToSlash(filepath.Join(dir, "docs"))
	os.Mkdir(filepath.Join(dir, "docs"), 0o755)
	os.WriteFile(path, []byte(`{"folders": {"docs": "`+docs+`", "社内/規程": "`+docs+`"}}`), 0o644)
	file, _, err := LoadServerFile(path)
	if err != nil || file.Folders["docs"] != docs {
		t.Fatalf("folders: %+v, %v", file.Folders, err)
	}
	list, migrated := file.ServerWorkspaces()
	if !migrated || len(list) != 2 {
		t.Fatalf("ワークスペースとして読むはず: %+v %v", list, migrated)
	}
	if w := list[0]; w.Name != "docs" || len(w.Folders) != 1 || w.Folders[0] != (WorkspaceFolder{Path: docs, Name: "docs", Main: true, Writable: true}) {
		t.Errorf("移したワークスペース: %+v", w)
	}
	// モデルが呼ぶ名前に使えない形(/ を含む)なら、フォルダの名前だけはフォルダ名にする
	if w := list[1]; w.Name != "社内/規程" || w.Folders[0].Name != "docs" {
		t.Errorf("名前に / のある資料のフォルダ: %+v", w)
	}
	for _, c := range []struct{ json, want string }{
		{`{"folders": {"docs": "docs"}}`, "絶対パス"},
		{`{"folders": {"docs": "` + docs + `/nothing"}}`, "フォルダがありません"},
		{`{"folders": {" ": "` + docs + `"}}`, "名前が空"},
	} {
		os.WriteFile(path, []byte(c.json), 0o644)
		if _, _, err := LoadServerFile(path); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", c.json, err)
		}
	}
}

// workspaces はワークスペースの名前とフォルダの組。アプリのワークスペースと同じ決まりで確かめる(0065 の決定事項 8・9)
func TestServerFileWorkspaces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.json")
	rules := filepath.ToSlash(filepath.Join(dir, "rules"))
	minutes := filepath.ToSlash(filepath.Join(dir, "minutes"))
	os.Mkdir(rules, 0o755)
	os.Mkdir(minutes, 0o755)
	folder := func(name, p string, main bool) string {
		m := ""
		if main {
			m = `, "main": true, "writable": true`
		}
		return `{"name": "` + name + `", "path": "` + p + `"` + m + `}`
	}
	good := `{"workspaces": [{"name": "社内規程", "folders": [` + folder("規程", rules, true) + `, ` + folder("議事録", minutes, false) + `]}]}`
	os.WriteFile(path, []byte(good), 0o644)
	file, _, err := LoadServerFile(path)
	if err != nil {
		t.Fatal(err)
	}
	list, migrated := file.ServerWorkspaces()
	if migrated || len(list) != 1 || len(list[0].Folders) != 2 || list[0].MainFolder().Name != "規程" || list[0].Folders[1].Writable {
		t.Errorf("workspaces: %+v %v", list, migrated)
	}

	long := strings.Repeat("あ", 101)
	for _, c := range []struct{ json, want string }{
		{`{"workspaces": [{"name": " ", "folders": [` + folder("規程", rules, true) + `]}]}`, "名前が空"},
		{`{"workspaces": [{"name": "a", "folders": [` + folder("規程", rules, true) + `]}, {"name": "A", "folders": [` + folder("規程", rules, true) + `]}]}`, "2つあります"},
		{`{"workspaces": [{"name": "a", "folders": []}]}`, "フォルダがありません"},
		{`{"workspaces": [{"name": "a", "folders": [` + folder("規程", rules, false) + `]}]}`, "メインのフォルダは1つ"},
		{`{"workspaces": [{"name": "a", "folders": [` + folder("規程", rules, true) + `, ` + folder("規程", minutes, false) + `]}]}`, "名前 規程 のフォルダが2つ"},
		{`{"workspaces": [{"name": "a", "folders": [` + folder("a/b", rules, true) + `]}]}`, "使えません"},
		{`{"workspaces": [{"name": "a", "folders": [` + folder("規程", "rules", true) + `]}]}`, "絶対パス"},
		{`{"workspaces": [{"name": "a", "folders": [` + folder("規程", rules+"/nothing", true) + `]}]}`, "ありません"},
		{`{"workspaces": [{"name": "a", "folders": [{"name": "規程", "path": "` + rules + `", "main": true, "description": "` + long + `"}]}]}`, "長すぎます"},
		// 前の形と両方あれば、どちらが正しいか分からないので起動しない。直し方を書く
		{`{"folders": {"docs": "` + rules + `"}, "workspaces": [{"name": "a", "folders": [` + folder("規程", rules, true) + `]}]}`, "folders を消してください"},
	} {
		os.WriteFile(path, []byte(c.json), 0o644)
		if _, _, err := LoadServerFile(path); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", c.json, err)
		}
	}
}

// 足したフォルダの名前の既定はフォルダ名で、重なれば -2 を付ける
func TestFolderName(t *testing.T) {
	p := filepath.Join(t.TempDir(), "docs")
	if n := FolderName(p, []string{"rules"}); n != "docs" {
		t.Errorf("重ならない: %s", n)
	}
	if n := FolderName(p, []string{"Docs", "docs-2"}); n != "docs-3" {
		t.Errorf("重なる: %s", n)
	}
}
