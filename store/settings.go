package store

// settings.go: アプリ全体の設定と人ごとの設定。

import (
	"context"
	"database/sql"
	"errors"
)

// ---- 設定 -------------------------------------------------------------------------------
//
// アプリ全体の設定(app_settings)は利用者によらない。人ごとの設定(user_settings)は持ち主で分ける(0017 の 6)。

func (s *SQLite) AppSetting(ctx context.Context, key string) (string, bool, error) {
	return s.setting(ctx, "SELECT value FROM app_settings WHERE key = ?", key)
}

func (s *SQLite) SetAppSetting(ctx context.Context, key, value string) error {
	// UPSERT: 無ければ追加、あれば値を書き換える
	_, err := s.db.ExecContext(ctx, `INSERT INTO app_settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

func (s *SQLite) UserSetting(ctx context.Context, key string) (string, bool, error) {
	owner, err := s.owner(ctx)
	if err != nil {
		return "", false, err
	}
	return s.setting(ctx, "SELECT value FROM user_settings WHERE owner = ? AND key = ?", owner, key)
}

func (s *SQLite) SetUserSetting(ctx context.Context, key, value string) error {
	owner, err := s.owner(ctx)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO user_settings (owner, key, value) VALUES (?, ?, ?)
		ON CONFLICT(owner, key) DO UPDATE SET value = excluded.value`, owner, key, value)
	return err
}

// setting は設定を1つ読む。無ければ ok=false。
func (s *SQLite) setting(ctx context.Context, query string, args ...any) (string, bool, error) {
	var v string
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return v, err == nil, err
}
