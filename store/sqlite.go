// Package store は、会話を SQLite に保存する部分(aibow.Store の実装)。
//
// SQLite は1つのファイルに全部が入る小さなデータベース。サーバーを立てずに、
// アプリの中から SQL で読み書きできる。ドライバーの modernc.org/sqlite は SQLite の C のコードを
// Go に変換したもので、C コンパイラ(cgo)なしでビルドできる。
//
// Go からは標準の database/sql を通して使う。ドライバーを import すると "sqlite" という名前で登録され、
// sql.Open("sqlite", …) で開けるようになる。
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
	"uuid"

	"aibow"

	_ "modernc.org/sqlite" // "sqlite" ドライバーを登録する
)

// SQLite は aibow.Store を SQLite で実装したもの。
type SQLite struct {
	db *sql.DB
}

var _ aibow.Store = (*SQLite)(nil) // aibow.Store のメソッドを全部持っているか、コンパイル時に確かめる

// Open は DB ファイルを開く(無ければ作る)。テーブルが古ければ作り変える(マイグレーション)。
func Open(path string) (*SQLite, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("DB のフォルダを作れません: %w", err)
	}
	// _pragma は、接続を開くたびに実行する設定(modernc.org/sqlite の書き方)。
	//   foreign_keys(1) : REFERENCES を効かせる。SQLite は既定で無効なので、
	//                     これが無いと ON DELETE CASCADE(会話を消すと発言も消える)が動かない
	//   busy_timeout    : 別の接続が書き込み中なら、エラーにせず最大 5 秒待つ
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("DB を開けません(%s): %w", path, err)
	}
	// 書き込みは1つずつしかできないので、接続も1つにして順番待ちさせる
	db.SetMaxOpenConns(1)

	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("DB を準備できません(%s): %w", path, err)
	}
	return &SQLite{db: db}, nil
}

func (s *SQLite) Close() error {
	return s.db.Close()
}

// ---- マイグレーション(テーブルの作り変え)-------------------------------------------------
//
// DB ファイルは利用者の手元に残り続けるので、アプリを更新してテーブルの形が変わったときは、
// 古い DB を新しい形に作り変える必要がある。
// 今の版を DB ファイルの中の PRAGMA user_version(SQLite が用意している整数1つの置き場)に記録し、
// 足りない版の SQL だけを順に実行する。版を足すときは migrations の末尾に追加する(過去の版は書き換えない)。
var migrations = []string{
	// 版 1: 会話・発言・アプリ設定
	`
CREATE TABLE conversations (
	id         TEXT PRIMARY KEY, -- UUIDv7
	title      TEXT NOT NULL,
	settings   TEXT NOT NULL,    -- この会話の設定(JSON)
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE TABLE messages (
	id              TEXT PRIMARY KEY, -- UUIDv7。並び順もこれで決まる
	conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
	role            TEXT NOT NULL,    -- system / user / assistant
	content         TEXT NOT NULL,    -- 答え(モデルに渡すのはこれだけ)
	thinking        TEXT NOT NULL,    -- 思考。画面で開いて見る用
	model           TEXT NOT NULL,    -- どのモデルの返事か(assistant のみ)
	stats           TEXT,             -- 生成の記録(JSON。assistant のみ)
	created_at      TEXT NOT NULL
);
CREATE INDEX messages_by_conversation ON messages(conversation_id, id);
CREATE TABLE app_settings (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL               -- JSON
);
`,
	// 版 2: 要約(compact の summary)。発言は消さずに、要約を別の表に積み重ねる
	`
CREATE TABLE compactions (
	id              TEXT PRIMARY KEY, -- UUIDv7。最新の要約もこの並びで決まる
	conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
	upto_message_id TEXT NOT NULL,    -- この発言(を含む)までを要約した
	summary         TEXT NOT NULL,    -- 要約の文。前回までの要約も含んだ、積み重ねた結果
	model           TEXT NOT NULL,    -- 要約を作ったモデル
	stats           TEXT,             -- 要約前後のトークン数・かかった秒数など(JSON。記録用で、判断には使わない)
	created_at      TEXT NOT NULL
);
CREATE INDEX compactions_by_conversation ON compactions(conversation_id, id);
`,
	// 版 3: ツール呼び出し。発言の role に tool が増え、どちらも JSON で持つ(今までの発言は NULL)
	`
ALTER TABLE messages ADD COLUMN tool_calls TEXT;  -- assistant: その回のツール呼び出しの一覧(名前・引数)
ALTER TABLE messages ADD COLUMN tool_result TEXT; -- tool: どの呼び出しの結果か・エラーか・かかった秒数(結果の文は content)
`,
}

