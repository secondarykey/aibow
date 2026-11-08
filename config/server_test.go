package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// logLevel の書き間違いはエラー(書ける値は LogLevels)
func TestServerFileLogLevel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.json")
	os.WriteFile(path, []byte(`{"logLevel": "verbose"}`), 0o644)
	if _, _, err := LoadServerFile(path); err == nil || !strings.Contains(err.Error(), "logLevel") {
		t.Errorf("書き間違い: %v", err)
	}
	os.WriteFile(path, []byte(`{"logLevel": "notice"}`), 0o644)
	if f, found, err := LoadServerFile(path); err != nil || !found || f.LogLevel != "notice" {
		t.Errorf("notice: %+v %v %v", f, found, err)
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

// server.json の folders は、名前と絶対パスで、フォルダがあること(0024 の決定事項 29)
func TestServerFileFolders(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.json")
	docs := filepath.ToSlash(filepath.Join(dir, "docs"))
	os.Mkdir(filepath.Join(dir, "docs"), 0o755)
	os.WriteFile(path, []byte(`{"folders": {"docs": "`+docs+`"}}`), 0o644)
	if file, _, err := LoadServerFile(path); err != nil || file.Folders["docs"] != docs {
		t.Errorf("folders: %+v, %v", file.Folders, err)
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
