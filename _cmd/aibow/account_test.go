package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
	"uuid"

	"github.com/secondarykey/aibow"
	apiclient "github.com/secondarykey/aibow/api/client"
	"github.com/secondarykey/aibow/auth"
	"github.com/secondarykey/aibow/store"
)

// アプリのログイン・ログアウトは、ブラウザと同じく起動したまま行い、つなぎ先は残す(0031 の決定事項 44)。
// 表示名・メールアドレス・パスワードも、アプリ用のトークンで変えられる(決定事項 45)。
// 相手は、aibow-server と同じ組み方のログインの口(auth の Handler・WithBearer・MeHandler)と本物の保存先
func TestAppAccount(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	hash, _ := auth.HashPassword("bob-password-1")
	now := time.Now()
	if err := st.CreateUser(context.Background(), auth.User{ID: uuid.NewV7(), Name: "bob", Password: hash, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	sessions, keys := auth.NewSessions(st), auth.NewKeys(st)
	mux := http.NewServeMux()
	mux.Handle("/aibow/auth/", keys.WithBearer(auth.Handler(sessions, true)))
	mux.Handle("GET /aibow/auth/key", keys.MeHandler(true))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// 画面でつないで起動した形(トークンはまだ無い)
	path := filepath.Join(t.TempDir(), "connection.json")
	conn := newConnection(appConfig{connPath: path, serverSource: serverScreen, serverURL: srv.URL,
		conn: aibow.ConnectionFile{ServerURL: srv.URL}})
	conn.client = apiclient.New(srv.URL)
	s := &ChatService{conn: conn}
	load := func() aibow.ConnectionFile {
		t.Helper()
		f, err := aibow.LoadConnectionFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}

	if a, err := s.Account(); err != nil || !a.NeedLogin {
		t.Fatalf("トークンが無ければログインの画面: %+v %v", a, err)
	}
	if _, err := s.Login("bob", "違う"); err == nil {
		t.Error("違うパスワードでログインできる")
	}
	a, err := s.Login("bob", "bob-password-1")
	if err != nil || a.Name != "bob" || !a.CanLogout || a.NeedLogin {
		t.Fatalf("ログイン: %+v %v", a, err)
	}
	if f := load(); f.ServerURL == "" || f.Token == "" || f.User != "bob" {
		t.Errorf("ログインしたら connection.json にトークン: %+v", f)
	}

	// 表示名・メールアドレス(アプリ用のトークンで)
	if a, err := s.SetMyDisplayName(" 佐藤 花子 "); err != nil || a.DisplayName != "佐藤 花子" {
		t.Errorf("表示名: %+v %v", a, err)
	}
	if a, err := s.SetMyEmail("sato@example.com"); err != nil || a.Email != "sato@example.com" {
		t.Errorf("メールアドレス: %+v %v", a, err)
	}

	// パスワードを変えても、このアプリは新しいトークンでつながったまま。前のトークンは使えない
	old := conn.client.Key()
	if err := s.ChangeMyPassword("bob-password-1", "bob-password-2"); err != nil {
		t.Fatal(err)
	}
	if conn.client.Key() == old {
		t.Error("トークンが替わっていない")
	}
	if a, err := s.Account(); err != nil || a.Name != "bob" {
		t.Errorf("パスワードを変えたあと: %+v %v", a, err)
	}
	if _, err := keys.User(context.Background(), old); err == nil {
		t.Error("前のトークンがまだ使える")
	}
	if tok, _ := load().PlainToken(); tok != conn.client.Key() {
		t.Error("connection.json のトークンが新しいものになっていない")
	}

	// ログアウト:その場でログインの画面になり、つなぎ先は残す(起動し直さない)
	key := conn.client.Key()
	if err := s.Logout(); err != nil {
		t.Fatal(err)
	}
	if a, err := s.Account(); err != nil || !a.NeedLogin || a.LastUser != "bob" {
		t.Errorf("ログアウトしたあと: %+v %v", a, err)
	}
	if f := load(); f.ServerURL == "" || f.Token != "" {
		t.Errorf("ログアウトしたら、つなぎ先は残しトークンは消す: %+v", f)
	}
	if _, err := keys.User(context.Background(), key); err == nil {
		t.Error("ログアウトしたトークンがまだ使える")
	}
	if v := s.ServerConnection(); v.Pending != "" {
		t.Errorf("ログアウトは起動し直さなくてよい: %+v", v)
	}
	if a, err := s.Login("bob", "bob-password-2"); err != nil || a.Name != "bob" {
		t.Errorf("ログインし直す: %+v %v", a, err)
	}
}

// settings.json に手で書いたつなぎ先(API キー)では、ログイン・ログアウトしない
func TestAppAccountHandWritten(t *testing.T) {
	s := &ChatService{conn: newConnection(appConfig{serverSource: serverSettings, serverURL: "http://127.0.0.1:1"})}
	s.conn.client = apiclient.New("http://127.0.0.1:1")
	if err := s.Logout(); err == nil {
		t.Error("手で書いたつなぎ先でログアウトできる")
	}
	if _, err := s.Login("bob", "x"); err == nil {
		t.Error("手で書いたつなぎ先でログインできる")
	}
}
