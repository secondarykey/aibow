package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/secondarykey/aibow"
)

// lib が無く、つなぐ先も無いときのエラーの文。信頼したつなぎ先があれば、llama.cpp ではなくログインを促す(まだログインしていないだけなので)
func TestLibMissingMessage(t *testing.T) {
	dir := t.TempDir()
	conn := filepath.Join(dir, "connection.json")
	t.Setenv("AIBOW_LIB", filepath.Join(dir, "lib"))
	t.Setenv("AIBOW_DB", filepath.Join(dir, "aibow.db"))
	t.Setenv("AIBOW_SETTINGS", filepath.Join(dir, "settings.json"))
	t.Setenv("AIBOW_CONNECTION", conn)
	t.Setenv("AIBOW_SERVER_URL", "")

	_, err := loadConfig()
	if err == nil || !strings.Contains(err.Error(), "llama.cpp のフォルダが見つかりません") {
		t.Fatalf("信頼したつなぎ先が無いとき: %v", err)
	}

	known := aibow.ConnectionFile{Known: &aibow.KnownServer{URL: "https://192.168.11.4:8089", Fingerprint: "AB:CD", User: "bob"}}
	if err := known.Save(conn); err != nil {
		t.Fatal(err)
	}
	_, err = loadConfig()
	if err == nil || !strings.Contains(err.Error(), "aibow-server(https://192.168.11.4:8089)にはまだログインしていません") ||
		strings.Contains(err.Error(), "見つかりません") {
		t.Fatalf("信頼したつなぎ先だけがあるとき: %v", err)
	}
}
