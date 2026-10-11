# aibow — ステップ1: 自作チャット

ローカルLLMを自分のプログラムの中で直接動かす、最小のチャットアプリです。
このアプリを少しずつ育てて、最終的に「ローカルでバイブコーディングできるエージェント」と「MCP」まで進みます。

```
ウィンドウ(_cmd/aibow/frontend の React) ⇄ Wails ⇄ aibow ⇄ 関数呼び出し ⇄ llama.cpp(_cmd/aibow/lib の DLL)⇄ GPU
                                                              ↑
                                  _cmd/aibow/models\*.gguf  または  Ollama が保存したモデル
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
| `engine.go` | llama.cpp を呼んで文章を生成する部分(モデルの読み込み・入れ替え、トークン化、計算、選択の繰り返し) |
| `models.go` | 使えるモデルの一覧を作る(`models\` フォルダと Ollama の保存フォルダ) |
| `prompt.go` | 会話を、モデルが理解する書式(ChatML)の1本の文字列にする |
| `stream.go` | 生成された文字を「思考」と「答え」に振り分ける |
| `*_test.go` | テスト(モデルなしで実行できる) |
| `_cmd/aibow/main.go` | デスクトップアプリの起動(Wails のアプリとウィンドウを作る) |
| `_cmd/aibow/config.go` | 環境変数から設定を読む |
| `_cmd/aibow/service.go` | 画面から呼ばれる Go のメソッド(モデルの一覧・切り替え・チャット) |
| `_cmd/aibow/frontend/src/` | チャット画面(React + TypeScript)。`App.tsx` が全体、`useChat.ts` / `useModels.ts` が Go とのやり取り |
| `_cmd/aibow/frontend/bindings/` | `wails3 generate bindings` が作る、Go のメソッドを呼ぶ関数と型(自動生成。手で直さない) |
| `_cmd/aibow/build/` / `Taskfile.yml` | `wails3 dev` / `wails3 build` の設定(アプリ名、アイコン、OS ごとのビルド手順) |
| `go.work` | ルートと `_cmd/aibow` の2つのモジュールをまとめる(下の説明を参照) |
| `_cmd/aibow/lib\` | llama.cpp の DLL(自分で取得。Git には入れない) |
| `_cmd/aibow/models\` | 自分でダウンロードしたモデル(Git には入れない) |

llama.cpp の呼び出しには [yzma](https://github.com/hybridgroup/yzma) という Go のライブラリを使っています。
llama.cpp の C の関数を Go から呼べるようにするだけの薄い層で、C コンパイラ(cgo)は不要です。

Wails に依存するのは `_cmd/aibow` だけです。`_cmd/aibow` は自分の `go.mod` を持つ別のモジュールにしてあり、
ルートの `go.mod` は yzma だけに依存します。`go.work` は「この2つのモジュールを一緒に扱う」という Go のワークスペースの設定で、
これがあるので、`_cmd/aibow` でビルドすると、ルートの `aibow` パッケージ(手元のコード)が使われます。
(`_` で始まるフォルダは `go test ./...` などの `./...` に含まれないので、ルートのテストは Wails なしで動きます)

## 準備(最初の1回だけ)

bash(Git Bash など)で、アプリのフォルダ `_cmd/aibow` に移動して作業します。
llama.cpp の DLL(`lib\`)とモデル(`models\`)は、モデルを動かすこのアプリの持ち物なので、アプリのフォルダに置きます。

```bash
cd /d/Go/Projects/aibow/_cmd/aibow
```
以降のコマンドは、すべてこのフォルダで実行します。

以前の版(ブラウザ版)でリポジトリのルートに `lib\` と `models\` を置いていた場合は、ダウンロードし直さなくても、
ルートの `lib\` と `models\` をフォルダごと `_cmd\aibow\` へ移せば準備の 3・4 は済みます。

### 1. 道具の確認

```bash
go version
```
Go のバージョンを表示します。yzma が Go 1.26 以上を必要とするため、`go.mod` に `go 1.26.0` と書いてあります。
手元の Go が古い場合でも、Go が必要なバージョンを自動でダウンロードして使います(Go 1.21 以降の機能)。

```bash
node --version
```
Node.js のバージョンを表示します。画面(React)のビルドに使う npm は Node.js に付いてきます。入っていなければ https://nodejs.org/ から入れてください。

```bash
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.26
```
Wails の CLI(`wails3` コマンド)を入れます。バージョンは `_cmd/aibow/go.mod` の Wails と合わせます。

```bash
wails3 doctor
```
Wails でビルドするのに必要なもの(Windows なら WebView2 など)がそろっているかを確認します。

### 2. 依存ライブラリの取得

```bash
go mod tidy
```
`_cmd/aibow/go.mod` に書いてあるライブラリ(Wails と yzma など)をダウンロードし、改ざん検知用のチェックサムを `go.sum` に書き出します。

```bash
cd frontend && npm install && cd ..
```
画面で使うパッケージ(React、Vite、Wails の JavaScript 用ランタイムなど)を `frontend/node_modules/` にダウンロードします。
`wails3 dev` / `wails3 build` は `node_modules` が無いときだけ自動で `npm install` するので、パッケージを増減したときは手で実行します。

### 3. llama.cpp の取得

```bash
go run github.com/hybridgroup/yzma@v1.29.1 install --lib lib --processor cuda
```
yzma に付属しているインストーラーを、インストールせずにその場で1回だけ実行します。aibow のビルドとは無関係の、準備用の道具です。

- `go run パッケージ@バージョン` … 指定したプログラムをダウンロードしてビルドし、そのまま実行する
- `install` … llama.cpp のビルド済み DLL を GitHub からダウンロードする
- `--lib lib` … 置き場所。相対パスはコマンドを実行したフォルダが基準なので、`_cmd\aibow\lib\` にできる
- `--processor cuda` … NVIDIA GPU 用のビルドを選ぶ。CUDA の実行に必要な DLL(cudart)も一緒に入る

`go run パッケージ@バージョン` は今いるフォルダの `go.mod` / `go.work` を見ないので、`_cmd/aibow` で実行しても問題ありません。
バージョン(`@v1.29.1`)は、ルートの `go.mod` に書いてある yzma のバージョンと合わせてください。yzma と llama.cpp は組み合わせが決まっています。

GitHub の llama.cpp のリリースページから zip を手で落として `_cmd\aibow\lib\` に展開しても同じです。

### 4. モデルの用意

次のどちらでも構いません。両方あれば、両方とも画面の一覧に出ます。

**A. Ollama で管理する**

```bash
ollama pull qwen3.5:4b
```
Ollama がモデルをダウンロードして保存します。すでに pull 済みなら何もしなくて構いません。
aibow は Ollama の保存フォルダ(既定は `C:\Users\<ユーザー>\.ollama\models`、環境変数 `OLLAMA_MODELS` があればそちら)を見に行きます。

注意: Ollama のモデルの中には、Ollama 独自の形式になっていて llama.cpp では読めないものがあります。
読めなかった場合は画面にエラーが出て、元のモデルに戻ります。その場合は B の方法で GGUF を用意してください。

**B. GGUF ファイルを自分で置く**

GGUF は llama.cpp 用のモデルファイルの形式です。ダウンロードして `_cmd\aibow\models\` フォルダに置くだけです。例えば Qwen3.5 4B なら、次の URL をブラウザで開くとダウンロードが始まります(約3GB)。

https://huggingface.co/bartowski/Qwen_Qwen3.5-4B-GGUF/resolve/main/Qwen_Qwen3.5-4B-Q4_K_M.gguf

`Q4_K_M` は「重みを4ビット程度に圧縮したもの」という意味で、元のサイズの約1/4になり、VRAM 8GB に余裕で収まります。

## 起動

```bash
cd /d/Go/Projects/aibow/_cmd/aibow
```
アプリのフォルダに移動します。`lib\` と `models\` はカレントフォルダから探すので、必ずここで起動します。

```bash
wails3 dev
```
開発モードで起動します。中では次のことが順に行われます。

- Go のコードからバインディング(`frontend/bindings/`)を作り直す
- Go をビルドする(`bin/aibow.exe`)
- 画面の開発サーバー(Vite)を起動する。画面のコードを保存すると、ウィンドウにすぐ反映される
- アプリを起動する。`_cmd/aibow` の Go のコードを保存すると、ビルドし直して起動し直す

リポジトリのルートの `aibow` パッケージ(`engine.go` など)は見張っていないので、そちらを変えたときは `Ctrl+C` で止めて起動し直してください。

起動すると、次のようなログが出ます。

```
計算装置: CPU [CPU] <CPU の名前>
計算装置: CUDA0 [GPU] <GPU の名前>(VRAM 空き ○ / ○ GB)
モデルの置き場所: models / Ollama: C:\Users\<ユーザー>\.ollama\models
  ollama   qwen3.5:4b                               ○ GB
  ollama   qwen3.5:9b                               ○ GB
