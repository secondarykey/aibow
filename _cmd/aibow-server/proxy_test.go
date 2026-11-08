package main

import (
	"errors"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/secondarykey/aibow/api"
	"github.com/secondarykey/aibow/auth"
	"github.com/secondarykey/aibow/config"
	"github.com/secondarykey/aibow/store"
)

// 元の相手の決め方(0044 の決定事項 6〜9)。ヘッダーを信じるのは、つないできた相手が信頼するプロキシのときだけ
func TestProxyOrigin(t *testing.T) {
	extra := []netip.Prefix{netip.MustParsePrefix("10.0.0.5/32"), netip.MustParsePrefix("192.168.10.0/24")}
	for _, tc := range []struct {
		name    string
		extra   []netip.Prefix
		header  string // 選んだヘッダー(空なら X-Forwarded-For)
		remote  string
		headers map[string][]string
		want    string // 元の相手(空なら読めない値のエラー)
		fwd     bool
	}{
		{name: "書かなくても 127.0.0.1 は信頼する", remote: "127.0.0.1:5000",
			headers: map[string][]string{"X-Forwarded-For": {"192.0.2.1"}}, want: "192.0.2.1", fwd: true},
		{name: "::1 も", remote: "[::1]:5000",
			headers: map[string][]string{"X-Forwarded-For": {"192.0.2.1"}}, want: "192.0.2.1", fwd: true},
		{name: "127.0.0.0/8 全体", remote: "127.0.0.2:5000",
			headers: map[string][]string{"X-Forwarded-For": {"192.0.2.1"}}, want: "192.0.2.1", fwd: true},
		{name: "書かなければ、ほかの PC のプロキシは信頼しない", remote: "10.0.0.5:5000",
			headers: map[string][]string{"X-Forwarded-For": {"192.0.2.1"}}, want: "10.0.0.5"},
		{name: "書いたほかの PC のプロキシ", extra: extra, remote: "10.0.0.5:5000",
			headers: map[string][]string{"X-Forwarded-For": {"192.0.2.1"}}, want: "192.0.2.1", fwd: true},
		{name: "一覧に無い相手のヘッダーは偽れない", extra: extra, remote: "198.51.100.7:5000",
			headers: map[string][]string{"X-Forwarded-For": {"127.0.0.1"}}, want: "198.51.100.7"},
		{name: "左を偽っても、右から読む", remote: "127.0.0.1:5000",
			headers: map[string][]string{"X-Forwarded-For": {"127.0.0.1, 192.0.2.1"}}, want: "192.0.2.1", fwd: true},
		{name: "2段のプロキシ(両方とも一覧に)", extra: extra, remote: "127.0.0.1:5000",
			headers: map[string][]string{"X-Forwarded-For": {"203.0.113.9, 192.0.2.1, 10.0.0.5"}}, want: "192.0.2.1", fwd: true},
		{name: "全部が一覧に入っていたら、いちばん左", extra: extra, remote: "127.0.0.1:5000",
			headers: map[string][]string{"X-Forwarded-For": {"192.168.10.20, 10.0.0.5"}}, want: "192.168.10.20", fwd: true},
		{name: "複数行は上から順につなげる", extra: extra, remote: "127.0.0.1:5000",
			headers: map[string][]string{"X-Forwarded-For": {"192.0.2.1", "10.0.0.5"}}, want: "192.0.2.1", fwd: true},
		{name: "IIS の ARR はポートも書く", remote: "127.0.0.1:5000",
			headers: map[string][]string{"X-Forwarded-For": {"192.0.2.1:51234"}}, want: "192.0.2.1", fwd: true},
		{name: "IPv4 を IPv6 の形で書いた値は IPv4", remote: "127.0.0.1:5000",
			headers: map[string][]string{"X-Forwarded-For": {"::ffff:192.0.2.1"}}, want: "192.0.2.1", fwd: true},
		{name: "ヘッダーが無ければ接続の相手", remote: "127.0.0.1:5000", want: "127.0.0.1"},
		{name: "読めない値", remote: "127.0.0.1:5000",
			headers: map[string][]string{"X-Forwarded-For": {"proxy.example.local"}}},
		{name: "空の値", remote: "127.0.0.1:5000",
			headers: map[string][]string{"X-Forwarded-For": {""}}},
		{name: "読めない値でも、読む前に信頼しない相手があれば届かない", remote: "127.0.0.1:5000",
			headers: map[string][]string{"X-Forwarded-For": {"unknown, 192.0.2.1"}}, want: "192.0.2.1", fwd: true},
		{name: "Forwarded の引用符と IPv6", header: "Forwarded", remote: "127.0.0.1:5000",
			headers: map[string][]string{"Forwarded": {`for=192.0.2.60;proto=http;by=203.0.113.43, for="[2001:db8::1]:4711"`}}, want: "2001:db8::1", fwd: true},
		{name: "Forwarded の FOR(大文字)", header: "Forwarded", remote: "127.0.0.1:5000",
			headers: map[string][]string{"Forwarded": {"proto=https;FOR=192.0.2.60"}}, want: "192.0.2.60", fwd: true},
		{name: "Forwarded の伏せた名前は読めない", header: "Forwarded", remote: "127.0.0.1:5000",
			headers: map[string][]string{"Forwarded": {"for=_hidden"}}},
		{name: "Forwarded の unknown も", header: "Forwarded", remote: "127.0.0.1:5000",
			headers: map[string][]string{"Forwarded": {"for=unknown"}}},
		{name: "Forwarded に for= が無い", header: "Forwarded", remote: "127.0.0.1:5000",
			headers: map[string][]string{"Forwarded": {"proto=https"}}},
		{name: "X-Real-IP", header: "X-Real-IP", remote: "127.0.0.1:5000",
			headers: map[string][]string{"X-Real-Ip": {"192.0.2.1"}}, want: "192.0.2.1", fwd: true},
		{name: "X-Real-IP は並びにしない", header: "X-Real-IP", remote: "127.0.0.1:5000",
			headers: map[string][]string{"X-Real-Ip": {"192.0.2.1, 10.0.0.5"}}},
		{name: "選ばなかったヘッダーは見ない(X-Forwarded-For を選んで X-Real-IP)", remote: "127.0.0.1:5000",
			headers: map[string][]string{"X-Real-Ip": {"192.0.2.1"}}, want: "127.0.0.1"},
		{name: "選ばなかったヘッダーは見ない(X-Real-IP を選んで X-Forwarded-For)", header: "X-Real-IP", remote: "127.0.0.1:5000",
			headers: map[string][]string{"X-Forwarded-For": {"192.0.2.1"}}, want: "127.0.0.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			header, err := config.ProxyHeaderName(tc.header)
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = tc.remote
			for k, vs := range tc.headers {
				for _, v := range vs {
					r.Header.Add(k, v)
				}
			}
			a, fwd, err := newProxyTrust(tc.extra, header).origin(r)
			if tc.want == "" {
				if !errors.Is(err, errBadForward) {
					t.Fatalf("読めない値のエラーのはず: %v %v", a, err)
				}
				return
			}
			if err != nil || a.String() != tc.want || fwd != tc.fwd {
				t.Errorf("元の相手 %v(転送 %v)・%v。want %s(%v)", a, fwd, err, tc.want, tc.fwd)
			}
		})
	}
}

