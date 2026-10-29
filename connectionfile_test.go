package aibow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 書いて読むと同じになり、トークンはそのまま入っていない(Windows では DPAPI で包む)
func TestConnectionFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aibow", "connection.json")
	if c, err := LoadConnectionFile(path); err != nil || c.ServerURL != "" {
		t.Fatalf("無いときは空: %+v %v", c, err)
	}
	c := ConnectionFile{ServerURL: "https://192.168.1.10:8089", Fingerprint: "AB:CD", User: "bob", ExpiresAt: time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)}
	if err := c.SetToken("aibowapp_秘密"); err != nil {
		t.Fatal(err)
	}
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "秘密") && c.TokenProtected() {
		t.Errorf("包んだのに、トークンがそのまま入っている: %s", data)
	}
	got, err := LoadConnectionFile(path)
	if err != nil || got.ServerURL != c.ServerURL || got.Fingerprint != "AB:CD" || got.User != "bob" || !got.ExpiresAt.Equal(c.ExpiresAt) {
		t.Fatalf("読んだもの: %+v %v", got, err)
	}
	if tok, err := got.PlainToken(); err != nil || tok != "aibowapp_秘密" {
		t.Errorf("戻したトークン: %q %v", tok, err)
	}
	if err := RemoveConnectionFile(path); err != nil {
		t.Fatal(err)
	}
	if err := RemoveConnectionFile(path); err != nil {
		t.Errorf("無いときに消してもエラーにしない: %v", err)
	}

	os.WriteFile(path, []byte(`{"serverUrl": "ftp://x"}`), 0o600)
	if _, err := LoadConnectionFile(path); err == nil {
		t.Error("つなぎ先の形が違えばエラー")
	}
	os.WriteFile(path, []byte(`{"serverUrl": "", "known": {"url": "ftp://x"}}`), 0o600)
	if _, err := LoadConnectionFile(path); err == nil {
		t.Error("信頼したつなぎ先の形が違えばエラー")
	}
}

// 信頼したつなぎ先(Known)は、つなぐ先(ServerURL)とは別に読み書きでき、Known の無い前の版のファイルではつなぐ先を使う
func TestConnectionFileKnown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "connection.json")
	k := KnownServer{URL: "https://192.168.1.10:8089", Fingerprint: "AB:CD", User: "bob"}
	if err := (ConnectionFile{Known: &k}).Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := LoadConnectionFile(path)
	if err != nil || got.ServerURL != "" || got.Trusted() != k {
		t.Fatalf("信頼しただけ(つながない): %+v %v", got, err)
	}

	old := ConnectionFile{ServerURL: "https://192.168.1.10:8089", Fingerprint: "AB:CD", User: "bob"}
	if old.Trusted() != k {
		t.Errorf("known の無いファイルは、つなぐ先を信頼したつなぎ先とみなす: %+v", old.Trusted())
	}
	if (ConnectionFile{}).Trusted() != (KnownServer{}) {
		t.Error("何も無ければ空")
	}
}
