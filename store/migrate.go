package store

// migrate.go: マイグレーション(テーブルの作り変え)。版の一覧と、版ごとに何を足したかは AGENTS.md。

import (
	"database/sql"
	"fmt"
)

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
	// 版 15: 読み込んだファイル(0030)。発言に付けたファイル(テキスト。段2で画像も同じ表に入れる)。
	// 発言とは別の表にするのは、中身(バイト列)が重いので発言の行を軽く保つため。発言を消すと一緒に消える。
	// 持ち主は発言(を通して会話)で決まるので列を持たない
	`
CREATE TABLE files (
	id         TEXT PRIMARY KEY,  -- UUIDv7。発言の中の並びもこれで決まる
	message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
	kind       TEXT NOT NULL,     -- 種類: text
	name       TEXT NOT NULL,     -- ファイル名
	format     TEXT NOT NULL,     -- 形式(テキストは読んだときの文字コード)
	hash       TEXT NOT NULL,     -- data の SHA-256(16進)
	data       BLOB NOT NULL      -- 中身(テキストは UTF-8 にしたもの)
);
CREATE INDEX files_by_message ON files(message_id, id);
`,
	// 版 16: 送ったファイルを捨てる(0030 の決定事項 26〜28)。捨てたら中身を空にし、印を付ける(名前と種類は残す)
	`
ALTER TABLE files ADD COLUMN discarded INTEGER NOT NULL DEFAULT 0;  -- 0 / 1
`,
	// 版 17: 表示名と最後に使った時刻(0031 の段3)。表示名は人に見せる名前(空ならログインの名前を出す)。
	// 最後に使った時刻は、前に書いてから1日以上たったときだけ書く(auth の used.go)。空は一度も使っていない
	`
ALTER TABLE users ADD COLUMN display_name TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN last_used_at TEXT NOT NULL DEFAULT '';
`,
	// 版 18: グループ(0031 の段4)。グループ user_groups(名前は UNIQUE)・メンバー user_group_members(長の印 leader を持つ。
	// 抜けると行ごと消えるので、長でもなくなる)・会話とプロファイルの「指定したグループ」の表(グループを消すと一緒に消える)。
	// 表の名前を groups にしないのは、SQLite の窓関数の GROUPS と重なるため
	`
CREATE TABLE user_groups (
	id         TEXT PRIMARY KEY,
	name       TEXT NOT NULL UNIQUE,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE TABLE user_group_members (
	group_id TEXT NOT NULL REFERENCES user_groups(id) ON DELETE CASCADE,
	user_id  TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	leader   INTEGER NOT NULL DEFAULT 0,  -- 0 / 1。グループの長(メンバーを足す・外せる)
	PRIMARY KEY (group_id, user_id)
);
CREATE INDEX user_group_members_by_user ON user_group_members(user_id);
CREATE TABLE conversation_group_shares (
	conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
	group_id        TEXT NOT NULL REFERENCES user_groups(id) ON DELETE CASCADE,
	PRIMARY KEY (conversation_id, group_id)
);
CREATE INDEX conversation_group_shares_by_group ON conversation_group_shares(group_id);
CREATE TABLE profile_group_shares (
	profile_id TEXT NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
	group_id   TEXT NOT NULL REFERENCES user_groups(id) ON DELETE CASCADE,
	PRIMARY KEY (profile_id, group_id)
);
CREATE INDEX profile_group_shares_by_group ON profile_group_shares(group_id);
`,
	// 版 19: メールアドレス(0031 の決定事項 40〜43)。今は持つだけ。空でなければ1人1つ(大文字小文字は同じとみなすので lower() の索引)。
	// 部分索引(WHERE email <> '')にするのは、空(付けていない)の人が何人いても重なりにしないため
	`
ALTER TABLE users ADD COLUMN email TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX users_email ON users(lower(email)) WHERE email <> '';
`,
	// 版 20: aibow-server の生成1回ごとの記録(使用量。0033)。会話の中身は写さず、数字と、だれ・いつ・どのモデルかだけ。
	// 利用者と会話の ID は外部キーにしない(消しても記録を残すため。決定事項 4・8)。消された人も読めるよう名前を写す。
	// 分からない ID は ''、分からない順番待ちの秒数は NULL。集計は期間(at)で絞るので at に索引、人ごとに見るので (user_id, at) にも
	`
CREATE TABLE generation_log (
	id              TEXT PRIMARY KEY,
	at              TEXT NOT NULL,
	user_id         TEXT NOT NULL,
	user_name       TEXT NOT NULL,
	route           TEXT NOT NULL,   -- browser / app / api
	purpose         TEXT NOT NULL,   -- reply / summary
	conversation_id TEXT NOT NULL,
	model           TEXT NOT NULL,
	prompt_tokens   INTEGER NOT NULL,
	reused_tokens   INTEGER NOT NULL,
	gen_tokens      INTEGER NOT NULL,
	image_tokens    INTEGER NOT NULL,
	prompt_sec      REAL NOT NULL,
	gen_sec         REAL NOT NULL,
	encode_sec      REAL NOT NULL,
	wait_sec        REAL,
	stop_reason     TEXT NOT NULL
);
CREATE INDEX generation_log_at ON generation_log(at);
CREATE INDEX generation_log_user ON generation_log(user_id, at);
`,
	// 版 21: 今の組の中の compact(0040)で省略して渡す発言の印。今までの発言は省略しない(0)
	`
ALTER TABLE messages ADD COLUMN elided INTEGER NOT NULL DEFAULT 0;
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
