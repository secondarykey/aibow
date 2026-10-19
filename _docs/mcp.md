# MCP(クライアントとサーバー)

aibow を MCP(Model Context Protocol)のクライアントにして、既製の MCP サーバーのツールをモデルに使わせる仕組みと、
aibow のツールを MCP のサーバーとして出す仕組み(自作の MCP サーバー。末尾の「自作の MCP サーバー」)の説明です。
仕様は [features/0006-mcp-client.md](features/0006-mcp-client.md)(クライアント)と [features/0007-mcp-server.md](features/0007-mcp-server.md)(サーバー)。
ツール呼び出しとエージェントループそのものは [tools.md](tools.md)。

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
| `mcp/client/client.go` | サーバーの起動のしかた(`ServerConfig`)と、サーバーを起動・停止してツールを渡す係(`Manager`)。子プロセスの後片付け(`treeTransport`) |
| `mcp/client/remote.go` | サーバーのツールを `tool.Tool` として見せる(`remoteTool`)。`tools/call` の結果を文章にする・長い結果を切る |
| `mcp/client/log.go` | 送受信したメッセージとサーバーのエラー出力を、1行ずつログに出す |
| `mcp/client/client_test.go` | テスト。SDK でテスト用のサーバーを作り、プロセスを起動せずにつなぐ(`NewInMemoryTransports`)。本物の子プロセスは、テストの実行ファイル自身をサーバーとして起動して確かめる |
| `settingsfile.go` | 設定ファイル `settings.json` の読み込み(`mcpServers`) |
| `toolprefs.go` | ツールの扱い(`ToolPolicy`: 使わない / 確認する / 確認しない、会話の上書きだけの「使う」)と、その PC の既定 `tools.json`(`ToolPrefs`) |
| `agent.go` | `RemoteTools`(会話の仕組みから見た MCP の口)・送信の前にサーバーを起動する(`startRemote`)・組み込みのツールの後ろに並べる(`toolsFor`)・ツールの扱いを決める(`toolPolicy` / `policyOf` / `needsConfirm`)・会話の設定の画面に出す扱い(`ToolStates`)・ツールの一覧を覚える(`rememberTools`) |
| `internal/proc/` | 子プロセスまでまとめて止める仕組み(`run_command` と共有) |

- `mcp/client` は `tool` を import し、ルートの `aibow`(LLM・会話の仕組み)は import しない。会話の仕組みは MCP を知らず、`RemoteTools` の interface だけを見る
- やり取りは公式の Go SDK(`github.com/modelcontextprotocol/go-sdk`)に任せている。SDK のパッケージ名が `mcp` なので、こちらは `mcp/` の下に置いてパッケージ名を `client` にしている(ステップ5で作るサーバーは `mcp/server/` に並べる)
- SDK が引っ張ってくる間接の依存:`google/jsonschema-go`・`segmentio/encoding`・`yosida95/uritemplate`・`golang.org/x/oauth2`・`golang.org/x/sync`・`golang.org/x/time`。どれも cgo なし(`CGO_ENABLED=0` でビルドできる)

## 設定ファイル

設定の置き場所は3つに分けています。

| 置き場所 | 書くのは | 中身 | 会話をサーバーに置く形にしたら |
|---|---|---|---|
| DB(`app_settings` / `conversations.settings`) | aibow | 会話と、画面で変える設定。会話ごとに使う MCP のサーバー・会話だけのツールの扱いの上書きもここ | サーバーへ |
| `settings.json`(`%APPDATA%\aibow\settings.json`、`AIBOW_SETTINGS`) | 人が手で | その PC でどう起動するか。今は `mcpServers` だけ | 手元に残る |
| `tools.json`(`%APPDATA%\aibow\tools.json`、`AIBOW_TOOLS`) | aibow(画面で変えたもの) | ツールの扱いの PC の既定と、サーバーを起動したときに見えたツールの一覧 | 手元に残る |
| `window.json`(`%APPDATA%\aibow\window.json`、`AIBOW_WINDOW`) | aibow(閉じるとき) | ウィンドウの位置・大きさ・最大化([ui.md](ui.md) の「次の起動で戻すもの」) | 手元に残る |

MCP のサーバーの起動のしかた(`npx` のパスなど)は「その PC の設定」で、会話のデータとは性格が違うので、DB ではなくファイルにしています。