// mount の外側で r.RemoteAddr を書き換える(決定事項 10)。読めない値は 400、login: false で外の元の相手は 403(決定事項 8・12)
func TestFromProxy(t *testing.T) {
	seen := ""
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.RemoteAddr
		io.WriteString(w, "ok")
	})
	do := func(h http.Handler, remote, xff string) int {
		t.Helper()
		seen = ""
		r := httptest.NewRequest("GET", "/aibow/v1/models", nil)
		r.RemoteAddr = remote
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	p := newProxyTrust(nil, "X-Forwarded-For")
	h := fromProxy(p, true, mount("/aibow", inner))
	if code := do(h, "127.0.0.1:5000", "192.0.2.1"); code != http.StatusOK || seen != "192.0.2.1:0" {
		t.Errorf("元の相手に書き換えて渡すはず: %d %q", code, seen)
	}
	if code := do(h, "[::1]:5000", "2001:db8::1"); code != http.StatusOK || seen != "[2001:db8::1]:0" {
		t.Errorf("IPv6 の元の相手: %d %q", code, seen)
	}
	if code := do(h, "127.0.0.1:5000", ""); code != http.StatusOK || seen != "127.0.0.1:5000" {
		t.Errorf("ヘッダーが無ければ書き換えない: %d %q", code, seen)
	}
	if code := do(h, "127.0.0.1:5000", "unknown"); code != http.StatusBadRequest || seen != "" {
		t.Errorf("読めない値は 400 で、中に渡さない: %d %q", code, seen)
	}

	// login: false は、サーバーの PC の上の人だけ
	h = fromProxy(p, false, inner)
	if code := do(h, "127.0.0.1:5000", "192.0.2.1"); code != http.StatusForbidden || seen != "" {
		t.Errorf("login: false で外の元の相手は 403: %d %q", code, seen)
	}
	if code := do(h, "127.0.0.1:5000", ""); code != http.StatusOK {
		t.Errorf("login: false でもサーバーの PC の上からは通す: %d", code)
	}
	if code := do(h, "127.0.0.1:5000", "127.0.0.1"); code != http.StatusOK {
		t.Errorf("同じ PC のプロキシを同じ PC から通ったときも通す: %d", code)
	}
}

