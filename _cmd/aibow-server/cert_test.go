package main

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apiclient "github.com/secondarykey/aibow/api/client"
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
	c := config{StatePath: filepath.Join(dir, stateFileName)}
	if err := runCert(c, nil); err == nil {
		t.Error("ホストが無ければ断る")
	}
	if err := runCert(c, []string{"my-pc.local"}); err != nil {
		t.Fatal(err)
	}
	if _, err := tls.LoadX509KeyPair(filepath.Join(dir, certFileName), filepath.Join(dir, keyFileName)); err != nil {
		t.Errorf("作ったファイルを読めない: %v", err)
	}
	if err := runCert(c, []string{"my-pc.local"}); err == nil || !strings.Contains(err.Error(), "もうあります") {
		t.Errorf("上書きしない: %v", err)
	}
}
