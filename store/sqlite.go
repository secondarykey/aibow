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
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
	"uuid"

	"github.com/secondarykey/aibow"
	"github.com/secondarykey/aibow/models"

	_ "modernc.org/sqlite" // "sqlite" ドライバーを登録する
)

// SQLite は aibow.Store を SQLite で実装したもの。
//
// 会話・プロファイル・人ごとの設定には持ち主(owner。利用者の ID)の列があり、ctx の利用者(aibow.UserOf)の行だけを読み書きする(0017)。
// 発言と要約は会話を通して持ち主が決まるので、列を持たず、会話の持ち主で確かめる。
type SQLite struct {
	db *sql.DB
	// oneUser は、ctx に利用者が無い呼び出しを、ログインなしの形の利用者(aibow.LocalUser)のものとして扱うか(OneUser)
	oneUser bool
}

var _ aibow.Store = (*SQLite)(nil) // aibow.Store のメソッドを全部持っているか、コンパイル時に確かめる
var _ aibow.ConversationLocks = (*SQLite)(nil)

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

// OneUser は、ctx に利用者が無い呼び出しを、ログインなしの形の利用者(aibow.LocalUser)のものとして扱うようにする。
// デスクトップのアプリの手元の DB(aibow.db)のため。使うのはその PC の1人だけで、利用者の印を付ける HTTP の層が無い。
// 呼ばなければ、利用者が無い呼び出しは aibow.ErrNoUser で断る(aibow-server の server.db。印を付け忘れたときに、ほかの人の会話を見せないため)。
// Open の直後、使い始める前に呼ぶ。
func (s *SQLite) OneUser() {
	s.oneUser = true
}