モデルを読み込み中: qwen3.5:4b (...)
読み込み完了 (○ 秒): <モデルの種類> / ○B パラメータ / ○ GB / ○ 層 / GPU に載せる層: 999 / コンテキスト長: 16384
aibow 起動
```

ウィンドウが開きます。モデルはウィンドウが出てから裏で読み込むので、読み終わるまでは送信できません。
止めるときはウィンドウを閉じます(VRAM も解放され、`wails3 dev` も終わります)。生成中に閉じても、生成を止めてから終了します。

### 実行ファイルを作る

```bash
wails3 build
```
画面をビルドして埋め込み、アイコン付きの実行ファイル `bin\aibow.exe` を作ります。コンソールは出ません。

```bash
wails3 task run
```
`bin\aibow.exe` を、`_cmd/aibow` をカレントフォルダにして起動します。`bin\aibow.exe` をエクスプローラーからダブルクリックすると
カレントフォルダが `bin\` になり、`lib\` が見つかりません。その場合は、起動できない理由がウィンドウに表示されます
(`AIBOW_LIB` / `AIBOW_MODELS_DIR` を絶対パスで設定しておけば、どこから起動しても動きます)。

- `計算装置` に GPU が出てこない場合は、GPU 用ではない llama.cpp が入っているか、ドライバーが古い可能性があります(手順3の `--processor cuda` を確認)。
- Ollama のモデルが一覧に出てこない場合は、ログの `Ollama:` の場所が正しいか確認してください。違う場所に保存しているなら、環境変数 `OLLAMA_MODELS` で指定します。

### 起動時に落ちた場合

`GGML_ASSERT(...) failed` と出て落ちたことが一度ありましたが、同じ条件で10回起動し直しても再現しませんでした(原因は未特定)。
もう一度起きたら、llama.cpp 自身のログを出して起動し、落ちる直前のログと、そのとき動いていた他のアプリ(Ollama、ゲームなど)を記録してください。

```bash
AIBOW_LLAMA_LOG=1 wails3 dev 2>&1 | tee aibow.log
```
`2>&1` でエラー出力も含めて、`tee` で画面に出しつつ `aibow.log` にも保存します。

### モデルの切り替え

画面上部のドロップダウンで選ぶと、aibow が今のモデルを VRAM から降ろして、新しいモデルを読み込みます(数秒〜十数秒)。
会話はそのまま続けられるので、同じ質問を 4B と 9B で比べる、といった使い方ができます。
回答の下の統計行の先頭に、どのモデルの返事かが表示されます。

aibow を起動したまま `ollama pull` した場合は、aibow のウィンドウに戻ると一覧に出てきます(ウィンドウにフォーカスが戻るたびに一覧を作り直しています)。

### 設定の変更

環境変数で変えられます。bash では `名前=値 コマンド` と書くと、そのコマンドにだけ渡せます(`export 名前=値` ならそのターミナルの中でずっと有効)。

| 環境変数 | 既定値 | 内容 |
|---|---|---|
| `AIBOW_MODEL` | 一覧の先頭 | 起動時に読み込むモデル。`ollama:qwen3.5:4b` のような Ollama の名前か、GGUF ファイルのパス |
| `AIBOW_MODELS_DIR` | `models` | GGUF ファイルを探すフォルダ(相対パスはカレントフォルダが基準) |
| `OLLAMA_MODELS` | `%USERPROFILE%\.ollama\models` | Ollama の保存フォルダ(Ollama 自身と同じ変数) |
| `AIBOW_LIB` | `lib` | llama.cpp の DLL があるフォルダ(相対パスはカレントフォルダが基準) |
| `AIBOW_CTX` | `16384` | コンテキスト長(一度に扱えるトークン数 = プロンプト + 返事)。増やすと長い会話ができるが VRAM を使う(Qwen3.5 4B で 1 トークンあたり約 32KB) |
| `AIBOW_GPU_LAYERS` | `999` | GPU に載せる層の数。VRAM に入りきらないモデルは減らす(残りは CPU で計算) |
| `AIBOW_MAX_TOKENS` | `8192` | 1回の返事で生成するトークン数の上限(思考 + 答えの合計) |
| `AIBOW_PRESENCE_PENALTY` | `1.5` | すでに生成した言葉を選ばれにくくする強さ。思考の堂々巡りを抑える。`0` で無効 |
| `AIBOW_LLAMA_LOG` | なし | `1` にすると llama.cpp 自身の詳しいログ(どの層をどこに置いたか、読めない理由など)を出す。起動時に数千行出る |

```bash
AIBOW_MODEL=ollama:qwen3.5:4b wails3 dev
```

Git の worktree など、別の場所にあるコードから起動するときは、`lib\` と `models\` は Git に入っていないので、本体の場所を指定します。
`frontend/node_modules/` も Git に入っていないので、worktree の `_cmd/aibow/frontend` で `npm install` するか、本体の `node_modules` へのジャンクションを作ります。

```bash
AIBOW_LIB=/d/Go/Projects/aibow/_cmd/aibow/lib AIBOW_MODELS_DIR=/d/Go/Projects/aibow/_cmd/aibow/models wails3 dev
```

## このステップで観察してほしいこと

- **右パネルの「モデルに渡した文字列」**
  会話が `<|im_start|>user …<|im_end|>` という書式の1本の文字列になっていて、最後が `<|im_start|>assistant` で終わっています。
  モデルはこの「続き」を書いているだけです。送信のたびに会話全部が入っていることも分かります。
- **「思考する」のオン/オフ**
  オフにすると、文字列の最後に空の `<think></think>` が入ります。モデルは「考え終わった」と思い込んで、すぐ答え始めます。
- **統計行**
  `モデル名 ・ 初回応答 ○s ・ プロンプト ○ tok(再利用 ○)○s ・ 生成 ○ tok ・ ○ tok/s` の形式で出ます。
  プロンプトを読む速さと、1トークンずつ生成する速さが大きく違うことが分かります。
  「再利用」は KV キャッシュ(前回読み終えた部分の計算結果)を使い回せたトークン数です。
  Qwen3.5 は一部の層が途中まで巻き戻せない仕組みのため、多くの場合 0 になります。
- **モデルの切り替え**
  読み込み時間、生成速度、答えの質がモデルの大きさでどう変わるか。VRAM に載りきらないモデルで `AIBOW_GPU_LAYERS` を減らすと、どれくらい遅くなるか。
- **ターミナルのログ**
  起動時の計算装置とモデル一覧、1回ごとのトークン数・速度・終了理由が出ます。

## ロードマップ

| ステップ | 作るもの | 学べること |
|---|---|---|
| **1. チャット(今ここ)** | llama.cpp を直接動かすチャット | トークン化、生成ループ、サンプリング、チャットテンプレート、思考の仕組み |
| 2. ツール呼び出し | 時刻取得・計算などの簡単なツール | モデルが書く `<tool_call>` を読み取って実行し、結果を返して続きを書かせる「エージェントループ」 |
| 3. ミニコーディングエージェント | ファイル読み書き・一覧・コマンド実行ツール(作業フォルダ限定・実行前に確認) | バイブコーディングツールの中身。安全策の必要性 |
| 4. MCPクライアント | ステップ3のツールを MCP サーバー経由に置き換え、既存の MCP サーバーもつなぐ | MCP が「ツールの差し込み口の共通規格」であること |
| 5. MCPサーバー自作 | Go で自作サーバー | 同じサーバーを aibow と Claude Desktop の両方から使える |
| 6. 既製ツールとの比較 | aibow に OpenAI 互換 API の口を作り、Cline などからつなぐ | 既製ツールが裏でやっていること |
