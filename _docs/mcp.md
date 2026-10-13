# MCP(クライアント)

aibow を MCP(Model Context Protocol)のクライアントにして、既製の MCP サーバーのツールをモデルに使わせる仕組みの説明です。
仕様は [features/0006-mcp-client.md](features/0006-mcp-client.md)。ツール呼び出しとエージェントループそのものは [tools.md](tools.md)。

## MCP とは

ツールやデータを AI アプリにつなぐための決まりごと。中身は JSON-RPC 2.0 のメッセージのやり取りです。
ローカルでは、**クライアント(aibow)がサーバーを子プロセスとして起動し、標準入出力で1行ずつ JSON を送り合います**(stdio のトランスポート)。
標準出力は MCP のメッセージ専用で、サーバーのログはエラー出力に出す決まりです。
どこか(イントラの中の別の PC など)で動いているサーバーには、同じ JSON を **HTTP の POST** で送ります(Streamable HTTP のトランスポート)。
どちらのつなぎ方でも、やり取りするメッセージ(`tools/list`・`tools/call` など)は同じです。違うのは運び方だけです。

| つなぎ方 | settings.json | サーバーを動かすのは | 作業フォルダ |
|---|---|---|---|
| stdio | `command`(と `args`・`env`) | aibow(子プロセスとして起動し、閉じたら止める) | `${workdir}` で渡せる |
| Streamable HTTP | `url`(と `headers`) | どこかで動いている(aibow は話しかけるだけ) | 渡せない(別の PC かもしれず、こちらのフォルダは見えない) |

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
| `toolprefs.go` | ツールの扱い(`ToolPolicy`: 使わない / 確認する / 確認しない)と、その PC の既定 `tools.json`(`ToolPrefs`) |
| `agent.go` | `RemoteTools`(会話の仕組みから見た MCP の口)・送信の前にサーバーを起動する(`startRemote`)・組み込みのツールの後ろに並べる(`toolsFor`)・ツールの扱いを決める(`toolPolicy` / `needsConfirm`)・ツールの一覧を覚える(`rememberTools`) |
| `internal/proc/` | 子プロセスまでまとめて止める仕組み(`run_command` と共有) |

- `mcpclient` は `tool` を import し、ルートの `aibow`(LLM・会話の仕組み)は import しない。会話の仕組みは MCP を知らず、`RemoteTools` の interface だけを見る
- やり取りは公式の Go SDK(`github.com/modelcontextprotocol/go-sdk`)に任せている。SDK のパッケージ名が `mcp` なので、こちらは `mcpclient`
- SDK が引っ張ってくる間接の依存:`google/jsonschema-go`・`segmentio/encoding`・`yosida95/uritemplate`・`golang.org/x/oauth2`・`golang.org/x/sync`・`golang.org/x/time`。どれも cgo なし(`CGO_ENABLED=0` でビルドできる)

## 設定ファイル

設定の置き場所は3つに分けています。

| 置き場所 | 書くのは | 中身 | 会話をサーバーに置く形にしたら |
|---|---|---|---|
| DB(`app_settings` / `conversations.settings`) | aibow | 会話と、画面で変える設定。会話ごとに使う MCP のサーバー・会話だけのツールの扱いの上書きもここ | サーバーへ |
| `settings.json`(`%APPDATA%\aibow\settings.json`、`AIBOW_SETTINGS`) | 人が手で | その PC でどう起動するか。今は `mcpServers` だけ | 手元に残る |
| `tools.json`(`%APPDATA%\aibow\tools.json`、`AIBOW_TOOLS`) | aibow(画面で変えたもの) | ツールの扱いの PC の既定と、サーバーを起動したときに見えたツールの一覧 | 手元に残る |

MCP のサーバーの起動のしかた(`npx` のパスなど)は「その PC の設定」で、会話のデータとは性格が違うので、DB ではなくファイルにしています。

- 形は多くのツールで使われている `mcpServers` に合わせています(`command` / `args` / `env`、`url` / `headers`)。`type` は書かなくてよく(`command` か `url` で決まる)、書いてあれば確かめます。
  古い HTTP+SSE のトランスポート(`type: sse`。2025-03-26 版から非推奨)には対応しません。OAuth(ブラウザでのログイン)には、まだ対応していません(TODO.md のロードマップの外)
- 読むのは起動したときだけ。ファイルが無ければ MCP を使わず、書き間違いはトースト(何行目か・どのサーバーか)で伝えて MCP を使わずに起動します

