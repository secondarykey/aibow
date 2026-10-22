package auth_test

// ログインとセッションのテスト。保存先は本物(store の SQLite。一時フォルダ)を使う。
// auth_test パッケージにしているのは、store が auth を import していて、auth の中からは store を import できないため。

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/secondarykey/aibow/auth"
	"github.com/secondarykey/aibow/store"
)

var ctx = context.Background()

func openStore(t *testing.T) *store.SQLite {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func addUser(t *testing.T, st auth.Store, name, password string) auth.User {
	t.Helper()
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	u := auth.User{ID: uuid.NewV7(), Name: name, Password: hash, CreatedAt: now, UpdatedAt: now}
	if err := st.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	return u
}

func TestPassword(t *testing.T) {
	h1, _ := auth.HashPassword("secret")
	h2, _ := auth.HashPassword("secret")
	if h1 == h2 {
		t.Error("同じパスワードでも、塩が違うので違う値になる")
	}
	if strings.Contains(h1, "secret") || !strings.HasPrefix(h1, "pbkdf2-sha256$600000$") {
		t.Errorf("保存する形: %s", h1)
	}
	if !auth.CheckPassword(h1, "secret") || auth.CheckPassword(h1, "Secret") || auth.CheckPassword("", "") || auth.CheckPassword("xx", "secret") {
		t.Error("確かめ方が違う")
	}
	p := auth.NewPassword()
	if len(p) != 19 || strings.Count(p, "-") != 3 || p == auth.NewPassword() {
		t.Errorf("作ったパスワード: %q", p)
	}
}

func TestUsersInStore(t *testing.T) {
	st := openStore(t)
	// ログインなしの形の利用者は、DB を作ったときからいる(パスワードは空)
	local, err := st.UserByName(ctx, "local")
	if err != nil || local.ID.String() != "00000000-0000-7000-8000-000000000001" || local.Password != "" {
		t.Fatalf("local: %+v %v", local, err)
	}
	u := addUser(t, st, "bob", "pw")
	if err := st.CreateUser(ctx, auth.User{ID: uuid.NewV7(), Name: "bob"}); !errors.Is(err, auth.ErrUserExists) {
		t.Errorf("同じ名前: %v", err)
	}
	if list, _ := st.Users(ctx); len(list) != 2 || list[1].Name != "bob" {
		t.Errorf("一覧: %+v", list)
	}
	if err := st.DeleteUser(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UserByID(ctx, u.ID); !errors.Is(err, auth.ErrUserNotFound) {
		t.Errorf("消した利用者: %v", err)
	}
}

func TestSessions(t *testing.T) {
	st := openStore(t)
	u := addUser(t, st, "bob", "pw")
	s := auth.NewSessions(st)
	if _, _, err := s.Login(ctx, "bob", "違う"); !errors.Is(err, auth.ErrLogin) {
		t.Errorf("違うパスワード: %v", err)
	}
	if _, _, err := s.Login(ctx, "alice", "pw"); !errors.Is(err, auth.ErrLogin) {
		t.Errorf("いない名前も同じエラー: %v", err)
	}
	if _, _, err := s.Login(ctx, "local", ""); !errors.Is(err, auth.ErrLogin) {
		t.Errorf("パスワードを作っていない人はログインできない: %v", err)
	}
	token, got, err := s.Login(ctx, " bob ", "pw")
	if err != nil || got.ID != u.ID {
		t.Fatalf("ログイン: %+v %v", got, err)
	}
	if who, _, err := s.User(ctx, token); err != nil || who.Name != "bob" {
		t.Errorf("合言葉の持ち主: %+v %v", who, err)
	}
	if _, _, err := s.User(ctx, token+"x"); !errors.Is(err, auth.ErrNoSession) {
		t.Errorf("違う合言葉: %v", err)
	}
	if err := s.Logout(ctx, token); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.User(ctx, token); !errors.Is(err, auth.ErrNoSession) {
		t.Errorf("ログアウトしたら使えない: %v", err)
	}
}

// 期限は「最後に使ってから 30 日」:使えば延び、30 日使わなければ切れる
func TestSessionExpiry(t *testing.T) {
	st := openStore(t)
	addUser(t, st, "bob", "pw")
	s := auth.NewSessions(st)
	now := time.Now()
	auth.SetNow(s, func() time.Time { return now })
	token, _, err := s.Login(ctx, "bob", "pw")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour)
	if _, extended, err := s.User(ctx, token); err != nil || extended {
		t.Errorf("1時間後は延ばさない(毎回 DB に書かない): %v %v", extended, err)
	}
	now = now.Add(20 * 24 * time.Hour)
	if _, extended, err := s.User(ctx, token); err != nil || !extended {
		t.Errorf("20 日後に使うと延びる: %v %v", extended, err)
	}
	now = now.Add(29 * 24 * time.Hour) // ログインから 49 日。延ばしてから 29 日
	if _, _, err := s.User(ctx, token); err != nil {
		t.Errorf("延ばしたので、まだ使える: %v", err)
	}
	now = now.Add(31 * 24 * time.Hour)
	if _, _, err := s.User(ctx, token); !errors.Is(err, auth.ErrNoSession) {
		t.Errorf("30 日使わなければ切れる: %v", err)
	}
}

// HTTP の口:ログインで cookie が付き、me で名前が分かり、ログアウトで消える
func TestHTTP(t *testing.T) {
	st := openStore(t)
	addUser(t, st, "bob", "pw")
	s := auth.NewSessions(st)
	srv := httptest.NewServer(s.Middleware(auth.Handler(s, true)))
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}

	me := func() auth.Me {
		t.Helper()
		res, err := c.Get(srv.URL + "/aibow/auth/me")
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var m auth.Me
		json.NewDecoder(res.Body).Decode(&m)
		return m
	}
	post := func(path, body, ct string) *http.Response {
		t.Helper()
		res, err := c.Post(srv.URL+path, ct, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res
	}

	if m := me(); !m.Login || m.Name != "" {
		t.Errorf("ログインの前: %+v", m)
	}
	if res := post("/aibow/auth/login", `{"name":"bob","password":"x"}`, "application/json"); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("違うパスワードは 401: %d", res.StatusCode)
	}
	if res := post("/aibow/auth/login", `{"name":"bob","password":"pw"}`, "text/plain"); res.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("JSON でない本文は断る: %d", res.StatusCode)
	}
	res := post("/aibow/auth/login", `{"name":"bob","password":"pw"}`, "application/json")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("ログイン: %d", res.StatusCode)
	}
	ck := res.Cookies()
	if len(ck) != 1 || !ck[0].HttpOnly || ck[0].SameSite != http.SameSiteStrictMode || ck[0].MaxAge != int(auth.SessionTTL.Seconds()) {
		t.Errorf("cookie: %+v", ck)
	}
	if m := me(); m.Name != "bob" {
		t.Errorf("ログインしたあと: %+v", m)
	}
	post("/aibow/auth/logout", "", "application/json")
	if m := me(); m.Name != "" {
		t.Errorf("ログアウトしたあと: %+v", m)
	}
}

