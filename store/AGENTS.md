# store パッケージ

`aibow.Store`(ルートの `store.go` の interface)の SQLite 実装です。
会話の仕組みの側の説明は [_docs/conversations.md](../_docs/conversations.md) にあります。

| ファイル | 役割 |
|---|---|
| `sqlite.go` | `Store` の SQLite 実装(`modernc.org/sqlite`)の土台。開く(`Open`)・ctx の利用者(`owner`・`isAdmin`)・値の変換と、ファイルをまたいで使う小さな部品 |
| `migrate.go` | マイグレーション(`migrations` と `migrate`。版の一覧は下) |
| `conversations.go` | 会話の読み書きと「生成中」の印(`LockConversation`)。発言と要約の持ち主の条件 `ownConversation`・`ownMessage` もここ |
| `share.go` | 会話の共有とほかの人の会話を読む(0018 の 11-2)。誰に見えるかの条件 `conversationShared`・共有の相手を覚える `setShareTargets`(プロファイルを配るのにも使う)・共有の相手を選ぶ一覧 `Directory` |
| `messages.go` | 発言と、発言に付けた読み込んだファイル(`files`。0030) |
| `compactions.go` | 要約(compact) |
| `profiles.go` | プロファイルとプロファイルを配る(0018 の 11-1)。見える・変えられるの条件 `profileVisible`・`profileUsed`・`profileEditable` |
| `settings.go` | アプリ全体の設定(`app_settings`)と人ごとの設定(`user_settings`) |
| `users.go` | 利用者とセッションの保存(`auth.Store` の実装。aibow-server の server.db だけで使う) |
| `groups.go` | グループとグループの長(0031)。誰が変えられるかの条件 `groupEditable` もここ |
| `usage.go` | aibow-server の生成の記録(使用量。0033)の保存と集計(利用者ごと・モデルごと・日ごと) |
| `remote/` | 会話の保存を HTTP 越しに使う(`remote` パッケージ。0017 の 10-3)。aibow-server の保存の口(`Handler`。`/_aibow/store/{メソッド}`)と、アプリの側の `aibow.Store` の HTTP 版(`Client`) |
| `sqlite_test.go` | 一時フォルダの DB を使ったテスト(マイグレーション・持ち主で分かれることを含む) |

- ドライバーは `modernc.org/sqlite`(cgo 不要)。標準の `database/sql` 経由で使う。このドライバーを import するのはこのパッケージだけ。
  C コンパイラなし(`CGO_ENABLED=0`)でビルドできる状態を保つ
