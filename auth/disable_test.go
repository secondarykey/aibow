package auth_test

// API キーの期限・消す、利用者を無効にする(0060)のテスト。

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/secondarykey/aibow/auth"
	"github.com/secondarykey/aibow/store"
)

// API キーの期限:選べるのは 30・90・365・0(無期限)。期限を過ぎると ErrKeyExpired(401・key_expired)で、違うキーは invalid_api_key のまま。
// 期限切れのキーは消さない。消すとすぐ使えなくなり、無いものを消すと ErrNoAPIKey
func TestAPIKeyExpires(t *testing.T) {
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	for _, days := range []int{30, 90, 365} {
		exp, err := auth.KeyExpires(now, days)
		if err != nil || !exp.Equal(now.AddDate(0, 0, days)) {
			t.Errorf("%d 日: %v %v", days, exp, err)
		}
	}
	if exp, err := auth.KeyExpires(now, 0); err != nil || !exp.IsZero() {
		t.Errorf("0 は無期限: %v %v", exp, err)
	}
	if _, err := auth.KeyExpires(now, 7); err == nil {
		t.Error("選べない日数は断る")
	}

	st := openStore(t)
	u := addUser(t, st, "bob", "pw")
	k := auth.NewKeys(st)
	auth.SetKeysNow(k, func() time.Time { return now })
	key := auth.NewAPIKey()
	exp, _ := auth.KeyExpires(now, 30)
	if err := st.SetAPIKey(ctx, u.ID, auth.KeyID(key), now, exp); err != nil {
		t.Fatal(err)
	}
	if has, got, err := st.HasAPIKey(ctx, u.ID); err != nil || !has || !got.Equal(exp) {
		t.Errorf("期限を読める: %v %v %v", has, got, err)
	}
	if _, err := k.User(ctx, key); err != nil {
		t.Errorf("期限の前は使える: %v", err)
	}

	srv := httptest.NewServer(k.RequireKey(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})))
	defer srv.Close()
	call := func(key string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest("GET", srv.URL+"/v1/models", nil)
		req.Header.Set("Authorization", "Bearer "+key)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		buf := make([]byte, 1024)
		n, _ := res.Body.Read(buf)
		return res.StatusCode, string(buf[:n])
	}

	now = exp
	if _, err := k.User(ctx, key); !errors.Is(err, auth.ErrKeyExpired) {
		t.Errorf("期限ちょうどで切れる: %v", err)
	}
	if code, body := call(key); code != http.StatusUnauthorized || !strings.Contains(body, `"key_expired"`) {
		t.Errorf("期限切れは key_expired: %d %s", code, body)
	}
	if code, body := call("aibow_違う"); code != http.StatusUnauthorized || !strings.Contains(body, `"invalid_api_key"`) {
		t.Errorf("違うキーは invalid_api_key のまま: %d %s", code, body)
	}
	if has, _, _ := st.HasAPIKey(ctx, u.ID); !has {
		t.Error("期限切れのキーは消さない(管理の画面で期限切れと見えるように)")
	}

	// 無期限のキー(版 23 より前のキーも、列が空なので同じ)
	forever := auth.NewAPIKey()
	if err := st.SetAPIKey(ctx, u.ID, auth.KeyID(forever), now, time.Time{}); err != nil {
		t.Fatal(err)
	}
	now = now.AddDate(10, 0, 0)
	if _, err := k.User(ctx, forever); err != nil {
		t.Errorf("無期限のキーはいつでも使える: %v", err)
	}
	if has, got, _ := st.HasAPIKey(ctx, u.ID); !has || !got.IsZero() {
		t.Errorf("無期限の期限はゼロ: %v %v", has, got)
	}

	// 消す
	if err := st.DeleteAPIKey(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if code, _ := call(forever); code != http.StatusUnauthorized {
		t.Errorf("消したキーはすぐ使えない: %d", code)
	}
	if err := st.DeleteAPIKey(ctx, u.ID); !errors.Is(err, auth.ErrNoAPIKey) {
		t.Errorf("無いキーを消すと ErrNoAPIKey: %v", err)
	}
}

