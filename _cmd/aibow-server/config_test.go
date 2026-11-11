package main

import (
	"flag"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/secondarykey/aibow/config"
	"github.com/secondarykey/aibow/log"
)

// 強さの順(引数 > 設定ファイル > 環境変数 > 既定)を、項目ごとに別の場所で決めて確かめる。
func TestResolveOrder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, configFileName)
	os.WriteFile(path, []byte(`{"port": 9000, "ctx": 16384, "parallel": 2, "lib": "file-lib"}`), 0o644)

	t.Setenv("AIBOW_SERVER_PORT", "9100") // 設定ファイルに負ける
	t.Setenv("AIBOW_SERVER_CTX", "")
	t.Setenv("AIBOW_SERVER_PARALLEL", "3") // 設定ファイルに負ける(0023)
	t.Setenv("AIBOW_MODEL", "env-model")   // 設定ファイルに無いので効く
	t.Setenv("AIBOW_LIB", "env-lib")       // 引数に負ける
	t.Setenv("AIBOW_MODELS_DIR", "")       // どこにも無いので既定
	t.Setenv("AIBOW_API_PORT", "8000")     // 古い名前。読まずに注意を出す
	t.Setenv("AIBOW_SERVER_LOGIN", "")     // どこにも無いので既定(要る。0020)

	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	f := defineFlags(fs)
	if err := fs.Parse([]string{"-config", path, "-lib", "arg-lib"}); err != nil {
		t.Fatal(err)
	}
	var env config.Env
	c, notices, err := resolve(fs, f, &env)
	if err != nil {
		t.Fatal(err)
	}
	want := serverConfig{Port: 9000, Ctx: 16384, Parallel: 2, Model: "env-model", Lib: "arg-lib", ModelsDir: "models", Host: "127.0.0.1", Login: true, ProxyHeader: "X-Forwarded-For",
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
		var env2 config.Env
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
		var env config.Env
		if _, _, err := resolve(fs, f, &env); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: エラーに %q が入っていません: %v", tc.path, tc.want, err)
		}
	}
}

// ログのレベル:-debug > 設定ファイルの logLevel > 既定(go run は Info。go test も go-build の下で動くので Info)。書き間違いは config の TestServerFileLogLevel
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
		if got := logLevel(serverConfig{LogLevel: tc.file}, tc.debug); got != tc.want {
			t.Errorf("logLevel(%q, %v) = %v, want %v", tc.file, tc.debug, got, tc.want)
		}
	}
}

