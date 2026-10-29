# aibow

ローカル LLM を**自分のプログラムの中で直接動かす**チャットアプリです。
Ollama などの LLM サーバーを立てずに、アプリの中で llama.cpp を動かして会話します。ファイルを読み書きするコーディングエージェントや、MCP にも対応しています。

仕組みを学ぶために作っている学習用のプロジェクトです。中身や作り方に興味が出たら [DEVELOPMENT.md](DEVELOPMENT.md) へどうぞ。

## できること

- **llama.cpp をプロセスの中で直接動かすチャット**: Ollama で `ollama pull` したモデルのファイルを、そのまま読めます(Ollama は起動していなくてよい)。思考の表示・会話の保存・長い会話の compact
- **ミニコーディングエージェント**: 作業フォルダのファイルの読み書き・コマンドの実行(実行前に確認)
- **MCP**: 既製の MCP サーバーのツールを使えるほか、aibow のツールを MCP サーバー(`aibow-mcp`)として Claude Code などから使えます
- **aibow-server**: aibow のモデルを OpenAI 互換の API として出し、Cline などからも使えます。ブラウザから同じ画面で会話することも、ほかの PC のアプリから使うこともできます
- **利用者と共有**: aibow-server にログインを付けると、会話が人ごとに分かれます。管理者は利用者と使えるモデルを管理し、会話やプロファイルを共有できます
- **中身が見える**: モデルに渡した文字列・トークン数・KV キャッシュの再利用数・生成速度などを、画面とログで確かめられます

## 使ってみる

配布用の実行ファイルはまだ無いので、手元でビルドして使います。Windows + NVIDIA GPU を前提にしています。

### 要るもの

- [Go](https://go.dev/) 1.27 以上(古くても、要る版を Go が自動で取ってきます)
- [Node.js](https://nodejs.org/) 20.19 以上(画面のビルドに使う)
- [Git](https://git-scm.com/)(コマンドは Git Bash で打つ前提で書いています)
- Wails の CLI(画面のアプリのビルドに使う):

```bash
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.26
```

### 1. 取ってくる

```bash
git clone https://github.com/secondarykey/aibow.git
cd aibow/_cmd/aibow
```
以降のコマンドは、アプリのフォルダ `_cmd/aibow` で打ちます。llama.cpp(`lib\`)とモデル(`models\`)もこのフォルダに置きます。

### 2. llama.cpp の取得

```bash
go run github.com/hybridgroup/yzma@v1.29.1 install --lib lib --processor cuda
```
llama.cpp の DLL を `lib\` にダウンロードします(最初の1回だけ)。NVIDIA GPU が無い PC では `--processor cpu` にします(遅くなります)。
aibow-server につないで使うだけの PC には要りません(下の「aibow-server を使う」)。

### 3. モデルの用意

どちらか(両方でもよい)。主に Qwen3.5(4B / 9B)で確かめています。

- Ollama で取ってくる:`ollama pull qwen3.5:4b`(Ollama が保存したファイルを aibow が直接読みます)
- GGUF を置く:`models\` に `.gguf` のファイルを置く(例: [Qwen3.5 4B の Q4_K_M](https://huggingface.co/bartowski/Qwen_Qwen3.5-4B-GGUF/resolve/main/Qwen_Qwen3.5-4B-Q4_K_M.gguf)。約3GB)

### 4. ビルドして起動する

```bash
wails3 build
./bin/aibow.exe
```
画面をビルドして埋め込んだ `bin\aibow.exe` を作り、起動します(初回はダウンロードとビルドに時間がかかります)。
ウィンドウが出たら、入力欄の下でモデルを選んで話しかけます。会話と設定は `%APPDATA%\aibow\` に保存されます。

- `lib\` と `models\` は**カレントフォルダから**探すので、`_cmd/aibow` で起動します。エクスプローラーからダブルクリックするときは、環境変数 `AIBOW_LIB`・`AIBOW_MODELS_DIR` に絶対パスを書いておきます
- 起動できないときは、理由がウィンドウに出ます。ログは `%APPDATA%\aibow\logs\` にあります

手順の詳しい説明・うまく動かないとき・環境変数は [_docs/setup.md](_docs/setup.md)、画面の使い方と設定は [_docs/usage.md](_docs/usage.md) にあります。

## aibow-server を使う(ブラウザ・ほかの PC・Cline から)

aibow-server は、モデルを動かして、ブラウザ・ほかの PC のアプリ・Cline などに使わせる別の exe です。リポジトリのフォルダ(`aibow`)で作ります。

```bash
go run ./_cmd/dev build
./aibow-server.exe -lib _cmd/aibow/lib -models _cmd/aibow/models
```
画面をビルドしてから `aibow-server.exe` を作り、起動します(Go と Node.js だけで作れます)。既定では `127.0.0.1:8089` だけで待ち受けます。
画面のアプリがアプリの中でモデルを動かしている間は、同時に動かさないでください(VRAM にモデルを2つ載せられないため)。

**初めて起動したときの状態**:利用者は `local` の1人だけで(パスワードなし)、**管理者はいません。決まったパスワードの admin も作りません**
(変え忘れたまま外に出したときに誰でも入れてしまうため)。aibow-server は**既定でログインが要る**ので、そのままではログインできる人がいません。
最初にサーバーの PC で管理者を作ります(aibow-server を動かしたままでも打てます)。

```bash
./aibow-server.exe user add alice
./aibow-server.exe user admin alice
```
`user add` は、本人がパスワードを決めるリンク(`http://127.0.0.1:8089/#reset=…`。1 時間・一度だけ)を出します。ブラウザで開いてパスワード(12 文字以上)を決めると、そのままログインして会話できます。
管理者は、設定の画面の「利用者」のタブで、ほかの人を作って招待のリンクを渡せます。

- **画面のアプリからつなぐ**:アプリの設定の画面の「サーバー」のタブで、aibow-server が起動のときに出すつなぎ先を入れ、ログインします。
  つなぐだけの PC には `lib\`・`models\` は要りません(初めは「起動できません」と出ますが、そのまま「サーバー」のタブでつないで起動し直します)
- **ほかの PC から使う**:証明書を作り(`aibow-server cert <アドレス>`)、`-host 0.0.0.0` で起動します。外で待ち受けるとログインが必ず要ります
- **Cline など**:Base URL は `http://127.0.0.1:8089/v1`。API キー(`aibow-server user key <名前>`)が要ります
- 1人で自分の PC だけで使うなら、`-login=false` でログインなしにもできます(127.0.0.1 の外で待ち受けるときは効きません)

手順の続き(ほかの PC から使う準備・パスワードを忘れたとき)は [_docs/setup.md](_docs/setup.md)、設定ファイルと細かい使い方は [_docs/usage.md](_docs/usage.md) の「aibow-server」にあります。

## ドキュメント

| 読むもの | 内容 |
|---|---|
| [_docs/setup.md](_docs/setup.md) | 準備と起動の詳しい手順・うまく動かないとき・aibow-server を始める・ほかの PC から使う |
| [_docs/usage.md](_docs/usage.md) | 画面の使い方、設定と環境変数、aibow-server・MCP・ツールの使い方 |
| [DEVELOPMENT.md](DEVELOPMENT.md) | 開発する人向け:しくみ・開発モードで動かす・確かめ方・ロードマップ・仕組みごとの説明への案内 |

## ライセンス

[MIT](LICENSE)