## 会話ごとに使うサーバー

ツールの説明は毎回プロンプトに入るので、全部のサーバーを載せると会話の上限を食い尽くします
(filesystem と Blender を両方つなぐと、説明だけで約 22,700 文字。会話の上限 8,192 では会話ができなくなった)。
そこで、会話の設定(DB)に「使う MCP のサーバー」(`mcp_servers`)を持たせ、会話ごとに選びます。

- **新しい会話の既定は「使わない」**。「新しい会話の既定の設定」に入るので、既定を変えれば引き継げる
- 選んでいないサーバーはモデルに見せず、起動もしない(`Manager.Start` / `Tools` に、会話で選んだサーバーの名前を渡す)
- 画面:入力欄の上、作業フォルダの隣の「MCP」の印(プラグのアイコン。`McpBar.tsx`)。押すとサーバーの一覧が上に開き、オン・オフする
- settings.json から消したサーバーの名前が会話に残っていても、無視する

## ツールの扱い(使わない / 確認する / 確認しない)

ツールの扱いは2つの層で決めます。決めるのは `Conversations.toolPolicy` の1か所だけです。
MCP のツールだけでなく、ローカルのツール(aibow の中で動くツール。`write_file` など)も同じ仕組みで扱います。

ツールは**グループ**に分かれます:**ローカル**(`tools.json` の `local`)と、**MCP のサーバーごと**(`servers`)。

1. **会話ごとの上書き**(会話の設定の `tool_policies`。DB)。会話を消せば一緒に消える
2. **PC の既定**(`tools.json`)。グループごと「使わない」なら、ツールごとの扱いにかかわらず使わない。そうでなければ、ツールごと(`tools`)→ グループごと、の順に見る
3. どれも決めていなければ:MCP のツールは「確認する」。ローカルのツールは、書き換えるツール・コマンドは「確認する」、読むだけのツールは「確認しない」(今までの動き)

| 値 | 意味 |
|---|---|
| `off`(使わない) | モデルに見せない。説明もプロンプトに入らないので、会話の上限の節約になる |
| `ask`(確認する) | 実行する前に確認のカードを出す(MCP のツールの決まりの既定)。読むだけのツールを「確認する」にしたときは、カードに引数を出す(`argsPreview`) |
| `allow`(確認しない) | 聞かずに実行する。**今は画面から選べない**(`tools.json` か会話の設定を手で書いたときだけ効く。ログに「聞かずに実行します」と出る) |

- PC の既定を会話の DB に入れないのは、ツールは手元で動くことが多く、「聞かずに動かしてよいか」はその PC を使う人が決めることだから。
  会話をサーバーに置く形(TODO.md のクライアント/サーバー構成の B)にしても、`tools.json` は手元に残る
- 例:PC の既定で `write_file` は「確認する」、会話A だけ「確認しない」に上書き → 会話A では聞かず、会話B では聞く(`remote_test.go` の `TestToolPolicyInConversations`)
- 画面で変えられるのは、PC の既定の「使う / 使わない」だけ(設定の画面の「ツール」。`ToolSettings.tsx`)。
  グループの「使う」を外すとグループごと使わない(一覧も畳む)。使うグループでは、ツールごとに選べ、「すべて使う / すべて使わない」もある。
  Blender の `…_for_cli`(13 個)を使わないにすると、Blender の説明はほぼ半分になる
- グループごと「使わない」にした MCP のサーバーは、入力欄の上のプラグの一覧にも出さず、会話で選んであっても起動しない(`usableServers`)
- `tools.json` は、画面で変えたものを aibow が書く(一時ファイルに書いてから名前を変える)。壊れていたら、お知らせで伝えて決まりの既定で動き、ファイルは書き換えない
- ツールの一覧(`known`)は、サーバーを起動するたびに置き換える。一覧がまだ無いサーバーは、設定の画面を開いたときに**一時的に起動して一覧だけ受け取り、すぐ止める**(`Manager.ListTools` / `Conversations.LoadToolList`。会話で使う前に「使わない」を決められるように)。`${workdir}` を使うサーバーには空の一時フォルダを渡し、終わったら消す。動いているサーバーの一覧には入れない。設定の画面の読み直すボタンでも受け取り直せる(filesystem 14 個・Blender 26 個とも約 2 秒)
- settings.json からサーバーを消しても、`tools.json` のそのサーバーの扱いは残す(戻したときに設定し直さなくてよいように)。設定の画面には settings.json にあるサーバーだけを出す
- ツールの名前は、ローカルのツールはそのまま(`write_file`)、MCP のツールはサーバーの名前が前に付く(`Blender__render`)ので、`tools.json` の `tools` の中で重ならない
- 「確認しない」を画面で選べるようにすること(確認のカードの「この会話では今後確認しない」「いつも確認しない」など)と、会話ごとの上書きを画面で変えることは後日

