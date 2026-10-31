package auth_test

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/secondarykey/aibow/auth"
)

// 最後に使った時刻は、ログイン・セッション・API キー・アプリ用のトークンのどれで使っても書くが、
// 前に書いてから1日以上たったときだけ書く(0031 の決定事項 21)
func TestLastUsed(t *testing.T) {
	st := openStore(t)
	u := addUser(t, st, "bob", "pw")
	s := auth.NewSessions(st)
	k := auth.NewKeys(st)
	now := time.Date(2026, 11, 1, 9, 0, 0, 0, time.Local)
	auth.SetNow(s, func() time.Time { return now })
	auth.SetKeysNow(k, func() time.Time { return now })
	used := func() time.Time {
		t.Helper()
		got, err := st.UserByID(ctx, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got.LastUsedAt
	}
	if !used().IsZero() {
		t.Fatal("一度も使っていなければゼロ")
	}

	token, _, err := s.Login(ctx, "bob", "pw")
	if err != nil {
		t.Fatal(err)
	}
	if !used().Equal(now) {
		t.Errorf("ログインで書く: %v", used())
	}
	login := now

	// 1日たつまでは、使っても書かない
	now = now.Add(23 * time.Hour)
	if _, _, err := s.User(ctx, token); err != nil {
		t.Fatal(err)
	}
	if !used().Equal(login) {
		t.Errorf("1日たつ前に書いた: %v", used())
	}
	// 1日たったら書く
	now = now.Add(2 * time.Hour)
	if _, _, err := s.User(ctx, token); err != nil {
		t.Fatal(err)
	}
	if !used().Equal(now) {
		t.Errorf("セッションで1日たったら書く: %v", used())
	}

	// API キー
	key := auth.NewAPIKey()
	if err := st.SetAPIKey(ctx, u.ID, auth.KeyID(key), now); err != nil {
		t.Fatal(err)
	}
	now = now.Add(25 * time.Hour)
	if _, err := k.User(ctx, key); err != nil {
		t.Fatal(err)
	}
	if !used().Equal(now) {
		t.Errorf("API キーで書く: %v", used())
	}

	// アプリ用のトークン(ログインとトークンでの呼び出し)
	now = now.Add(25 * time.Hour)
	app, err := s.IssueAppToken(ctx, "bob", "pw", "pc")
	if err != nil {
		t.Fatal(err)
	}
	if !used().Equal(now) {
		t.Errorf("アプリのログインで書く: %v", used())
	}
	now = now.Add(25 * time.Hour)
	if _, err := k.User(ctx, app.Token); err != nil {
		t.Fatal(err)
	}
	if !used().Equal(now) {
		t.Errorf("アプリ用のトークンで書く: %v", used())
	}
}

func TestCheckDisplayName(t *testing.T) {
	for in, want := range map[string]string{"  佐藤 花子(営業) ": "佐藤 花子(営業)", "": "", strings.Repeat("あ", 64): strings.Repeat("あ", 64)} {
		if got, err := auth.CheckDisplayName(in); err != nil || got != want {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	for _, in := range []string{"佐藤\n花子", "a\tb", strings.Repeat("あ", 65)} {
		if _, err := auth.CheckDisplayName(in); err == nil {
			t.Errorf("%q は断る", in)
		}
	}
}

// 本人が表示名を変える(POST /aibow/auth/display)。me に表示名が入る
func TestDisplayNameHTTP(t *testing.T) {
	st := openStore(t)
	addUser(t, st, "bob", "pw")
	s := auth.NewSessions(st)
	srv := httptest.NewServer(s.Middleware(auth.Handler(s, true)))
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	post := func(path, body string) (int, auth.Me) {
		t.Helper()
		res, err := c.Post(srv.URL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var m auth.Me
		json.NewDecoder(res.Body).Decode(&m)
		return res.StatusCode, m
	}
	if code, _ := post("/aibow/auth/display", `{"display_name":"佐藤"}`); code != http.StatusUnauthorized {
		t.Errorf("ログインの前: %d", code)
	}
	post("/aibow/auth/login", `{"name":"bob","password":"pw"}`)
	if code, m := post("/aibow/auth/display", `{"display_name":" 佐藤 花子 "}`); code != http.StatusOK || m.DisplayName != "佐藤 花子" {
		t.Errorf("変える: %d %+v", code, m)
	}
	if code, _ := post("/aibow/auth/display", `{"display_name":"a\nb"}`); code != http.StatusBadRequest {
		t.Errorf("制御文字: %d", code)
	}
	res, err := c.Get(srv.URL + "/aibow/auth/me")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var m auth.Me
	json.NewDecoder(res.Body).Decode(&m)
	if m.Name != "bob" || m.DisplayName != "佐藤 花子" {
		t.Errorf("me: %+v", m)
	}
}
