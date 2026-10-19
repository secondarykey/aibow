# _cmd/aibow(デスクトップアプリ)

Wails v3 のデスクトップアプリです。ルートの `aibow` パッケージ(ライブラリ)を画面につなぎます。
画面そのもの(配置・操作・フォーカス・スクロール・ウィンドウ・IME)は [_docs/ui.md](../../_docs/ui.md)、
返事の表示(Markdown・コードの色付け・mermaid の図)は [_docs/rendering.md](../../_docs/rendering.md) にあります。

## ファイル

| ファイル | 役割 |
|---|---|
| `main.go` | Wails のアプリとウィンドウを作る(Frameless。タイトルバーは画面の `TitleBar.tsx`)。ウィンドウは初めて起動したとき 720×820(次からは閉じたときの位置と大きさ。`windowstate.go`)、最小 480×480。最初に `startLog`(aibow/log の `Init`)でログの出口を組む。`wails3 dev`(`isDev`)はターミナルと `%APPDATA%\aibow\logs\aibow-dev_<日付>.log` の両方に Info 以上、exe は `%APPDATA%\aibow\logs\aibow_<日付>.log` に Notice 以上(`AIBOW_LOG_DIR`。どちらも 10 日分を残して古いものは起動時に消す。`logKeepDays`)。異常終了(Go の panic など)は同じフォルダの `aibow_crash.log` に残し、次の起動で「前回は異常終了しました」を画面のお知らせとログで伝える(`log.CatchCrash`)。書けなくても起動は止めず、画面のお知らせで伝える |
| `appversion.go` / `build_dev.go` / `build_production.go` | 表示するバージョン(`wails3 dev` では patch +1 と `+dev`)。dev かどうかは `production` のビルドタグで見分ける([_docs/deploy.md](../../_docs/deploy.md)) |
| `windowstate.go` | ウィンドウの位置・大きさ・最大化を `%APPDATA%\aibow\window.json`(`AIBOW_WINDOW`)に覚え、次の起動で戻す。仕組みは [_docs/ui.md](../../_docs/ui.md) の「次の起動で戻すもの」 |
| `config.go` | 環境変数の読み込み。`settings.json` を読んで MCP のサーバーの係(`mcp/client` の `Manager`)を作るのは `main.go` の `startMCP`(アプリを閉じるときに止める)、`tools.json` を読むのは `loadToolPrefs` |
| `service.go` | `ChatService`。画面と `aibow.Conversations` / `Engine` の中継だけをする(モデル・会話・設定(`TryLimits` / `SetGPULayers` / 画面の設定の `SetDisplay` など)・メーター(`Usage`)・`Send` / `Compact` / `StopSummary`)。画面の設定(質問を右上に出す・明るさ)は DB に置き、実際のライト / ダークは画面の `theme.ts` が決める([_docs/ui.md](../../_docs/ui.md) の「明るさ」)。生成の回ごとの統計・compact・ツールの実行のログもここで出す |
| `frontend/src/` | チャット画面(React + TypeScript)。Vite でビルドした `frontend/dist` を `go:embed` で埋め込む。中身は [_docs/ui.md](../../_docs/ui.md) と [_docs/rendering.md](../../_docs/rendering.md) |
| `frontend/bindings/` | `wails3 generate bindings` の生成物(コミットする。手で直さない) |
| `build/` / `Taskfile.yml` | `wails3 init`(react テンプレート)由来のビルド設定。ios / android / docker は削除して `.gitignore` 済み |
| `version` | バージョンの唯一の正。`//go:embed` で exe に入る。扱いは [_docs/deploy.md](../../_docs/deploy.md) |
| `frontend/bindings/` | `wails3 generate bindings` の生成物(コミットする。手で直さない)。Go のモジュール名のパスに出る(`bindings/github.com/secondarykey/aibow/…`。画面のアプリ自身は `…/_cmd/aibow`) |
| `go.mod` / ルートの `go.work` | `_cmd/aibow` は別モジュール(`github.com/secondarykey/aibow/_cmd/aibow`。Wails に依存)。`go.work` と `replace` でルートとまとめる |
| `lib\` / `models\` | llama.cpp の DLL とモデル。Git には入れない |

- Wails に依存するコードは `_cmd/aibow` だけに置く。会話や生成の仕組みはルートの `aibow` パッケージに置き、`ChatService` は中継に徹する
- `lib\` / `models\` をアプリのフォルダに置くのは、モデルを動かすのがこのアプリだけだから(VRAM 8GB に2プロセス分は載らない)。
  カレントフォルダから探すので、起動・準備は `_cmd/aibow` で行う

## 環境変数

`AIBOW_MODEL` / `AIBOW_MODELS_DIR` / `AIBOW_LIB` / `AIBOW_DB` / `AIBOW_SETTINGS` / `AIBOW_TOOLS` / `AIBOW_WINDOW` / `AIBOW_LOG_DIR` / `AIBOW_HISTORY_TOKENS` / `AIBOW_MAX_TOKENS` / `AIBOW_GPU_LAYERS` / `AIBOW_PRESENCE_PENALTY` / `AIBOW_LLAMA_LOG`、
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
- モデルは会話ごと(0014 の 3)。送信の前に会話の仕組みが会話のモデルを読み込むときは、`modelLoader`(`aibow.ModelLoader`。`ChatService` のメソッドにすると画面から呼べてしまうので別の型)が `loadModel` を呼ぶ。
  `loadModel` はモデルごとに覚えた GPU の層数で読み込む(`WorkspaceSettings.LayersFor`)。`SetGPULayers` は読み込んでいるモデルの値として覚え(`model_gpu_layers`)、`AppSettings` はその値を返す。
  画面は入力欄の下でモデルを選んだとき、`SwitchModel` で読み込めたら会話のモデルを変える
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
- 会話は、新しい会話の画面で最初に送信したときに `ChatService.NewConversation(プロファイルの ID, settings)` で作る(空の会話を作らない。ID が空なら最初に選ばれるプロファイル)
- プロファイル(0014):一覧 `Profiles`・作る `CreateProfile`・変える `UpdateProfile`・しまう `SetProfileArchived`・消す `DeleteProfile`・最初に選ぶ `SetDefaultProfile`・
  選んだときの写し `ProfileDefaults`・会話の設定から保存 `SaveToProfile` / `SaveAsProfile`。会話の詳細(`Conversation`)の `profile` に会話のプロファイル(決まり)が入る
- 吹き出しを「モデルに渡さない」にする / 戻すのは `ChatService.SetExcluded(会話の ID, 吹き出しの中の発言の ID, 渡さないか)`(0011)
- 最後の返事を作り直すのは `ChatService.Regenerate(会話の ID)`、最後の発言を直して送り直すのは `ChatService.Resend(会話の ID, 文)`(0011)。どちらも `Send` と同じく chat イベントで途中経過を送り、`done` で終わる(作り直すでは `user` イベントは来ない)
- ツールの実行前の確認には `ChatService.ConfirmTool(会話の ID, confirm イベントの ID, 実行するか, 理由, これから先の扱い)` で答える(これから先の扱いは `ToolPolicy` の値。空ならこの1回だけ。0010)。作業フォルダは `ChatService.ChooseWorkDir` でフォルダを選ぶダイアログを出す
- ツールと MCP:会話で選べる MCP のサーバーは `MCPServers`(設定の画面は `AllMCPServers`)、組み込みのツールの一覧は `BuiltinTools`、ツールの扱いの PC の既定(`tools.json`)は `ToolPrefs` / `SetToolPolicy`(新しい会話で最初から使うグループはプロファイルの値。`UpdateProfile` で変える)、MCP のサーバーのツールの一覧を受け取り直すのは `LoadToolList`、会話の設定の画面に出す会話でのツールの扱いは `ToolStates`(会話の ID と設定を渡す。プロファイルで使えないものは `from: "profile"`)、設定の画面に出す PC での扱いは `ToolDefaults`、グループごとまとめて確認しないは `SetGroupPolicy`。仕組みは [_docs/mcp.md](../../_docs/mcp.md)
- 左下に出すバージョンは `ChatService.Version`(`wails3 dev` では patch +1 と `+dev`。`appversion.go`)
- IME の問題のため、Go が `events.Windows.WindowSetFocus` で `aibow:window:focus` を送っている([_docs/ui.md](../../_docs/ui.md))