func migrate(db *sql.DB) error {
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > len(migrations) {
		return fmt.Errorf("DB の版(%d)がこの aibow の知っている版(%d)より新しいです。新しい aibow で作られた DB です", version, len(migrations))
	}
	for v := version; v < len(migrations); v++ {
		// 1つの版の作り変えは、途中で失敗しても半端な形が残らないよう、トランザクションで行う
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[v]); err != nil {
			tx.Rollback()
			return fmt.Errorf("版 %d への作り変えに失敗しました: %w", v+1, err)
		}
		// PRAGMA には ? のパラメーターが使えないので、数値を文字列に埋め込む
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", v+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// ---- 値の変換 ---------------------------------------------------------------------------
//
// ID は TEXT(UUID の文字列)、日時は TEXT(UTC、桁数固定)で保存する。DB を直接のぞいても読めるように。
// 日時の桁数を固定しているのは、文字列の並び順 = 時刻の並び順にするため(ORDER BY updated_at)。

const timeFormat = "2006-01-02T15:04:05.000000Z"

func formatTime(t time.Time) string { return t.UTC().Format(timeFormat) }

func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(timeFormat, s)
	return t.Local(), err
}

// uuid.UUID は database/sql の Scan / Value を持たないので、文字列を経由して変換する
func parseID(s string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("ID が UUID の形ではありません(%q): %w", s, err)
	}
	return id, nil
}

// ---- 会話 -------------------------------------------------------------------------------

// rowScanner は *sql.Row と *sql.Rows の共通部分(1行を変数に読み込む)。
type rowScanner interface {
	Scan(dest ...any) error
}

const conversationColumns = "id, title, settings, created_at, updated_at"

func scanConversation(r rowScanner) (aibow.Conversation, error) {
	var c aibow.Conversation
	var id, settings, created, updated string
	if err := r.Scan(&id, &c.Title, &settings, &created, &updated); err != nil {
		return c, err
	}
	var err error
	if c.ID, err = parseID(id); err != nil {
		return c, err
	}
	if err := json.Unmarshal([]byte(settings), &c.Settings); err != nil {
		return c, fmt.Errorf("会話 %s の設定が読めません: %w", id, err)
	}
	if c.CreatedAt, err = parseTime(created); err != nil {
		return c, err
	}
	if c.UpdatedAt, err = parseTime(updated); err != nil {
		return c, err
	}
	return c, nil
}

func (s *SQLite) ListConversations() ([]aibow.Conversation, error) {
	rows, err := s.db.Query("SELECT " + conversationColumns + " FROM conversations ORDER BY updated_at DESC, id DESC")
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

func (s *SQLite) Conversation(id uuid.UUID) (aibow.Conversation, []aibow.StoredMessage, error) {
	c, err := scanConversation(s.db.QueryRow("SELECT "+conversationColumns+" FROM conversations WHERE id = ?", id.String()))
	if errors.Is(err, sql.ErrNoRows) {
		return c, nil, aibow.ErrNotFound
	}
	if err != nil {
		return c, nil, err
	}
	ms, err := s.messages(id)
	return c, ms, err
}

func (s *SQLite) CreateConversation(c aibow.Conversation) error {
	settings, err := json.Marshal(c.Settings)
	if err != nil {
		return err
	}
	_, err = s.db.Exec("INSERT INTO conversations ("+conversationColumns+") VALUES (?, ?, ?, ?, ?)",
		c.ID.String(), c.Title, string(settings), formatTime(c.CreatedAt), formatTime(c.UpdatedAt))
	return err
}

func (s *SQLite) UpdateConversation(c aibow.Conversation) error {
	settings, err := json.Marshal(c.Settings)
	if err != nil {
		return err
	}
	res, err := s.db.Exec("UPDATE conversations SET title = ?, settings = ?, updated_at = ? WHERE id = ?",
		c.Title, string(settings), formatTime(c.UpdatedAt), c.ID.String())
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return aibow.ErrNotFound
	}
	return nil
}

