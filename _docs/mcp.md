# MCP(クライアント)

aibow を MCP(Model Context Protocol)のクライアントにして、既製の MCP サーバーのツールをモデルに使わせる仕組みの説明です。
仕様は [features/0006-mcp-client.md](features/0006-mcp-client.md)。ツール呼び出しとエージェントループそのものは [tools.md](tools.md)。

## MCP とは

ツールやデータを AI アプリにつなぐための決まりごと。中身は JSON-RPC 2.0 のメッセージのやり取りです。
ローカルでは、**クライアント(aibow)がサーバーを子プロセスとして起動し、標準入出力で1行ずつ JSON を送り合います**(stdio のトランスポート)。
標準出力は MCP のメッセージ専用で、サーバーのログはエラー出力に出す決まりです。

```
aibow ── 起動する(npx …)──→ MCP のサーバー(別のプロセス)
aibow ── tools/list ──────→ ツールの定義(名前・説明・inputSchema)が返る → tool.Tool にしてモデルに見せる
aibow ── tools/call ──────→ 結果(content)が返る → ツールの結果としてモデルに渡す
```

ツールの定義(`name` / `description` / `inputSchema`)は、0004 の `tool.Def`(名前・説明・引数の JSON Schema)とほぼ同じ形です。
なので、遠くのツールも `tool.Tool` として見せれば、エージェントループ(`agent.go`)は何も変えずに使えます。
**モデルから見ると、組み込みのツールと MCP のツールに違いはありません**。違うのは、実行がどこで起きるかだけです。

## ファイル

| ファイル | 役割 |
|---|---|
| `mcpclient/mcpclient.go` | サーバーの起動のしかた(`ServerConfig`)と、サーバーを起動・停止してツールを渡す係(`Manager`)。子プロセスの後片付け(`treeTransport`) |
| `mcpclient/remote.go` | サーバーのツールを `tool.Tool` として見せる(`remoteTool`)。`tools/call` の結果を文章にする・長い結果を切る |
| `mcpclient/log.go` | 送受信したメッセージとサーバーのエラー出力を、1行ずつログに出す |
| `mcpclient/mcpclient_test.go` | テスト。SDK でテスト用のサーバーを作り、プロセスを起動せずにつなぐ(`NewInMemoryTransports`)。本物の子プロセスは、テストの実行ファイル自身をサーバーとして起動して確かめる |
| `settingsfile.go` | 設定ファイル `settings.json` の読み込み(`mcpServers`) |
| `agent.go` | `RemoteTools`(会話の仕組みから見た MCP の口)・送信の前にサーバーを起動する(`startRemote`)・組み込みのツールの後ろに並べる(`toolsFor`) |
| `internal/proc/` | 子プロセスまでまとめて止める仕組み(`run_command` と共有) |

- `mcpclient` は `tool` を import し、ルートの `aibow`(LLM・会話の仕組み)は import しない。会話の仕組みは MCP を知らず、`RemoteTools` の interface だけを見る
- やり取りは公式の Go SDK(`github.com/modelcontextprotocol/go-sdk`)に任せている。SDK のパッケージ名が `mcp` なので、こちらは `mcpclient`
- SDK が引っ張ってくる間接の依存:`google/jsonschema-go`・`segmentio/encoding`・`yosida95/uritemplate`・`golang.org/x/oauth2`・`golang.org/x/sync`・`golang.org/x/time`。どれも cgo なし(`CGO_ENABLED=0` でビルドできる)

## 設定ファイル

設定の置き場所は2つに分けています。

| 置き場所 | 中身 |
|---|---|
| DB(`app_settings` / `conversations.settings`) | 画面で変える設定 |
| `settings.json`(`%APPDATA%\aibow\settings.json`、`AIBOW_SETTINGS` で変えられる) | 手で書く設定・その PC でどう起動するかの設定。今は `mcpServers` だけ |

MCP のサーバーの起動のしかた(`npx` のパスなど)は「その PC の設定」で、会話のデータとは性格が違うので、DB ではなくファイルにしています。

- 形は多くのツールで使われている `mcpServers` に合わせています(`command` / `args` / `env`)。`type` / `url` は読みますが、stdio 以外(`http`)はエラーです
- 読むのは起動したときだけ。ファイルが無ければ MCP を使わず、書き間違いはトースト(何行目か・どのサーバーか)で伝えて MCP を使わずに起動します

## サーバーの起動と終了

- **作業フォルダは起動の引数で渡す**:`args` か `env` の中の `${workdir}` を会話の作業フォルダに置き換え、プロセスのカレントフォルダも作業フォルダにします
  - MCP の Roots(クライアントがフォルダを伝える仕組み)は 2026-07-28 版で非推奨になり、「設定かツールの引数で渡す」のが新しいやり方のため
  - そのため、`${workdir}` を使うサーバーは**作業フォルダごとに1つ**起動します。使わないサーバーは1つを全部の会話で共有します。作業フォルダを決めていない会話には、`${workdir}` を使うサーバーを出しません
  - **Roots には対応していないと名乗る**(空の `ClientCapabilities`)。SDK の既定は昔からの名残で「対応している」と名乗りますが、filesystem サーバーは、クライアントが Roots に対応していると Roots のフォルダを引数のフォルダの代わりに使います。
    名乗らないと、サーバーのログに「Client does not support MCP Roots, using allowed directories set from server args」と出ます
- **起動するのは、要る最初の送信のとき**(`Send` の最初。そのツールも会話の上限に数えるので、上限を確かめる前)。アプリの起動は遅くしません。
  画面のメーターの計算(`toolsFor`)は起動済みのサーバーのツールだけを見て、起動はしません。なので、最初の送信の前のメーターには MCP のツールが入っていません
