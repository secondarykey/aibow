package main

import (
	"flag"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/secondarykey/aibow/log"
)

// 強さの順(引数 > 設定ファイル > 環境変数 > 既定)を、項目ごとに別の場所で決めて確かめる。
func TestResolveOrder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, configFileName)
	os.WriteFile(path, []byte(`{"port": 9000, "ctx": 16384, "lib": "file-lib"}`), 0o644)

	t.Setenv("AIBOW_SERVER_PORT", "9100") // 設定ファイルに負ける
	t.Setenv("AIBOW_SERVER_CTX", "")
	t.Setenv("AIBOW_MODEL", "env-model") // 設定ファイルに無いので効く
	t.Setenv("AIBOW_LIB", "env-lib")     // 引数に負ける
	t.Setenv("AIBOW_MODELS_DIR", "")     // どこにも無いので既定
	t.Setenv("AIBOW_API_PORT", "8000")   // 古い名前。読まずに注意を出す
	t.Setenv("AIBOW_SERVER_LOGIN", "")   // どこにも無いので既定(要る。0020)

	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	f := defineFlags(fs)
	if err := fs.Parse([]string{"-config", path, "-lib", "arg-lib"}); err != nil {
		t.Fatal(err)
	}
	var env envReader
	c, notices, err := resolve(fs, f, &env)
	if err != nil {
		t.Fatal(err)
	}
	want := config{Port: 9000, Ctx: 16384, Model: "env-model", Lib: "arg-lib", ModelsDir: "models", Host: "127.0.0.1", Login: true,
		File: path, StatePath: filepath.Join(dir, stateFileName), DBPath: filepath.Join(dir, dbFileName), ToolsPath: filepath.Join(dir, toolsFileName)}
	if !reflect.DeepEqual(c, want) {
		t.Errorf("設定:\n got  %+v\n want %+v", c, want)
	}
	if len(notices) != 1 || !strings.Contains(notices[0], "AIBOW_API_PORT") {
		t.Errorf("古い環境変数の注意: %q", notices)
	}
}

// ログインは既定で要る。外すのは、書いたときだけ(引数 > 設定ファイル > 環境変数。0020 の決定事項 1)
func TestResolveLogin(t *testing.T) {
	dir := t.TempDir()
	none := filepath.Join(dir, "none.json")
	off := filepath.Join(dir, "off.json")
	os.WriteFile(none, []byte(`{}`), 0o644)
	os.WriteFile(off, []byte(`{"login": false}`), 0o644)
	for _, k := range []string{"AIBOW_SERVER_HOST", "AIBOW_SERVER_LOGIN"} {
		t.Setenv(k, "")
	}
	login := func(path, env string, args ...string) bool {
		t.Helper()
		t.Setenv("AIBOW_SERVER_LOGIN", env)
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		f := defineFlags(fs)
		if err := fs.Parse(append([]string{"-config", path}, args...)); err != nil {
			t.Fatal(err)
		}
		var env2 envReader
		c, _, err := resolve(fs, f, &env2)
		if err != nil {
			t.Fatal(err)
		}
		return c.Login
	}
	for _, c := range []struct {
		name, path, env string
		args            []string
		want            bool
	}{
		{"何も書かない", none, "", nil, true},
		{"環境変数で外す", none, "0", nil, false},
		{"設定ファイルで外す", off, "1", nil, false},
		{"引数で外す", none, "", []string{"-login=false"}, false},
		{"引数が設定ファイルより強い", off, "", []string{"-login"}, true},
	} {
		if got := login(c.path, c.env, c.args...); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
	}
}

// -config で指したファイルが無いときはエラー。書き間違えた欄もエラー(黙って効かないと理由が分からないため)。
func TestResolveConfigErrors(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	os.WriteFile(bad, []byte("{\n  \"prot\": 9000\n}"), 0o644)
	for _, tc := range []struct {
		path, want string
	}{
		{filepath.Join(dir, "none.json"), "見つかりません"},
		{bad, "prot"},
	} {
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		f := defineFlags(fs)
		fs.Parse([]string{"-config", tc.path})
		var env envReader
		if _, _, err := resolve(fs, f, &env); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: エラーに %q が入っていません: %v", tc.path, tc.want, err)
		}
	}
}