func (s *SQLite) TouchConversation(id uuid.UUID, at time.Time, autoTitle string) error {
	// タイトルは空のときだけ書く。読んでから書くのではなく1つの UPDATE で行うので、
	// 同じときに画面から名前を変えられても、どちらかが半端に消えることがない
	res, err := s.db.Exec(`UPDATE conversations
		SET updated_at = ?, title = CASE WHEN title = '' THEN ? ELSE title END
		WHERE id = ?`, formatTime(at), autoTitle, id.String())
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return aibow.ErrNotFound
	}
	return nil
}

func (s *SQLite) DeleteConversation(id uuid.UUID) error {
	// 発言と要約は ON DELETE CASCADE でいっしょに消える
	_, err := s.db.Exec("DELETE FROM conversations WHERE id = ?", id.String())
	return err
}

// ---- 発言 -------------------------------------------------------------------------------

func (s *SQLite) messages(conv uuid.UUID) ([]aibow.StoredMessage, error) {
	// UUIDv7 は先頭が作成時刻なので、ID の文字列順 = 作った順になる。順番用の列はいらない
	rows, err := s.db.Query(`SELECT id, role, content, thinking, model, stats, tool_calls, tool_result, created_at
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
		if err := rows.Scan(&id, &m.Role, &m.Content, &m.Thinking, &m.Model, &stats, &calls, &result, &created); err != nil {
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
	return out, rows.Err()
}

func (s *SQLite) AddMessage(m aibow.StoredMessage) error {
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
	_, err = s.db.Exec(`INSERT INTO messages (id, conversation_id, role, content, thinking, model, stats, tool_calls, tool_result, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID.String(), m.ConversationID.String(), m.Role, m.Content, m.Thinking, m.Model, stats, calls, result, formatTime(m.CreatedAt))
	return err
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

// ---- 要約 -------------------------------------------------------------------------------

func (s *SQLite) AddCompaction(c aibow.Compaction) error {
	var stats sql.NullString
	if c.Stats != nil {
		b, err := json.Marshal(c.Stats)
		if err != nil {
			return err
		}
		stats = sql.NullString{String: string(b), Valid: true}
	}
	_, err := s.db.Exec(`INSERT INTO compactions (id, conversation_id, upto_message_id, summary, model, stats, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		c.ID.String(), c.ConversationID.String(), c.UptoMessageID.String(), c.Summary, c.Model, stats, formatTime(c.CreatedAt))
	return err
}

func (s *SQLite) Compactions(conv uuid.UUID) ([]aibow.Compaction, error) {
	rows, err := s.db.Query(`SELECT id, upto_message_id, summary, model, stats, created_at
		FROM compactions WHERE conversation_id = ? ORDER BY id`, conv.String())
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

// ---- アプリ設定 -------------------------------------------------------------------------

func (s *SQLite) AppSetting(key string) (string, bool, error) {
	var v string
	err := s.db.QueryRow("SELECT value FROM app_settings WHERE key = ?", key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return v, err == nil, err
}

func (s *SQLite) SetAppSetting(key, value string) error {
	// UPSERT: 無ければ追加、あれば値を書き換える
	_, err := s.db.Exec(`INSERT INTO app_settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}
