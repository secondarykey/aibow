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
}
