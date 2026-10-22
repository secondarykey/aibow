package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/secondarykey/aibow/auth"
	"github.com/secondarykey/aibow/store"
)

// 利用者の管理の口(0018 の決定事項 7)。ブラウザでログインした管理者だけが呼べ、パスワードと API キーは返事で一度だけ渡す
func TestAdminUsers(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	var env envReader
	initial := initialSettings(&env, 0)
	alicePW, err := createUser(ctx, st, initial, "alice", "")
	if err != nil {
		t.Fatal(err)
	}
	bobPW, err := createUser(ctx, st, initial, "bob", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := setAdmin(ctx, st, "alice", true); err != nil {
		t.Fatal(err)
	}
	sessions := auth.NewSessions(st)
	srv := httptest.NewServer(sessions.Middleware(adminHandler(st, initial)))
	defer srv.Close()
	login := func(name, pw string) string {
		token, _, err := sessions.Login(ctx, name, pw)
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	alice, bob := login("alice", alicePW), login("bob", bobPW)

	do := func(token, method, path, body string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}

	if code, _ := do("", "GET", "/aibow/admin/users", ""); code != http.StatusUnauthorized {
		t.Errorf("ログインしていない: %d", code)
	}
	if code, _ := do(bob, "GET", "/aibow/admin/users", ""); code != http.StatusForbidden {
		t.Errorf("管理者でない: %d", code)
	}

	code, body := do(alice, "GET", "/aibow/admin/users", "")
	var list []adminUser
	json.Unmarshal([]byte(body), &list)
	if code != http.StatusOK || len(list) != 3 || list[0].Name != "local" || !list[0].Local || !list[1].Admin || list[2].Admin || !list[2].Password {
		t.Fatalf("一覧: %d %s", code, body)
	}

	code, body = do(alice, "POST", "/aibow/admin/users", `{"name":"carol"}`)
	var made map[string]string
	json.Unmarshal([]byte(body), &made)
	if code != http.StatusOK || made["name"] != "carol" || made["password"] == "" {
		t.Fatalf("作る: %d %s", code, body)
	}
	if _, _, err := sessions.Login(ctx, "carol", made["password"]); err != nil {
		t.Errorf("返したパスワードでログインできない: %v", err)
	}
	if code, _ := do(alice, "POST", "/aibow/admin/users", `{"name":"carol"}`); code != http.StatusConflict {
		t.Errorf("同じ名前: %d", code)
	}
	if code, _ := do(alice, "POST", "/aibow/admin/users", `{"name":"a b"}`); code != http.StatusBadRequest {
		t.Errorf("使えない名前: %d", code)
	}
	if code, _ := do(alice, "POST", "/aibow/admin/users", ""); code != http.StatusUnsupportedMediaType {
		t.Errorf("JSON でない本文: %d", code)
	}

	// パスワードを作り直すと、その人のログインは切れる
	code, body = do(alice, "POST", "/aibow/admin/users/bob/password", "")
	if code != http.StatusOK || !strings.Contains(body, `"password"`) {
		t.Fatalf("パスワードを作り直す: %d %s", code, body)
	}
	if _, _, err := sessions.User(ctx, bob); err == nil {
		t.Error("パスワードを作り直しても、前のログインが残っている")
	}

	code, body = do(alice, "POST", "/aibow/admin/users/carol/key", "")
	var key map[string]string
	json.Unmarshal([]byte(body), &key)
	if code != http.StatusOK || !strings.HasPrefix(key["key"], "aibow_") {
		t.Fatalf("API キー: %d %s", code, body)
	}
	if u, err := auth.NewKeys(st).User(ctx, key["key"]); err != nil || u.Name != "carol" {
		t.Errorf("返したキーで carol にならない: %+v %v", u, err)
	}
	if code, _ := do(alice, "POST", "/aibow/admin/users/nobody/key", ""); code != http.StatusNotFound {
		t.Errorf("いない人: %d", code)
	}

	for _, name := range []string{"alice", "local"} {
		if code, _ := do(alice, "DELETE", "/aibow/admin/users/"+name, ""); code != http.StatusBadRequest {
			t.Errorf("%s は消せないはず: %d", name, code)
		}
	}
	if code, body := do(alice, "DELETE", "/aibow/admin/users/carol", ""); code != http.StatusOK {
		t.Errorf("消す: %d %s", code, body)
	}
	if _, err := st.UserByName(ctx, "carol"); err == nil {
		t.Error("消えていない")
	}
}