// ログのレベル:-debug > 設定ファイルの logLevel > 既定(go run は Info。go test も go-build の下で動くので Info)。書き間違いはエラー
func TestLogLevel(t *testing.T) {
	for _, tc := range []struct {
		file  string
		debug bool
		want  slog.Level
	}{
		{"", false, log.LevelInfo},
		{"", true, log.LevelDebug},
		{"warn", true, log.LevelDebug},
		{"warn", false, log.LevelWarn},
		{"notice", false, log.LevelNotice},
	} {
		if got := logLevel(config{LogLevel: tc.file}, tc.debug); got != tc.want {
			t.Errorf("logLevel(%q, %v) = %v, want %v", tc.file, tc.debug, got, tc.want)
		}
	}
	path := filepath.Join(t.TempDir(), configFileName)
	os.WriteFile(path, []byte(`{"logLevel": "verbose"}`), 0o644)
	if _, _, err := loadConfigFile(path); err == nil || !strings.Contains(err.Error(), "logLevel") {
		t.Errorf("書き間違い: %v", err)
	}
}

// 127.0.0.1 の外で待ち受けるなら、ログインと API キーが要る。証明書と秘密鍵は両方要る(0017 の 10-2)
func TestResolveHost(t *testing.T) {
	path := filepath.Join(t.TempDir(), configFileName)
	os.WriteFile(path, []byte(`{"host": "0.0.0.0", "login": false}`), 0o644)
	for _, k := range []string{"AIBOW_SERVER_HOST", "AIBOW_SERVER_LOGIN", "AIBOW_SERVER_TLS_CERT", "AIBOW_SERVER_TLS_KEY"} {
		t.Setenv(k, "")
	}
	resolveArgs := func(args ...string) (config, []string, error) {
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		f := defineFlags(fs)
		if err := fs.Parse(append([]string{"-config", path}, args...)); err != nil {
			t.Fatal(err)
		}
		var env envReader
		return resolve(fs, f, &env)
	}
	c, notices, err := resolveArgs()
	if err != nil || c.Local() || !c.Login || !c.KeysRequired() || len(notices) != 1 || !strings.Contains(notices[0], "ログインを要る") {
		t.Errorf("0.0.0.0: %+v %q %v", c, notices, err)
	}
	c, _, _ = resolveArgs("-host", "127.0.0.1")
	if !c.Local() || c.Login || c.KeysRequired() || c.Scheme() != "http" {
		t.Errorf("127.0.0.1(ログインなし): %+v", c)
	}
	c, _, _ = resolveArgs("-host", "localhost", "-login")
	if !c.Local() || !c.KeysRequired() {
		t.Errorf("localhost でもログインが要るならキーも要る: %+v", c)
	}
	if _, _, err := resolveArgs("-tls-cert", "a.pem"); err == nil || !strings.Contains(err.Error(), "両方") {
		t.Errorf("証明書だけ: %v", err)
	}
	c, _, _ = resolveArgs("-tls-cert", "a.pem", "-tls-key", "b.pem")
	if c.Scheme() != "https" {
		t.Errorf("証明書があれば https: %+v", c)
	}
}

// server.json の mcpServers は、アプリの settings.json と同じ形で読み、同じように確かめる(0024 の決定事項 23)
func TestConfigMCPServers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, configFileName)
	os.WriteFile(path, []byte(`{"mcpServers": {"fetch": {"command": "uvx", "args": ["mcp-server-fetch"]}}}`), 0o644)
	file, _, err := loadConfigFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if f := file.MCPServers["fetch"]; f.Command != "uvx" || len(f.Args) != 1 {
		t.Errorf("mcpServers: %+v", file.MCPServers)
	}
	os.WriteFile(path, []byte(`{"mcpServers": {"broken": {}}}`), 0o644)
	if _, _, err := loadConfigFile(path); err == nil || !strings.Contains(err.Error(), "broken") {
		t.Errorf("書き間違えたサーバーは、名前の分かるエラーのはず: %v", err)
	}
}

// server.json の folders は、名前と絶対パスで、フォルダがあること(0024 の決定事項 29)
func TestConfigFolders(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, configFileName)
	docs := filepath.ToSlash(filepath.Join(dir, "docs"))
	os.Mkdir(filepath.Join(dir, "docs"), 0o755)
	os.WriteFile(path, []byte(`{"folders": {"docs": "`+docs+`"}}`), 0o644)
	if file, _, err := loadConfigFile(path); err != nil || file.Folders["docs"] != docs {
		t.Errorf("folders: %+v, %v", file.Folders, err)
	}
	for _, c := range []struct{ json, want string }{
		{`{"folders": {"docs": "docs"}}`, "絶対パス"},
		{`{"folders": {"docs": "` + docs + `/nothing"}}`, "フォルダがありません"},
		{`{"folders": {" ": "` + docs + `"}}`, "名前が空"},
	} {
		os.WriteFile(path, []byte(c.json), 0o644)
		if _, _, err := loadConfigFile(path); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", c.json, err)
		}
	}
}
