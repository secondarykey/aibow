# AGENTS.md

このリポジトリで作業する AI エージェント向けの指示です。

## プロジェクトの目的

aibow は、ローカルLLMを使ったアプリを**自分で作りながら仕組みを学ぶ**ための学習プロジェクトです。
最終目標は次の2つです。

- ローカルLLMでバイブコーディングできるエージェントを自作し、その中身を理解する
- MCP の使い方と作り方(クライアント・サーバー両方)を理解する

完成品を早く作ることより、**各段階で何が起きているかが読んで分かること**を優先してください。

## 現在の構成(ステップ2-1: 会話の保存)

```
ウィンドウ(_cmd/aibow/frontend の React) ⇄ Wails v3 ⇄ aibow ⇄ yzma ⇄ llama.cpp(_cmd/aibow/lib\ の DLL)⇄ GPU
                                                     ⇅          ↑ _cmd/aibow/models\*.gguf / Ollama の保存フォルダ
                                          aibow/store(SQLite: %APPDATA%\aibow\aibow.db)
```

**Ollama などの外部 LLM サーバーは使わない**。aibow のプロセスの中で llama.cpp を直接動かす。
これはユーザーの明確な方針なので、LLM サーバーを別プロセスで立てる構成を提案・導入しないこと。
画面は Wails v3 のデスクトップアプリ。HTTP サーバーは立てない(以前のブラウザ版から移行した)。

