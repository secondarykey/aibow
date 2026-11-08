package store

// messages.go: 発言と、発言に付けた読み込んだファイル(0030)。

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"uuid"

	"github.com/secondarykey/aibow"
)

// ---- 発言 -------------------------------------------------------------------------------

// messages は会話 conv の発言。持ち主は呼ぶ側(Conversation)が確かめてある。
func (s *SQLite) messages(ctx context.Context, conv uuid.UUID) ([]aibow.StoredMessage, error) {
	// UUIDv7 は先頭が作成時刻なので、ID の文字列順 = 作った順になる。順番用の列はいらない
	rows, err := s.db.QueryContext(ctx, `SELECT id, role, content, thinking, model, stats, tool_calls, tool_result, excluded, elided, created_at
		FROM messages WHERE conversation_id = ? ORDER BY id`, conv.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []aibow.StoredMessage{}
	for rows.Next() {
		m := aibow.StoredMessage{ConversationID: conv}
		var id, created string
		var stats, calls, result sql.NullString // NULL が入りうる列は Null〜 で受ける
		if err := rows.Scan(&id, &m.Role, &m.Content, &m.Thinking, &m.Model, &stats, &calls, &result, &m.Excluded, &m.Elided, &created); err != nil {
			return nil, err
		}
		if m.ID, err = parseID(id); err != nil {
			return nil, err
		}
		if m.CreatedAt, err = parseTime(created); err != nil {
			return nil, err
		}
		if stats.Valid {
			m.Stats = &aibow.ReplyStats{}
			if err := json.Unmarshal([]byte(stats.String), m.Stats); err != nil {
				return nil, fmt.Errorf("発言 %s の記録が読めません: %w", id, err)
			}
		}
		if calls.Valid {
			if err := json.Unmarshal([]byte(calls.String), &m.ToolCalls); err != nil {
				return nil, fmt.Errorf("発言 %s のツール呼び出しが読めません: %w", id, err)
			}
		}
		if result.Valid {
			m.ToolResult = &aibow.ToolResult{}
			if err := json.Unmarshal([]byte(result.String), m.ToolResult); err != nil {
				return nil, fmt.Errorf("発言 %s のツールの結果が読めません: %w", id, err)
			}
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// 読み込んだファイルは、発言を読み終えてから読む(接続は1本なので、発言の rows を開いたまま別に読むと待ち続ける)
	rows.Close()
	return out, s.attachFiles(ctx, conv, out)
}

// attachFiles は、会話 conv の発言に、読み込んだファイル(0030)を付ける。
func (s *SQLite) attachFiles(ctx context.Context, conv uuid.UUID, ms []aibow.StoredMessage) error {
	rows, err := s.db.QueryContext(ctx, `SELECT f.id, f.message_id, f.kind, f.name, f.format, f.hash, f.data, f.discarded
		FROM files f JOIN messages m ON m.id = f.message_id WHERE m.conversation_id = ? ORDER BY f.id`, conv.String())
	if err != nil {
		return err
	}
	defer rows.Close()
	at := map[string]int{}
	for i, m := range ms {
		at[m.ID.String()] = i
	}
	for rows.Next() {
		var f aibow.File
		var id, msg string
		if err := rows.Scan(&id, &msg, &f.Kind, &f.Name, &f.Format, &f.Hash, &f.Data, &f.Discarded); err != nil {
			return err
		}
		if len(f.Data) == 0 {
			f.Data = nil // 捨てたファイル(空の BLOB)
		}
		if f.ID, err = parseID(id); err != nil {
			return err
		}
		if i, ok := at[msg]; ok {
			ms[i].Files = append(ms[i].Files, f)
		}
	}
	return rows.Err()
}

func (s *SQLite) AddMessage(ctx context.Context, m aibow.StoredMessage) error {
	owner, err := s.owner(ctx)
	if err != nil {
		return err
	}
	stats, err := nullJSON(m.Stats, m.Stats == nil)
	if err != nil {
		return err
	}
	calls, err := nullJSON(m.ToolCalls, len(m.ToolCalls) == 0)
	if err != nil {
		return err
	}
	result, err := nullJSON(m.ToolResult, m.ToolResult == nil)
	if err != nil {
		return err
	}
	// 発言と読み込んだファイル(0030)は、1つのトランザクションで足す(ファイルだけが欠けた発言を残さない)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// 会話がその人のものなときだけ足す(INSERT … SELECT … WHERE で、確かめるのと足すのを1つの文にする)
	res, err := tx.ExecContext(ctx, `INSERT INTO messages (id, conversation_id, role, content, thinking, model, stats, tool_calls, tool_result, excluded, elided, created_at)
		SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ? WHERE `+ownConversation,
		m.ID.String(), m.ConversationID.String(), m.Role, m.Content, m.Thinking, m.Model, stats, calls, result, m.Excluded, m.Elided, formatTime(m.CreatedAt),
		m.ConversationID.String(), owner)
	if err := notFoundIfNone(res, err, aibow.ErrNotFound); err != nil {
		return err
	}
	for _, f := range m.Files {
		data := f.Data
		if data == nil {
			data = []byte{} // 捨てたファイル(送り直す・写すで付いていく)。列は NOT NULL なので、NULL ではなく空の BLOB にする
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO files (id, message_id, kind, name, format, hash, data, discarded) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			f.ID.String(), m.ID.String(), f.Kind, f.Name, f.Format, f.Hash, data, f.Discarded); err != nil {
			return fmt.Errorf("読み込んだファイル %s を保存できません: %w", f.Name, err)
		}
	}
	return tx.Commit()
}

// DeleteMessages は発言を消す。返事1つ(ツールの結果を含む)は何件かになるので、まとめて1つのトランザクションで消す。
func (s *SQLite) DeleteMessages(ctx context.Context, ids []uuid.UUID) error {
	owner, err := s.owner(ctx)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE id = ? AND `+ownMessage, id.String(), owner); err != nil {
			return fmt.Errorf("発言 %s を消せません: %w", id, err)
		}
	}
	return tx.Commit()
}

// DiscardFile は、ファイルの中身を空にして捨てた印を付ける(0030)。中身のあった場所は secure_delete でゼロに上書きされる。
// 自分の会話のファイルだけ(発言を通して持ち主を確かめる)。
func (s *SQLite) DiscardFile(ctx context.Context, id uuid.UUID) error {
	owner, err := s.owner(ctx)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE files SET data = x'', discarded = 1
		WHERE id = ? AND message_id IN (SELECT id FROM messages WHERE `+ownMessage+`)`, id.String(), owner)
	return notFoundIfNone(res, err, aibow.ErrNotFound)
}

// SetMessagesExcluded は発言の「モデルに渡さない」の印を変える。返事1つ(ツールの結果を含む)は何件かになるので、まとめて1つのトランザクションで書く。
func (s *SQLite) SetMessagesExcluded(ctx context.Context, ids []uuid.UUID, excluded bool) error {
	owner, err := s.owner(ctx)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `UPDATE messages SET excluded = ? WHERE id = ? AND `+ownMessage, excluded, id.String(), owner); err != nil {
			return fmt.Errorf("発言 %s の印を変えられません: %w", id, err)
		}
	}
	return tx.Commit()
}

// SetMessagesElided は発言に「省略して渡す」の印を付ける(0040)。一歩(返事とその結果)は何件かになるので、まとめて1つのトランザクションで書く。
func (s *SQLite) SetMessagesElided(ctx context.Context, ids []uuid.UUID) error {
	owner, err := s.owner(ctx)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `UPDATE messages SET elided = 1 WHERE id = ? AND `+ownMessage, id.String(), owner); err != nil {
			return fmt.Errorf("発言 %s に省略の印を付けられません: %w", id, err)
		}
	}
	return tx.Commit()
}

// nullJSON は v を JSON にする。empty なら NULL(その発言には無い値)。
func nullJSON(v any, empty bool) (sql.NullString, error) {
	if empty {
		return sql.NullString{}, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return sql.NullString{}, err
	}
	return sql.NullString{String: string(b), Valid: true}, nil
}