// 127.0.0.1 の外で待ち受けるなら、ログインと API キーが要る。証明書と秘密鍵は両方要る(0017 の 10-2)
func TestResolveHost(t *testing.T) {
	path := filepath.Join(t.TempDir(), configFileName)
	os.WriteFile(path, []byte(`{"host": "0.0.0.0", "login": false}`), 0o644)
	for _, k := range []string{"AIBOW_SERVER_HOST", "AIBOW_SERVER_LOGIN", "AIBOW_SERVER_TLS_CERT", "AIBOW_SERVER_TLS_KEY"} {
		t.Setenv(k, "")
	}
	resolveArgs := func(args ...string) (serverConfig, []string, error) {
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		f := defineFlags(fs)
		if err := fs.Parse(append([]string{"-config", path}, args...)); err != nil {
			t.Fatal(err)
		}
		var env config.Env
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

// addr は host と port をまとめて書く形で、書いた所の強さで効く。同じ所に addr と host・port を書くとエラー(0042)
func TestResolveAddr(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(body), 0o644)
		return p
	}
	none := write("none.json", `{}`)
	fileAddr := write("addr.json", `{"addr": "0.0.0.0:9000"}`)
	both := write("both.json", `{"addr": "0.0.0.0:9000", "port": 9001}`)
	for _, k := range []string{"AIBOW_SERVER_HOST", "AIBOW_SERVER_PORT", "AIBOW_SERVER_ADDR", "AIBOW_SERVER_BASE", "AIBOW_SERVER_LOGIN"} {
		t.Setenv(k, "")
	}
	resolveWith := func(path string, envs map[string]string, args ...string) (serverConfig, error) {
		t.Helper()
		for k, v := range envs {
			t.Setenv(k, v)
		}
		defer func() {
			for k := range envs {
				os.Setenv(k, "")
			}
		}()
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		f := defineFlags(fs)
		if err := fs.Parse(append([]string{"-config", path}, args...)); err != nil {
			t.Fatal(err)
		}
		var env config.Env
		c, _, err := resolve(fs, f, &env)
		return c, err
	}
	for _, tc := range []struct {
		name string
		path string
		envs map[string]string
		args []string
		host string
		port int
	}{
		{"何も書かない", none, nil, nil, "127.0.0.1", defaultPort},
		{"引数の addr", none, nil, []string{"-addr", "0.0.0.0:9100"}, "0.0.0.0", 9100},
		{"アドレスなしの addr はポートだけ", none, nil, []string{"-addr", ":9100"}, "127.0.0.1", 9100},
		{"IPv6", none, nil, []string{"-addr", "[::1]:9100"}, "::1", 9100},
		{"設定ファイルの addr", fileAddr, nil, nil, "0.0.0.0", 9000},
		{"引数の port は設定ファイルの addr のポートより強い", fileAddr, nil, []string{"-port", "9200"}, "0.0.0.0", 9200},
		{"引数の addr は設定ファイルの addr より強い", fileAddr, nil, []string{"-addr", "127.0.0.1:9300"}, "127.0.0.1", 9300},
		{"環境変数の addr", none, map[string]string{"AIBOW_SERVER_ADDR": "localhost:9400"}, nil, "localhost", 9400},
		{"設定ファイルの addr は環境変数の addr より強い", fileAddr, map[string]string{"AIBOW_SERVER_ADDR": "localhost:9400"}, nil, "0.0.0.0", 9000},
	} {
		c, err := resolveWith(tc.path, tc.envs, tc.args...)
		if err != nil || c.Host != tc.host || c.Port != tc.port {
			t.Errorf("%s: %s:%d %v(want %s:%d)", tc.name, c.Host, c.Port, err, tc.host, tc.port)
		}
	}
	for _, tc := range []struct {
		name, path string
		envs       map[string]string
		args       []string
		want       string
	}{
		{"引数で addr と port", none, nil, []string{"-addr", "0.0.0.0:9000", "-port", "9001"}, "一緒に"},
		{"設定ファイルに addr と port", both, nil, nil, "両方"},
		{"環境変数に addr と host", none, map[string]string{"AIBOW_SERVER_ADDR": ":9000", "AIBOW_SERVER_HOST": "0.0.0.0"}, nil, "一緒に"},
		{"ポートなし", none, nil, []string{"-addr", "0.0.0.0"}, "アドレス:ポート"},
		{"ポートが数でない", none, nil, []string{"-addr", "0.0.0.0:http"}, "1〜65535"},
		{"ポートが大きすぎる", none, nil, []string{"-addr", "0.0.0.0:70000"}, "1〜65535"},
	} {
		if _, err := resolveWith(tc.path, tc.envs, tc.args...); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: エラーに %q が入っていません: %v", tc.name, tc.want, err)
		}
	}
}

// base は "/aibow" の形にそろえる。"" と "/" は頭なし。URL に使えない文字や . / .. は断る(0042)
func TestCleanBase(t *testing.T) {
	for in, want := range map[string]string{
		"": "", "/": "", " / ": "", "aibow": "/aibow", "/aibow": "/aibow", "/aibow/": "/aibow", "a/b-c_d.e~f": "/a/b-c_d.e~f",
	} {
		if got, err := cleanBase(in); err != nil || got != want {
			t.Errorf("cleanBase(%q) = %q %v, want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"C:/Program Files/Git/aibow", "/a//b", "/a/../b", "/./a", "/a b", "/a?b", "/a#b", "/a%2f", "/あ", "/{x}"} {
		if got, err := cleanBase(in); err == nil {
			t.Errorf("cleanBase(%q) = %q, エラーのはず", in, got)
		}
	}
	// 引数 > 設定ファイル > 環境変数。つなぐ側のアドレス(URL)にも付く
	path := filepath.Join(t.TempDir(), configFileName)
	os.WriteFile(path, []byte(`{"base": "/file"}`), 0o644)
	for _, k := range []string{"AIBOW_SERVER_HOST", "AIBOW_SERVER_PORT", "AIBOW_SERVER_ADDR"} {
		t.Setenv(k, "")
	}
	t.Setenv("AIBOW_SERVER_BASE", "/env")
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	f := defineFlags(fs)
	fs.Parse([]string{"-config", path, "-base", "aibow/"})
	var env config.Env
	c, _, err := resolve(fs, f, &env)
	if err != nil || c.Base != "/aibow" || c.URL() != "http://127.0.0.1:8089/aibow" {
		t.Errorf("引数の base: %q %q %v", c.Base, c.URL(), err)
	}
}
