package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 画面は / と /assets/ だけで配り、index.html にはブラウザの印を足す。それ以外のパスは、これまでどおり api と画面用の口が受け取る
func TestRoutes(t *testing.T) {
	named := func(name string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, name) })
	}
	h := routes(named("api"), named("ui"), screen())

	get := func(method, path string) (int, string) {
		t.Helper()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
		return rec.Code, rec.Body.String()
	}

	code, body := get("GET", "/")
	if code != http.StatusOK || !strings.Contains(body, browserMarker) {
		t.Errorf("GET / は印の付いた index.html のはず: %d %.200s", code, body)
	}
	// 印は head の中(画面のスクリプトより前)にある
	if i, j := strings.Index(body, browserMarker), strings.Index(body, "<script"); j >= 0 && i > j {
		t.Errorf("印がスクリプトより後ろにある")
	}
	for _, c := range []struct{ method, path, want string }{
		{"GET", "/v1/models", "api"},
		{"POST", "/v1/chat/completions", "api"},
		{"GET", "/models", "api"},
		{"POST", "/aibow/count-tokens", "api"},
		{"POST", "/", "api"}, // GET 以外の / は画面ではない(api の 404 の形)
		{"GET", "/nothing", "api"},
		{"POST", "/aibow/ui/call/Version", "ui"},
		{"GET", "/aibow/ui/events", "ui"},
		{"POST", "/aibow/auth/login", "ui"},              // ログインの口も、ブラウザの会話の係が受け持つ
		{"POST", "/aibow/store/ListConversations", "ui"}, // 画面のアプリの保存の口も(同じ server.db)
	} {
		if _, body := get(c.method, c.path); body != c.want {
			t.Errorf("%s %s は %s が受け取るはず: %.80q", c.method, c.path, c.want, body)
		}
	}
	// /assets/ は画面のファイル(無いファイルは 404。api には渡さない)
	if code, body := get("GET", "/assets/nothing.js"); code != http.StatusNotFound || body == "api" {
		t.Errorf("GET /assets/ の無いファイル: %d %q", code, body)
	}
}
