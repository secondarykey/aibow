package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/secondarykey/aibow/internal/appversion"
)

// setAppVersion は、テストの間だけアプリのバージョンを決める(パッケージで1つなので、終わったら戻す)
func setAppVersion(t *testing.T, v string) {
	t.Helper()
	SetAppVersion(v)
	t.Cleanup(func() { appVersion.Store(nil) })
}

// 全部のリクエストに X-Aibow-App-Version が付く。決めていなければ付けない(0054 の完了の条件 1)
func TestAppVersionOnEveryRequest(t *testing.T) {
	var mu sync.Mutex
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = append(got, r.URL.Path+"="+r.Header.Get(appversion.AppHeader))
		mu.Unlock()
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	c.Me()
	if len(got) != 1 || got[0] != "/_aibow/auth/key=" {
		t.Errorf("決めていなければ付けない: %v", got)
	}

	setAppVersion(t, " 0.9.3+dev ")
	got = nil
	c.Me()
	c.Models()
	c.Post(context.Background(), "/_aibow/store/list", map[string]string{}, nil)
	c.AppLogin("bob", "pw", "pc1")
	ProbeServer(context.Background(), srv.URL, "")
	if len(got) != 5 {
		t.Fatalf("5 回呼んだ: %v", got)
	}
	for _, g := range got {
		if g[len(g)-len("=0.9.3+dev"):] != "=0.9.3+dev" {
			t.Errorf("全部に付く(+dev もそのまま): %v", got)
			break
		}
	}
}

// 返事の見出しから、最後に知ったサーバーのバージョンを覚える。初めて知ったとき・変わったときだけ知らせる。
// 見出しの無い古い aibow-server でも、エラーにならずに空のまま(完了の条件 4)
func TestServerVersionFromResponse(t *testing.T) {
	var mu sync.Mutex
	server, minApp := "", ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if server != "" {
			w.Header().Set(appversion.ServerHeader, server)
			w.Header().Set(appversion.MinAppHeader, minApp)
		}
		mu.Unlock()
		if r.URL.Path == "/_aibow/auth/token" {
			w.WriteHeader(http.StatusUnauthorized) // エラーの返事からも覚える
			w.Write([]byte(`{"error":{"message":"違います"}}`))
			return
		}
		w.Write([]byte(`{"login":false,"name":""}`))
	}))
	defer srv.Close()
	set := func(s, m string) { mu.Lock(); server, minApp = s, m; mu.Unlock() }

	c := New(srv.URL)
	var notes []string
	c.OnServerVersion(func(s, m string) { notes = append(notes, s+"/"+m) })
	if s, m := c.ServerVersion(); s != "" || m != "" {
		t.Errorf("まだ返事を受けていない: %q %q", s, m)
	}
	if _, _, err := c.Me(); err != nil {
		t.Fatalf("見出しの無い古い aibow-server でもエラーにならない: %v", err)
	}
	if s, _ := c.ServerVersion(); s != "" || len(notes) != 0 {
		t.Errorf("見出しが無ければ空で、知らせない: %q %v", s, notes)
	}

	set("0.9.3", "0.9.3")
	c.Me()
	c.Me()
	set("0.9.4", "0.9.3")
	c.AppLogin("bob", "x", "pc1")
	if s, m := c.ServerVersion(); s != "0.9.4" || m != "0.9.3" {
		t.Errorf("最後の返事の値: %q %q", s, m)
	}
	if len(notes) != 2 || notes[0] != "0.9.3/0.9.3" || notes[1] != "0.9.4/0.9.3" {
		t.Errorf("初めて知ったとき・変わったときだけ: %v", notes)
	}

	p, err := ProbeServer(context.Background(), srv.URL, "")
	if err != nil || p.ServerVersion != "0.9.4" || p.MinAppVersion != "0.9.3" {
		t.Errorf("Probe にも入る: %+v %v", p, err)
	}
}
