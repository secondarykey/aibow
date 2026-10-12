# store パッケージ

`aibow.Store`(ルートの `store.go` の interface)の SQLite 実装です。
会話の仕組みの側の説明は [_docs/conversations.md](../_docs/conversations.md) にあります。

| ファイル | 役割 |
|---|---|
| `sqlite.go` | `Store` の SQLite 実装(`modernc.org/sqlite`)。マイグレーションもここ |
| `sqlite_test.go` | 一時フォルダの DB を使ったテスト(マイグレーションを含む) |

- ドライバーは `modernc.org/sqlite`(cgo 不要)。標準の `database/sql` 経由で使う。このドライバーを import するのはこのパッケージだけ。
  C コンパイラなし(`CGO_ENABLED=0`)でビルドできる状態を保つ
- マイグレーションは `PRAGMA user_version` で版を管理する。版を足すときは `migrations` の末尾に追加する(過去の版は書き換えない)。
  先のステップで要る列や表を先回りして作らない
  - 版 1: 会話・発言・設定
  - 版 2: `compactions`(要約。[_docs/compact.md](../_docs/compact.md))
- ID(`uuid.NewV7()`)は TEXT で入れる。発言の順番は ID の並びで決める(順番用の列は持たない)
- DB ファイルの場所はこのパッケージでは決めない。`_cmd/aibow/config.go` が `AIBOW_DB`(既定は `%APPDATA%\aibow\aibow.db`)から決めて `Open` に渡す
- 試すときは `AIBOW_DB` を一時フォルダのファイルにする(ユーザーの DB に試しの会話を混ぜない)