- 形は多くのツールで使われている `mcpServers` に合わせています(`command` / `args` / `env`、`url` / `headers`)。`type` は書かなくてよく(`command` か `url` で決まる)、書いてあれば確かめます。
  古い HTTP+SSE のトランスポート(`type: sse`。2025-03-26 版から非推奨)には対応しません。OAuth(ブラウザでのログイン)には、まだ対応していません(TODO.md のロードマップの外)
- 読むのは起動したときだけ。ファイルが無ければ MCP を使わず、書き間違いはトースト(何行目か・どのサーバーか)で伝えて MCP を使わずに起動します

## 会話ごとに使うサーバー

ツールの説明は毎回プロンプトに入るので、全部のサーバーを載せると会話の上限を食い尽くします
(filesystem と Blender を両方つなぐと、説明だけで約 22,700 文字。会話の上限 8,192 では会話ができなくなった)。
そこで、会話の設定(DB)に「使う MCP のサーバー」(`mcp_servers`)を持たせ、会話ごとに選びます。

- **新しい会話では、選んだプロファイルの「使うツールのグループ」のサーバーを最初から選ぶ**(設定の画面のプロファイルの「ツール」。`Conversations.Defaults`・0014)。
  前は設定の画面の「ツール」のグループのチェック(`tools.json` の `notDefault`)で決めていて、今はプロファイル「既定」への引き継ぎのためだけに読む。会話ごとに、入力欄の上の「この会話のツール」から外せる。最初は「新しい会話の既定は使わない」にしていたが、「ツール」で使うにしたのに新しい会話で動かないのが分かりにくかった(ユーザーの報告)
- **settings.json に足したばかりのサーバーは、新しい会話で最初から使わない**(2026-10-15、ユーザーの指示)。足しただけで全部の新しい会話にツールの説明が入り、会話の上限を食うのを防ぐため。
  `tools.json` の `seen`(aibow がすでに見たサーバー)に無いサーバーを、起動したときに `notDefault` に入れる(`ToolPrefs.NoteServers`)。画面のお知らせとログでも伝える。最初から使うかは、プロファイルの「ツール」で決める。
  `seen` の無い前の `tools.json` なら、今あるサーバーは全部見たことにする(それまで最初から使っていたものを急に外さない)。settings.json から消しても `seen` には残す(戻したときに前の扱いのまま)
- 選んでいないサーバーはモデルに見せず、起動もしない(`Manager.Start` / `Tools` に、会話で選んだサーバーの名前を渡す)
- 画面:入力欄の上、作業フォルダの隣の「この会話のツール」(レンチのアイコン。`ToolBar.tsx`)。押すと一覧が上に開き、組み込みのツールと MCP のサーバーをオン・オフする。組み込みのツールを外した会話は、会話の設定の `builtin_off` が true になる
- settings.json から消したサーバーの名前が会話に残っていても、無視する

## ツールの扱い(使わない / 確認する / 確認しない)

ツールの扱いは2つの層で決めます。決めるのは `Conversations.toolPolicy` の1か所だけです。
MCP のツールだけでなく、組み込みのツール(aibow の中で動くツール。`write_file` など)も同じ仕組みで扱います。

ツールは**グループ**に分かれます:**ローカル**(`tools.json` の `local`)と、**MCP のサーバーごと**(`servers`)。

0. **プロファイルで使えない**(会話のプロファイルの決まり `rules.off`。0014)。グループごと(組み込み・MCP のサーバー)とツールごと。会話の上書きでも使えるようにはできず、
   使えないサーバーは選んであっても起動しない(`usableServers`)。プロファイルはそのつど読むので、あとから使えないにしても今ある会話にすぐ効く
1. **会話ごとの上書き**(会話の設定の `tool_policies`。DB)。会話を消せば一緒に消える
2. **PC の既定**(`tools.json`)。ツールごと(`tools`)→ グループごと(`builtin` / `servers`。確認する・確認しないを決める。後日の「サーバーごとにまとめて許可」のため)、の順に見る
3. どれも決めていなければ:MCP のツールは「確認する」。組み込みのツールは、書き換えるツール・コマンドは「確認する」、読むだけのツールは「確認しない」(今までの動き)

