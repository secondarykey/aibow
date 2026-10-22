package store

// users.go: 利用者とセッションの保存(auth.Store の実装。0017 の 10-1)。
//
// 会話の保存(aibow.Store)とは別の約束にしている。利用者を扱うのは aibow-server の HTTP の層とコマンドだけで、会話の仕組みは使わないため。

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"uuid"

	"github.com/secondarykey/aibow/auth"
)

var _ auth.Store = (*SQLite)(nil)

const userColumns = "id, name, password, created_at, updated_at"

func scanUser(r rowScanner) (auth.User, error) {
	var u auth.User
	var id, created, updated string
	if err := r.Scan(&id, &u.Name, &u.Password, &created, &updated); err != nil {
		return u, err
	}
	var err error
	if u.ID, err = parseID(id); err != nil {
		return u, err
	}
	if u.CreatedAt, err = parseTime(created); err != nil {
		return u, err
	}
	u.UpdatedAt, err = parseTime(updated)
	return u, err
}

func (s *SQLite) CreateUser(ctx context.Context, u auth.User) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO users ("+userColumns+") VALUES (?, ?, ?, ?, ?)",
		u.ID.String(), u.Name, u.Password, formatTime(u.CreatedAt), formatTime(u.UpdatedAt))
	// 名前は UNIQUE。SQLite のエラーの文で見分ける(ドライバーの型を使うと、このパッケージの外にドライバーの事情が漏れるため)
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed: users.name") {
		return auth.ErrUserExists
	}
	return err
}

func (s *SQLite) Users(ctx context.Context) ([]auth.User, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+userColumns+" FROM users ORDER BY created_at, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []auth.User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *SQLite) UserByName(ctx context.Context, name string) (auth.User, error) {
	return s.user(ctx, "name = ?", name)
}

func (s *SQLite) UserByID(ctx context.Context, id uuid.UUID) (auth.User, error) {
	return s.user(ctx, "id = ?", id.String())
}

func (s *SQLite) user(ctx context.Context, where string, arg any) (auth.User, error) {
	u, err := scanUser(s.db.QueryRowContext(ctx, "SELECT "+userColumns+" FROM users WHERE "+where, arg))
	if errors.Is(err, sql.ErrNoRows) {
		return u, auth.ErrUserNotFound
	}
	return u, err
}

func (s *SQLite) SetPassword(ctx context.Context, id uuid.UUID, hash string, at time.Time) error {
	res, err := s.db.ExecContext(ctx, "UPDATE users SET password = ?, updated_at = ? WHERE id = ?", hash, formatTime(at), id.String())
	return notFoundIfNone(res, err, auth.ErrUserNotFound)
}

// DeleteUser は利用者と、その人の会話(発言・要約は会話と一緒に消える)・プロファイル・人ごとの設定・セッションを、1つのトランザクションで消す。
// 会話を先に消す(会話がプロファイルを指しているので、逆だとプロファイルを消せない)。
func (s *SQLite) DeleteUser(ctx context.Context, id uuid.UUID) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	owner := id.String()
	for _, q := range []string{
		"DELETE FROM conversations WHERE owner = ?",
		"DELETE FROM profiles WHERE owner = ?",
		"DELETE FROM user_settings WHERE owner = ?",
		"DELETE FROM sessions WHERE user_id = ?",
	} {
		if _, err := tx.ExecContext(ctx, q, owner); err != nil {
			return fmt.Errorf("利用者 %s のものを消せません(%s): %w", owner, q, err)
		}
	}
	res, err := tx.ExecContext(ctx, "DELETE FROM users WHERE id = ?", owner)
	if err := notFoundIfNone(res, err, auth.ErrUserNotFound); err != nil {
		return err
	}
	return tx.Commit()
}

// ---- セッション ---------------------------------------------------------------------------

func (s *SQLite) CreateSession(ctx context.Context, ss auth.Session) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO sessions (id, user_id, created_at, expires_at) VALUES (?, ?, ?, ?)",
		ss.ID, ss.UserID.String(), formatTime(ss.CreatedAt), formatTime(ss.ExpiresAt))
	return err
}

func (s *SQLite) Session(ctx context.Context, id string) (auth.Session, error) {
	ss := auth.Session{ID: id}
	var user, created, expires string
	err := s.db.QueryRowContext(ctx, "SELECT user_id, created_at, expires_at FROM sessions WHERE id = ?", id).Scan(&user, &created, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return ss, auth.ErrNoSession
	}
	if err != nil {
		return ss, err
	}
	if ss.UserID, err = parseID(user); err != nil {
		return ss, err
	}
	if ss.CreatedAt, err = parseTime(created); err != nil {
		return ss, err
	}
	ss.ExpiresAt, err = parseTime(expires)
	return ss, err
}

func (s *SQLite) ExtendSession(ctx context.Context, id string, expires time.Time) error {
	res, err := s.db.ExecContext(ctx, "UPDATE sessions SET expires_at = ? WHERE id = ?", formatTime(expires), id)
	return notFoundIfNone(res, err, auth.ErrNoSession)
}

func (s *SQLite) DeleteSession(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE id = ?", id)
	return err
}

func (s *SQLite) DeleteUserSessions(ctx context.Context, user uuid.UUID) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE user_id = ?", user.String())
	return err
}

func (s *SQLite) DeleteExpiredSessions(ctx context.Context, now time.Time) (int, error) {
	// 日時は桁数をそろえた UTC の文字列なので、文字列の大小 = 時刻の前後
	res, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at <= ?", formatTime(now))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}
