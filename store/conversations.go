package store

// conversations.go: 会話の読み書きと「生成中」の印。発言は messages.go、共有とほかの人の会話を読むのは share.go。

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/secondarykey/aibow"
	"github.com/secondarykey/aibow/models"
)

// ---- 会話 -------------------------------------------------------------------------------

const conversationColumns = "id, title, settings, profile_id, created_at, updated_at"

// conversationSelect は、会話を読むときの列(保存する列 + 共有の範囲)。
const conversationSelect = "SELECT " + conversationColumns + ", share FROM conversations"

func scanConversation(r rowScanner) (aibow.Conversation, error) {
	var c aibow.Conversation
	var id, settings, created, updated string
	var profile sql.NullString // プロファイルに紐づける前の会話は NULL
	if err := r.Scan(&id, &c.Title, &settings, &profile, &created, &updated, &c.Share); err != nil {
		return c, err
	}
	var err error
	if c.ID, err = parseID(id); err != nil {
		return c, err
	}
	if profile.Valid {
		if c.ProfileID, err = parseID(profile.String); err != nil {
			return c, err
		}
	}
	if err := json.Unmarshal([]byte(settings), &c.Settings); err != nil {
		return c, fmt.Errorf("会話 %s の設定が読めません: %w", id, err)
	}
	// 以前の形のモデルの ID(file:<パス>)は、今の形(file:<ファイル名>)に読み替える(0018 の決定事項 15。DB は書き換えない)
	c.Settings.Model = models.IDOf(c.Settings.Model)
	if c.CreatedAt, err = parseTime(created); err != nil {
		return c, err
	}
	if c.UpdatedAt, err = parseTime(updated); err != nil {
		return c, err
	}
	return c, nil
}

func (s *SQLite) ListConversations(ctx context.Context) ([]aibow.Conversation, error) {
	owner, err := s.owner(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, conversationSelect+" WHERE owner = ? ORDER BY updated_at DESC, id DESC", owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []aibow.Conversation{} // 空でも nil ではなく [] にする(画面に null ではなく空の配列で届くように)
	for rows.Next() {
		c, err := scanConversation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *SQLite) Conversation(ctx context.Context, id uuid.UUID) (aibow.Conversation, []aibow.StoredMessage, error) {
	owner, err := s.owner(ctx)
	if err != nil {
		return aibow.Conversation{}, nil, err
	}
	// ほかの人の会話は、無い会話と同じに扱う(あることも見せない)
	c, err := scanConversation(s.db.QueryRowContext(ctx, conversationSelect+" WHERE id = ? AND owner = ?", id.String(), owner))
	if errors.Is(err, sql.ErrNoRows) {
		return c, nil, aibow.ErrNotFound
	}
	if err != nil {
		return c, nil, err
	}
	if c.ShareWith, c.ShareGroups, err = s.shareNames(ctx, "conversation", c.Share, id); err != nil {
		return c, nil, err
	}
	ms, err := s.messages(ctx, id)
	return c, ms, err
}

func (s *SQLite) CreateConversation(ctx context.Context, c aibow.Conversation) error {
	owner, err := s.owner(ctx)
	if err != nil {
		return err
	}
	settings, err := json.Marshal(c.Settings)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "INSERT INTO conversations ("+conversationColumns+", owner) VALUES (?, ?, ?, ?, ?, ?, ?)",
		c.ID.String(), c.Title, string(settings), nullID(c.ProfileID), formatTime(c.CreatedAt), formatTime(c.UpdatedAt), owner)
	return err
}

func (s *SQLite) UpdateConversation(ctx context.Context, c aibow.Conversation) error {
	owner, err := s.owner(ctx)
	if err != nil {
		return err
	}
	settings, err := json.Marshal(c.Settings)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, "UPDATE conversations SET title = ?, settings = ?, profile_id = ?, updated_at = ? WHERE id = ? AND owner = ?",
		c.Title, string(settings), nullID(c.ProfileID), formatTime(c.UpdatedAt), c.ID.String(), owner)
	return notFoundIfNone(res, err, aibow.ErrNotFound)
}

func (s *SQLite) TouchConversation(ctx context.Context, id uuid.UUID, at time.Time, autoTitle string) error {
	owner, err := s.owner(ctx)
	if err != nil {
		return err
	}
	// タイトルは空のときだけ書く。読んでから書くのではなく1つの UPDATE で行うので、
	// 同じときに画面から名前を変えられても、どちらかが半端に消えることがない
	res, err := s.db.ExecContext(ctx, `UPDATE conversations
		SET updated_at = ?, title = CASE WHEN title = '' THEN ? ELSE title END
		WHERE id = ? AND owner = ?`, formatTime(at), autoTitle, id.String(), owner)
	return notFoundIfNone(res, err, aibow.ErrNotFound)
}

func (s *SQLite) DeleteConversation(ctx context.Context, id uuid.UUID) error {
	owner, err := s.owner(ctx)
	if err != nil {
		return err
	}
	// 発言と要約は ON DELETE CASCADE でいっしょに消える
	_, err = s.db.ExecContext(ctx, "DELETE FROM conversations WHERE id = ? AND owner = ?", id.String(), owner)
	return err
}

// LockConversation は会話に「生成中」の印を付ける(印が無い・期限が過ぎた・自分の印なら付けて延ばす)。
// 確かめるのと付けるのを1つの UPDATE で行うので、2つの画面が同時に付けようとしても、片方だけが付けられる。
func (s *SQLite) LockConversation(ctx context.Context, id uuid.UUID, holder string, until time.Time) error {
	owner, err := s.owner(ctx)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE conversations SET busy_by = ?, busy_until = ?
		WHERE id = ? AND owner = ? AND (busy_by IS NULL OR busy_by = ? OR busy_until <= ?)`,
		holder, formatTime(until), id.String(), owner, holder, formatTime(time.Now()))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	// 付けられなかった:会話が無いのか、ほかの印があるのか
	if _, _, err := s.Conversation(ctx, id); err != nil {
		return err
	}
	return aibow.ErrBusy
}

// UnlockConversation は、holder の印を外す。
func (s *SQLite) UnlockConversation(ctx context.Context, id uuid.UUID, holder string) error {
	owner, err := s.owner(ctx)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "UPDATE conversations SET busy_by = NULL, busy_until = NULL WHERE id = ? AND owner = ? AND busy_by = ?",
		id.String(), owner, holder)
	return err
}

// 発言と要約は持ち主の列を持たないので、会話の持ち主で確かめる(SQL の条件の部分)。
const (
	// ownConversation は「会話 ? が持ち主 ? のものか」(足すとき・要約を読むとき)
	ownConversation = "EXISTS (SELECT 1 FROM conversations WHERE id = ? AND owner = ?)"
	// ownMessage は「この発言の会話が持ち主 ? のものか」(発言の ID だけで書き換えるとき)
	ownMessage = "conversation_id IN (SELECT id FROM conversations WHERE owner = ?)"
)

// nullID は ID を保存する形にする。空(ゼロ)なら NULL(プロファイルを持たない会話)。
func nullID(id uuid.UUID) sql.NullString {
	if id == (uuid.UUID{}) {
		return sql.NullString{}
	}
	return sql.NullString{String: id.String(), Valid: true}
}