扱いを決めるのは `policyOf`(`toolPolicy` の中身)。ツールの実体が無くても(起動していない MCP のサーバーのツールでも)決められるよう、グループ・名前・書き換えるツールかで受け取り、どこで決まったか(プロファイル / 会話 / PC / 決まりの既定)も返す。
会話の設定の画面は、これを `ToolStates` で受け取って出す(画面で同じ判断を書き直さない。0009)

| 値 | 意味 |
|---|---|
| `off`(使わない) | モデルに見せない。説明もプロンプトに入らないので、会話の上限の節約になる。**PC の既定の `off` は「既定では使わない」の意味で、禁止ではない**(会話で `on` にすれば使える) |
| `on`(使う) | **会話の上書きだけ**で使う値(`tools.json` には書けない)。PC の既定の `off` を打ち消して、この会話では使う。確認するかは変えず、PC の既定(ツールごと → グループごと。`off` は飛ばす。`ToolPrefs.confirmPolicy`)→ 決まりの既定で決める(2026-10-15、ユーザーと決めた) |
| `ask`(確認する) | 実行する前に確認のカードを出す(MCP のツールの決まりの既定)。読むだけのツールを「確認する」にしたときは、カードに引数を出す(`argsPreview`) |
| `allow`(確認しない) | 聞かずに実行する(ログに「聞かずに実行します」と出る)。設定の画面のプロファイルの「ツール」(この PC の既定。ツールごと・グループごとまとめて。どのプロファイルでも同じ)と、会話の設定の画面の「ツール」(この会話だけ)で選ぶ(0010) |

- PC の既定を会話の DB に入れないのは、ツールは手元で動くことが多く、「聞かずに動かしてよいか」はその PC を使う人が決めることだから。
  会話をサーバーに置く形(TODO.md のクライアント/サーバー構成の B)にしても、`tools.json` は手元に残る
- 例:PC の既定で `write_file` は「確認する」、会話A だけ「確認しない」に上書き → 会話A では聞かず、会話B では聞く(`remote_test.go` の `TestToolPolicyInConversations`)
- 画面で変えられるのは「使う / 使わない」だけ。新しい会話で最初は使わないツールはプロファイルの値(`tools_off`。設定の画面のプロファイルの「ツール」の `ProfileTools.tsx`。会話を作るときに off の上書きとして写す。
  前は PC の既定 tools.json の off で、起動したときにプロファイルへ移す)、会話の上書きは会話の設定の画面の「ツール」(`ConversationTools.tsx`。`on` / `off`。既定と同じにしたら上書きを消す)。
  グループの見出しにチェックは置かない(新しい会話で最初から使うグループはプロファイルで選ぶ。0014)。グループは畳んで出し、▶ で開ける。ツールごとのチェックと「すべて使う / すべて使わない」もある。
  Blender の `…_for_cli`(13 個)を使わないにすると、Blender の説明はほぼ半分になる
