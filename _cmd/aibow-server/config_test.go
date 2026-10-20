package main

import (
	"flag"
	"log/slog"
	"os"
	"path/filepath"
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
	want := config{Port: 9000, Ctx: 16384, Model: "env-model", Lib: "arg-lib", ModelsDir: "models",
		File: path, StatePath: filepath.Join(dir, stateFileName)}
	if c != want {
		t.Errorf("設定:\n got  %+v\n want %+v", c, want)
	}
	if len(notices) != 1 || !strings.Contains(notices[0], "AIBOW_API_PORT") {
		t.Errorf("古い環境変数の注意: %q", notices)
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
