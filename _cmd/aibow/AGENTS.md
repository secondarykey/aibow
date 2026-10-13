# _cmd/aibow(デスクトップアプリ)

Wails v3 のデスクトップアプリです。ルートの `aibow` パッケージ(ライブラリ)を画面につなぎます。
画面そのもの(配置・操作・フォーカス・スクロール・ウィンドウ・IME)は [_docs/ui.md](../../_docs/ui.md)、
返事の表示(Markdown・コードの色付け・mermaid の図)は [_docs/rendering.md](../../_docs/rendering.md) にあります。

## ファイル

| ファイル | 役割 |
|---|---|
| `main.go` | Wails のアプリとウィンドウを作る(Frameless。タイトルバーは画面の `TitleBar.tsx`)。ウィンドウは起動時 720×820、最小 480×480 |
| `appversion.go` / `build_dev.go` / `build_production.go` | 表示するバージョン(`wails3 dev` では patch +1 と `+dev`)。dev かどうかは `production` のビルドタグで見分ける([_docs/deploy.md](../../_docs/deploy.md)) |
| `config.go` | 環境変数の読み込み。`settings.json` を読んで MCP のサーバーの係(`mcpclient.Manager`)を作るのは `main.go` の `startMCP`(アプリを閉じるときに止める)、`tools.json` を読むのは `loadToolPrefs` |
| `service.go` | `ChatService`。画面と `aibow.Conversations` / `Engine` の中継だけをする(モデル・会話・設定(`SetLimits` など)・メーター(`Usage`)・`Send` / `Compact` / `StopSummary`)。生成の回ごとの統計・compact・ツールの実行のログもここで出す |
| `frontend/src/` | チャット画面(React + TypeScript)。Vite でビルドした `frontend/dist` を `go:embed` で埋め込む。中身は [_docs/ui.md](../../_docs/ui.md) と [_docs/rendering.md](../../_docs/rendering.md) |
| `frontend/bindings/` | `wails3 generate bindings` の生成物(コミットする。手で直さない) |
| `build/` / `Taskfile.yml` | `wails3 init`(react テンプレート)由来のビルド設定。ios / android / docker は削除して `.gitignore` 済み |
| `version` | バージョンの唯一の正。`//go:embed` で exe に入る。扱いは [_docs/deploy.md](../../_docs/deploy.md) |
| `Taskfile.local.yml` | ローカルデプロイ用の個人のファイル。**Git に入れない(ひな形もコミットしない)**。[_docs/deploy.md](../../_docs/deploy.md) |
| `go.mod` / ルートの `go.work` | `_cmd/aibow` は別モジュール(Wails に依存)。`go.work` でルートとまとめる |
| `lib\` / `models\` | llama.cpp の DLL とモデル。Git には入れない |

- Wails に依存するコードは `_cmd/aibow` だけに置く。会話や生成の仕組みはルートの `aibow` パッケージに置き、`ChatService` は中継に徹する
- `lib\` / `models\` をアプリのフォルダに置くのは、モデルを動かすのがこのアプリだけだから(VRAM 8GB に2プロセス分は載らない)。
  カレントフォルダから探すので、起動・準備は `_cmd/aibow` で行う

## 環境変数

`AIBOW_MODEL` / `AIBOW_MODELS_DIR` / `AIBOW_LIB` / `AIBOW_DB` / `AIBOW_SETTINGS` / `AIBOW_TOOLS` / `AIBOW_HISTORY_TOKENS` / `AIBOW_MAX_TOKENS` / `AIBOW_GPU_LAYERS` / `AIBOW_PRESENCE_PENALTY` / `AIBOW_LLAMA_LOG`、
Ollama の保存先は Ollama と同じ `OLLAMA_MODELS`。意味と既定値は [_docs/usage.md](../../_docs/usage.md) の表にある。

- `AIBOW_CTX` は 0002 で廃止(設定されていたらログとトーストで伝える)
- 新しい設定も `AIBOW_` で始まる環境変数にし、[_docs/usage.md](../../_docs/usage.md) の表に追加する
- 画面で変えられる設定(会話の上限・返事の上限・GPU の層数・presence penalty)の環境変数は「DB にまだ値が無いときの初期値」で、画面で変えたら DB の値が優先

## Wails とフロントエンドの方針

- Wails は現在 v3.0.0-beta.26(`wails3` CLI と同じ版)。構成はユーザーの wails3 スキルの標準(`_cmd/<名前>/build/config.yml`・Taskfile・`wails3 dev`)に従う
- フロントエンドは React + TypeScript + Vite(`wails3 init -t react` のテンプレート)。npm のパッケージは react / react-dom / @wailsio/runtime / react-icons(アイコン。Lucide の `react-icons/lu` を使う)/ marked(返事の Markdown の表示)/ highlight.js(コードの色付け)/ mermaid(図)と開発用の vite / typescript など。使い方は [_docs/rendering.md](../../_docs/rendering.md)。
  marked・highlight.js・mermaid は版を固定している。上げるときは、出てから2週間以上たった版にする(ユーザーがメジャーアップデート直後の版を避けて選ぶこともある)。
  mermaid が使うパッケージのうち、入れたときに出てから2週間たっていなかったものは `package.json` の `overrides` で1つ前の版に固定している。
  npm は lockfile の書き方が版で違う(npm 10 は `libc` の欄を消す)ので、パッケージを足すときは `npx npm@11.20.0 install …` を使い、関係ない差分を出さない
  それ以外を足すときは、理由と代替案を示してユーザーに確認する

## 起動と終了

- 起動時のモデルはウィンドウを出してから裏で読み込み、終わったら `models` イベントで画面に知らせる
- 起動に失敗しても(lib が無い、環境変数の値が不正など)ウィンドウは出し、`Models()` の `error` で理由を画面に出す。
  リリース版(`wails3 build`)はコンソールが無く、`log.Fatal` だと何も見えずに終わるため。起動時のエラーで `log.Fatal` しない
- アプリ終了時は `ServiceShutdown` で生成を止める

## Go と画面のやり取り

- 画面 → Go は生成されたバインディング(`ChatService.Models()` など)で呼ぶ。Go のメソッドを変えたら `wails3 generate bindings -f '' -clean=true -ts -i` で作り直す
  (Taskfile と同じフラグ。`-i` は interface 生成なので、画面で `new` しない)。`wails3 dev` / `wails3 build` は自動で作り直す
- Go → 画面は Wails のイベント。`chat` イベントは `RegisterEvent[aibow.ChatEvent]` で型を登録してあり、画面では `ev.data` が `ChatEvent` 型になる。
  `type` は `user`(保存したユーザーの発言。画面は ID を表示に付ける)/ `summarizing` / `summary`(要約の断片)/ `compact`(compact した・要約に失敗した)/
  `usage`(メーター)/ `prompt` / `thinking` / `content` / `reply`(返事を1件保存した。ツールを使うと1回の送信で何回も来る)/
  `tool_start` / `confirm`(ツールの実行前の確認を待っている)/ `tool_result`(ツールの結果を保存した)/ `notice`(お知らせ。画面はトースト)/ `error` / `done`
- `conversation_id` が付いていて、画面は表示中の会話のものだけを出す。`done` には Go が保存した最後の返事(`reply`)が入る。
  画面は `reply` / `tool_result` で届いた保存済みの発言から吹き出しを作る([_docs/tools.md](../../_docs/tools.md))
- `Send` の戻り値とイベントは別経路で届き順序が保証されないので、終わりは必ず `done` イベントで判定する(`Compact` も同じ)
- 停止は `Call` の Promise の `cancel()` → Go の `ctx` がキャンセルされる。
  送信の途中の要約中だけは `ChatService.StopSummary()` で要約だけを止める(その回は truncate で返事に進む。[_docs/compact.md](../../_docs/compact.md))
- 会話は、新しい会話の画面で最初に送信したときに `ChatService.NewConversation(settings)` で作る(空の会話を作らない)
- ツールの実行前の確認には `ChatService.ConfirmTool(会話の ID, confirm イベントの ID, 実行するか, 理由)` で答える。作業フォルダは `ChatService.ChooseWorkDir` でフォルダを選ぶダイアログを出す
- ツールと MCP:会話で選べる MCP のサーバーは `MCPServers`(設定の画面は `AllMCPServers`)、組み込みのツールの一覧は `BuiltinTools`、ツールの扱いの PC の既定(`tools.json`)は `ToolPrefs` / `SetToolPolicy` / `SetGroupDefault`(新しい会話で最初から使うか)、MCP のサーバーのツールの一覧を受け取り直すのは `LoadToolList`。仕組みは [_docs/mcp.md](../../_docs/mcp.md)
- 左下に出すバージョンは `ChatService.Version`(`wails3 dev` では patch +1 と `+dev`。`appversion.go`)
- IME の問題のため、Go が `events.Windows.WindowSetFocus` で `aibow:window:focus` を送っている([_docs/ui.md](../../_docs/ui.md))