- 会話では、「ツール」のチェックにかかわらず、組み込みと settings.json の MCP のサーバーを全部選べる(入力欄の上の「この会話のツール」)。最初はグループのチェックを外すと「どの会話でも使えない」にしていたが、会話で選んでいたものまで消えて分かりにくかったので変えた(ユーザーの報告)。前の画面で書いたグループの `off` は、読むときに `notDefault` に移す(`moveGroupOff`)
- PC の既定でツールを「使わない」にしても、会話の設定の画面でツールごとに「使う」にできる(禁止する仕組みは無い。0009)
- `tools.json` は、画面で変えたものを aibow が書く(一時ファイルに書いてから名前を変える)。壊れていたら、お知らせで伝えて決まりの既定で動き、ファイルは書き換えない
- ツールの一覧(`known`)は、サーバーを起動するたびに置き換える。一覧がまだ無いサーバーは、設定の画面を開いたときに**一時的に起動して一覧だけ受け取り、すぐ止める**(`Manager.ListTools` / `Conversations.LoadToolList`。会話で使う前に「使わない」を決められるように)。`${workdir}` を使うサーバーには空の一時フォルダを渡し、終わったら消す。動いているサーバーの一覧には入れない。設定の画面の読み直すボタンでも受け取り直せる(filesystem 14 個・Blender 26 個とも約 2 秒)
- settings.json からサーバーを消しても、`tools.json` のそのサーバーの扱いは残す(戻したときに設定し直さなくてよいように)。設定の画面には settings.json にあるサーバーだけを出す
- ツールの名前は、組み込みのツールはそのまま(`write_file`)、MCP のツールはサーバーの名前が前に付く(`Blender__render`)ので、`tools.json` の `tools` の中で重ならない
- 確認の答え(`Confirmation`)には、これから先の扱い(`Remember`。`ToolPolicy` の値。空ならこの1回だけ)を持たせられる。答えたときに会話の上書きに書く(`Conversations.SetToolPolicy`)。
  ツールの扱いは呼ぶたびに会話の設定を読み直して決めるので、送信の途中でも次の呼び出しから効く。確認のカードの「この会話では今後聞かない」(allow)・「今後許可しない」(off)がこれを使う

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
- **ツールの名前には、サーバーの名前を前に付けます**(`filesystem__read_file`、`Blender__get_objects_summary`。`mcp/client` の `ToolName`)。
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
- **全部、実行前に確認します**(`tool.Confirmable`。扱いが「確認しない」なら聞かない)。中身が見えないので差分は作れず、確認のカードにはサーバーの名前と、渡す引数(JSON)を出します。
- **サーバーからの確認(MRTR)にも答えます**(0007 の 5-3。下の「サーバーからの確認」)。aibow-mcp の書き換えるツールは、実行の途中で差分を送ってきて確かめます
  サーバーは「読むだけ」(`readOnlyHint`)などの印を付けられますが、MCP の仕様でも信じてはいけないヒントとされています。信頼して確認を省く仕組みは TODO.md のロードマップの外
- 結果の `content` のうち文章をつなぎます。画像などは「(画像 image/png は渡せません)」のように種類だけを書きます。`isError` はエラーの結果にします
- 結果は 8,000 文字で切ります(組み込みのツールは自分で切るが、MCP のサーバーは切らずに返してくることがある)
- 時間の上限は 60 秒(`tool.TimeLimiter`)。サーバーごとに `settings.json` の `callTimeout`(秒)で変えられます(0007 の決定事項 8。自作の aibow-mcp の `run_command` は 120 秒まで動くため)。時間切れ・停止のときは、SDK が取り消しの知らせ(`notifications/cancelled`)を送ってすぐに返ります
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

SDK で作ったサーバー(テストのもの)とは 2026-07-28 版で話し、`server/discover` の次にすぐ `tools/list` が続きます(`go test -v -run TestCommandServer ./mcp/client` で見られます)。

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

## 自作の MCP サーバー(aibow-mcp。ステップ5)

aibow のツール(`tool` パッケージ。時刻・計算・ファイルの読み書き・コマンド)を MCP のサーバーとして出します。
上のクライアントの反対側で、`tools/list` に定義の一覧を、`tools/call` に実行の結果を返します。

```
MCP のクライアント(Claude Code・aibow など)── 起動する(aibow-mcp <作業フォルダ>)──→ aibow-mcp
                                          ── tools/list ──→ tool のツールの定義(名前・説明・引数は aibow の組み込みと同じ)
                                          ── tools/call ──→ tool のツールを実行した結果
```

### ファイル

| ファイル | 役割 |
|---|---|
| `mcp/server/server.go` | サーバーの中身(`server.New`)。`tool` のツールを SDK の `Server.AddTool` に渡し、`tools/call` を `tool.Tool.Run` につなぐ |
| `mcp/server/confirm.go` | 書き換えるツールを実行する前に、サーバーの側からユーザーに確かめる(MRTR。`confirmed`)。差分を `_meta` と `message` に入れ、送り直しで答えと合言葉(`requestState`)を確かめる |
| `mcp/server/server_test.go` / `confirm_test.go` | テスト。プロセスを起動せずに(`NewInMemoryTransports`)SDK のクライアントでつなぎ、一覧・呼び出し・エラー・確認(受け入れ・拒否・確認に対応していないクライアント・確認のあとでファイルが変わった)・停止でコマンドの子プロセスまで止まることを確かめる |
| `mcp/internal/meta/` | クライアントとサーバーの両方で使う `_meta` の名前(`aibow/preview`) |
| `mcp/client/local.go` | aibow の中で自作サーバーを動かし、組み込みのツールを MCP 越しに渡す(`Local`。下の「aibow の中で動かす」) |
| `_cmd/aibow-mcp/main.go` | 標準入出力のサーバーの exe。引数(作業フォルダ)を読んで `server.New` を `StdioTransport` で動かすだけ |

