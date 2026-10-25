package auth_test

// アプリ用のトークンのテスト(0019)。

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/secondarykey/aibow/auth"
)

// 期限は作ってから 10 日で固定(使っても延びない)。PC ごとに1本で、同じ PC でログインし直すと前のものは使えない
func TestAppTokens(t *testing.T) {
	st := openStore(t)
	u := addUser(t, st, "bob", "pw")
	s := auth.NewSessions(st)
	k := auth.NewKeys(st)
	now := time.Now()
	auth.SetNow(s, func() time.Time { return now })
	auth.SetKeysNow(k, func() time.Time { return now })

	if _, err := s.IssueAppToken(ctx, "bob", "違う", "pc1"); !errors.Is(err, auth.ErrLogin) {
		t.Errorf("違うパスワード: %v", err)
	}
	if _, err := s.IssueAppToken(ctx, "bob", "pw", " "); err == nil {
		t.Error("PC の名前が無ければ断る")
	}
	first, err := s.IssueAppToken(ctx, "bob", "pw", "pc1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(first.Token, "aibowapp_") || first.Name != "bob" || !first.ExpiresAt.Equal(now.Add(auth.AppTokenTTL)) {
		t.Errorf("返事: %+v", first)
	}
	if who, err := k.User(ctx, first.Token); err != nil || who.ID != u.ID {
		t.Errorf("トークンの持ち主: %+v %v", who, err)
	}
	other, _ := s.IssueAppToken(ctx, "bob", "pw", "pc2")
	again, _ := s.IssueAppToken(ctx, "bob", "pw", "pc1")
	if _, err := k.User(ctx, first.Token); !errors.Is(err, auth.ErrBadKey) {
		t.Errorf("同じ PC でログインし直したら、前のトークンは使えない: %v", err)
	}
	if _, err := k.User(ctx, other.Token); err != nil {
		t.Errorf("ほかの PC のトークンは残る: %v", err)
	}

	now = now.Add(9 * 24 * time.Hour)
	if _, err := k.User(ctx, again.Token); err != nil {
		t.Errorf("9 日後はまだ使える: %v", err)
	}
	now = now.Add(24 * time.Hour)
	if _, err := k.User(ctx, again.Token); !errors.Is(err, auth.ErrTokenExpired) {
		t.Errorf("使っていても 10 日で切れる: %v", err)
	}
	if _, err := k.User(ctx, again.Token); !errors.Is(err, auth.ErrBadKey) {
		t.Errorf("切れたトークンは消える: %v", err)
	}

	// ログアウトとパスワードの作り直しで消える
	fresh, _ := s.IssueAppToken(ctx, "bob", "pw", "pc3")
	if err := s.RevokeAppToken(ctx, fresh.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := k.User(ctx, fresh.Token); !errors.Is(err, auth.ErrBadKey) {
		t.Errorf("ログアウトしたら使えない: %v", err)
	}
	fresh, _ = s.IssueAppToken(ctx, "bob", "pw", "pc3")
	if err := st.DeleteUserSessions(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := k.User(ctx, fresh.Token); !errors.Is(err, auth.ErrBadKey) {
		t.Errorf("パスワードを作り直したら使えない: %v", err)
	}
}

// HTTP の口:トークンを出す・API キーと同じ口で通る・期限切れは token_expired・revoke で消える
func TestAppTokenHTTP(t *testing.T) {
	st := openStore(t)
	addUser(t, st, "bob", "pw")
	s := auth.NewSessions(st)
	k := auth.NewKeys(st)
	now := time.Now()
	auth.SetNow(s, func() time.Time { return now })
	auth.SetKeysNow(k, func() time.Time { return now })
	mux := http.NewServeMux()
	mux.Handle("/aibow/auth/", auth.Handler(s, true))
	mux.Handle("/v1/", k.RequireKey(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		who, _ := auth.UserFrom(r.Context())
		w.Write([]byte(who.Name))
	})))
	srv := httptest.NewServer(mux)
	defer srv.Close()
	call := func(path, bearer, body string) (int, string) {
		t.Helper()
		method := "POST"
		if body == "" && strings.HasPrefix(path, "/v1/") {
			method = "GET"
		}
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var b strings.Builder
		buf := make([]byte, 1024)
		n, _ := res.Body.Read(buf)
		b.Write(buf[:n])
		return res.StatusCode, b.String()
	}

	if code, _ := call("/aibow/auth/token", "", `{"name":"bob","password":"x","device":"pc1"}`); code != http.StatusUnauthorized {
		t.Errorf("違うパスワード: %d", code)
	}
	code, body := call("/aibow/auth/token", "", `{"name":"bob","password":"pw","device":"pc1"}`)
	var login auth.AppLogin
	if code != http.StatusOK || json.Unmarshal([]byte(body), &login) != nil || login.Token == "" {
		t.Fatalf("トークン: %d %s", code, body)
	}
	if code, body := call("/v1/models", login.Token, ""); code != http.StatusOK || body != "bob" {
		t.Errorf("トークンで通る: %d %s", code, body)
	}
	now = now.Add(auth.AppTokenTTL)
	if code, body := call("/v1/models", login.Token, ""); code != http.StatusUnauthorized || !strings.Contains(body, "token_expired") {
		t.Errorf("期限切れ: %d %s", code, body)
	}
	code, body = call("/aibow/auth/token", "", `{"name":"bob","password":"pw","device":"pc1"}`)
	json.Unmarshal([]byte(body), &login)
	if code, _ := call("/aibow/auth/token/revoke", "aibow_キー", ""); code != http.StatusBadRequest {
		t.Errorf("API キーは revoke で消さない: %d", code)
	}
	if code, _ := call("/aibow/auth/token/revoke", login.Token, ""); code != http.StatusOK {
		t.Errorf("revoke: %d", code)
	}
	if code, _ := call("/v1/models", login.Token, ""); code != http.StatusUnauthorized {
		t.Errorf("消したトークン: %d", code)
	}

	// ログインの要らない形では出さない
	srv2 := httptest.NewServer(auth.Handler(s, false))
	defer srv2.Close()
	res, _ := http.Post(srv2.URL+"/aibow/auth/token", "application/json", strings.NewReader(`{"name":"bob","password":"pw","device":"pc1"}`))
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("ログインなしの形: %d", res.StatusCode)
	}
}

