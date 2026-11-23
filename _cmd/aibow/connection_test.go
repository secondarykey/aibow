package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	apiclient "github.com/secondarykey/aibow/api/client"
	"github.com/secondarykey/aibow/auth"
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

// 証明書を作り直したあと、今つないでいるのと同じ aibow-server に新しいフィンガープリントで「接続」すると、
// つなぐ先のフィンガープリントも新しくして保存し、トークンとログインの名前は残して、起動し直すようにする(0057 の段2)。
// 以前は「同じ aibow-server」として何も保存せず、信頼したつなぎ先だけが新しくなって、起動し直しても古いフィンガープリントで断られていた
func TestConnectSameServerNewCert(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"login": true, "name": ""}`))
	}))
	defer srv.Close()
	fp := auth.Fingerprint(srv.Certificate().Raw)

	path := filepath.Join(t.TempDir(), "connection.json")
	expires := time.Now().Add(24 * time.Hour).UTC().Truncate(time.Second)
	now := config.ConnectionFile{ServerURL: srv.URL, Fingerprint: "古いフィンガープリント", User: "bob", Token: "包んだトークン", ExpiresAt: expires,
		Known: &config.KnownServer{URL: srv.URL, Fingerprint: "古いフィンガープリント", User: "bob"}}
	if err := now.Save(path); err != nil {
		t.Fatal(err)
	}
	s := &ChatService{conn: newConnection(appConfig{connPath: path, conn: now, serverURL: srv.URL, serverSource: serverScreen, serverKey: "aibowapp_x"})}
	r, err := s.ConnectServer(ConnectRequest{URL: srv.URL, Fingerprint: fp})
	if err != nil || r.Same {
		t.Fatalf("証明書が変わったなら Same にしない: %+v %v", r, err)
	}
	f, err := config.LoadConnectionFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if f.Fingerprint != fp || f.Known == nil || f.Known.Fingerprint != fp {
		t.Errorf("つなぐ先と信頼したつなぎ先の両方が新しいフィンガープリントになるはず: %+v", f)
	}
	if f.Token != now.Token || f.User != "bob" || !f.ExpiresAt.Equal(expires) || f.Known.User != "bob" {
		t.Errorf("トークンとログインの名前は残すはず: %+v", f)
	}
	if v := s.ServerConnection(); v.Pending != "server" || !v.PendingCert || v.PendingLogin {
		t.Errorf("起動し直してください(新しい証明書で。ログインし直さない)のお知らせに要るもの: %+v", v)
	}
	if s.conn.key != "aibowapp_x" {
		t.Errorf("ログアウトのために持っているトークンも残すはず: %q", s.conn.key)
	}

	// もう一度同じフィンガープリントで「接続」しても、トークンは残ったまま
	if _, err := s.ConnectServer(ConnectRequest{URL: srv.URL, Fingerprint: fp}); err != nil {
		t.Fatal(err)
	}
	if f, _ := config.LoadConnectionFile(path); f.Token != now.Token {
		t.Errorf("2回目でもトークンは残るはず: %+v", f)
	}
}

// 使っている途中で 401 になったら、トークンを手放してログインの画面に戻す知らせを1回だけ出す(0057 の決定事項 1)。
// 前のキーで送った呼び出しの返事が遅れて届いても、今のキーは手放さない
func TestUnauthorizedSignsOutOnce(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer aibowapp_new" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":{"message":"期限が切れました","type":"invalid_request_error","code":"token_expired"}}`))
			return
		}
		w.Write([]byte(`{"login": true, "name": "bob"}`))
	}))
	defer srv.Close()

	cfg := appConfig{connPath: filepath.Join(t.TempDir(), "connection.json"), serverURL: srv.URL, serverSource: serverScreen, serverKey: "aibowapp_old"}
	c := newConnection(cfg)
	c.client = apiclient.New(srv.URL)
	c.client.SetKey(cfg.serverKey)
	c.loginRequired = true
	c.watchUnauthorized()
	signedOut := 0
	c.onSignedOut = func() { signedOut++ }

	// 一覧の取り直しのように、何本も断られても知らせは1回
	for range 3 {
		if _, _, err := c.client.Me(); err == nil {
			t.Fatal("期限切れのトークンなのに通った")
		}
	}
	if signedOut != 1 || c.client.Key() != "" || !c.needLogin() {
		t.Errorf("1回だけ知らせ、トークンを手放してログインの画面にするはず: 知らせ %d 回・キー %q・ログインが要る %v", signedOut, c.client.Key(), c.needLogin())
	}

	// ログインし直したあと、前のキーで断られた返事が遅れて届いても、今のキーは手放さない
	c.client.SetKey("aibowapp_new")
	c.unauthorized("aibowapp_old", &apiclient.APIError{Status: http.StatusUnauthorized, Message: "期限が切れました"})
	if signedOut != 1 || c.client.Key() != "aibowapp_new" {
		t.Errorf("前のキーの 401 では何もしないはず: 知らせ %d 回・キー %q", signedOut, c.client.Key())
	}
}

// settings.json・環境変数で手で書いた API キーは、401 でもログインの画面にしない(直せないので、エラーで伝える)
func TestUnauthorizedIgnoresHandWrittenKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"違います","code":"invalid_api_key"}}`))
	}))
	defer srv.Close()
	c := newConnection(appConfig{serverURL: srv.URL, serverSource: serverSettings, serverKey: "aibow_x"})
	c.client = apiclient.New(srv.URL)
	c.client.SetKey("aibow_x")
	c.watchUnauthorized()
	called := false
	c.onSignedOut = func() { called = true }
	if _, _, err := c.client.Me(); err == nil {
		t.Fatal("401 なのに通った")
	}
	if called || c.client.Key() != "aibow_x" {
		t.Errorf("手で書いたキーでは何もしないはず: 知らせ %v・キー %q", called, c.client.Key())
	}
}