## サーバーの起動と終了

- **作業フォルダは起動の引数で渡す**:`args` か `env` の中の `${workdir}` を会話の作業フォルダに置き換え、プロセスのカレントフォルダも作業フォルダにします
  - MCP の Roots(クライアントがフォルダを伝える仕組み)は 2026-07-28 版で非推奨になり、「設定かツールの引数で渡す」のが新しいやり方のため
  - そのため、`${workdir}` を使うサーバーは**作業フォルダごとに1つ**起動します。使わないサーバーは1つを全部の会話で共有します。作業フォルダを決めていない会話には、`${workdir}` を使うサーバーを出しません
  - **Roots には対応していないと名乗る**(空の `ClientCapabilities`)。SDK の既定は昔からの名残で「対応している」と名乗りますが、filesystem サーバーは、クライアントが Roots に対応していると Roots のフォルダを引数のフォルダの代わりに使います。
    名乗らないと、サーバーのログに「Client does not support MCP Roots, using allowed directories set from server args」と出ます
- **起動するのは、要る最初の送信のとき**(`Send` の最初。そのツールも会話の上限に数えるので、上限を確かめる前)。アプリの起動は遅くしません。
  画面のメーターの計算(`toolsFor`)は起動済みのサーバーのツールだけを見て、起動はしません。なので、最初の送信の前のメーターには MCP のツールが入っていません
- 起動に失敗したら、トーストに出して、そのサーバーのツール無しで送信を続けます。落ちたら(つながりが切れたら)一覧から外し、次の送信で起動し直します
- 起動の上限は 60 秒(初回の `npx` はパッケージをダウンロードするため)。サーバーごとに `startupTimeout`(秒)で変えられます
- **起動できなかった理由**は、お知らせ(トースト)に出ます。サーバーがエラー出力に何か書いていれば、その最後の行を添えます(リリース版はログが見えないので、ログを見なくても原因が分かるように)。時間切れなら `startupTimeout` の延ばし方を書きます
- **閉じるとき**:SDK の `CommandTransport` が MCP の決まりどおり「標準入力を閉じる → 待つ(2 秒)→ SIGTERM → 止める」を行います。
  ただし Windows では起動したプロセス(`npx.cmd` を動かす cmd.exe)しか止めず、その子の node が残ります。
  そこで起動した直後にジョブオブジェクトに入れ(`internal/proc`)、閉じた後にジョブごと止めます(`treeTransport`)
- Windows では `exec.Command("npx", …)` で `npx.cmd` をそのまま起動できました(Go の `LookPath` が `PATHEXT` で `.cmd` を見つける)。filesystem サーバーの説明にある `cmd /c npx …` は要りません
- コンソールの窓は出しません(`CREATE_NO_WINDOW`。`proc.Prepare`)

## url のサーバー(Streamable HTTP)

- SDK の `StreamableClientTransport` を使います。aibow → サーバーのメッセージは1つずつ HTTP の POST で送り、返事はその POST の応答(JSON か、SSE で何回かに分けて)で返ってきます
- サーバーから好きなときに知らせを送るための、つなぎっぱなしの GET(standalone SSE)は開きません(ツールの一覧が変わった知らせなどは使わないため。つなぎっぱなしの接続を持たないほうが、イントラのプロキシなどで困りにくい)
- `headers` に書いたもの(API キーなど)を、毎回のリクエストに付けます(`headerTransport`)。ログには出しません。`settings.json` にはそのまま書かれるので、ファイルの扱いに気をつけてください。`http://` ではヘッダーも暗号化されずに流れます
- `${workdir}` は使えません(書くとエラー)。作業フォルダごとには分けず、1つのつながりを全部の会話で共有します
- **つながりが切れたことの見分け方**:stdio はプロセスが終わるとつながりが閉じますが、HTTP はリクエストのたびにつなぐので、ツールの呼び出しが失敗して初めて分かります。
  失敗したのが「サーバーが答えたエラー」(JSON-RPC のエラー。知らないツールなど)ならつながりは生きているのでそのまま、それ以外(つなげない・サーバーが落ちた)なら一覧から外して、次の送信でつなぎ直します。
  SDK は HTTP で送れなかったことも JSON-RPC のエラーの形(-32005 rejected by transport)で返すので、番号で見分けています(`answeredByServer`)

