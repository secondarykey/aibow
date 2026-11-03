# バージョンとローカルデプロイ・リリース

バージョンを上げる・アプリをインストールする・リリースする(GitHub Actions)ときに読みます。

| ファイル | 役割 |
|---|---|
| `_cmd/aibow/version` | バージョンの唯一の正。`//go:embed` で exe に入る |
| `_cmd/version.go` | `version` を config.yml / package.json / `_cmd/aibow-mcp/version` / `_cmd/aibow-server/version` に書き写すスクリプト(リポジトリのルートで `go run _cmd/version.go`) |
| `_cmd/aibow-mcp/version` / `appversion.go`(`_cmd/aibow-server` も同じ) | MCP のサーバー(aibow-mcp)と OpenAI 互換 API(aibow-server)のバージョン。`_cmd/aibow/version` の写しで、手で書き換えない(`//go:embed` は自分のフォルダの下しか埋め込めないため写しを置く)。exe(`go build`・`go install`)は `version` のまま、`go run` で起動したものは patch を1つ上げて `+dev`(自分の exe が `go-build…` の一時フォルダにあるかで見分ける) |
| `_cmd/aibow/appversion.go` / `build_dev.go` / `build_production.go` | 画面・ウィンドウの名前・ログに出すバージョン。`wails3 dev` で起動したものは patch を1つ上げて `+dev` を付ける(`0.3.1` なら `0.3.2+dev`。まだデプロイしていない次のバージョンを作っている途中のため)。リリースのビルドは `version` のまま。見分けは、Taskfile がリリースのビルドにだけ付ける `production` のビルドタグ(Taskfile は変えない) |
| `_cmd/aibow/Taskfile.local.yml` | ローカルデプロイ(`wails3 task local:deploy`)。各自が持つ個人用のファイルで、**Git に入れない(ひな形もコミットしない)**。`Taskfile.yml` から optional で読む |
| `.github/workflows/versionup.yml` | 正式なリリース。`main` への push で版を決め、ビルド情報をコミットして `v`+バージョンのタグを打ち、`release.yml` を呼ぶ |
| `.github/workflows/release.yml` | 3つの OS で aibow・aibow-server・aibow-mcp を作って zip にし、GitHub のリリースの**下書き**を作る。`versionup.yml` から呼ばれるほか、手で `v` のタグを push しても動く |
| `.github/variables` | CI が使う Wails の CLI の版(`WAILS_VERSION`)。`_cmd/aibow/go.mod` の Wails と同じ版にする。`KEY=VALUE` の行だけを書く(コメントがあると CI が落ちる) |

### ローカルデプロイと正式なリリースの分け方

- `wails3 task local:deploy` は手元へのインストール。バージョンの反映 → ビルド → ビルド情報だけのコミット → インストールまでを行い、**タグは打たない**。
  deploy した exe を人に渡しても、それは正式なリリースではない
- 正式なリリースは `main` に push したとき。GitHub Actions(`versionup.yml`)が版を決めて `v`+バージョンのタグを打ち、`release.yml` がリリースの下書きを作る。
  下書きの zip を確かめてから、GitHub の画面で公開する
- 版の決め方は deploy も versionup も同じ `go run _cmd/version.go -auto`:`v`+今のバージョンのタグがある(= リリース済み)なら patch を上げ、無ければそのまま。
  リリースのあと `git fetch` で手元にタグを取ってくれば、次の deploy から次の版になる。main に push するたびにリリースが1つ作られる
- versionup はコミットとタグを `GITHUB_TOKEN` で push する。`GITHUB_TOKEN` の push はワークフローを起動しないので、自分の push で versionup がもう一度動くことはなく、
  タグの push で release.yml も起動しない。そのため versionup の最後のジョブが release.yml を呼ぶ(`workflow_call`。wails3 スキルの標準は PAT でタグを push する形だが、PAT を用意しなくてよいこちらにした)
