package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/secondarykey/aibow/api"
	"github.com/secondarykey/aibow/auth"
	"github.com/secondarykey/aibow/config"
	"github.com/secondarykey/aibow/store"
)

// サーバーの PC の事情を変える口(aibow 専用の口)は、ログインが要る形では管理者だけ。127.0.0.1 から来ても管理者でなければ 403(0046 の完了の条件 1)。
// ログインなしの形(キーが要らない)では通す。プロキシ越しの外の元の相手は、手前の fromProxy が 403 にする(0044 の決定事項 12)
func TestAPIAdminOnly(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/server.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	var env config.Env
	key := map[string]string{}
	for _, name := range []string{"alice", "bob"} {
		if _, err := createUser(ctx, st, initialSettings(&env, 0), name, ""); err != nil {
			t.Fatal(err)
		}
		if key[name], _, err = renewKey(ctx, st, name, auth.DefaultKeyDays, ""); err != nil {
			t.Fatal(err)
		}
	}
	alice, err := st.UserByName(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetAdmin(ctx, alice.ID, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	keys := auth.NewKeys(st)
	proxies := newProxyTrust(nil, "X-Forwarded-For")

	unload := func(h http.Handler, remote, xff, key string) int {
		t.Helper()
		r := httptest.NewRequest("POST", "/models/unload", strings.NewReader(`{}`))
		r.RemoteAddr = remote
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		if key != "" {
			r.Header.Set("Authorization", "Bearer "+key)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	// 通ると、モデルを切り替えられないサーバーなので 404(断られると 403)
	const passed = http.StatusNotFound

	h := fromProxy(proxies, true, apiHandler(api.New(&usageGen{}, nil, 4096, api.Defaults{MaxTokens: 100}), keys, true))
	for _, c := range []struct {
		name, remote, xff, key string
		want                   int
	}{
		{"管理者でない人は、127.0.0.1 からでも断る", "127.0.0.1:5000", "", key["bob"], http.StatusForbidden},
		{"::1 からでも", "[::1]:5000", "", key["bob"], http.StatusForbidden},
		{"ほかの PC の、管理者でない人", "192.168.1.20:5000", "", key["bob"], http.StatusForbidden},
		{"管理者は 127.0.0.1 から通る", "127.0.0.1:5000", "", key["alice"], passed},
		{"ほかの PC の管理者も通る", "192.168.1.20:5000", "", key["alice"], passed},
		{"プロキシ越しの管理者も通る", "127.0.0.1:5000", "192.0.2.1", key["alice"], passed},
		{"キーが無ければ 401", "127.0.0.1:5000", "", "", http.StatusUnauthorized},
	} {
		if got := unload(h, c.remote, c.xff, c.key); got != c.want {
			t.Errorf("%s: %d(期待 %d)", c.name, got, c.want)
		}
	}

	// ログインなしの形(login: false。キーが要らない)
	h = fromProxy(proxies, false, apiHandler(api.New(&usageGen{}, nil, 4096, api.Defaults{MaxTokens: 100}), keys, false))
	if got := unload(h, "127.0.0.1:5000", "", ""); got != passed {
		t.Errorf("ログインなしの形は、キーなしで通る: %d", got)
	}
	if got := unload(h, "127.0.0.1:5000", "192.0.2.1", ""); got != http.StatusForbidden {
		t.Errorf("ログインなしの形でも、プロキシ越しの外の人は 403: %d", got)
	}
}

// 画面用の口を呼んだ人:ログインなしの形はサーバーの PC の事情を変えてよい。ログインが要る形でログインしていなければ 401 になるエラー(0046)
func TestWhoManagesPC(t *testing.T) {
	r := httptest.NewRequest("POST", "/_aibow/ui/call/UnloadModel", nil)
	r.RemoteAddr = "127.0.0.1:5000"
	c, err := who(false)(r)
	if err != nil || !c.ManagesPC() || c.Admin {
		t.Errorf("ログインなしの形: %+v %v(管理者の印は付けずに、変えてよいことにする)", c, err)
	}
	if _, err := who(true)(r); err != errNotLoggedIn {
		t.Errorf("ログインが要る形で、127.0.0.1 からでもログインしていなければエラー: %v", err)
	}
}