// 利用者を無効にする:5つの入口(ログイン・アプリのログイン・セッション・アプリ用のトークン・API キー・パスワードを決めるリンク)をすべて断る。
// パスワードが違うときは ErrLogin のまま。有効に戻すと、同じ API キーがまた使える
func TestDisabledUser(t *testing.T) {
	st := openStore(t)
	u := addUser(t, st, "bob", "pw")
	s := auth.NewSessions(st)
	k := auth.NewKeys(st)

	session, _, err := s.Login(ctx, "bob", "pw")
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.IssueAppToken(ctx, "bob", "pw", "pc1")
	if err != nil {
		t.Fatal(err)
	}
	key := auth.NewAPIKey()
	if err := st.SetAPIKey(ctx, u.ID, auth.KeyID(key), time.Now(), time.Time{}); err != nil {
		t.Fatal(err)
	}
	link, _, err := s.IssueLink(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}

	if err := st.SetDisabled(ctx, u.ID, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.UserByID(ctx, u.ID); !got.Disabled {
		t.Error("無効の印を読める")
	}

	// ログイン:パスワードが合ったときだけ無効だと言う
	if _, _, err := s.Login(ctx, "bob", "違う"); !errors.Is(err, auth.ErrLogin) {
		t.Errorf("パスワードが違えば ErrLogin のまま: %v", err)
	}
	if _, _, err := s.Login(ctx, "bob", "pw"); !errors.Is(err, auth.ErrUserDisabled) {
		t.Errorf("ログイン: %v", err)
	}
	if _, err := s.IssueAppToken(ctx, "bob", "pw", "pc2"); !errors.Is(err, auth.ErrUserDisabled) {
		t.Errorf("アプリのログイン: %v", err)
	}
	if _, err := s.IssueAppToken(ctx, "bob", "違う", "pc2"); !errors.Is(err, auth.ErrLogin) {
		t.Errorf("アプリのログインも、パスワードが違えば ErrLogin のまま: %v", err)
	}
	// 無効にしたときに、セッション・アプリ用のトークン・リンクは消えている
	if _, _, err := s.User(ctx, session); !errors.Is(err, auth.ErrNoSession) {
		t.Errorf("セッション: %v", err)
	}
	if _, err := k.User(ctx, app.Token); !errors.Is(err, auth.ErrBadKey) {
		t.Errorf("アプリ用のトークンは消えている: %v", err)
	}
	if _, err := s.LinkUser(ctx, link); !errors.Is(err, auth.ErrBadLink) {
		t.Errorf("リンクは消えている: %v", err)
	}
	// API キーは残るが、使えない
	if has, _, _ := st.HasAPIKey(ctx, u.ID); !has {
		t.Error("無効にしても API キーは残す")
	}
	if _, err := k.User(ctx, key); !errors.Is(err, auth.ErrUserDisabled) {
		t.Errorf("API キー: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	k.RequireKey(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), `"user_disabled"`) {
		t.Errorf("API キーは user_disabled: %d %s", rec.Code, rec.Body.String())
	}

	// 無効にしたあとで出したリンク(管理者が再設定を押した)も使えない
	link, _, err = s.IssueLink(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.LinkUser(ctx, link); !errors.Is(err, auth.ErrUserDisabled) {
		t.Errorf("リンク: %v", err)
	}
	if _, _, err := s.UseLink(ctx, link, "new-password-123"); !errors.Is(err, auth.ErrUserDisabled) {
		t.Errorf("リンクでパスワードを決める: %v", err)
	}

	// 有効に戻すと元どおり(同じ API キー・同じパスワード)
	if err := st.SetDisabled(ctx, u.ID, false, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got, err := k.User(ctx, key); err != nil || got.Name != "bob" {
		t.Errorf("有効に戻すと同じ API キーが使える: %v", err)
	}
	if _, _, err := s.Login(ctx, "bob", "pw"); err != nil {
		t.Errorf("有効に戻すとログインできる: %v", err)
	}
}

// disabledStore は、持ち主を読むと必ず無効の印が付いている保存先(印が付いたのに、セッションなどが残っているときを作るため)。
type disabledStore struct{ *store.SQLite }

func (d disabledStore) UserByID(c context.Context, id uuid.UUID) (auth.User, error) {
	u, err := d.SQLite.UserByID(c, id)
	u.Disabled = true
	return u, err
}

// 無効の印が付いたのにセッション・アプリ用のトークン・リンクが残っていても、入口で断る(SetDisabled が消し損ねたときのため)
func TestDisabledSessionLeft(t *testing.T) {
	st := openStore(t)
	u := addUser(t, st, "bob", "pw")
	session, _, _ := auth.NewSessions(st).Login(ctx, "bob", "pw")
	app, _ := auth.NewSessions(st).IssueAppToken(ctx, "bob", "pw", "pc1")
	link, _, _ := auth.NewSessions(st).IssueLink(ctx, u.ID)

	d := disabledStore{st}
	s := auth.NewSessions(d)
	k := auth.NewKeys(d)
	if _, _, err := s.User(ctx, session); !errors.Is(err, auth.ErrNoSession) {
		t.Errorf("セッション: %v", err)
	}
	if _, err := st.Session(ctx, auth.KeyID(session)); !errors.Is(err, auth.ErrNoSession) {
		t.Errorf("断ったセッションは消す: %v", err)
	}
	if _, err := k.User(ctx, app.Token); !errors.Is(err, auth.ErrUserDisabled) {
		t.Errorf("アプリ用のトークン: %v", err)
	}
	if _, err := s.LinkUser(ctx, link); !errors.Is(err, auth.ErrUserDisabled) {
		t.Errorf("リンク: %v", err)
	}
}