- 起動に失敗したら、トーストに出して、そのサーバーのツール無しで送信を続けます。落ちたら(つながりが切れたら)一覧から外し、次の送信で起動し直します
- 起動の上限は 60 秒(初回の `npx` はパッケージをダウンロードするため)
- **閉じるとき**:SDK の `CommandTransport` が MCP の決まりどおり「標準入力を閉じる → 待つ(2 秒)→ SIGTERM → 止める」を行います。
  ただし Windows では起動したプロセス(`npx.cmd` を動かす cmd.exe)しか止めず、その子の node が残ります。
  そこで起動した直後にジョブオブジェクトに入れ(`internal/proc`)、閉じた後にジョブごと止めます(`treeTransport`)
- Windows では `exec.Command("npx", …)` で `npx.cmd` をそのまま起動できました(Go の `LookPath` が `PATHEXT` で `.cmd` を見つける)。filesystem サーバーの説明にある `cmd /c npx …` は要りません
- コンソールの窓は出しません(`CREATE_NO_WINDOW`。`proc.Prepare`)

## ツール

- 組み込みのツールの後ろに、サーバーの名前の順に並べます(毎回同じ順にする。プロンプトが変わると KV キャッシュが効かなくなるため)
- **組み込みのツールと名前が重なったツールは出しません**(同じ名前が2つあると、モデルも aibow も区別できない)。filesystem サーバーでは `read_file`・`write_file`・`edit_file` が重なります。
  どう扱うかは 0006 の未決事項。重なったものは起動したときにログに出します
- `inputSchema` はそのまま定義の引数にします。ただし SDK は `inputSchema` を `map[string]any` で渡してくるので、サーバーが書いた順は残らず、JSON にし直すとキーの名前の順になります
- 引数は Qwen3.5 の書式で文字列として届くので、`tool.CheckArgs` が JSON Schema の型で変換します(真偽値・配列・オブジェクトも。[tools.md](tools.md))
- **全部、実行前に確認します**(`tool.Confirmable`)。中身が見えないので差分は作れず、確認のカードにはサーバーの名前と、渡す引数(JSON)を出します。
  サーバーは「読むだけ」(`readOnlyHint`)などの印を付けられますが、MCP の仕様でも信じてはいけないヒントとされています。信頼して確認を省く仕組みは TODO.md のロードマップの外
- 結果の `content` のうち文章をつなぎます。画像などは「(画像 image/png は渡せません)」のように種類だけを書きます。`isError` はエラーの結果にします
- 結果は 8,000 文字で切ります(組み込みのツールは自分で切るが、MCP のサーバーは切らずに返してくることがある)
- 時間の上限は 60 秒(`tool.TimeLimiter`)。時間切れ・停止のときは、SDK が取り消しの知らせ(`notifications/cancelled`)を送ってすぐに返ります
- 結果には、どのサーバーのツールかを保存します(`ToolResult.Server`)。画面のカードは、名前の前にサーバーの名前を小さく出します

## 版の取り決め(実際のやり取り)

2026-07-28 版の MCP はステートレスになりました。最初の挨拶(`initialize` / `notifications/initialized`)が無くなり、
毎回のリクエストが、版と自分の能力を `_meta` に入れて送ります(LLM が「覚えていないので毎回全部渡す」のと同じ考え方)。
前もって確かめたいときは `server/discover` を呼びます。SDK は新しい版で話しかけ、だめなら古い版の挨拶に戻ります。

filesystem サーバー(2026-10 の `npx -y @modelcontextprotocol/server-filesystem`)は古い 2025-11-25 版で話しました。ログはこうなります。

```
MCP filesystem → {"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/clientCapabilities":{},…,"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}
MCP filesystem ← {"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"Method not found"}}
MCP filesystem → {"jsonrpc":"2.0","id":2,"method":"initialize","params":{"clientInfo":{"name":"aibow",…},"protocolVersion":"2025-11-25","capabilities":{}}}
MCP filesystem ← {"jsonrpc":"2.0","id":2,"result":{"protocolVersion":"2025-11-25","capabilities":{"tools":{"listChanged":true}},"serverInfo":{"name":"secure-filesystem-server","version":"1.0.0"}}}
MCP filesystem → {"jsonrpc":"2.0","method":"notifications/initialized","params":{}}
MCP filesystem → {"jsonrpc":"2.0","id":3,"method":"tools/list","params":{}}
MCP filesystem ← {"jsonrpc":"2.0","id":3,"result":{"tools":[{"name":"read_file",…
MCP filesystem: つながりました(10.6 秒。MCP の版 2025-11-25・サーバー secure-filesystem-server 1.0.0・ツール 14 個: …)
```

SDK で作ったサーバー(テストのもの)とは 2026-07-28 版で話し、`server/discover` の次にすぐ `tools/list` が続きます(`go test -v -run TestCommandServer ./mcpclient` で見られます)。

## ログ

- 起動(コマンド)・つながったとき(かかった秒数・版・サーバーの名前・ツールの数と名前)・止めたとき
- 送受信した JSON-RPC のメッセージ(`→` が aibow から、`←` がサーバーから。1行 400 文字まで)。SDK の `LoggingTransport` を使っている
- サーバーのエラー出力(`MCP <名前> (stderr) …`)
- MCP のツールの定義で、プロンプトが何トークン長くなるか(`ツール: 組み込み N 個 + MCP M 個。…約 K トークン長くなります`)と、名前が重なって出さなかったツール