// owner は、ctx の利用者(読み書きする行の持ち主)を、保存する形(UUID の文字列)で返す。
func (s *SQLite) owner(ctx context.Context) (string, error) {
	if id, ok := aibow.UserOf(ctx); ok {
		return id.String(), nil
	}
	if s.oneUser {
		return aibow.LocalUser.String(), nil
	}
	return "", aibow.ErrNoUser
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
	// 版 4: ユーザーが選んで「モデルに渡さない」にした発言(0011)。今までの発言は渡す(0)
	`
ALTER TABLE messages ADD COLUMN excluded INTEGER NOT NULL DEFAULT 0;
`,
	// 版 5: 設定のプロファイル(0014)。会話はどのプロファイルから作ったかを覚える。
	// 会話の profile_id は外部キーにして、使っている会話があるプロファイルを消せないようにする(ON DELETE を付けないと、消す方が失敗する)。
	// 会話はプロファイルの決まりを引くので、黙って消えると決まりも黙って外れてしまう。消したいときは「しまう」(archived)。
	// 今ある会話は NULL のまま。起動したときにプロファイル「既定」に紐づける(aibow.Conversations.PrepareProfiles)
	`
CREATE TABLE profiles (
	id         TEXT PRIMARY KEY, -- UUIDv7
	name       TEXT NOT NULL,
	settings   TEXT NOT NULL,    -- 値(JSON)。会話を作るときに写す
	rules      TEXT NOT NULL,    -- 決まり(JSON)。会話からそのつど引く
	archived   INTEGER NOT NULL DEFAULT 0, -- しまった(新しい会話では選べないが、決まりは残る)
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
ALTER TABLE conversations ADD COLUMN profile_id TEXT REFERENCES profiles(id);
CREATE INDEX conversations_by_profile ON conversations(profile_id);
`,
	// 版 6: 会話の持ち主(0017 の 10-0)。会話とプロファイルに owner(利用者の ID)を足し、表示の設定と「新しい会話で最初に選ばれるプロファイル」を
	// 人ごとの設定(user_settings)に移す。今ある会話・プロファイル・設定は、ログインなしの形の利用者(aibow.LocalUser)のものにする。
	// 利用者の表はまだ作らない(10-1)ので、owner は外部キーにしない。
	// DEFAULT は今ある行を埋めるためのもの(SQLite は NOT NULL の列を足すときに既定の値が要る)。足すときはいつも owner を書く。
	// 発言と要約は会話を通して持ち主が決まるので、列を足さない
	`
ALTER TABLE conversations ADD COLUMN owner TEXT NOT NULL DEFAULT '00000000-0000-7000-8000-000000000001';
ALTER TABLE profiles ADD COLUMN owner TEXT NOT NULL DEFAULT '00000000-0000-7000-8000-000000000001';
CREATE INDEX conversations_by_owner ON conversations(owner, updated_at);
CREATE INDEX profiles_by_owner ON profiles(owner, id);
CREATE TABLE user_settings (
	owner TEXT NOT NULL,              -- 利用者の ID
	key   TEXT NOT NULL,
	value TEXT NOT NULL,              -- JSON
	PRIMARY KEY (owner, key)
);
INSERT INTO user_settings (owner, key, value)
	SELECT '00000000-0000-7000-8000-000000000001', key, value FROM app_settings
	WHERE key IN ('default_profile', 'pin_question', 'theme');
DELETE FROM app_settings WHERE key IN ('default_profile', 'pin_question', 'theme');
`,
	// 版 7: 利用者とセッション(0017 の 10-1。auth パッケージ)。ログインなしの形の利用者(aibow.LocalUser)を、名前 local で入れておく
	// (パスワードは空 = ログインできない。aibow-server user passwd local で作れば、ログインが要る形でもその人の会話を使える)。
	// 会話などの owner は、ALTER TABLE では外部キーにできないので、そのまま(利用者を消すときは、その人の行も消す。DeleteUser)
	`
CREATE TABLE users (
	id         TEXT PRIMARY KEY,         -- UUIDv7(ログインなしの形の利用者だけ決まった値)
	name       TEXT NOT NULL UNIQUE,     -- ログインの名前
	password   TEXT NOT NULL,            -- パスワードのハッシュ(pbkdf2-sha256$回数$塩$値)。空ならログインできない
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
INSERT INTO users (id, name, password, created_at, updated_at) VALUES ('00000000-0000-7000-8000-000000000001', 'local', '',
	strftime('%Y-%m-%dT%H:%M:%S', 'now') || '.000000Z', strftime('%Y-%m-%dT%H:%M:%S', 'now') || '.000000Z');
CREATE TABLE sessions (
	id         TEXT PRIMARY KEY,         -- 合言葉(cookie の値)の SHA-256。合言葉そのものは保存しない
	user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	created_at TEXT NOT NULL,
	expires_at TEXT NOT NULL             -- 最後に使ってから 30 日(使うと延びる)
);
CREATE INDEX sessions_by_user ON sessions(user_id);
`,
	// 版 8: API キー(0017 の 10-2)。1人1本なので、利用者の ID を主キーにする(作り直すと置き換わる)。保存するのはキーの SHA-256 だけ
	`
CREATE TABLE api_keys (
	user_id    TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
	id         TEXT NOT NULL UNIQUE,     -- キーの SHA-256。キーそのものは保存しない
	created_at TEXT NOT NULL
);
`,
	// 版 9: 会話の「生成中」の印(0017 の 10-3。aibow.ConversationLocks)。ブラウザとアプリが同じ会話に同時に送らないように。
	// 印を付けた側(busy_by)と期限(busy_until)。期限を過ぎた印は無いものとして扱う(落ちたアプリの印が残り続けないように)
	`
ALTER TABLE conversations ADD COLUMN busy_by TEXT;
ALTER TABLE conversations ADD COLUMN busy_until TEXT;
`,
	// 版 10: 管理者の印(0018 の 11-0)。aibow-server user admin <名前> で付ける。今いる人は管理者でない(0)
	`
ALTER TABLE users ADD COLUMN admin INTEGER NOT NULL DEFAULT 0;
`,
	// 版 11: プロファイルを配る(0018 の 11-1)。配る範囲(share)と、指定した人(profile_shares)。
	// owner_name は配ったときの作った人の名前(作った人を消しても、受け取った人に誰が配ったかを見せるため)
	`
ALTER TABLE profiles ADD COLUMN share TEXT NOT NULL DEFAULT '';  -- '' 配らない / all 全員 / users 指定した人
ALTER TABLE profiles ADD COLUMN owner_name TEXT NOT NULL DEFAULT '';
CREATE TABLE profile_shares (
	profile_id TEXT NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
	user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	PRIMARY KEY (profile_id, user_id)
);
CREATE INDEX profile_shares_by_user ON profile_shares(user_id);
`,
	// 版 12: 会話の共有(0018 の 11-2)。共有の範囲(share)と、指定した人(conversation_shares)。プロファイルの版 11 と同じ形
	`
ALTER TABLE conversations ADD COLUMN share TEXT NOT NULL DEFAULT '';  -- '' 非公開 / all 全員 / users 指定した人
CREATE TABLE conversation_shares (
	conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
	user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	PRIMARY KEY (conversation_id, user_id)
);
CREATE INDEX conversation_shares_by_user ON conversation_shares(user_id);
`,
	// 版 13: アプリ用のトークン(0019)。画面のアプリでログインすると aibow-server が出す、期限が固定(10 日)の合言葉。
	// PC ごとに1本なので、利用者と PC の名前の組は UNIQUE(同じ PC でログインし直すと置き換わる)。保存するのはトークンの SHA-256 だけ
	`
CREATE TABLE app_tokens (
	id         TEXT PRIMARY KEY,         -- トークンの SHA-256。トークンそのものは保存しない
	user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	device     TEXT NOT NULL,            -- ログインした PC の名前(アプリが送ってくる)
	created_at TEXT NOT NULL,
	expires_at TEXT NOT NULL,            -- 作ってから 10 日(使っても延ばさない)
	UNIQUE (user_id, device)
);
`,
	// 版 14: パスワードを決めるリンク(0022)。管理者かコマンドが出し、本人がパスワードを決める。期限は 1 時間・一度だけ。
	// 1人1本なので利用者の ID を主キーにする(出し直すと置き換わる)。保存するのは合言葉の SHA-256 だけ
	`
CREATE TABLE password_links (
	user_id    TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
	id         TEXT NOT NULL UNIQUE,     -- 合言葉の SHA-256。合言葉そのものは保存しない
	created_at TEXT NOT NULL,
	expires_at TEXT NOT NULL             -- 出してから 1 時間
);
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
	if c.ShareWith, err = s.shareNames(ctx, "conversation_shares", "conversation_id", c.Share, id); err != nil {
		return c, nil, err
	}
	ms, err := s.messages(ctx, id)
	return c, ms, err
}

// ---- 会話の共有(0018 の 11-2) --------------------------------------------------------------
//
// ほかの人の会話を読めるかは、ここの条件(conversationReadable)の1か所で決める(プロファイルの profileVisible と同じ考え方)。
// 読むのは ReadConversation だけで、書くメソッド(UpdateConversation・AddMessage など)は今までどおり持ち主の行だけを触る。
// なので、共有された人も管理者も書き込めない(決定事項 1・2)。

// conversationShared は、:me に共有されたほかの人の会話(全員か、:me が相手)
const conversationShared = `(conversations.owner <> :me AND (conversations.share = 'all'
	OR (conversations.share = 'users' AND EXISTS (SELECT 1 FROM conversation_shares s WHERE s.conversation_id = conversations.id AND s.user_id = :me))))`

// shareNames は、共有の相手(指定した人)の名前。範囲が users でなければ nil。
func (s *SQLite) shareNames(ctx context.Context, table, column, share string, id uuid.UUID) ([]string, error) {
	if share != aibow.ShareUsers {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, "SELECT u.name FROM "+table+" s JOIN users u ON u.id = s.user_id WHERE s."+column+" = ? ORDER BY u.name", id.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// SetConversationShare は、自分の会話の共有の範囲と相手を変える。相手は名前で受け取り、いない名前ならエラー。自分は相手にしない。
func (s *SQLite) SetConversationShare(ctx context.Context, id uuid.UUID, share string, with []string) error {
	owner, err := s.owner(ctx)
	if err != nil {
		return err
	}
	switch share {
	case aibow.ShareNone, aibow.ShareAll, aibow.ShareUsers:
	default:
		return fmt.Errorf("共有の範囲 %q は知らない値です(\"\"・all・users のどれか)", share)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, "UPDATE conversations SET share = ? WHERE id = ? AND owner = ?", share, id.String(), owner)
	if err := notFoundIfNone(res, err, aibow.ErrNotFound); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM conversation_shares WHERE conversation_id = ?", id.String()); err != nil {
		return err
	}
	if share == aibow.ShareUsers {
		for _, n := range with {
			var uid string
			err := tx.QueryRowContext(ctx, "SELECT id FROM users WHERE name = ?", n).Scan(&uid)
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("共有の相手 %q という利用者はいません", n)
			}
			if err != nil {
				return err
			}
			if uid == owner {
				continue
			}
			if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO conversation_shares (conversation_id, user_id) VALUES (?, ?)", id.String(), uid); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// conversationRefs は、ほかの人の会話の一覧(持ち主の名前付き)を where の条件で読む。
func (s *SQLite) conversationRefs(ctx context.Context, where string, args ...any) ([]aibow.ConversationRef, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT conversations.id, conversations.title, COALESCE(u.name, ''), conversations.updated_at
		FROM conversations LEFT JOIN users u ON u.id = conversations.owner WHERE `+where+` ORDER BY conversations.updated_at DESC, conversations.id DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []aibow.ConversationRef{}
	for rows.Next() {
		var r aibow.ConversationRef
		var id, updated string
		if err := rows.Scan(&id, &r.Title, &r.OwnerName, &updated); err != nil {
			return nil, err
		}
		if r.ID, err = parseID(id); err != nil {
			return nil, err
		}
		if r.UpdatedAt, err = parseTime(updated); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *SQLite) SharedConversations(ctx context.Context) ([]aibow.ConversationRef, error) {
	me, err := s.owner(ctx)
	if err != nil {
		return nil, err
	}
	return s.conversationRefs(ctx, conversationShared, sql.Named("me", me))
}

func (s *SQLite) UserConversations(ctx context.Context, name string) ([]aibow.ConversationRef, error) {
	args, _, err := s.profileArgs(ctx)
	if err != nil {
		return nil, err
	}
	if args[1].(sql.NamedArg).Value.(int) != 1 {
		return nil, aibow.ErrNotAdmin
	}
	return s.conversationRefs(ctx, "u.name = :name", sql.Named("name", name))
}

func (s *SQLite) ReadConversation(ctx context.Context, id uuid.UUID) (aibow.SharedRead, error) {
	args, me, err := s.profileArgs(ctx)
	if err != nil {
		return aibow.SharedRead{}, err
	}
	admin := args[1].(sql.NamedArg).Value.(int) == 1
	var r aibow.SharedRead
	var owner string
	var shared bool
	err = s.db.QueryRowContext(ctx, "SELECT conversations.owner, COALESCE(u.name, ''), "+conversationShared+
		" FROM conversations LEFT JOIN users u ON u.id = conversations.owner WHERE conversations.id = :id",
		sql.Named("me", me), sql.Named("id", id.String())).Scan(&owner, &r.OwnerName, &shared)
	if errors.Is(err, sql.ErrNoRows) {
		return r, aibow.ErrNotFound
	}
	if err != nil {
		return r, err
	}
	// 自分の会話は Conversation で読む。共有されていない人には(管理者でなければ)、あることも見せない
	if owner == me || (!shared && !admin) {
		return r, aibow.ErrNotFound
	}
	if r.Conversation, err = scanConversation(s.db.QueryRowContext(ctx, conversationSelect+" WHERE id = ?", id.String())); err != nil {
		return r, err
	}
	r.Conversation.Settings.WorkDir = "" // 持ち主の PC のパスは見せない
	if r.Messages, err = s.messages(ctx, id); err != nil {
		return r, err
	}
	if r.Compactions, err = s.compactions(ctx, id, "conversation_id = ?", id.String()); err != nil {
		return r, err
	}
	if err := s.db.QueryRowContext(ctx, "SELECT name FROM users WHERE id = ?", me).Scan(&r.ReaderName); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return r, err
	}
	r.ByAdmin = !shared
	return r, nil
}

func (s *SQLite) UserNames(ctx context.Context) ([]string, error) {
	if _, err := s.owner(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT name FROM users WHERE id <> ? ORDER BY name", aibow.LocalUser.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
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

// notFoundIfNone は、書き換えた行が無ければ notFound を返す(無いか、その人のものでない)。
func notFoundIfNone(res sql.Result, err error, notFound error) error {
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return notFound
	}
	return nil
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

// ---- 発言 -------------------------------------------------------------------------------

// messages は会話 conv の発言。持ち主は呼ぶ側(Conversation)が確かめてある。
func (s *SQLite) messages(ctx context.Context, conv uuid.UUID) ([]aibow.StoredMessage, error) {
	// UUIDv7 は先頭が作成時刻なので、ID の文字列順 = 作った順になる。順番用の列はいらない
	rows, err := s.db.QueryContext(ctx, `SELECT id, role, content, thinking, model, stats, tool_calls, tool_result, excluded, created_at
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
		if err := rows.Scan(&id, &m.Role, &m.Content, &m.Thinking, &m.Model, &stats, &calls, &result, &m.Excluded, &created); err != nil {
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
	// 会話がその人のものなときだけ足す(INSERT … SELECT … WHERE で、確かめるのと足すのを1つの文にする)
	res, err := s.db.ExecContext(ctx, `INSERT INTO messages (id, conversation_id, role, content, thinking, model, stats, tool_calls, tool_result, excluded, created_at)
		SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ? WHERE `+ownConversation,
		m.ID.String(), m.ConversationID.String(), m.Role, m.Content, m.Thinking, m.Model, stats, calls, result, m.Excluded, formatTime(m.CreatedAt),
		m.ConversationID.String(), owner)
	return notFoundIfNone(res, err, aibow.ErrNotFound)
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

// ---- プロファイル -------------------------------------------------------------------------

const profileColumns = "id, name, settings, rules, archived, created_at, updated_at"

// ---- プロファイルを配る(0018 の 11-1) ----------------------------------------------------
//
// 誰に何が見えて、誰が変えられるかを、ここの SQL の条件だけで決める(認可を1か所に置く)。
// 条件の ? には、順に ctx の利用者(me)と、管理者か(admin。0 / 1)を入れる(profileArgs)。

const (
	// profileVisible は、me に見える(新しい会話で選べる)プロファイル
	profileVisible = `(profiles.owner = :me OR profiles.share = 'all'
		OR (profiles.share = 'users' AND EXISTS (SELECT 1 FROM profile_shares s WHERE s.profile_id = profiles.id AND s.user_id = :me))
		OR (profiles.share <> '' AND :admin = 1))`
	// profileUsed は、me がそのプロファイルから作った会話がある(配られなくなっても決まりを引くため。決定事項 21)
	profileUsed = `EXISTS (SELECT 1 FROM conversations c WHERE c.profile_id = profiles.id AND c.owner = :me)`
	// profileEditable は、me が変えられるプロファイル(自分の配っていないもの・管理者なら配られたもの。決定事項 9・20)
	profileEditable = `((profiles.share = '' AND profiles.owner = :me) OR (profiles.share <> '' AND :admin = 1))`
	// profileSelect は、読むときの列(保存している列 + 持ち主・配る範囲・作った人の名前・変えられないか・もう配られていないか)
	profileSelect = "SELECT " + profileColumns + ", owner, share, owner_name, NOT " + profileEditable + ", NOT " + profileVisible + " FROM profiles"
)

// profileArgs は、上の条件の名前付きの引数(me・admin)。
func (s *SQLite) profileArgs(ctx context.Context) ([]any, string, error) {
	me, err := s.owner(ctx)
	if err != nil {
		return nil, "", err
	}
	admin, err := s.isAdmin(ctx, me)
	if err != nil {
		return nil, "", err
	}
	return []any{sql.Named("me", me), sql.Named("admin", admin)}, me, nil
}

// isAdmin は、利用者 me が管理者か(利用者の表にいなければ false。デスクトップのアプリの aibow.db では、いつも false)。
func (s *SQLite) isAdmin(ctx context.Context, me string) (int, error) {
	var admin int
	err := s.db.QueryRowContext(ctx, "SELECT admin FROM users WHERE id = ?", me).Scan(&admin)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return admin, err
}

// fillShareWith は、変えられる人にだけ、配る相手の名前を入れる。
func (s *SQLite) fillShareWith(ctx context.Context, p *aibow.Profile) error {
	if p.ReadOnly || p.Share != aibow.ShareUsers {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT u.name FROM profile_shares s JOIN users u ON u.id = s.user_id
		WHERE s.profile_id = ? ORDER BY u.name`, p.ID.String())
	if err != nil {
		return err
	}
	defer rows.Close()
	p.ShareWith = []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		p.ShareWith = append(p.ShareWith, name)
	}
	return rows.Err()
}

// SetProfileShare は、配る範囲と相手を変える(aibow.Store の約束の説明を参照)。相手は名前で受け取り、いない名前ならエラー。
func (s *SQLite) SetProfileShare(ctx context.Context, id uuid.UUID, share string, with []string) error {
	args, me, err := s.profileArgs(ctx)
	if err != nil {
		return err
	}
	if args[1].(sql.NamedArg).Value.(int) != 1 {
		return aibow.ErrNotAdmin
	}
	switch share {
	case aibow.ShareNone, aibow.ShareAll, aibow.ShareUsers:
	default:
		return fmt.Errorf("配る範囲 %q は知らない値です(\"\"・all・users のどれか)", share)
	}
	p, err := s.Profile(ctx, id)
	if err != nil {
		return err
	}
	if p.ReadOnly || p.Withdrawn {
		return aibow.ErrProfileReadOnly
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// 初めて配るときに、作った人の名前を覚える(作った人を消しても、受け取った人に誰が配ったかを見せるため)
	name := p.OwnerName
	if name == "" {
		if err := tx.QueryRowContext(ctx, "SELECT name FROM users WHERE id = ?", p.Owner.String()).Scan(&name); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE profiles SET share = ?, owner_name = ?, updated_at = ? WHERE id = ?",
		share, name, formatTime(time.Now()), id.String()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM profile_shares WHERE profile_id = ?", id.String()); err != nil {
		return err
	}
	if share == aibow.ShareUsers {
		for _, n := range with {
			var uid string
			err := tx.QueryRowContext(ctx, "SELECT id FROM users WHERE name = ?", n).Scan(&uid)
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("配る相手 %q という利用者はいません", n)
			}
			if err != nil {
				return err
			}
			if uid == me {
				continue // 自分には配らなくても見える
			}
			if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO profile_shares (profile_id, user_id) VALUES (?, ?)", id.String(), uid); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// editableErr は、変えようとして1行も変わらなかったときのエラー。見えるなら変えられないもの、見えなければ無いもの。
func (s *SQLite) editableErr(ctx context.Context, id uuid.UUID) error {
	if _, err := s.Profile(ctx, id); err != nil {
		return err
	}
	return aibow.ErrProfileReadOnly
}

func scanProfile(r rowScanner) (aibow.Profile, error) {
	var p aibow.Profile
	var id, values, rules, created, updated, owner string
	if err := r.Scan(&id, &p.Name, &values, &rules, &p.Archived, &created, &updated, &owner, &p.Share, &p.OwnerName, &p.ReadOnly, &p.Withdrawn); err != nil {
		return p, err
	}
	var err error
	if p.ID, err = parseID(id); err != nil {
		return p, err
	}
	if p.Owner, err = parseID(owner); err != nil {
		return p, err
	}
	if err := json.Unmarshal([]byte(values), &p.Values); err != nil {
		return p, fmt.Errorf("プロファイル %s の値が読めません: %w", id, err)
	}
	p.Values.Model = models.IDOf(p.Values.Model) // 会話と同じく、以前の形のモデルの ID を読み替える
	if err := json.Unmarshal([]byte(rules), &p.Rules); err != nil {
		return p, fmt.Errorf("プロファイル %s の決まりが読めません: %w", id, err)
	}
	if p.CreatedAt, err = parseTime(created); err != nil {
		return p, err
	}
	if p.UpdatedAt, err = parseTime(updated); err != nil {
		return p, err
	}
	return p, nil
}

// ListProfiles は、ctx の利用者に見えるプロファイル(自分のもの・配られたもの。profileVisible)。
func (s *SQLite) ListProfiles(ctx context.Context) ([]aibow.Profile, error) {
	args, _, err := s.profileArgs(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, profileSelect+" WHERE "+profileVisible+" ORDER BY id", args...)
	if err != nil {
		return nil, err
	}
	out := []aibow.Profile{}
	for rows.Next() {
		p, err := scanProfile(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if err := s.fillShareWith(ctx, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Profile は、見えるプロファイルか、ctx の利用者がそのプロファイルから作った会話があるプロファイル(配られなくなっても決まりを引くため)。
func (s *SQLite) Profile(ctx context.Context, id uuid.UUID) (aibow.Profile, error) {
	args, _, err := s.profileArgs(ctx)
	if err != nil {
		return aibow.Profile{}, err
	}
	args = append(args, sql.Named("id", id.String()))
	p, err := scanProfile(s.db.QueryRowContext(ctx, profileSelect+" WHERE ("+profileVisible+" OR "+profileUsed+") AND profiles.id = :id", args...))
	if errors.Is(err, sql.ErrNoRows) {
		return p, aibow.ErrProfileNotFound
	}
	if err != nil {
		return p, err
	}
	return p, s.fillShareWith(ctx, &p)
}

// profileJSON は、プロファイルの値と決まりを保存する形(JSON)にする。
func profileJSON(p aibow.Profile) (values, rules string, err error) {
	v, err := json.Marshal(p.Values)
	if err != nil {
		return "", "", err
	}
	r, err := json.Marshal(p.Rules)
	if err != nil {
		return "", "", err
	}
	return string(v), string(r), nil
}

func (s *SQLite) CreateProfile(ctx context.Context, p aibow.Profile) error {
	owner, err := s.owner(ctx)
	if err != nil {
		return err
	}
	values, rules, err := profileJSON(p)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "INSERT INTO profiles ("+profileColumns+", owner) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		p.ID.String(), p.Name, values, rules, p.Archived, formatTime(p.CreatedAt), formatTime(p.UpdatedAt), owner)
	return err
}

// UpdateProfile は、変えられるプロファイル(profileEditable)だけを書き換える。配る範囲は変えない(SetProfileShare)。
func (s *SQLite) UpdateProfile(ctx context.Context, p aibow.Profile) error {
	args, _, err := s.profileArgs(ctx)
	if err != nil {
		return err
	}
	values, rules, err := profileJSON(p)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, "UPDATE profiles SET name = :name, settings = :values, rules = :rules, archived = :archived, updated_at = :at WHERE id = :id AND "+profileEditable,
		append(args, sql.Named("name", p.Name), sql.Named("values", values), sql.Named("rules", rules), sql.Named("archived", p.Archived),
			sql.Named("at", formatTime(p.UpdatedAt)), sql.Named("id", p.ID.String()))...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return s.editableErr(ctx, p.ID)
	}
	return nil
}

// DeleteProfile は、使っている会話が無いときだけプロファイルを消す。
// 外部キーでも消せないようになっているが、SQLite の制約のエラーは何が理由か読みにくいので、先に数えて ErrProfileInUse を返す。
// 数えてから消すまでの間に会話が増えないよう、1つのトランザクションで行う。
func (s *SQLite) DeleteProfile(ctx context.Context, id uuid.UUID) error {
	args, _, err := s.profileArgs(ctx)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var used int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM conversations WHERE profile_id = ?", id.String()).Scan(&used); err != nil {
		return err
	}
	if used > 0 {
		return aibow.ErrProfileInUse
	}
	res, err := tx.ExecContext(ctx, "DELETE FROM profiles WHERE id = :id AND "+profileEditable, append(args, sql.Named("id", id.String()))...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		tx.Rollback() // 接続は1本なので、トランザクションを閉じてから読む(閉じないと、読む側が待ち続ける)
		return s.editableErr(ctx, id)
	}
	return tx.Commit()
}

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
