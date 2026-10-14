# バージョンとローカルデプロイ

バージョンを上げる・アプリをインストールするときに読みます。

| ファイル | 役割 |
|---|---|
| `_cmd/aibow/version` | バージョンの唯一の正。`//go:embed` で exe に入る |
| `_cmd/version.go` | `version` を config.yml / package.json / `_cmd/aibow-mcp/version` に書き写すスクリプト(リポジトリのルートで `go run _cmd/version.go`) |
| `_cmd/aibow-mcp/version` / `appversion.go` | MCP のサーバー(aibow-mcp)のバージョン。`_cmd/aibow/version` の写しで、手で書き換えない(`//go:embed` は自分のフォルダの下しか埋め込めないため写しを置く)。exe(`go build`・`go install`)は `version` のまま、`go run` で起動したものは patch を1つ上げて `+dev`(自分の exe が `go-build…` の一時フォルダにあるかで見分ける) |
| `_cmd/aibow/appversion.go` / `build_dev.go` / `build_production.go` | 画面・ウィンドウの名前・ログに出すバージョン。`wails3 dev` で起動したものは patch を1つ上げて `+dev` を付ける(`0.3.1` なら `0.3.2+dev`。まだデプロイしていない次のバージョンを作っている途中のため)。リリースのビルドは `version` のまま。見分けは、Taskfile がリリースのビルドにだけ付ける `production` のビルドタグ(Taskfile は変えない) |
| `_cmd/aibow/Taskfile.local.yml` | ローカルデプロイ(`wails3 task local:deploy`)。各自が持つ個人用のファイルで、**Git に入れない(ひな形もコミットしない)**。`Taskfile.yml` から optional で読む |

- CI でビルドしないので、`wails3 task local:deploy` が CI の代わり。バージョンの反映 → ビルド → ビルド情報だけのコミット → `v`+バージョンのタグ → インストールまでを行う
- バージョンを手で config.yml などに書かない。`_cmd/aibow/version` だけを変え、`go run _cmd/version.go` で書き写す(deploy は自動で行う)
- `v`+今のバージョンのタグがある(= デプロイ済み)なら、deploy が patch を自動で上げる。minor / major は `go run _cmd/version.go -bump`
- `Taskfile.local.yml` とそのひな形・コピーはコミットしない(インストール先などローカルの設定をリポジトリに残さないため。ユーザーの方針)
- `Taskfile.local.yml` のタスクを、コミットする `Taskfile.yml` のタスクから呼ばない(持っていない環境で壊れる)
- deploy がビルド情報としてコミットするファイル(`Taskfile.local.yml` の `BUILD_INFO_FILES`)には `../aibow-mcp/version` も入れる(入れないと、バージョンを上げたときに写しだけがコミットされずに残る)
- deploy を試すときは `DEPLOY_DEST` を一時フォルダにし、試しに作ったコミットとタグは消しておく(タグはリポジトリ全体で共有される)