// ログインが要らない形では、me は login=false で、ログインの口は断る
func TestHTTPWithoutLogin(t *testing.T) {
	st := openStore(t)
	s := auth.NewSessions(st)
	srv := httptest.NewServer(s.Middleware(auth.Handler(s, false)))
	defer srv.Close()
	res, err := http.Get(srv.URL + "/aibow/auth/me")
	if err != nil {
		t.Fatal(err)
	}
	var m auth.Me
	json.NewDecoder(res.Body).Decode(&m)
	res.Body.Close()
	if m.Login {
		t.Errorf("me: %+v", m)
	}
	res, _ = http.Post(srv.URL+"/aibow/auth/login", "application/json", strings.NewReader(`{}`))
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("login: %d", res.StatusCode)
	}
}

// API キー:無い・違うと 401、合っていれば持ち主が ctx に入る。作り直すと前のキーは使えない
func TestAPIKeys(t *testing.T) {
	st := openStore(t)
	u := addUser(t, st, "bob", "pw")
	k := auth.NewKeys(st)
	key := auth.NewAPIKey()
	if !strings.HasPrefix(key, "aibow_") || len(key) < 40 {
		t.Errorf("キーの形: %q", key)
	}
	if err := st.SetAPIKey(ctx, u.ID, auth.KeyID(key), time.Now()); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(k.RequireKey(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		who, _ := auth.UserFrom(r.Context())
		w.Write([]byte(who.Name))
	})))
	defer srv.Close()
	get := func(auth string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest("GET", srv.URL+"/v1/models", nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var b strings.Builder
		buf := make([]byte, 512)
		n, _ := res.Body.Read(buf)
		b.Write(buf[:n])
		return res.StatusCode, b.String()
	}
	if code, body := get(""); code != http.StatusUnauthorized || !strings.Contains(body, "invalid_api_key") {
		t.Errorf("キーが無い: %d %s", code, body)
	}
	if code, _ := get("Bearer aibow_違う"); code != http.StatusUnauthorized {
		t.Errorf("違うキー: %d", code)
	}
	if code, body := get("Bearer " + key); code != http.StatusOK || body != "bob" {
		t.Errorf("合っているキー: %d %s", code, body)
	}
	if has, _ := st.HasAPIKey(ctx, u.ID); !has {
		t.Error("キーを持っているはず")
	}
	again := auth.NewAPIKey()
	st.SetAPIKey(ctx, u.ID, auth.KeyID(again), time.Now())
	if code, _ := get("Bearer " + key); code != http.StatusUnauthorized {
		t.Errorf("作り直す前のキーは使えない: %d", code)
	}
	if code, _ := get("Bearer " + again); code != http.StatusOK {
		t.Errorf("作り直したキー: %d", code)
	}
}