func TestFingerprint(t *testing.T) {
	der := []byte("証明書の中身")
	sum := sha256.Sum256(der)
	want := strings.ToUpper(strings.Join(strings.Split(fmt.Sprintf("% x", sum), " "), ":"))
	if got := auth.Fingerprint(der); got != want {
		t.Errorf("指紋の形: %s(期待 %s)", got, want)
	}
}

// ログインが要るのにログインできる管理者がいなければ、me の setup が true(0020)。管理者を作れば false。ログインが要らない形では出さない
func TestMeSetup(t *testing.T) {
	st := openStore(t)
	u := addUser(t, st, "alice", "pw")
	s := auth.NewSessions(st)
	me := func(required bool) auth.Me {
		t.Helper()
		rec := httptest.NewRecorder()
		auth.Handler(s, required).ServeHTTP(rec, httptest.NewRequest("GET", "/aibow/auth/me", nil))
		var m auth.Me
		json.NewDecoder(rec.Body).Decode(&m)
		return m
	}
	if m := me(true); !m.Setup {
		t.Errorf("管理者がいない: %+v", m)
	}
	if m := me(false); m.Setup {
		t.Errorf("ログインが要らない形: %+v", m)
	}
	st.SetAdmin(ctx, u.ID, true, time.Now())
	if m := me(true); m.Setup {
		t.Errorf("管理者を作ったあと: %+v", m)
	}
}
