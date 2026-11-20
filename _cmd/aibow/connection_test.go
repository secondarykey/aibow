package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/secondarykey/aibow/config"
)

// 信頼したつなぎ先は、確かめただけでも覚え(次の起動でつなぐ先にはしない)、ログアウトしても残る(2026-10-29、ユーザーと決めた)。
// 相手は、ログインの要らない aibow-server のふりをする HTTP のサーバー(/_aibow/auth/me だけ答える)
func TestConnectionKeepsKnownServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_aibow/auth/me" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"login": false}`))
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "connection.json")
	s := &ChatService{conn: newConnection(appConfig{connPath: path})}
	load := func() config.ConnectionFile {
		t.Helper()
		f, err := config.LoadConnectionFile(path)
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
	if r, err := s.ConnectServer(ConnectRequest{URL: srv.URL}); err != nil || r.Same {
		t.Fatal(r, err)
	}
	if f := load(); f.ServerURL != p.URL || f.Known == nil || f.Known.URL != p.URL {
		t.Fatalf("つないだとき: %+v", f)
	}

	// アプリモードに戻す:つなぐ先は消え、信頼したつなぎ先は残る
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

// 「接続」は、ログインの要る aibow-server でもトークンをもらわずに保存する(ログインは起動し直したあとのログインの画面。0047 の決定事項 1・4・5)
func TestConnectWithoutLogin(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_aibow/auth/me" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"login": true, "name": ""}`))
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "connection.json")
	// 前に同じサーバーへ bob でログインしていた(信頼したつなぎ先だけが残っている)
	prev := config.ConnectionFile{Known: &config.KnownServer{URL: srv.URL, User: "bob"}}
	if err := prev.Save(path); err != nil {
		t.Fatal(err)
	}
	s := &ChatService{conn: newConnection(appConfig{connPath: path, conn: prev})}
	r, err := s.ConnectServer(ConnectRequest{URL: srv.URL})
	if err != nil || r.Same {
		t.Fatal(r, err)
	}
	f, err := config.LoadConnectionFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if f.ServerURL == "" || f.Token != "" || f.User != "" || f.Known == nil || f.Known.User != "bob" {
		t.Errorf("つなぐ先だけを書き、トークンは無く、最後にログインした名前は残るはず: %+v", f)
	}
	if v := s.ServerConnection(); v.Pending != "server" || !v.PendingLogin {
		t.Errorf("起動し直したらログインの画面、のお知らせに要るもの: %+v", v)
	}
}

// サーバーモードで、今つないでいるのと同じ aibow-server に「接続」しても何も変えない(0047 の決定事項 7)
func TestConnectSameServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"login": false}`))
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "connection.json")
	now := config.ConnectionFile{ServerURL: srv.URL}
	s := &ChatService{conn: newConnection(appConfig{connPath: path, conn: now, serverURL: srv.URL, serverSource: serverScreen})}
	r, err := s.ConnectServer(ConnectRequest{URL: srv.URL + "/"})
	if err != nil || !r.Same {
		t.Fatalf("同じつなぎ先なら Same: %+v %v", r, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("何も書かないはず: %v", err)
	}
	if v := s.ServerConnection(); v.Pending != "" {
		t.Errorf("起動し直さなくてよいはず: %+v", v)
	}
}