- 中身を `package main` に書かないのは、テストでプロセスを起動せずにつなぐため・aibow の中でメモリ内のつなぎ方で動かすため(5-4)
- `mcp/server` は `tool` と SDK だけを使い、LLM・会話の仕組み・画面を import しない
- 画面のアプリ(`_cmd/aibow`)と別の exe にしているのは、Windows の画面のアプリは標準入出力を持たない GUI のプログラムで、標準入出力で話すサーバーに向かないため

### 作り

- **ツールの定義は組み込みとまったく同じ**(`tool.Def` の名前・説明・引数の JSON Schema をそのまま `inputSchema` にする)。自分で作るサーバーなので、4B が書きやすい形のまま出せる
- `tools/list` の並びは SDK が名前の順にする(ここで足した順ではない)
- **読むだけの印**:確認の要らないツール(`tool.Confirmable` でないもの)に `readOnlyHint` を付ける。クライアントへのヒントで、MCP の仕様でも「信じてはいけない」とされている。aibow はまだ信じない(0007 の決定事項 9)
- **引数**:MCP では引数が JSON の値(数は数、真偽値は真偽値)で届くが、確かめるのは `tool.CheckArgs` に任せる(知らない引数・足りない引数・型の違いを、組み込みと同じ文で返す)。
  `CheckArgs` は Qwen3.5 の書式に合わせて「値は文字列で届く」前提なので、JSON の値をいったん文字列に戻して渡す(文字列はそのまま、ほかは JSON の書き方のまま)。`null` は書かなかったのと同じ
- **ツールの失敗は `isError` の結果で返す**(Go の error にしない)。MCP では、error は呼び出しそのものが成り立たなかったとき(知らないツールなど)に使い、
  ツールの失敗(ファイルが無い、など)は結果の中身としてモデルに読ませて立て直させる決まり(aibow の「エラー: …」と同じ考え方)
- **時間の上限**:サーバーの側でも、組み込みと同じ上限(10 秒。`run_command` は 120 秒)で止める。Claude Code などほかのクライアントがどうするかは分からないため
- **停止**:クライアントが止めると、SDK が `notifications/cancelled` を送り、サーバーはそのリクエストの処理の context を止める(SDK の `jsonrpc2.Connection.Cancel`)。ツールは ctx を見て後片付けをして返す
- **確認の要るツール**(`write_file`・`edit_file`・`run_command`)は、実行する前にサーバーの側からユーザーに確かめる(下の「サーバーからの確認」)。
  確認(elicitation)に対応していないクライアントには、**書き換えずに**「このクライアントは実行前の確認(elicitation)に対応していないため、書き換えません」を `isError` で返す(0007 の決定事項 6)
- **Environment の説明**:`run_command` の説明は「Environment の節に書いたシェルで動かす」と言っている。aibow はシステムプロンプトにその節を書くが、
  ほかのクライアントは書かないので、作業フォルダとシェルをサーバーの `instructions`(つないだときにクライアントに渡す説明)に入れる
- **ログ**は標準エラー出力と、アプリと同じフォルダのファイル(`%APPDATA%\aibow\logs\aibow-mcp_<日付>.log`。`AIBOW_LOG_DIR`)に書く(`_cmd/aibow-mcp/log.go`。形はアプリの `startLog` にならう)。
  標準出力に何か書くと、クライアントが MCP のメッセージとして読もうとして壊れる。
  起動(版・作業フォルダ・ツール)は Notice、ツールの実行(名前・秒数・結果の文字数・失敗の理由)は Info。aibow から起動すると、aibow のログにも `MCP <名前> (stderr) …` として入る
  (ただし aibow の exe は Notice 以上しかファイルに残さないので、サーバーの行はサーバー自身のファイルで見る)。
  作業フォルダごとに複数のプロセスが同じ日のファイルに書くので、各行の終わりに `pid=…` を付けている。異常終了は `aibow-mcp_crash.log` に残る