- マイグレーションは `PRAGMA user_version` で版を管理する。版を足すときは `migrations` の末尾に追加する(過去の版は書き換えない)。
  先のステップで要る列や表を先回りして作らない
  - 版 1: 会話・発言・設定
  - 版 2: `compactions`(要約。[_docs/compact.md](../_docs/compact.md))
  - 版 3: `messages` に `tool_calls` / `tool_result`(ツール呼び出しと結果。JSON。[_docs/tools.md](../_docs/tools.md))
  - 版 4: `messages` に `excluded`(ユーザーが選んで「モデルに渡さない」にした発言。0 / 1。[_docs/features/0011-exclude-messages.md](../_docs/features/0011-exclude-messages.md))
  - 版 5: `profiles`(設定のプロファイル。値 `settings` と決まり `rules` は JSON、しまった `archived`)と、`conversations` に `profile_id`(外部キー。
    `ON DELETE` を付けないので、使っている会話があるプロファイルは消せない。今ある会話は NULL で、起動したときに `PrepareProfiles` が紐づける)。
    [_docs/features/0014-profiles.md](../_docs/features/0014-profiles.md)
  - 版 6: 会話の持ち主(0017 の 10-0)。`conversations` と `profiles` に `owner`(利用者の ID)、人ごとの設定の表 `user_settings`(持ち主 + キー → 値)。
    今ある会話・プロファイルと、`app_settings` の `default_profile`・`pin_question`・`theme` は、ログインなしの形の利用者(`aibow.LocalUser`)のものにする(設定は `user_settings` に移す)。
    利用者の表はまだ無いので `owner` は外部キーにしない。発言と要約は会話を通して持ち主が決まるので列を持たない
  - 版 7: 利用者 `users`(名前は UNIQUE・パスワードのハッシュ。空ならログインできない)とセッション `sessions`(合言葉の SHA-256・持ち主・期限)。
    ログインなしの形の利用者を名前 `local`・パスワード空で入れる。`auth.Store` の実装は `users.go`([auth](../auth) パッケージ。0017 の 10-1)。
    利用者を消すときは、その人の会話・プロファイル・人ごとの設定・セッションを1つのトランザクションで消す(`owner` は外部キーではないため)
  - 版 8: API キー `api_keys`(1人1本なので利用者の ID が主キー。キーの SHA-256 は UNIQUE。利用者を消すと一緒に消える)。0017 の 10-2
  - 版 9: `conversations` に「生成中」の印 `busy_by`・`busy_until`(`aibow.ConversationLocks`。ブラウザとアプリが同じ会話に同時に送らないように。期限を過ぎた印は無いものとして扱う)。0017 の 10-3
  - 版 10: `users` に管理者の印 `admin`(0 / 1。`aibow-server user admin` で付ける)。0018 の 11-0
  - 版 11: プロファイルを配る(0018 の 11-1)。`profiles` に配る範囲 `share`(`''` / `all` / `users`)と、配ったときの作った人の名前 `owner_name`、
    指定した人の表 `profile_shares`(プロファイル + 利用者。どちらかを消すと一緒に消える)
  - 版 12: 会話の共有(0018 の 11-2)。`conversations` に共有の範囲 `share`(`''` / `all` / `users`)と、指定した人の表 `conversation_shares`(会話か利用者を消すと一緒に消える)
  - 版 13: アプリ用のトークン `app_tokens`(0019)。トークンの SHA-256・持ち主・PC の名前・期限(作ってから 10 日)。利用者と PC の名前の組は UNIQUE(PC ごとに1本)。
    利用者を消すと一緒に消え、パスワードを作り直すとブラウザのセッションと一緒に消える(`DeleteUserSessions`)
  - 版 14: パスワードを決めるリンク `password_links`(0022)。1人1本なので利用者の ID が主キー、合言葉の SHA-256 は UNIQUE・期限(出してから 1 時間)。利用者を消すと一緒に消える
  - 版 15: 読み込んだファイル `files`(0030)。発言に付けたファイルの種類・名前・形式・ハッシュ・中身(BLOB)。発言を消すと一緒に消える(作り直す・送り直す・会話を消す)。
    持ち主は発言を通して決まるので列を持たない。発言と同じトランザクションで足し(`AddMessage`)、発言を読んだあとにまとめて読む(`attachFiles`。接続が1本なので、発言の rows を閉じてから)
  - 版 16: `files` に捨てた印 `discarded`(0 / 1。0030 の決定事項 26〜28)。捨てると中身を空の BLOB にする(`DiscardFile`。secure_delete で元の中身はゼロに上書きされる)。
    捨てたファイルを足すとき(送り直す・写す)も空の BLOB で入れる(列が NOT NULL のため)
  - 版 17: `users` に表示名 `display_name`(空ならログインの名前を出す)と最後に使った時刻 `last_used_at`(空は一度も使っていない。前に書いてから1日以上たったときだけ書く)。0031 の段3
  - 版 18: グループ(0031 の段4)。`user_groups`(名前は UNIQUE。`groups` は SQLite の窓関数の GROUPS と重なるので避けた)・`user_group_members`(グループ + 利用者 + 長の印 `leader`)・
    会話とプロファイルの「指定したグループ」の表 `conversation_group_shares`・`profile_group_shares`。どれもグループか相手を消すと一緒に消える。グループの読み書きは `groups.go`
  - 版 19: `users` にメールアドレス `email`(0031 の決定事項 40〜43。今は持つだけ)。空でなければ1人1つ:索引 `users_email` は `lower(email)` の UNIQUE で、
    `WHERE email <> ''` の部分索引(付けていない人が何人いても重ならない)。重なりは `auth.ErrEmailExists`
  - 版 20: aibow-server の生成1回ごとの記録 `generation_log`(使用量。0033)。時刻・利用者の ID と写した名前・道・用途・会話の ID・モデル・トークン数と秒数・順番待ちの秒数(分からなければ NULL)・終了理由。
    会話の中身は持たない。利用者と会話の ID は外部キーにしない(消しても記録を残す。分からない ID は空の文字列)。索引は `at` と `(user_id, at)`。
    読み書きは `usage.go`(書くのは記録付きの engine、読むのは使用量の口。持ち主では絞らない)。集計は SQL(日の区切りはサーバーの PC の時刻のずれを date() に足す)
  - 版 21: `messages` に `elided`(今の組の中の compact で、省略して渡す発言の印。0 / 1。[_docs/features/0040-compact-in-turn.md](../_docs/features/0040-compact-in-turn.md))。
    `SetMessagesElided` で付けるだけで、外す口は無い。`AddMessage` も印をそのまま入れる(会話を写すときに付いていく)
