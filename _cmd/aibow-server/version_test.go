package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/secondarykey/aibow/internal/appversion"
)

// 返事には、道によらず(/v1/・/_aibow/・画面・頭の外の 404)版の見出しが2つ付く(0054 の決定事項 8)。
// 組み立ては main と同じ:fromProxy の内側・mount の外(中の口は 404 の偽物で、エラーの返事にも付くことも見る)
func TestVersionHeadersOnEveryResponse(t *testing.T) {
	h := fromProxy(newProxyTrust(nil, "X-Forwarded-For"), true,
		withVersion(newAppSeen(), mount("/aibow", routes(http.NotFoundHandler(), http.NotFoundHandler(), screen()))))
	for _, path := range []string{"/aibow/v1/models", "/aibow/_aibow/auth/me", "/aibow/", "/aibow/no-such", "/other"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if got := rec.Header().Get(appversion.ServerHeader); got != appversion.Version() {
			t.Errorf("%s: %s = %q(%d)", path, appversion.ServerHeader, got, rec.Code)
		}
		if got := rec.Header().Get(appversion.MinAppHeader); got != appversion.MinApp {
			t.Errorf("%s: %s = %q", path, appversion.MinAppHeader, got)
		}
	}
}

// 画面のアプリの版は、(相手, 版)ごとに1回だけ Notice で出る。見出しの無いリクエスト(Cline など)では出ない(決定事項 9・13)
func TestAppVersionLoggedOncePerPeer(t *testing.T) {
	buf := captureLog(t)
	h := withVersion(newAppSeen(), http.NotFoundHandler())
	send := func(remote, version string) {
		r := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		r.RemoteAddr = remote
		if version != "" {
			r.Header.Set(appversion.AppHeader, version)
		}
		h.ServeHTTP(httptest.NewRecorder(), r)
	}
	send("192.0.2.1:5000", "")          // Cline など
	send("192.0.2.1:5000", "0.9.2")     // 初めて
	send("192.0.2.1:5001", "0.9.2")     // 同じ相手・同じ版(ポートは違う)
	send("192.0.2.1:5000", "0.9.3+dev") // 版が変わった
	send("192.0.2.7:5000", "0.9.2")     // ほかの相手
	send("192.0.2.8:5000", "\x01;")     // 使える文字が無い
	out := buf.String()
	if n := strings.Count(out, "画面のアプリ("); n != 3 {
		t.Errorf("(相手, 版)ごとに1回で3行: %d\n%s", n, out)
	}
	for _, want := range []string{"画面のアプリ(0.9.2)が 192.0.2.1 から", "画面のアプリ(0.9.3+dev)が 192.0.2.1 から", "画面のアプリ(0.9.2)が 192.0.2.7 から"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q が無い:\n%s", want, out)
		}
	}
	if strings.Count(out, "level=INFO+2 ") != 3 { // Notice は slog の Info+2
		t.Errorf("Notice で出す:\n%s", out)
	}
}

// 覚える組の数には上限がある(見出しの値を変えながら送られても、メモリが増え続けない)
func TestAppSeenLimit(t *testing.T) {
	s := newAppSeen()
	for i := range maxAppSeen {
		s.first("192.0.2.1", strings.Repeat("1", i%60+1)+"."+string(rune('a'+i/60)))
	}
	if s.first("192.0.2.9", "9.9.9") {
		t.Error("上限を超えたら記録しない")
	}
}

func TestCleanVersion(t *testing.T) {
	for in, want := range map[string]string{" 0.9.2+dev ": "0.9.2+dev", "1.0.0-rc.1": "1.0.0-rc.1", "0.9\x1b[31m": "0.931m", "": ""} {
		if got := cleanVersion(in); got != want {
			t.Errorf("cleanVersion(%q) = %q, want %q", in, got, want)
		}
	}
	if got := cleanVersion(strings.Repeat("9", 100)); len(got) != 64 {
		t.Errorf("64 文字まで: %d", len(got))
	}
}
