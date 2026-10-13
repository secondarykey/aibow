# 全体の構成とファイル

aibow がどう組み立てられているか(画面・Wails・llama.cpp・保存)と、リポジトリのファイルの役割です。
仕組みごとの詳しい説明は、同じフォルダの各ファイル(`engine.md`・`tools.md` など)にあります。

## しくみ

```
ウィンドウ(_cmd/aibow/frontend の React) ⇄ Wails ⇄ aibow ⇄ 関数呼び出し ⇄ llama.cpp(_cmd/aibow/lib の DLL)⇄ GPU
                                                  ⇅                           ↑
                                  会話の保存(SQLite)   _cmd/aibow/models\*.gguf  または  Ollama が保存したモデル
```

画面は [Wails v3](https://v3.wails.io/) のデスクトップアプリです。Wails は OS に入っている WebView(Windows なら WebView2)で HTML の画面を出し、
画面の JavaScript から Go のメソッドを呼べるようにする枠組みです。画面は React + TypeScript で書き、Vite でビルドして実行ファイルに埋め込みます。
Go のメソッドを呼ぶための TypeScript の関数と型(バインディング)は、`wails3 generate bindings` が Go のコードから自動で作ります。

aibow が llama.cpp(LLM を計算するライブラリ)を直接読み込み、モデルの読み込みから1トークンずつの生成まで、自分で行います。
Ollama のサーバーは使いません。ただし、**Ollama で `ollama pull` したモデルはそのまま使えます**。
Ollama が保存したファイルを aibow が直接読むので、Ollama は起動していなくて構いません。
「モデルの管理は Ollama、動かすのは aibow」という分担です。

## ファイル構成

| ファイル | 役割 |
|---|---|
| `aibow.go` | パッケージの説明。リポジトリのルートは LLM を動かす部分(`aibow` パッケージ)で、画面のことは知らない |
| `engine.go` | llama.cpp を呼んで文章を生成する部分(モデルの読み込み・入れ替え、トークン化とトークン数を数えること、計算、選択の繰り返し) |
| `models.go` | 使えるモデルの一覧を作る(`models\` フォルダと Ollama の保存フォルダ) |
| `format.go` / `format_qwen35.go` | 書式(チャットテンプレート)。会話を、モデルが理解する書式の1本の文字列にし、生成された文字を「思考」「答え」「ツール呼び出し」に振り分ける。今は Qwen3.5 の書式だけ |
| `stream.go` | 生成された文字の切れ目や、文字にならないバイトの扱い |
| `conversation.go` | 会話の仕組み。発言を保存し、送信のたびに履歴を並べ直してモデルに渡す(モデル自身は何も覚えていない) |
| `agent.go` | エージェントループ。モデルがツールを呼んだら実行し、結果を渡して続きを書かせる、をツールを呼ばなくなるまで繰り返す |
| `tool.go` / `tool_time.go` / `tool_calc.go` | ツールの形と、組み込みのツール(現在時刻・計算) |
| `compact.go` | 長くなった会話を「会話の上限」に収める仕組み(古い発言を捨てる / 要約する)と、メーターの計算 |
| `store.go` | 会話の保存先の約束(interface)と、会話・発言・要約の形。会話の仕組みは保存先が SQLite かどうかを知らない |
| `appsettings.go` | アプリ全体の設定(会話の上限・返事の上限・GPU の層数・最後に使ったモデル・新しい会話の既定の設定) |
| `store/sqlite.go` | 会話・発言・要約を SQLite のファイルに保存する部分(テーブルの作り変え = マイグレーションもここ) |
| `*_test.go` | テスト(モデルなしで実行できる) |
| `_cmd/aibow/main.go` | デスクトップアプリの起動(Wails のアプリとウィンドウを作る) |
| `_cmd/aibow/config.go` | 環境変数から設定を読む |
| `_cmd/aibow/service.go` | 画面から呼ばれる Go のメソッド(モデル・会話・設定・メーター・送信・今すぐ要約)。会話の仕組みとの中継だけをする |
| `_cmd/aibow/frontend/src/` | チャット画面(React + TypeScript)。`App.tsx` が全体、`use*.ts` が Go とのやり取り、`components/` が表示 |
| `_cmd/aibow/frontend/bindings/` | `wails3 generate bindings` が作る、Go のメソッドを呼ぶ関数と型(自動生成。手で直さない) |
| `_cmd/aibow/build/` / `Taskfile.yml` | `wails3 dev` / `wails3 build` の設定(アプリ名、アイコン、OS ごとのビルド手順) |
| `_cmd/aibow/version` | アプリのバージョン(唯一の正)。exe に埋め込まれ、タスクバーなどに出るウィンドウ名に入る |
| `_cmd/version.go` | `version` の値を `build/config.yml` / `frontend/package.json` に書き写す・上げるスクリプト |
| `go.work` | ルートと `_cmd/aibow` の2つのモジュールをまとめる(下の説明を参照) |
| `_cmd/aibow/lib\` | llama.cpp の DLL(自分で取得。Git には入れない) |
| `_cmd/aibow/models\` | 自分でダウンロードしたモデル(Git には入れない) |

llama.cpp の呼び出しには [yzma](https://github.com/hybridgroup/yzma) という Go のライブラリを使っています。
llama.cpp の C の関数を Go から呼べるようにするだけの薄い層で、C コンパイラ(cgo)は不要です。
会話の保存には SQLite のドライバー [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite) を使っています。
SQLite の C のコードを Go に変換したもので、こちらも C コンパイラは不要です。

Wails に依存するのは `_cmd/aibow` だけです。`_cmd/aibow` は自分の `go.mod` を持つ別のモジュールにしてあり、
ルートの `go.mod` は yzma と SQLite のドライバーだけに依存します。`go.work` は「この2つのモジュールを一緒に扱う」という Go のワークスペースの設定で、
これがあるので、`_cmd/aibow` でビルドすると、ルートの `aibow` パッケージ(手元のコード)が使われます。
(`_` で始まるフォルダは `go test ./...` などの `./...` に含まれないので、ルートのテストは Wails なしで動きます)
