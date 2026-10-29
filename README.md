# aibow

ローカル LLM を**自分のプログラムの中で直接動かす**チャットアプリです。
Ollama などの LLM サーバーを立てずに、アプリの中で llama.cpp を動かして会話します。ファイルを読み書きするコーディングエージェントや、MCP にも対応しています。

## できること

- **llama.cpp をプロセスの中で直接動かすチャット**: Ollama で `ollama pull` したモデルのファイルを、そのまま読めます(Ollama は起動していなくてよい)。思考の表示・会話の保存・長い会話の compact
- **ミニコーディングエージェント**: 作業フォルダのファイルの読み書き・コマンドの実行(実行前に確認)
- **MCP**: 既製の MCP サーバーのツールを使えるほか、aibow のツールを MCP サーバー(`aibow-mcp`)として Claude Code などから使えます
- **組織で使う(aibow-server)**: 1台の GPU の PC でモデルを動かし、ほかの PC のアプリ・ブラウザ・Cline など(OpenAI 互換の API)から使えます。
  ログインと利用者・会話やプロファイルの共有・管理者による使えるモデルの管理もできます
- **中身が見える**: モデルに渡した文字列・トークン数・KV キャッシュの再利用数・生成速度などを、画面とログで確かめられます

## 使ってみる

いちばん小さい形(この PC の中でモデルを動かすデスクトップのアプリ)で動かします。
配布用の実行ファイルはまだ無いので、手元でビルドします。Windows + NVIDIA GPU を前提にしています。

要るもの:[Go](https://go.dev/) 1.27 以上・[Node.js](https://nodejs.org/) 20.19 以上・[Git](https://git-scm.com/)(コマンドは Git Bash で打つ前提)と、Wails の CLI。

```bash
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.26
```

### 1. 取ってくる

```bash
git clone https://github.com/secondarykey/aibow.git
cd aibow/_cmd/aibow
```
以降は、アプリのフォルダ `_cmd/aibow` で打ちます。

### 2. llama.cpp の取得

```bash
go run github.com/hybridgroup/yzma@v1.29.1 install --lib lib --processor cuda
```
llama.cpp の DLL を `lib\` にダウンロードします(最初の1回だけ。GPU が無ければ `--processor cpu`)。Go があれば、aibow のソースが無いフォルダでも打てます。

- **版**:今は **yzma v1.29.1 → llama.cpp v0.6.0(ビルド b11429)** の組み合わせです。違う版だと読み込めないことがあります(上のコマンドはこの版を入れます)
- **Go が無いとき**:llama.cpp のリリース [b11429](https://github.com/ggml-org/llama.cpp/releases/tag/b11429) から `llama-b11429-bin-win-cuda-13.4-x64.zip` と `cudart-llama-bin-win-cuda-13.4-x64.zip` を落とし、どちらも `lib\` に展開します

### 3. モデルの用意

`ollama pull qwen3.5:4b` で取るか、GGUF のファイル(例: [Qwen3.5 4B の Q4_K_M](https://huggingface.co/bartowski/Qwen_Qwen3.5-4B-GGUF/resolve/main/Qwen_Qwen3.5-4B-Q4_K_M.gguf)。約3GB)を `models\` に置きます。

### 4. ビルドして起動する

```bash
wails3 build
./bin/aibow.exe
```
ウィンドウが出たら、入力欄の下でモデルを選んで話しかけます。`lib\` と `models\` はカレントフォルダから探すので、`_cmd/aibow` で起動します。
起動できないときは、理由がウィンドウに出ます。

## もっと使う

| やりたいこと | 読むもの |
|---|---|
| うまく動かない・環境変数で場所を変える | [_docs/setup.md](_docs/setup.md) |
| 画面の使い方・設定・ツールと MCP | [_docs/usage.md](_docs/usage.md) |
| aibow-server で組織で使う(ブラウザ・ほかの PC・利用者の管理・HTTPS) | [_docs/setup.md](_docs/setup.md) の「aibow-server を使い始める」。初めて起動したときは管理者がいないので、まずこの手順で作ります |
| 中身を知りたい・自分でビルドを変えたい | [DEVELOPMENT.md](DEVELOPMENT.md) |

## ライセンス

[MIT](LICENSE)