// サーバーの PC の上からだけの操作(api.IsLocal)は、プロキシの後ろでも元の相手を見る(完了の条件 1)
func TestFromProxyIsLocal(t *testing.T) {
	srv := api.New(&usageGen{}, nil, 4096, api.Defaults{MaxTokens: 100})
	h := fromProxy(newProxyTrust(nil, "X-Forwarded-For"), true, srv.Handler())
	unload := func(remote, xff string) int {
		t.Helper()
		r := httptest.NewRequest("POST", "/models/unload", strings.NewReader(`{}`))
		r.RemoteAddr = remote
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	// 通ると、モデルを切り替えられないサーバーなので 404(断られると 403)
	if code := unload("127.0.0.1:5000", "192.0.2.1"); code != http.StatusForbidden {
		t.Errorf("プロキシ越しの外の人は、サーバーの PC の上からではない: %d", code)
	}
	if code := unload("127.0.0.1:5000", ""); code != http.StatusNotFound {
		t.Errorf("サーバーの PC の上から直接は通る: %d", code)
	}
	if code := unload("198.51.100.7:5000", "127.0.0.1"); code != http.StatusForbidden {
		t.Errorf("外からヘッダーでサーバーの PC の上を名乗っても通らない: %d", code)
	}
}

// ログインを続けて間違えたときの制限は、元の相手ごとに数える(1人が止められても、ほかの人はログインできる)
func TestFromProxyLoginLimit(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	h := fromProxy(newProxyTrust(nil, "X-Forwarded-For"), true, auth.Handler(auth.NewSessions(st), true))
	login := func(xff string) int {
		t.Helper()
		r := httptest.NewRequest("POST", "/_aibow/auth/login", strings.NewReader(`{"name":"nobody","password":"wrong-password"}`))
		r.RemoteAddr = "127.0.0.1:5000"
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Forwarded-For", xff)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	for range 5 {
		if code := login("192.0.2.1"); code != http.StatusUnauthorized {
			t.Fatalf("間違えたら 401: %d", code)
		}
	}
	if code := login("192.0.2.1"); code != http.StatusTooManyRequests {
		t.Errorf("続けて間違えた元の相手は止める: %d", code)
	}
	if code := login("192.0.2.2"); code != http.StatusUnauthorized {
		t.Errorf("別の元の相手は止めない(プロキシごとに数えない): %d", code)
	}
}

// server.json の書けない値(壊れた CIDR・知らない proxyHeader)で起動が止まる。書いた値は読める(決定事項 3・4)
func TestResolveProxies(t *testing.T) {
	dir := t.TempDir()
	run := func(body string) (serverConfig, error) {
		t.Helper()
		path := filepath.Join(dir, "server.json")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		f := defineFlags(fs)
		fs.Parse([]string{"-config", path})
		var env config.Env
		c, _, err := resolve(fs, f, &env)
		return c, err
	}
	c, err := run(`{}`)
	if err != nil || c.ProxyHeader != "X-Forwarded-For" || len(c.TrustedProxies) != 0 {
		t.Errorf("書かなければ X-Forwarded-For で、足すプロキシは無し: %+v %v", c.TrustedProxies, err)
	}
	c, err = run(`{"trustedProxies": ["10.0.0.5", "192.168.10.0/24", "::ffff:10.0.0.6"], "proxyHeader": "forwarded"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := newProxyTrust(c.TrustedProxies, c.ProxyHeader).describe(); got != "この PC の上, 10.0.0.5, 192.168.10.0/24, 10.0.0.6" || c.ProxyHeader != "Forwarded" {
		t.Errorf("読んだ一覧: %q %q", got, c.ProxyHeader)
	}
	for body, want := range map[string]string{
		`{"trustedProxies": ["10.0.0.0/33"]}`:         "trustedProxies",
		`{"trustedProxies": ["proxy.example.local"]}`: "trustedProxies",
		`{"proxyHeader": "X-Client-IP"}`:              "proxyHeader",
	} {
		if _, err := run(body); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: エラーに %q が入っていません: %v", body, want, err)
		}
	}
	// 全部のアドレスを含む範囲は止めない(起動のときに注意だけ。決定事項 5)
	c, err = run(`{"trustedProxies": ["0.0.0.0/0"]}`)
	if err != nil {
		t.Fatal(err)
	}
	if all := newProxyTrust(c.TrustedProxies, c.ProxyHeader).all(); len(all) != 1 || all[0] != "0.0.0.0/0" {
		t.Errorf("全部のアドレスを含む範囲: %v", all)
	}
}
