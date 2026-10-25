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
- **ブラウザからも使える**: aibow-server がアプリと同じ画面を配るので、アプリを入れなくてもブラウザで会話できます(ブラウザではツールを使いません)
- **利用者と共有**: aibow-server にログインを付けると、会話が人ごとに分かれ、ほかの PC からも使えます。管理者は利用者と使えるモデルを管理し、会話やプロファイルを共有できます
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

### 環境変数(セットアップに関わるもの)

llama.cpp とモデルは、既定ではカレントフォルダの `lib\`・`models\` を探します。ほかの場所に置いたときや、別のフォルダ(worktree など)から起動するときは、環境変数で場所を指します。

| 環境変数 | 既定 | 内容 |
|---|---|---|
| `AIBOW_LIB` | `lib` | llama.cpp の DLL のフォルダ |
| `AIBOW_MODELS_DIR` | `models` | GGUF を置くフォルダ |
| `OLLAMA_MODELS` | `%USERPROFILE%\.ollama\models` | Ollama の保存フォルダ(Ollama 自身と同じ変数) |
| `AIBOW_MODEL` | 最後に使ったモデル | 起動のときに読み込むモデル(例: `ollama:qwen3.5:4b`) |
| `AIBOW_GPU_LAYERS` | `999`(全部) | GPU に載せる層の数。VRAM に入らないときに減らす |
| `AIBOW_DB`・`AIBOW_LOG_DIR` | `%APPDATA%\aibow\` の下 | 会話の DB とログの場所(試すときは一時フォルダにすると、ふだんの会話と混ざらない) |
| `AIBOW_SERVER_URL` | なし | 画面のアプリを aibow-server につなぐ(画面の「サーバー」のタブでもつなげる) |

```bash
AIBOW_LIB=/d/aibow/lib AIBOW_MODELS_DIR=/d/aibow/models wails3 dev
```

- **読むのは起動のときだけ**です。変えたら起動し直します
- **画面で変えられる設定**(GPU の層数・会話の上限など)の環境変数は、まだ画面で変えていないときの初期値です。画面で一度変えると、画面の値が優先されます
- **aibow-server のつなぎ先**は、`settings.json` の `serverUrl` > `AIBOW_SERVER_URL` > 画面で決めたもの、の順に強く、手で書いてあれば画面では変えられません
- **aibow-server** は、起動の引数 > 設定ファイル `server.json` > 環境変数、の順に強くなります。アプリと同じ名前の環境変数(`AIBOW_LIB`・`AIBOW_MODELS_DIR` など)を読みます

全部の環境変数と効き方は [_docs/usage.md](_docs/usage.md) の「環境変数の効き方」と表にあります。

### aibow-server を動かす(ブラウザ・Cline・ほかの PC から使うとき)

画面のアプリとは別の exe です。ブラウザに配る画面(`_cmd/aibow/frontend`)を exe に埋め込むので、**画面のビルドと一緒にする入り口を使います**
(`go build` だけで作ると、前にビルドした古い画面が入ります)。どちらもリポジトリのフォルダで実行し、要るのは Go と Node.js です(wails3 は要りません)。

開発で動かすときは、画面を vite の開発用サーバーから配り、aibow-server を `go run` で動かします(画面のソースを保存すると、ブラウザにすぐ出ます)。
ブラウザは `http://127.0.0.1:9246/` を開きます。後ろに aibow-server の引数を付けられます。

```bash
go run ./_cmd/dev server
```

exe を作るときは、画面をビルドしてから aibow-server.exe を作ります。

```bash
go run ./_cmd/dev build
./aibow-server.exe -lib _cmd/aibow/lib -models _cmd/aibow/models
```
既定では `127.0.0.1:8089` だけで待ち受け、**ログインが要ります**。ブラウザで `http://127.0.0.1:8089/` を開いてログインすると、アプリと同じ画面で会話できます。
画面のアプリがアプリの中でモデルを動かしている間は、同時に動かさないでください(VRAM にモデルを2つ載せられないため)。

**初めて起動したときの利用者**:`local` の1人だけで(パスワードなし)、**管理者はいません。既定の admin(決まったパスワードの管理者)も作りません**。
決まったパスワードの管理者がいると、変え忘れたまま外に出したときに誰でも入れてしまうので、最初の管理者はサーバーの PC でコマンドを打てる人が決めます。
そのままではログインできる人がいないので、最初にサーバーの PC で管理者を作ります(aibow-server を動かしたままでも打てます。パスワードは `user add` のときだけ表示されます)。
管理者がいない間は、起動のときのログとブラウザのログインの画面に、このことが出ます。

```bash
./aibow-server.exe user add alice
./aibow-server.exe user admin alice
```
管理者でブラウザからログインすると、設定の画面の「利用者」のタブでほかの人を作れます。

1人で自分の PC だけで使うなら、`-login=false`(`server.json` の `"login": false`)でログインなしにもできます(利用者は `local` だけ。127.0.0.1 の外で待ち受けるときは、書いてもログインが要ります)。

- **画面のアプリからつなぐ**:アプリの設定の画面の「サーバー」のタブで、aibow-server が起動のときに出すつなぎ先を入れ、ログインします(API キーは要りません)
- **ほかの PC から使う**:証明書を作り(`aibow-server cert <アドレス>`)、`-host 0.0.0.0` で起動します。外で待ち受けるとログインが必ず要ります。手順は [_docs/setup.md](_docs/setup.md) の「ほかの PC から使えるようにする」
- **Cline など**:Base URL は `http://127.0.0.1:8089/v1`。API キー(`aibow-server user key <名前>`)が要ります(ログインなしにしたときだけ要らない)

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
| 10. 利用者とログイン ✅ | 会話をサーバーに置き、ほかの PC からも使う |
| 11. 共有 ✅ | 管理者・会話の公開範囲・共有の作業 |

進み具合の詳細は [TODO.md](TODO.md) にあります。

## ドキュメント

| 読むもの | 内容 |
|---|---|
| [_docs/setup.md](_docs/setup.md) | 準備と起動(llama.cpp とモデルの用意、起動、実行ファイル、インストール、aibow-server をログインありで始める・ほかの PC から使う) |
| [_docs/usage.md](_docs/usage.md) | 画面の使い方、設定と環境変数、各ステップで観察してほしいこと |
| [_docs/structure.md](_docs/structure.md) | 全体の構成とファイルの役割 |
| [_docs/](_docs/) | 仕組みごとの説明(エンジン・会話・compact・ツール・画面など)と、機能の仕様(`features/`) |
| [AGENTS.md](AGENTS.md) | このリポジトリで作業する AI エージェント向けの指示(方針・技術の決まりごと) |

## ライセンス

[MIT](LICENSE)