ただし **Ollama を「モデルの管理係」として使うのは OK**(ユーザーの方針)。
Ollama が `ollama pull` で保存したファイル(`manifests\` の目録と `blobs\` の GGUF)を aibow が直接読む。
Ollama のサーバーや API は使わない。Ollama のモデルの一部は llama.cpp で読めない独自形式なので、
読めない場合はエラーにして元のモデルに戻す。

| ファイル | 役割 |
|---|---|
| `aibow.go` | パッケージの説明。ルートは `aibow` パッケージ(ライブラリ)で、Wails を import しない |
| `engine.go` | llama.cpp の呼び出し。モデルの読み込み・入れ替え、KV キャッシュ再利用、decode、サンプリングの生成ループ |
| `models.go` | モデルの一覧(`models\` の *.gguf と Ollama の保存フォルダ)。ID は `file:<パス>` / `ollama:<名前>` |
| `prompt.go` | 会話 → ChatML 文字列(Qwen3.5 の公式チャットテンプレートを Go で書き直したもの) |
| `stream.go` | UTF-8 の切れ目の処理と、`</think>` で思考と答えを振り分ける処理 |
| `conversation.go` | 会話の仕組み(`Conversations`)。発言の保存、タイトルの自動付与と名前の変更、思考を除いた履歴での生成、答えが出なかった回の理由の文、生成は同時に1つ |
| `store.go` | 保存先の約束(`Store` interface)と、会話・発言の型(`Conversation` / `StoredMessage` / `ReplyStats`) |
| `appsettings.go` | アプリ全体の設定(`app_settings` の読み書き)。環境変数は DB に値が無いときの初期値 |
| `store/sqlite.go` | `Store` の SQLite 実装(`modernc.org/sqlite`)。`PRAGMA user_version` でマイグレーションを管理する(版を足すときは `migrations` の末尾に追加) |
| `*_test.go` | テンプレート、思考の振り分け、Ollama の目録、会話の仕組み(メモリ上の保存先 `memstore_test.go`)、SQLite(一時フォルダの DB)のテスト。モデル不要 |
| `_cmd/aibow/main.go` | Wails のアプリとウィンドウを作る。Wails に依存するコードは `_cmd/aibow` だけに置く |
| `_cmd/aibow/config.go` | 環境変数の読み込み |
| `_cmd/aibow/service.go` | `ChatService`。画面と `aibow.Conversations` / `Engine` の中継だけをする(モデル・会話・設定・`Send`) |
| `_cmd/aibow/frontend/src/` | チャット画面(React + TypeScript。左: 会話の一覧 / 中央: チャット(入力欄の下にモデルの選択)/ 右: 設定パネル(一覧の下の「設定」で開閉、初期は閉じている))。`useModels.ts` / `useConversations.ts` / `useChat.ts` / `useAppSettings.ts` が Go とのやり取り、`components/` が表示。Vite でビルドした `frontend/dist` を `go:embed` で埋め込む |
| `_cmd/aibow/frontend/bindings/` | `wails3 generate bindings` の生成物(コミットする。手で直さない) |
| `_cmd/aibow/build/` / `Taskfile.yml` | `wails3 init`(react テンプレート)由来のビルド設定。ios / android / docker は削除して `.gitignore` 済み |
| `_cmd/aibow/version` / `_cmd/version.go` | バージョンの唯一の正と、それを config.yml / package.json に書き写すスクリプト(リポジトリのルートで `go run _cmd/version.go`)。version は `//go:embed` で exe に入る |
| `_cmd/aibow/Taskfile.local.yml` | ローカルデプロイ(`wails3 task local:deploy`)。各自が持つ個人用のファイルで、**Git に入れない(ひな形もコミットしない)**。`Taskfile.yml` から optional で読む |
| `_cmd/aibow/go.mod` / `go.work` | `_cmd/aibow` は別モジュール(Wails に依存)。`go.work` でルートとまとめる |
| `_cmd/aibow/lib\` / `_cmd/aibow/models\` | llama.cpp の DLL とモデル。Git には入れない。モデルを動かすのはこのアプリだけなので(VRAM 8GB に2プロセス分は載らない)、アプリのフォルダに置く。起動・準備は `_cmd/aibow` で行う(カレントフォルダから探すため) |

- 画面 → Go は生成されたバインディング(`ChatService.Models()` など)で呼ぶ。Go のメソッドを変えたら `wails3 generate bindings -f '' -clean=true -ts -i` で作り直す
  (Taskfile と同じフラグ。`-i` は interface 生成なので、画面で `new` しない)。`wails3 dev` / `wails3 build` は自動で作り直す
- Go → 画面は Wails のイベント。`chat` イベントは `RegisterEvent[aibow.ChatEvent]` で型を登録してあり、画面では `ev.data` が `ChatEvent` 型になる。`type` は `prompt` / `thinking` / `content` / `error` / `done`。
  `conversation_id` が付いていて、画面は表示中の会話のものだけを出す。`done` には Go が保存した返事(`reply`)が入る。
  `Send` の戻り値とイベントは別経路で届き順序が保証されないので、終わりは必ず `done` イベントで判定する
- 停止(生成中の返事の吹き出しの中のボタン)は `Call` の Promise の `cancel()` → Go の `ctx` がキャンセルされる。アプリ終了時は `ServiceShutdown` で生成を止める
- 起動時のモデルはウィンドウを出してから裏で読み込み、終わったら `models` イベントで画面に知らせる
- Windows の WebView2 は、別のウィンドウから戻ると IME(日本語入力)が効かなくなることがある。Go が `events.Windows.WindowSetFocus` で
  `aibow:window:focus` を送り、画面の `imeFocus.tsx` が見えない input を経由して入力欄にフォーカスを付け直す(同じ要素への blur → focus では直らない)。
  IME 変換中・ボタンにフォーカスがあるときは触らない。新しい入力欄を足しても、テキスト入力なら自動で対象になる
- 起動に失敗しても(lib が無い、環境変数の値が不正など)ウィンドウは出し、`Models()` の `error` で理由を画面に出す。
  リリース版(`wails3 build`)はコンソールが無く、`log.Fatal` だと何も見えずに終わるため。起動時のエラーで `log.Fatal` しない
- 会話履歴は Go(`aibow.Conversations`)が持ち、SQLite に保存する。画面は会話の ID と新しい発言だけを送り、Go が毎回全件を並べ直してモデルに渡す(モデルはステートレス)
- 過去のアシスタント発言の思考は保存して画面で見られるが、モデルに渡すのは答えだけ(公式テンプレートと同じ)。システムプロンプトは発言として保存せず、会話の設定から毎回先頭に付ける
- 会話ごとの設定(システムプロンプト・上限トークン数・temperature・思考・presence penalty)は `conversations.settings`(JSON)。
  新しい会話は `app_settings` の既定をコピーする。アプリ全体の設定(コンテキスト長・GPU の層数・最後に使ったモデル)も `app_settings`
- 発言の保存では会話を丸ごと書き戻さない(`Store.TouchConversation` で更新日時と空のときだけのタイトル)。生成中に画面で変えた名前や設定を古い値で上書きしないため
- ID は `uuid.NewV7()`(作成順に並ぶ)。DB には TEXT で入れ、発言の順番は ID の並びで決める(順番用の列は持たない)
- 思考の ON/OFF は、プロンプト末尾を `<think>\n` にするか `<think>\n\n</think>\n\n` にするかで切り替える
- 生成とモデルの入れ替えは同時に1つだけ(コンテキストが1つのため)。`Engine.mu` で排他している。会話の側でも生成中の会話を1つだけ持ち(`Conversations.generating`)、生成中の会話は削除できない
- モデルの切り替えは「今のモデルを解放 → 新しいモデルを読み込む」の順(VRAM 8GB に2つは載らないため)。失敗したら元のモデルを読み直す。
  GPU の層数の変更(`SetGPULayers`)も同じ仕組みで読み直す。コンテキスト長の変更(`SetContextSize`)はコンテキストだけを作り直し、モデルは読み直さない

## 作業の進め方

- 全体の進み具合とロードマップは `TODO.md`、1つ1つの機能の仕様は `docs/features/<番号>-<名前>.md` にある。作業を始める前に両方を読む
- 機能の仕様は、ユーザーが Chat で相談して決めたものを書いた「発注書」。書かれている決定事項に従い、「やらないこと」は先回りして作らない
- 仕様の「未決事項」は勝手に決めず、案を示してユーザーに確認する。作業中に新しく決めたことは、その仕様のファイルに書き足す
- 着手したら仕様の「状態」と `TODO.md` を 🔧 作業中に、完了の条件を満たしたら ✅ 完了にする
- ステップは順番に進める。先のステップの機能を先回りして入れない

## 技術方針

- **言語は Go**(1.27 以上。標準の `uuid` パッケージを使う)。Python など別言語の実装を持ち込まない
- Go の外部パッケージは **yzma(github.com/hybridgroup/yzma)と、画面用の Wails v3(`_cmd/aibow` のみ)**。それ以外は標準ライブラリで書く。
  Wails は現在 v3.0.0-beta.26(`wails3` CLI と同じ版)。ルートの `go.mod` には Wails を入れない
  会話の保存に SQLite のドライバー `modernc.org/sqlite`(cgo 不要。ルートのモジュールの `store` パッケージだけで使う)。C コンパイラなし(`CGO_ENABLED=0`)でビルドできる状態を保つ
  追加が必要な場合は、理由と代替案を示してユーザーに確認してから追加する(MCP の公式 Go SDK はステップ4以降で検討してよい)
- yzma と llama.cpp はバージョンの組み合わせが決まっている。現在は yzma v1.29.1 + llama.cpp v0.6.0(ビルド b11429)。
  yzma を上げるときは、yzma の README の対応表を確認し、`lib\` の入れ直しも README に書く
- チャットテンプレートはライブラリ任せにせず、`prompt.go` に自分で書く(書式を理解することが学習目的のため)
- フロントエンドは React + TypeScript + Vite(`wails3 init -t react` のテンプレート)。npm のパッケージは react / react-dom / @wailsio/runtime と開発用の vite / typescript など。
  それ以外を足すときも、理由と代替案を示してユーザーに確認する。Wails の構成はユーザーの wails3 スキルの標準(`_cmd/<名前>/build/config.yml`・Taskfile・`wails3 dev`)に従う
- 設定は環境変数(`AIBOW_MODEL` / `AIBOW_MODELS_DIR` / `AIBOW_LIB` / `AIBOW_DB` / `AIBOW_CTX` / `AIBOW_GPU_LAYERS` / `AIBOW_MAX_TOKENS` / `AIBOW_PRESENCE_PENALTY` / `AIBOW_LLAMA_LOG`、Ollama の保存先は Ollama と同じ `OLLAMA_MODELS`)。
  新しい設定も `AIBOW_` で始まる環境変数にし、README の表に追加する。
  画面で変えられる設定(コンテキスト長・GPU の層数・上限トークン数・presence penalty)の環境変数は「DB にまだ値が無いときの初期値」で、画面で変えたら DB の値が優先

## バージョンとローカルデプロイ

- CI でビルドしないので、`wails3 task local:deploy` が CI の代わり。バージョンの反映 → ビルド → ビルド情報だけのコミット → `v`+バージョンのタグ → インストールまでを行う
- バージョンを手で config.yml などに書かない。`_cmd/aibow/version` だけを変え、`go run _cmd/version.go` で書き写す(deploy は自動で行う)
- `v`+今のバージョンのタグがある(= デプロイ済み)なら、deploy が patch を自動で上げる。minor / major は `go run _cmd/version.go -bump`
- `Taskfile.local.yml` とそのひな形・コピーはコミットしない(インストール先などローカルの設定をリポジトリに残さないため。ユーザーの方針)
- `Taskfile.local.yml` のタスクを、コミットする `Taskfile.yml` のタスクから呼ばない(持っていない環境で壊れる)
- deploy を試すときは `DEPLOY_DEST` を一時フォルダにし、試しに作ったコミットとタグは消しておく(タグはリポジトリ全体で共有される)

## コードの書き方

- コメントは日本語。「何をしているか」より「なぜそうしているか」「ここで何を学べるか」を書く
- 1つの関数に処理を詰め込みすぎず、流れが上から読めるようにする
- エラーは握りつぶさず、原因と対処が分かる日本語のメッセージで画面かログに出す
- 学習用のログ(トークン数、再利用数、速度、終了理由など)は残す。仕組みが観察できることが目的の一部

## ユーザーとのやり取り

- **コマンドやツールを使うときは、それが何をするものかを必ず説明する**。説明なしにコマンドを並べない
- 新しい概念(例: KV キャッシュ、tool_call、MCP のトランスポート)を導入するときは、短い説明を添える
- ユーザーの環境: Windows、NVIDIA GPU(VRAM 8GB)、メモリ 64GB。シェルは bash(Windows 上の Git Bash など。PowerShell の `$env:` 構文は使えない)。パスは `/d/Go/Projects/aibow` の形で書く
- 主に使うモデルは Qwen3.5(4B / 9B)。ユーザーは Ollama でのモデル管理に慣れている

## 調整の記録

- presence penalty(`AIBOW_PRESENCE_PENALTY`、既定 1.5、直近 256 生成トークンが対象): Qwen3.5 4B は「1+1は？」でも数百トークン考える。
  CPU で各1回試したところ、0 と 1.0 は 700 トークン以内に答えまで届かず、1.5 は 662 トークンで答えた。思考オフの日本語の答えの自然さはどれも同程度。
  試行は各1回なので、変えるときは複数回比べること
- 答えが空のまま終わった回(思考の途中で打ち切り・停止)は、理由の文を assistant の発言として保存する(質問が宙に浮かないように)。`conversation.go` の `noAnswerNote`

## 既知の問題

- 2026-10-12: Windows + CUDA で、Ollama の qwen3.5:4b を読み込む途中に `GGML_ASSERT(ctx->mem_buffer != NULL) failed`(ggml.c:1644、メモリ確保の失敗)で1回だけ落ちた。
  同じ条件で10回起動し直しても再現せず、原因は未特定。同じモデルファイルは Linux(CPU)の aibow と llama.cpp 単体では正常に動く。
  再発したら `AIBOW_LLAMA_LOG=1` でログを取り、そのとき動いていた他のアプリも確認する。
  C 側の assert はプロセスごと終了させるので、Go 側の「元のモデルに戻す」処理では防げない

## 確認方法

変更後は最低限、次を実行してください。

```bash
gofmt -l .                       # 整形が必要なファイルを表示(何も出なければ OK。_cmd も含む)
go test ./...                    # テンプレート、思考の振り分け、Ollama の目録、会話の仕組み、SQLite のテスト(モデル不要)
(cd _cmd/aibow && wails3 build)  # バインディング生成 → 画面の型チェックとビルド(tsc + vite)→ Go のビルド
go vet ./... ./_cmd/aibow        # よくある間違いを静的チェック(./... は _cmd を含まないので並べる)
```

`_cmd/aibow` は `frontend/dist` を埋め込むので、画面を一度もビルドしていないと `go vet` / `go build` が「パターンに一致するファイルが無い」で失敗します。先に `wails3 build` を実行します。

実際の生成は `_cmd/aibow` で `wails3 dev`(または `wails3 build` してから `wails3 task run`)で起動し、開いたウィンドウで送受信して確認します。
worktree には `lib\` / `models\` / `frontend/node_modules` が無い。
- `lib\` / `models\` は `AIBOW_LIB` / `AIBOW_MODELS_DIR` で本体(`/d/Go/Projects/aibow/_cmd/aibow/`)を指す(ユーザーの環境では、worktree のセッション開始時にこの2つが自動で設定される)。
  **worktree に `lib\` / `models\` を作らない・入れ直さない**(yzma の install や GGUF のダウンロードをしない)
- 本体の `lib\` / `models\` へのジャンクションやシンボリックリンクを張らない。worktree の解放時に Git 管理外のファイルが消され、リンク先の本体の実体まで消える
- 会話の DB は、試すときは `AIBOW_DB` を一時フォルダのファイルにする(ユーザーの `%APPDATA%\aibow\aibow.db` に試しの会話を混ぜない)
- `node_modules` は worktree の `_cmd/aibow/frontend` で `npm install` する(ユーザーの環境で本体へのジャンクションが自動で張られている場合は、そのまま使い、`npm install` しない)
画面を外から操作して確かめるときは、一時ビルドで `application.Options.Windows.AdditionalBrowserArgs` に
`--remote-debugging-port=9222` を足し、CDP(`http://localhost:9222/json`)で `Runtime.evaluate` する(この変更はコミットしない)。
- ユーザーはふだんから aibow を起動していることがある。一時ビルドでは `application.Options.Windows.WebviewUserDataPath` も一時フォルダにする
  (同じ WebView2 のデータフォルダを起動引数の違うプロセスで使うと、後から起動した方が WebView2 を作れず失敗する: 8007139f)
- 試しに起動したアプリを閉じるときは、**自分が起動したプロセスの PID だけ**を終了させる。`taskkill /IM aibow.exe` のように名前で終了させない(ユーザーのアプリまで閉じてしまう)

GPU や本物のモデルがない環境では、llama.cpp の CPU 版と小さな GGUF(重みがランダムな極小モデルでもよい)で
「読み込み → 生成 → 停止 → 2回目の送信で KV キャッシュが再利用される → モデルの切り替え → 壊れたモデルを選んだら元に戻る → 再起動しても会話が残る」までを確認してください。
Ollama の保存フォルダは、`manifests\registry.ollama.ai\library\<名前>\<タグ>`(目録 JSON)と `blobs\sha256-<ハッシュ>` を作れば模擬できます(`OLLAMA_MODELS` で指定)。
出力される文章の中身は確認できないので、思考と答えの振り分けは `stream_test.go` で確認します。