- 版(`serverInfo.version`・起動のログ・`aibow-mcp -version`)はアプリと同じ。`_cmd/aibow/version` の写しの `_cmd/aibow-mcp/version` を埋め込む(`go run _cmd/version.go` が書き写す)。
  exe(`go build`・`go install`)は `version` のまま、`go run` で起動したものは patch を1つ上げて `+dev`(`0.5.1` なら `0.5.2+dev`。`appversion.go`)。
  アプリはビルドタグ(`production`)で見分けるが、aibow-mcp は Taskfile を通さずに作るので、自分の exe が `go-build…` の一時フォルダにあるか(= `go run`)で見分ける

### 実際のやり取り

aibow のクライアント(`mcp/client`)からは、新しい 2026-07-28 版で話します(挨拶なし。`server/discover` の次にすぐ `tools/list`)。つながるまで約 0.1 秒でした。

```
MCP aibow → {"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{…,"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}
MCP aibow ← {"jsonrpc":"2.0","id":1,"result":{"resultType":"complete","_meta":{"io.modelcontextprotocol/serverInfo":{"name":"aibow-mcp",…}},…
MCP aibow → {"jsonrpc":"2.0","id":2,"method":"tools/list",…
MCP aibow: つながりました(0.1 秒。MCP の版 2026-07-28・サーバー aibow-mcp …・ツール 7 個: aibow__calculate, aibow__edit_file, …)
MCP aibow → {"jsonrpc":"2.0","id":3,"method":"tools/call",…
MCP aibow (stderr) … [INFO  ] ツール read_file: 実行しました(0.00 秒・結果 15 文字)
```

古い版のクライアント(`initialize` で始めるもの)とも話せます。ターミナルから1行ずつ JSON を流しても確かめられます(Git Bash)。

```bash
{ printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"bash","version":"0"}}}' '{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}' '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"read_file","arguments":{"path":"README.md"}}}'; sleep 1; } | aibow-mcp .
```

標準入力が閉じると、サーバーは「クライアントが去った」とみなして、答えを書く前に終わります。`sleep 1` は、答えが返るまで標準入力を開けておくためです。

SDK はリクエストを並行に処理するので、続けて送ったリクエストの答えが、送った順と入れ替わって返ることがあります(id で対応を取る。JSON-RPC の決まり)。

### サーバーからの確認(MRTR。0007 の 5-3)

組み込みの書き換えるツールは、実行する前に aibow が差分を見せて確かめている([tools.md](tools.md))。サーバーに移すとクライアントからは中身が見えないので、
**サーバーが差分を作り、実行の途中で「これでよいか」をクライアントに聞く**。2026-07-28 版の MCP の MRTR(Multi Round-Trip Requests)を使う。

```
aibow → tools/call write_file(引数)
aibow ← resultType: "input_required"・inputRequests {"confirm": elicitation(message・_meta の aibow/preview に差分・フォームは「拒否の理由」だけ)}・requestState(合言葉)
        確認のカードを出して、ユーザーの答えを待つ
aibow → tools/call write_file(同じ引数 + inputResponses {"confirm": {action: accept / decline, content: {reason}}}・requestState)
aibow ← 結果(書いた / 拒否しました。理由: …)
```

- **サーバーは何も覚えない**(ステートレス)。送り直しでは同じハンドラがもう一度呼ばれるので、差分を作り直し、その SHA-256 が `requestState`(1回目に見せたもの)と同じかを確かめてから書く。
  違えば「確認のあとでファイルが変わったため書き換えませんでした」(見せた差分と違うことをしない)
- **古い版のクライアント**(Claude Code など、2026-07-28 版より前で話すもの)には、SDK がサーバーからのリクエスト(`elicitation/create`)で聞き、同じハンドラを答え付きで呼び直す。ハンドラの書き方は同じ。
  クライアントが確認に対応しているかは `req.ClientCapabilities()`(新しい版はリクエストの `_meta`、古い版は最初の挨拶)で見る
- 確認の内容は2つの形で送る:`message`(人が読む文。ほかのクライアントはこれを出す)と、`_meta` の `aibow/preview`(`tool.Preview`。aibow のクライアントは今の差分のカードで出す)
- 拒否の理由は、答えの `content` の `reason` で受け取る(MCP の決まりでは `content` は受け入れたときのものだが、aibow のクライアントは拒否でも入れて送る)