- 会話の共有:ほかの人の会話を読むのは `ReadConversation` だけ(条件 `conversationShared` と管理者か)。書くメソッドは持ち主の行だけなので、共有された人も管理者も書き込めない。
  自分の会話を読むときは `share`(と1つ読むときは `ShareWith`)を入れる
- プロファイルの読み書き:見える・変えられるの判定は `profiles.go` の `profileVisible`・`profileUsed`・`profileEditable`(名前付きの引数 `:me`・`:admin`)の1か所だけ。
  `Profile` / `ListProfiles` は読むときに `Owner`・`OwnerName`・`ReadOnly`・`Withdrawn` を入れ、`ShareWith` は変えられる人にだけ入れる。
  利用者を消しても、配ったプロファイルと、ほかの人の会話が使っているプロファイルは残す(`DeleteUser`)。接続は1本(`SetMaxOpenConns(1)`)なので、トランザクションの途中でほかの読み出しをしない(閉じてから読む)
- モデルの ID:会話の設定とプロファイルの値の `model` は、読むときに `models.IDOf` で今の形(`file:<ファイル名>`)に読み替える(以前の `file:<パス>` を書き換えずに使うため。0018 の決定事項 15)
- 持ち主:会話・発言・要約・プロファイル・人ごとの設定は、`ctx` の利用者(`aibow.UserOf`)の行だけを読み書きする。ほかの人の行は無いものとして扱う(`ErrNotFound` など)。
  利用者の無い `ctx` は `aibow.ErrNoUser` で断る。`OneUser` を呼んだ DB(デスクトップのアプリの aibow.db)だけは、`aibow.LocalUser` のものとして扱う。
  アプリ全体の設定(`app_settings`)は利用者によらない。仕組みは [_docs/conversations.md](../_docs/conversations.md) の「持ち主」
- 接続の設定(`Open` の `_pragma`):`foreign_keys(1)`(`ON DELETE CASCADE` を効かせる)・`busy_timeout(5000)`・`secure_delete(1)`。
  `secure_delete` は、消した行の場所をゼロで上書きする。SQLite の既定では、行を消しても場所に「空き」の印を付けるだけで、
  中身が上書きされるか VACUUM するまで DB ファイルに残る(読み込んだ画像のバイト列が残ることを確かめた。0030)。
  会話を消すと、発言・要約・読み込んだファイルの中身まで DB ファイルから消える(`TestDeleteConversationErasesFiles`)
- ID(`uuid.NewV7()`)は TEXT で入れる。発言の順番は ID の並びで決める(順番用の列は持たない)
- DB ファイルの場所はこのパッケージでは決めない。`_cmd/aibow/config.go` が `AIBOW_DB`(既定は `%APPDATA%\aibow\aibow.db`)から決めて `Open` に渡す
- 試すときは `AIBOW_DB` を一時フォルダのファイルにする(ユーザーの DB に試しの会話を混ぜない)
