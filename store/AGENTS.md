# store パッケージ

`aibow.Store`(ルートの `store.go` の interface)の SQLite 実装です。
会話の仕組みの側の説明は [_docs/conversations.md](../_docs/conversations.md) にあります。

| ファイル | 役割 |
|---|---|
| `sqlite.go` | `Store` の SQLite 実装(`modernc.org/sqlite`)。マイグレーションもここ |
| `users.go` | 利用者とセッションの保存(`auth.Store` の実装。aibow-server の server.db だけで使う) |
| `remote/` | 会話の保存を HTTP 越しに使う(`remote` パッケージ。0017 の 10-3)。aibow-server の保存の口(`Handler`。`/aibow/store/{メソッド}`)と、アプリの側の `aibow.Store` の HTTP 版(`Client`) |
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
- モデルの ID:会話の設定とプロファイルの値の `model` は、読むときに `models.IDOf` で今の形(`file:<ファイル名>`)に読み替える(以前の `file:<パス>` を書き換えずに使うため。0018 の決定事項 15)
- 持ち主:会話・発言・要約・プロファイル・人ごとの設定は、`ctx` の利用者(`aibow.UserOf`)の行だけを読み書きする。ほかの人の行は無いものとして扱う(`ErrNotFound` など)。
  利用者の無い `ctx` は `aibow.ErrNoUser` で断る。`OneUser` を呼んだ DB(デスクトップのアプリの aibow.db)だけは、`aibow.LocalUser` のものとして扱う。
  アプリ全体の設定(`app_settings`)は利用者によらない。仕組みは [_docs/conversations.md](../_docs/conversations.md) の「持ち主」
- ID(`uuid.NewV7()`)は TEXT で入れる。発言の順番は ID の並びで決める(順番用の列は持たない)
- DB ファイルの場所はこのパッケージでは決めない。`_cmd/aibow/config.go` が `AIBOW_DB`(既定は `%APPDATA%\aibow\aibow.db`)から決めて `Open` に渡す
- 試すときは `AIBOW_DB` を一時フォルダのファイルにする(ユーザーの DB に試しの会話を混ぜない)