## ツール

- 組み込みのツールの後ろに、サーバーの名前の順に並べます(毎回同じ順にする。プロンプトが変わると KV キャッシュが効かなくなるため)
- **ツールの名前には、サーバーの名前を前に付けます**(`filesystem__read_file`、`Blender__get_objects_summary`。`mcpclient.ToolName`)。
  MCP の決まりごとには名前空間が無く、ツールの名前は「1つのサーバーの中で重ならない」ことしか決まっていません。モデルに見えるのはツールの名前の平らな一覧で、
  呼び出しも名前だけなので、サーバーが違っても名前が同じだと区別できません(filesystem サーバーの `read_file` と、組み込みの `read_file`)。
  そこでクライアントの側でサーバーの名前を前に付けて、名前空間の代わりにします(Claude Code の `mcp__<サーバー>__<ツール>` と同じ考え方)
  - 組み込みのツールには付けません。MCP のツールには、重なったときだけでなく**いつも**付けます(サーバーを足したら別のツールの名前が変わる、ということが起きないように)
  - 区切りは `__`。ステップ6の OpenAI 互換 API では、ツールの名前に使える文字が英数字・`_`・`-` に限られるので、`.` は使いません。サーバーの名前のそれ以外の文字は1文字ずつ `_` にします(`ServerPrefix`)。
    置き換えた結果がほかのサーバーと重なったら、`settings.json` を読むときにエラーにします
  - サーバーに `tools/call` で送るときは、前に付けた部分を外した元の名前を使います
  - 画面のカードは、サーバーの名前を枠で出し、ツールの名前からは前の部分を外して出します(マウスを乗せると、モデルに見せた名前が出る)
  - それでも組み込みのツールと名前が重なったら、MCP のほうを出さず、起動したときにログに出します
  - モデルが前の部分を落として書いたら(`list_directory`)、エラーの結果に「filesystem__list_directory のことですか?」を添えて、呼び直させます(`didYouMean`)。
    勝手に読み替えないのは、取り違えたことが見えなくなるため。名前空間を付ける前の会話の履歴に、前の部分の無い呼び出しが残っていると、4B はそれをまねした
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

## 実例: Claude の Blender の拡張を使う(uv の環境のぶつかり)

Claude アプリの Blender のコネクタは、拡張のフォルダで `uv run blender-mcp` を実行して、MCP サーバーを自分の子プロセスとして起動しています
(URL では待ち受けていない。Blender 自身が待ち受けている 9876 番は、アドオンの独自のやり方の入口で、MCP ではない)。
aibow から同じものを使うと、次のことが分かりました。

- `uv run --directory <拡張のフォルダ> blender-mcp` は、起動のたびにそのフォルダの環境(`.venv`)を作り直そうとする。
  Claude が起動したサーバーがその中のファイルを使っているので、「アクセスが拒否されました」(os error 5)・「別のプロセスが使用中です」(os error 32)で失敗して終わる
- `uvx --from <拡張のフォルダ> blender-mcp` は `uv.lock` を使わず最新の版を入れるので、`mcp` 2.x が入って動かない(`No module named 'mcp.server.fastmcp'`)
- **`env` の `UV_PROJECT_ENVIRONMENT` で環境を別のフォルダに作らせる**と、`uv.lock` の版のまま、Claude とぶつからずに起動できた(26 個のツール。2025-11-25 版で話す)

## ログ

- 起動(コマンド)・つながったとき(かかった秒数・版・サーバーの名前・ツールの数と名前)・止めたとき
- 送受信した JSON-RPC のメッセージ(`→` が aibow から、`←` がサーバーから。1行 400 文字まで)。SDK の `LoggingTransport` を使っている
- サーバーのエラー出力(`MCP <名前> (stderr) …`)
- MCP のツールの定義で、プロンプトが何トークン長くなるか(`ツール: 組み込み N 個 + MCP M 個。…約 K トークン長くなります`)と、名前が重なって出さなかったツール
