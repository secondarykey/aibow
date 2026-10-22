# aibow

ローカル LLM を**自分のプログラムの中で直接動かす**、学習用のチャットアプリです。
LLM サーバーに頼らず、モデルの読み込みから1トークンずつの生成、会話の管理、ツール呼び出しまでを Go で自作しながら、
「チャット AI の中で何が起きているか」を1段ずつ確かめていきます。
「ローカル LLM でバイブコーディングできるエージェント」と「MCP(クライアントとサーバー)」まで作り、今は組織で使える形に進めています。

## できること

- **llama.cpp をプロセスの中で直接動かすチャット**: Ollama などの外部の LLM サーバーは使いません(Ollama で `ollama pull` したモデルのファイルは、そのまま読めます)。思考の表示・会話の保存・長い会話の compact
- **ミニコーディングエージェント**: ツール呼び出し・作業フォルダのファイルの読み書き・コマンドの実行(実行前に確認)
- **MCP**: 既製の MCP サーバーのツールを使えるほか、aibow のツールを MCP サーバー(`aibow-mcp`)として Claude Code などから使えます
- **aibow-server**: aibow のモデルを OpenAI 互換の API として出し、Cline などからも使えます。画面のアプリの生成を aibow-server に任せることもできます
- **ブラウザからも使える**: aibow-server がアプリと同じ画面を配るので、アプリを入れなくてもブラウザで会話できます(今は aibow-server と同じ PC から。ブラウザではツールを使いません)
- **中身が見える**: モデルに渡した文字列・トークン数・KV キャッシュの再利用数・生成速度などを、画面とログで確かめられます

## しくみ

```
ウィンドウ(React)⇄ Wails v3 ⇄ aibow(Go)⇄ yzma ⇄ llama.cpp(DLL)⇄ GPU
                                  ⇅  ⇅                 ↑
                  会話の保存(SQLite)  ⇅    GGUF のモデル / Ollama が保存したモデル
                                     ⇅
                  MCP サーバー(子プロセスとして起動する / URL でつなぐ)

生成を aibow-server に任せるとき: aibow ⇄ HTTP ⇄ aibow-server ⇄ llama.cpp ⇄ GPU
Cline など ⇄ HTTP(OpenAI 互換)⇄ aibow-server
ブラウザ ⇄ HTTP(画面と会話)⇄ aibow-server(会話は aibow-server が保存する)
```

- **言語**: Go(C コンパイラ不要。`CGO_ENABLED=0` でビルドできる)
- **LLM**: [llama.cpp](https://github.com/ggml-org/llama.cpp) を [yzma](https://github.com/hybridgroup/yzma) で Go から直接呼ぶ。主に使うモデルは Qwen3.5(4B / 9B)
- **画面**: [Wails v3](https://v3.wails.io/) のデスクトップアプリ(React + TypeScript)
- **保存**: SQLite([modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite))
- **MCP**: 公式の [Go SDK](https://github.com/modelcontextprotocol/go-sdk)
- チャットテンプレートやエージェントループなど、学習の対象になる仕組みはライブラリに任せず自分で書いています

## 動かしてみる

Windows + NVIDIA GPU を前提にしています。Go 1.27 以上・Node.js 20.19 以上・Wails の CLI(`wails3`)が必要です。

```bash
cd _cmd/aibow
go run github.com/hybridgroup/yzma@v1.29.1 install --lib lib --processor cuda
```
llama.cpp の DLL を `lib\` にダウンロードします(最初の1回だけ)。モデルは `ollama pull qwen3.5:4b` で用意するか、GGUF を `models\` に置きます。

```bash
wails3 dev
```
アプリを開発モードで起動します。詳しい手順・うまく動かないとき・実行ファイルの作り方は [_docs/setup.md](_docs/setup.md) にあります。

## ロードマップ

ロードマップ1(ローカル LLM のエージェントと MCP を自作して学ぶ)

| ステップ | 作るもの |
|---|---|
| 1. チャット ✅ | llama.cpp を直接動かすチャット |
| 2. 会話の保存・compact・ツール呼び出し ✅ | 会話の管理と、エージェントループ |
| 3. ミニコーディングエージェント ✅ | ファイル読み書き・コマンド実行のツール(実行前に確認) |
| 4. MCP クライアント ✅ | 既製の MCP サーバーにつないで、そのツールを使う |
| 5. MCP サーバー自作 ✅ | aibow のツールを MCP サーバーとして出す |
| 6. 既製ツールとの比較 ✅ | OpenAI 互換 API の口を作り、Cline などからつなぐ |

ロードマップ2(組織で使えるようにする)

| ステップ | 作るもの |
|---|---|
| 7. 設定のプロファイル ✅ | 設定に名前を付けて切り替える |
| 8. aibow-server 越しの生成 ✅ | 画面のアプリの生成を aibow-server に任せる |
| 9. ブラウザからも使う ✅ | aibow-server が同じ画面をブラウザに配る |
| 10. 利用者とログイン | 会話をサーバーに置き、ほかの PC からも使う |
| 11. 共有 | 管理者・会話の公開範囲・共有の作業 |

進み具合の詳細は [TODO.md](TODO.md) にあります。

## ドキュメント

| 読むもの | 内容 |
|---|---|
| [_docs/setup.md](_docs/setup.md) | 準備と起動(llama.cpp とモデルの用意、起動、実行ファイル、インストール) |
| [_docs/usage.md](_docs/usage.md) | 画面の使い方、設定と環境変数、各ステップで観察してほしいこと |
| [_docs/structure.md](_docs/structure.md) | 全体の構成とファイルの役割 |
| [_docs/](_docs/) | 仕組みごとの説明(エンジン・会話・compact・ツール・画面など)と、機能の仕様(`features/`) |
| [AGENTS.md](AGENTS.md) | このリポジトリで作業する AI エージェント向けの指示(方針・技術の決まりごと) |

## ライセンス

[MIT](LICENSE)