aibow のクライアントの側(`mcp/client/remote.go` の `RunInteractive`):

- **送り直しは SDK に任せず、自分で書く**(`ClientOptions.MultiRoundTrip.Disabled`)。SDK の自動の送り直しは1つの ctx の中で回るので、ユーザーがカードを読んでいる時間まで `callTimeout` に数えてしまう。
  サーバーとの1往復ごとに上限をかけ、確認を待つ時間は数えない
- 名乗る能力は、確認(`elicitation` のフォーム)だけ。古い版のサーバーがリクエスト(`elicitation/create`)で聞いてくる形には答えない(`ElicitationHandler` を渡していないので、SDK がエラーを返す)
- 聞くのは会話の仕組み(`tool.Interactive` の `RunInteractive` に、聞く関数 `tool.Ask` を渡す。`agent.go` の `askFor`)。ツールの扱いが「確認しない」なら聞かずに受け入れる。
  聞いたときは今の確認のカードを出し、ボタン(「この会話では今後聞かない」「今後許可しない」など)も今までどおり効く。カードに残す答えは、サーバーからの確認の答え
- `settings.json` のサーバー(`aibow-mcp.exe` も)は、呼ぶ前の確認(引数)も今までどおり出すので、確認が2回出る(サーバーの名乗りは偽れるので、名乗りでは信頼しない)。
  aibow の中で自作サーバーを動かす 5-4 では、呼ぶ前の確認を省いて1回にする
- サーバーが送ってきた差分は、サーバーが書いたもの。本当にそのとおりのことをするかは分からないので、信頼していないサーバーの差分をそのまま信じないこと
- ログ:`MCP <名前>: write_file がユーザーの確認を求めています(1 回目の往復。…)`・`確認の答え: accept`。サーバーの側は `ユーザーに確認を求めます(input_required …)`・`ユーザーが実行を認めました`

### aibow の中で動かす(組み込みのツール。0007 の 5-4)

aibow の組み込みのツール(時刻・計算・ファイル・コマンド)は、aibow の中で自作サーバーを動かし、**MCP 越しに**呼ぶ。

```
aibow [ 会話の仕組み ] ⇄ MCP(メモリ内のつなぎ方。SDK の NewInMemoryTransports) ⇄ aibow の中の自作サーバー(mcp/server) [ tool のツール ]
```

- `mcp/client/local.go` の `Local`。会話の仕組みには `LocalTools`(`Conversations.SetLocalTools`)として渡す。アプリが起動時に作り、閉じるときに止める
- サーバーは作業フォルダごとに1つ(作業フォルダは起動の引数で渡すため)。要る最初のときにつなぐ(メモリ内なので一瞬)
- **モデルと会話の仕組みからは、今までの組み込みのツールと同じに見せる**:名前に `aibow__` を付けない・グループは組み込み(`tools.json` の `builtin`、会話の `tool_policies` はそのまま効く)・扱いの決まりの既定も同じ(書き換えるツールとコマンドは確認する、ほかは確認しない)。
  `tool.Remote` にしないので、結果のカードにサーバーの名前は出ない
- **確認は1回**:書き換えるツールには「確認を自分で求める」印(`tool.SelfConfirming`)を付け、会話の仕組みは呼ぶ前に確認しない。サーバーが差分を送ってきたときに、今と同じ差分のカードで1回だけ聞く。
  aibow が自分で立てたサーバーなので、送ってきた差分を信頼してよい(`settings.json` のサーバーは、名乗りを偽れるので呼ぶ前の確認も残す)
- 時間の上限は、サーバーの上限(10 秒・`run_command` は 120 秒。`server.TimeLimit`)に 5 秒足したもの(サーバーの「上限を超えた」の結果を受け取るため)
- 送受信のメッセージは `MCP 組み込み → {…}` / `MCP 組み込み ← {…}` としてログに出る。サーバーのログ(`ツール write_file: ユーザーに確認を求めます` など)は同じプロセスなので、aibow のログにそのまま出る
- つなげなかったときは、ログに Error を出して `tool` パッケージのツールを直接呼ぶ(予備。この場合は今までどおり呼ぶ前に確認する)。`SetLocalTools` をしない(aibow をライブラリとして使う・テスト)ときも直接呼ぶ

