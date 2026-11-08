package store

// compactions.go: 要約(compact)。

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"uuid"

	"github.com/secondarykey/aibow"
)

// ---- 要約 -------------------------------------------------------------------------------

func (s *SQLite) AddCompaction(ctx context.Context, c aibow.Compaction) error {
	owner, err := s.owner(ctx)
	if err != nil {
		return err
	}
	var stats sql.NullString
	if c.Stats != nil {
		b, err := json.Marshal(c.Stats)
		if err != nil {
			return err
		}
		stats = sql.NullString{String: string(b), Valid: true}
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO compactions (id, conversation_id, upto_message_id, summary, model, stats, created_at)
		SELECT ?, ?, ?, ?, ?, ?, ? WHERE `+ownConversation,
		c.ID.String(), c.ConversationID.String(), c.UptoMessageID.String(), c.Summary, c.Model, stats, formatTime(c.CreatedAt),
		c.ConversationID.String(), owner)
	return notFoundIfNone(res, err, aibow.ErrNotFound)
}

func (s *SQLite) Compactions(ctx context.Context, conv uuid.UUID) ([]aibow.Compaction, error) {
	owner, err := s.owner(ctx)
	if err != nil {
		return nil, err
	}
	return s.compactions(ctx, conv, "conversation_id = ? AND "+ownConversation, conv.String(), conv.String(), owner)
}

// compactions は、会話 conv の要約を where の条件で読む(自分の会話は Compactions、ほかの人の会話は ReadConversation から)。
func (s *SQLite) compactions(ctx context.Context, conv uuid.UUID, where string, args ...any) ([]aibow.Compaction, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, upto_message_id, summary, model, stats, created_at
		FROM compactions WHERE `+where+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []aibow.Compaction{}
	for rows.Next() {
		c := aibow.Compaction{ConversationID: conv}
		var id, upto, created string
		var stats sql.NullString
		if err := rows.Scan(&id, &upto, &c.Summary, &c.Model, &stats, &created); err != nil {
			return nil, err
		}
		if c.ID, err = parseID(id); err != nil {
			return nil, err
		}
		if c.UptoMessageID, err = parseID(upto); err != nil {
			return nil, err
		}
		if c.CreatedAt, err = parseTime(created); err != nil {
			return nil, err
		}
		if stats.Valid {
			c.Stats = &aibow.CompactStats{}
			if err := json.Unmarshal([]byte(stats.String), c.Stats); err != nil {
				return nil, fmt.Errorf("要約 %s の記録が読めません: %w", id, err)
			}
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