// ログインを5回続けて間違えると、合っていても15分は 429。待てばまたログインできる
func TestLoginLimit(t *testing.T) {
	st := openStore(t)
	addUser(t, st, "bob", "pw")
	s := auth.NewSessions(st)
	now := time.Now()
	auth.SetNow(s, func() time.Time { return now })
	srv := httptest.NewServer(s.Middleware(auth.Handler(s, true)))
	defer srv.Close()
	login := func(pw string) int {
		t.Helper()
		res, err := http.Post(srv.URL+"/aibow/auth/login", "application/json", strings.NewReader(`{"name":"bob","password":"`+pw+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	for i := range 5 {
		if code := login("x"); code != http.StatusUnauthorized {
			t.Fatalf("%d 回目: %d", i+1, code)
		}
	}
	if code := login("pw"); code != http.StatusTooManyRequests {
		t.Errorf("続けて間違えたあとは、合っていても断る: %d", code)
	}
	now = now.Add(16 * time.Minute)
	if code := login("pw"); code != http.StatusOK {
		t.Errorf("待てばログインできる: %d", code)
	}
}

// GET /aibow/auth/key は API キーの持ち主の名前(アプリが画面に出す)。キーの要らない形なら誰でもない
func TestKeyMe(t *testing.T) {
	st := openStore(t)
	u := addUser(t, st, "bob", "pw")
	key := auth.NewAPIKey()
	st.SetAPIKey(ctx, u.ID, auth.KeyID(key), time.Now())
	k := auth.NewKeys(st)
	for _, c := range []struct {
		required bool
		auth     string
		code     int
		want     auth.Me
	}{
		{true, "Bearer " + key, http.StatusOK, auth.Me{Login: true, Name: "bob"}},
		{true, "", http.StatusUnauthorized, auth.Me{}},
		{false, "", http.StatusOK, auth.Me{}},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/aibow/auth/key", nil)
		if c.auth != "" {
			req.Header.Set("Authorization", c.auth)
		}
		k.MeHandler(c.required).ServeHTTP(rec, req)
		var got auth.Me
		json.NewDecoder(rec.Body).Decode(&got)
		if rec.Code != c.code || (c.code == http.StatusOK && got != c.want) {
			t.Errorf("required=%v auth=%q: %d %+v", c.required, c.auth, rec.Code, got)
		}
	}

	// 管理者なら admin が付き、IsAdmin も true(0018 の 11-0)
	if err := st.SetAdmin(ctx, u.ID, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/aibow/auth/key", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	var admin bool
	k.RequireKey(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { admin = auth.IsAdmin(r) })).ServeHTTP(rec, req)
	if !admin {
		t.Error("管理者のキーなのに IsAdmin が false")
	}
	rec = httptest.NewRecorder()
	k.MeHandler(true).ServeHTTP(rec, req)
	var got auth.Me
	json.NewDecoder(rec.Body).Decode(&got)
	if !got.Admin {
		t.Errorf("管理者の Me: %+v", got)
	}
}
