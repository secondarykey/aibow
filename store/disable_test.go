package store

// 版 23(API キーの期限・利用者を無効にする。0060)のテスト。

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
	"uuid"

	"github.com/secondarykey/aibow/auth"
)

// 版 22 の DB を開くと、今ある API キーは無期限、今いる利用者は有効のまま
func TestMigrationFromVersion22(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v22.db")
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	all := ""
	for _, m := range migrations[:22] {
		all += m
	}
	if _, err := db.Exec(all + "PRAGMA user_version = 22;"); err != nil {
		t.Fatal(err)
	}
	user := uuid.NewV7()
	now := formatTime(time.Now())
	if _, err := db.Exec(`INSERT INTO users (id, name, password, created_at, updated_at) VALUES (?, 'bob', '', ?, ?)`, user.String(), now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO api_keys (user_id, id, created_at) VALUES (?, 'key-hash', ?)`, user.String(), now); err != nil {
		t.Fatal(err)
	}
	db.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c := context.Background()
	u, exp, err := s.APIKeyOwner(c, "key-hash")
	if err != nil || u.ID != user || !exp.IsZero() || u.Disabled {
		t.Errorf("今あるキーは無期限・今いる人は有効: %+v %v %v", u, exp, err)
	}
	if has, exp, err := s.HasAPIKey(c, user); !has || !exp.IsZero() || err != nil {
		t.Errorf("HasAPIKey: %v %v %v", has, exp, err)
	}
}

// SetDisabled:印を付けるとセッション・アプリ用のトークン・リンクを消し、API キーは残す。外すと印だけ外れる。いない人は ErrUserNotFound
func TestSetDisabled(t *testing.T) {
	s, _ := openTemp(t)
	c := context.Background()
	now := time.Now()
	u := auth.User{ID: uuid.NewV7(), Name: "bob", CreatedAt: now, UpdatedAt: now}
	if err := s.CreateUser(c, u); err != nil {
		t.Fatal(err)
	}
	later := now.Add(time.Hour)
	s.CreateSession(c, auth.Session{ID: "s1", UserID: u.ID, CreatedAt: now, ExpiresAt: later})
	s.SetAppToken(c, auth.AppToken{ID: "t1", UserID: u.ID, Device: "pc", CreatedAt: now, ExpiresAt: later})
	s.SetPasswordLink(c, auth.PasswordLink{ID: "l1", UserID: u.ID, CreatedAt: now, ExpiresAt: later})
	if err := s.SetAPIKey(c, u.ID, "k1", now, later); err != nil {
		t.Fatal(err)
	}

	if err := s.SetDisabled(c, u.ID, true, later); err != nil {
		t.Fatal(err)
	}
	got, _ := s.UserByName(c, "bob")
	if !got.Disabled {
		t.Errorf("印が付く: %+v", got)
	}
	if _, err := s.Session(c, "s1"); !errors.Is(err, auth.ErrNoSession) {
		t.Errorf("セッションは消える: %v", err)
	}
	if _, err := s.AppToken(c, "t1"); !errors.Is(err, auth.ErrBadKey) {
		t.Errorf("アプリ用のトークンは消える: %v", err)
	}
	if _, err := s.PasswordLink(c, "l1"); !errors.Is(err, auth.ErrBadLink) {
		t.Errorf("リンクは消える: %v", err)
	}
	if has, exp, _ := s.HasAPIKey(c, u.ID); !has || exp.IsZero() {
		t.Errorf("API キーは期限ごと残る: %v %v", has, exp)
	}

	if err := s.SetDisabled(c, u.ID, false, later); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.UserByID(c, u.ID); got.Disabled {
		t.Error("印が外れる")
	}
	if err := s.SetDisabled(c, uuid.NewV7(), true, now); !errors.Is(err, auth.ErrUserNotFound) {
		t.Errorf("いない人: %v", err)
	}

	// API キーを消す
	if err := s.DeleteAPIKey(c, u.ID); err != nil {
		t.Fatal(err)
	}
	if has, _, _ := s.HasAPIKey(c, u.ID); has {
		t.Error("消したキーは無い")
	}
	if err := s.DeleteAPIKey(c, u.ID); !errors.Is(err, auth.ErrNoAPIKey) {
		t.Errorf("無いキーを消す: %v", err)
	}
}