- zip に llama.cpp の DLL(`lib`)は入れない(GPU に合う版を各自が README の手順で取ってくるため)
- push や PR のたびにテストを流す CI(ci.yml)は置かない。3つの OS で毎回 wails3 build まで流すのは重すぎるため(2026-11-03、ユーザーと決めた)。
  テストは push の前に手元で AGENTS.md の「確認方法」を流す。ほかの OS で壊れていれば、main に push したときの release.yml のビルドで分かる

### CI で気を付けていること(wails3 スキルの注意事項)

- Wails の CLI は `.github/variables` の版で入れる(`@latest` は使わない)。読み込むステップは `shell: bash`(Windows の既定の pwsh では `$GITHUB_ENV` に書けない)
- Linux のランナーは `ubuntu-24.04` に固定する。`ubuntu-latest` は glibc が新しく、そこで作った exe は古いディストリで起動しない。Linux 版を動かすには、使う PC に `libgtk-4-1` と `libwebkitgtk-6.0-4` が要る(zip では依存を伝えられない)
- macOS 版は `wails3 package` の ad-hoc 署名だけで、署名と notarize をしていない。ダウンロードした Mac では Gatekeeper が止める。`macos-latest` は arm64 なので Intel の Mac では起動しない
- `build/appicon.icon` はコミットしてある(無いと macOS 26 のランナーで `actool` が失敗する)
- Windows のランナーは `core.autocrlf=true` なので、checkout の前に `false` にする(CRLF になると gofmt -l に全部出る)
- `wails3 update build-assets` には `-name aibow -binaryname aibow` を必ず付ける(付けないと空になる)
- `package-lock.json` をそろえるのは `npx npm@11.20.0`(npm の版で lockfile の書き方が違うため。`_cmd/aibow/AGENTS.md`)
- `upload-artifact@v7` と `download-artifact@v8` が対(番号のずれは書き間違いではない)
- versionup がコミットするファイルは `Taskfile.local.yml` の `BUILD_INFO_FILES` と同じ一覧にする。片方を変えたらもう片方も直す

### ローカルデプロイ

- deploy は画面のアプリ(`aibow.exe`)と一緒に `aibow-server.exe` と `aibow-mcp.exe` も作ってインストールする(`Taskfile.local.yml` の `build-tools`)。
  aibow-server は画面(`web/dist/app`)を埋め込むので、`wails3 build` が画面をビルドした後に `go build` する。
  どちらも `CGO_ENABLED=0`・`-trimpath -ldflags="-w -s"` で、コンソールのアプリなので `-H windowsgui` は付けない。`go build` なので版は `version` のまま(`+dev` が付かない)
- 起動中の exe は上書きできないので、deploy は最初に止める。aibow は名前で、aibow-server と aibow-mcp はインストール先から起動しているものだけを止める(`stop-tools`。`go run` や worktree で動かしているものを巻き込まない)
- バージョンを手で config.yml などに書かない。`_cmd/aibow/version` だけを変え、`go run _cmd/version.go` で書き写す(deploy は自動で行う)
- `v`+今のバージョンのタグがある(= リリース済み)なら、deploy が patch を自動で上げる。minor / major は `go run _cmd/version.go -bump` で上げてから deploy するか main に push する
- `Taskfile.local.yml` とそのひな形・コピーはコミットしない(インストール先などローカルの設定をリポジトリに残さないため。ユーザーの方針)
- `Taskfile.local.yml` のタスクを、コミットする `Taskfile.yml` のタスクから呼ばない(持っていない環境で壊れる)
- deploy がビルド情報としてコミットするファイル(`Taskfile.local.yml` の `BUILD_INFO_FILES`)には `../aibow-mcp/version` と `../aibow-server/version` も入れる(入れないと、バージョンを上げたときに写しだけがコミットされずに残る)
- deploy を試すときは `DEPLOY_DEST` を一時フォルダにし、試しに作ったコミットは消しておく
- 手でタグを作って push しない(`release.yml` が動き、リリースの下書きができる)。`git push --tags` も、手元にだけあるタグを全部 push してしまうので使わない
