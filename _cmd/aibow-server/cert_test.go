package main

import (
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	apiclient "github.com/secondarykey/aibow/api/client"
	"github.com/secondarykey/aibow/config"
)

// 作った自己署名の証明書で HTTPS を待ち受け、アプリ(api/client)が証明書を信頼すればつながり、信頼しなければ断られる
func TestSelfSignedCert(t *testing.T) {
	certPEM, keyPEM, err := selfSigned([]string{"192.168.1.10", "127.0.0.1", "localhost"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[],"ctx":32768,"gpu":true}`))
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{pair}}
	srv.StartTLS()
	defer srv.Close()

	dir := t.TempDir()
	certFile := filepath.Join(dir, certFileName)
	os.WriteFile(certFile, certPEM, 0o644)

	c := apiclient.New(srv.URL)
	if _, err := c.Models(); err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Errorf("信頼していない証明書ではつながらないはず: %v", err)
	}
	c = apiclient.New(srv.URL)
	if err := c.TrustCert(certFile); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Models(); err != nil {
		t.Errorf("信頼した証明書ならつながる: %v", err)
	}
}

// cert のコマンドは設定ファイルの隣に書き、もうあれば上書きしない
func TestRunCert(t *testing.T) {
	dir := t.TempDir()
	c := serverConfig{StatePath: filepath.Join(dir, stateFileName)}
	if err := runCert(io.Discard, c, nil); err == nil {
		t.Error("ホストが無ければ断る")
	}
	if err := runCert(io.Discard, c, []string{"my-pc.local"}); err != nil {
		t.Fatal(err)
	}
	if _, err := tls.LoadX509KeyPair(filepath.Join(dir, certFileName), filepath.Join(dir, keyFileName)); err != nil {
		t.Errorf("作ったファイルを読めない: %v", err)
	}
	if err := runCert(io.Discard, c, []string{"my-pc.local"}); err == nil || !strings.Contains(err.Error(), "もうあります") {
		t.Errorf("上書きしない: %v", err)
	}
}

// 貼る2行は、{ } に入れると server.json として読める。パスに \ や " が入っても壊れない(0058 の完了の条件 2)
func TestTLSSnippetLoads(t *testing.T) {
	for _, p := range [][2]string{
		{`C:\Users\me\AppData\Roaming\aibow\server-cert.pem`, `C:\Users\me\AppData\Roaming\aibow\server-key.pem`},
		{`/home/me/.config/aibow/a "b" & <c>.pem`, `/home/me/.config/aibow/k\ey.pem`},
	} {
		snippet := tlsSnippet(p[0], p[1])
		if n := strings.Count(snippet, "\n"); n != 1 {
			t.Errorf("2行で出す: %q", snippet)
		}
		if strings.Contains(snippet, `\u0026`) {
			t.Errorf("& を \\u0026 にしない: %q", snippet)
		}
		path := filepath.Join(t.TempDir(), configFileName)
		os.WriteFile(path, []byte("{\n"+snippet+"\n}\n"), 0o644)
		file, found, err := config.LoadServerFile(path)
		if err != nil || !found {
			t.Fatalf("貼った server.json を読めない: %v\n%s", err, snippet)
		}
		if file.TLSCert != filepath.ToSlash(p[0]) || file.TLSKey != filepath.ToSlash(p[1]) {
			t.Errorf("読めた値が違う: %q・%q", file.TLSCert, file.TLSKey)
		}
		// ほかの欄の後ろに置いても(前の行に , を足せば)読める
		os.WriteFile(path, []byte("{\n  \"port\": 8089,\n"+snippet+"\n}\n"), 0o644)
		if _, _, err := config.LoadServerFile(path); err != nil {
			t.Errorf("ほかの欄の後ろに置くと読めない: %v", err)
		}
	}
}

// server.json の値と作ったファイルを比べる(0058 の決定事項 6)
func TestCompareTLS(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, certFileName), filepath.Join(dir, keyFileName)
	f := func(c, k string) config.ServerFile { return config.ServerFile{TLSCert: c, TLSKey: k} }

	if got := compareTLS(config.ServerFile{}, false, certPath, keyPath); got != tlsNoFile {
		t.Errorf("server.json が無い: %v", got)
	}
	if got := compareTLS(f("", ""), true, certPath, keyPath); got != tlsNotSet {
		t.Errorf("TLS を書いていない: %v", got)
	}
	if got := compareTLS(f(filepath.ToSlash(certPath), keyPath), true, certPath, keyPath); got != tlsSame {
		t.Errorf("/ で書いても同じファイル: %v", got)
	}
	if got := compareTLS(f(certPath, filepath.Join(dir, "other-key.pem")), true, certPath, keyPath); got != tlsDifferent {
		t.Errorf("片方だけ同じなら違う: %v", got)
	}
	if got := compareTLS(f(certPath, ""), true, certPath, keyPath); got != tlsDifferent {
		t.Errorf("片方だけ書いてあれば違う: %v", got)
	}
	// Windows ではパスの大文字と小文字を区別しない
	want := tlsDifferent
	if runtime.GOOS == "windows" {
		want = tlsSame
	}
	if got := compareTLS(f(strings.ToUpper(certPath), strings.ToUpper(keyPath)), true, certPath, keyPath); got != want {
		t.Errorf("大文字にしたパス: %v(%s では %v)", got, runtime.GOOS, want)
	}
}

// cert の出力は、server.json の状態で添える一言を変え、待ち受けが PC の中だけならほかの PC からの書き方を添える(0058 の決定事項 6・10)
func TestRunCertGuide(t *testing.T) {
	type tc struct {
		name     string
		json     string // 空なら server.json を作らない。{cert}・{key} は作るファイルのパス
		host     string
		want     []string
		dontWant []string
	}
	for _, c := range []tc{
		{name: "server.json なし", host: "127.0.0.1",
			want:     []string{"server.json はまだありません", "{\n  \"tlsCert\": ", "\"tlsKey\": ", "\n}\n", `"host": "0.0.0.0"`, `addr(例 "0.0.0.0:8089")`},
			dontWant: []string{"今の設定と違います"}},
		{name: "TLS を書いていない", json: `{"port": 8089}`, host: "127.0.0.1",
			want:     []string{"の { } の中に、次の2行を書くと", "  \"tlsCert\": ", `"host": "0.0.0.0"`},
			dontWant: []string{"まだありません", "今の設定と違います", "{\n  \"tlsCert\""}},
		{name: "同じ", json: `{"tlsCert": "{cert}", "tlsKey": "{key}"}`, host: "127.0.0.1",
			want:     []string{"もうこの証明書を指しています"},
			dontWant: []string{"\"tlsCert\": ", "今の設定と違います"}},
		{name: "違う", json: `{"tlsCert": "old-cert.pem", "tlsKey": "old-key.pem"}`, host: "127.0.0.1",
			want: []string{"  \"tlsCert\": ", "今の設定と違います(今: tlsCert old-cert.pem・tlsKey old-key.pem)"}},
		{name: "addr を書いている", json: `{"addr": "127.0.0.1:8089"}`, host: "127.0.0.1",
			want:     []string{`"addr": "0.0.0.0:8089"`},
			dontWant: []string{`"host": "0.0.0.0"`}},
		{name: "PC の外で待ち受けている", json: `{"host": "0.0.0.0"}`, host: "0.0.0.0",
			want:     []string{"  \"tlsCert\": "},
			dontWant: []string{"ほかの PC から使うなら"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			cfg := serverConfig{StatePath: filepath.Join(dir, stateFileName), Host: c.host, Port: defaultPort}
			if c.json != "" {
				cfg.File = filepath.Join(dir, configFileName)
				js := strings.NewReplacer("{cert}", filepath.ToSlash(filepath.Join(dir, certFileName)),
					"{key}", filepath.ToSlash(filepath.Join(dir, keyFileName))).Replace(c.json)
				os.WriteFile(cfg.File, []byte(js), 0o644)
			}
			var out strings.Builder
			if err := runCert(&out, cfg, []string{"192.168.1.10"}); err != nil {
				t.Fatal(err)
			}
			for _, w := range c.want {
				if !strings.Contains(out.String(), w) {
					t.Errorf("%q が出ていない:\n%s", w, out.String())
				}
			}
			for _, w := range c.dontWant {
				if strings.Contains(out.String(), w) {
					t.Errorf("%q は出さない:\n%s", w, out.String())
				}
			}
			// 並びは 作ったもの → server.json → アプリの側の案内
			// (一時フォルダの名前にも server.json が入るので、行の頭で探す)
			s := out.String()
			fp, js, app := strings.Index(s, "フィンガープリント(SHA-256)"), strings.Index(s, "\nserver.json"), strings.Index(s, "\n画面のアプリからつなぐときは")
			if !(0 <= fp && fp < js && js < app) {
				t.Errorf("並びが違う:\n%s", s)
			}
		})
	}
}
