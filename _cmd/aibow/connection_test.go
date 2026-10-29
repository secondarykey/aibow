package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/secondarykey/aibow"
)

// 信頼したつなぎ先は、確かめただけでも覚え(次の起動でつなぐ先にはしない)、ログアウトしても残る(2026-10-29、ユーザーと決めた)。
// 相手は、ログインの要らない aibow-server のふりをする HTTP のサーバー(/aibow/auth/me だけ答える)
func TestConnectionKeepsKnownServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/aibow/auth/me" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"login": false}`))
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "connection.json")
	s := &ChatService{conn: newConnection(appConfig{connPath: path})}
	load := func() aibow.ConnectionFile {
		t.Helper()
		f, err := aibow.LoadConnectionFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}

	// 確かめただけ:信頼したつなぎ先として覚えるが、つなぐ先にはしない
	p, err := s.CheckServer(srv.URL, "")
	if err != nil || !p.Trusted {
		t.Fatalf("確かめる: %+v %v", p, err)
	}
	if f := load(); f.ServerURL != "" || f.Known == nil || f.Known.URL != p.URL {
		t.Fatalf("確かめただけのとき: %+v", f)
	}
	if v := s.ServerConnection(); v.KnownURL != p.URL || v.Pending != "" {
		t.Errorf("タブに出すもの: %+v", v)
	}

	// つなぐ:つなぐ先と信頼したつなぎ先の両方
	if err := s.ConnectServer(ConnectRequest{URL: srv.URL}); err != nil {
		t.Fatal(err)
	}
	if f := load(); f.ServerURL != p.URL || f.Known == nil || f.Known.URL != p.URL {
		t.Fatalf("つないだとき: %+v", f)
	}

	// ログアウトして戻す:つなぐ先は消え、信頼したつなぎ先は残る
	if err := s.DisconnectServer(); err != nil {
		t.Fatal(err)
	}
	if f := load(); f.ServerURL != "" || f.Token != "" || f.Known == nil || f.Known.URL != p.URL {
		t.Fatalf("戻したとき: %+v", f)
	}
	if v := s.ServerConnection(); v.Mode != "direct" || v.KnownURL != p.URL {
		t.Errorf("戻したあとのタブ: %+v", v)
	}
}

// 信頼したつなぎ先が無ければ、戻すときは今までどおりファイルを消す
func TestDisconnectWithoutKnownRemovesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "connection.json")
	s := &ChatService{conn: newConnection(appConfig{connPath: path})}
	if err := s.DisconnectServer(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("ファイルが残っている: %v", err)
	}
}
