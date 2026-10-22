# AGENTS.md

このリポジトリで作業する AI エージェント向けの指示です。
ここには**全体に関わること**だけを書きます。個別の仕組みの詳細は、下の「作業内容ごとに読むもの」から辿ってください。
触らない部分の説明は読まなくて構いません。

## プロジェクトの目的

aibow は、ローカルLLMを使ったアプリを**自分で作りながら仕組みを学ぶ**ための学習プロジェクトです。
最終目標は次の2つです。

- ローカルLLMでバイブコーディングできるエージェントを自作し、その中身を理解する
- MCP の使い方と作り方(クライアント・サーバー両方)を理解する

完成品を早く作ることより、**各段階で何が起きているかが読んで分かること**を優先してください。

## 全体の構成(ステップ9: ブラウザからも使う)

```
ウィンドウ(_cmd/aibow/frontend の React) ⇄ Wails v3 ⇄ aibow/ui ⇄ aibow ⇄ aibow/engine ⇄ yzma ⇄ llama.cpp(_cmd/aibow/lib\ の DLL)⇄ GPU
                                                                ⇅          ↑ aibow/format(書式)・aibow/models(_cmd/aibow/models\*.gguf / Ollama の保存フォルダ)
                                                     aibow/store(SQLite: %APPDATA%\aibow\aibow.db)
                                                     aibow/mcp/client ⇄ MCP のサーバー(%APPDATA%\aibow\settings.json に書いたもの。子プロセス)

MCP のクライアント(Claude Code・aibow など)⇄ 標準入出力 ⇄ aibow-mcp(_cmd/aibow-mcp)= aibow/mcp/server ⇄ aibow/tool
aibow の組み込みのツール: 会話の仕組み ⇄ aibow/mcp/client の Local ⇄ MCP(メモリ内)⇄ aibow/mcp/server ⇄ aibow/tool

OpenAI 互換のクライアント(Cline など)⇄ HTTP(127.0.0.1)⇄ aibow-server(_cmd/aibow-server)= aibow/api ⇄ aibow/engine
画面のアプリを aibow-server につなぐとき(settings.json の serverUrl。ステップ8):aibow ⇄ aibow/api/client ⇄ HTTP ⇄ aibow-server(アプリは llama.cpp を読み込まない。
  つながないとき(既定)は上の図のとおりアプリの中で動かし、そのときは aibow-server と同時に使わない)
ブラウザ(http://127.0.0.1:8089/。ステップ9)⇄ HTTP(127.0.0.1。画面と /aibow/ui/ の呼び出しと SSE)⇄ aibow-server = aibow/ui ⇄ aibow(ツールなし)⇄ aibow/engine
  (画面は aibow/web に埋め込んだもので、デスクトップのアプリと同じ。会話は aibow-server の server.db。生成は OpenAI 互換の口と同じ engine・同じ順番待ちの席 api.Seat)
```

- **Ollama などの外部 LLM サーバーは使わない**。aibow のプロセスの中で llama.cpp を直接動かす。
  これはユーザーの明確な方針なので、LLM サーバーを別プロセスで立てる構成を提案・導入しないこと
  (aibow 自身の `aibow-server` は別。中で動くのは aibow の engine と llama.cpp で、ユーザーが決めて作ったもの。ステップ6・8)
- ただし **Ollama を「モデルの管理係」として使うのは OK**(ユーザーの方針)。Ollama が保存したファイルを aibow が直接読む。Ollama のサーバーや API は使わない
- 画面は Wails v3 のデスクトップアプリ。画面のアプリは HTTP サーバーを立てない(以前のブラウザ版から移行した。OpenAI 互換の API は別の exe `aibow-server` が立てる)。
  ブラウザから使うときは、`aibow-server` が同じ画面(`web`)を配り、会話も持つ(ステップ9。[_docs/api.md](_docs/api.md) の「画面用の口」)。
  GUI の無い環境で確かめるための Wails の server モードは、確認用の一時ビルドだけで使う([_docs/env-cloud.md](_docs/env-cloud.md))

| 場所 | 中身 |
|---|---|
| ルート(`aibow` パッケージ。モジュール名は `github.com/secondarykey/aibow`) | 会話の仕組み(会話・エージェントループ・compact・設定・保存の約束 `Store`・生成の約束 `Generator`・会話の持ち主を `ctx` で運ぶ `WithUser`。0017)。Wails を import しないライブラリ |
| `engine/` | llama.cpp で生成する部分(`engine` パッケージ。モデルの読み込み・生成・トークン数・KV キャッシュ)。`format` と `models` を使い、会話の仕組みは知らない(生成だけを使えるように。0012 の 6-0) |
| `format/` | 書式(`format` パッケージ。チャットテンプレートと、会話の仕組みとエンジンの間で受け渡す形 `Message` / `Prompt` / `Output` / `ToolCall`)。`tool` だけを使う |
| `models/` | 使えるモデルの一覧(`models` パッケージ。GGUF と Ollama の目録と、画面で足した置き場所。ステップ8)と、GGUF の先頭からモデルの種類だけを読む `Arch`。何にも依存しない |
| `tool/` | ツール(`tool` パッケージ)。ツールの形と組み込みのツール・作業フォルダのツール。LLM・書式・会話・保存を import しない(MCP でツールだけを使うため) |
| `store/` | 会話の保存先(SQLite) |
| `mcp/client/` | MCP のクライアント(`client` パッケージ。SDK のパッケージ名 `mcp` と重ならないよう `mcp/` の下に置く。サーバーは `mcp/server/` に並べる)。`settings.json` の MCP のサーバーを起動し、そのツールを `tool.Tool` として見せる。aibow の中で `mcp/server` を動かし、組み込みのツールを MCP 越しに渡す(`Local`)。`tool` と `mcp/server` を import し、ルートの `aibow` は import しない |
| `mcp/server/` | MCP のサーバー(`server` パッケージ。ステップ5)。`tool` のツールを MCP のツールとして出す。書き換えるツールはサーバーからユーザーに確かめる(MRTR)。`tool` と SDK だけを使い、ルートの `aibow` は import しない |
| `api/` | OpenAI 互換の API(`api` パッケージ。ステップ6)。`engine` で生成だけを使い、ルートの `aibow`(会話の仕組み)は import しない。HTTP は標準の `net/http`。ステップ8で aibow 専用の口と欄(モデルの一覧・読み込み・トークン数など)を足した。生成の順番待ちの席 `Seat` はブラウザの会話(`ui`)と分け合う(ステップ9) |
| `auth/` | aibow-server の利用者とログイン(`auth` パッケージ。ステップ10の 10-1)。パスワードのハッシュ(`crypto/pbkdf2`)・cookie のセッション・ログインの口(`/aibow/auth/`)。認証だけを受け持ち、ルートの `aibow` は import しない。保存は `store`(server.db) |
| `api/client/` | aibow-server につなぐクライアント(`client` パッケージ。ステップ8)。会話の仕組みの `Generator` と `ModelLoader` の HTTP 版。ルートの `aibow` は import しない |
| `ui/` | 画面向けの処理(`ui` パッケージ。ステップ9の 9-0 で `_cmd/aibow` から切り出した)。画面から呼ぶメソッド(`Service`。モデル・会話・設定・送信)と、モデルを扱う係(直接 `DirectHost`・aibow-server 越し `RemoteHost`)。ルートの `aibow` を使い、Wails を import しない。デスクトップのアプリは `ChatService` に埋め込んで Wails に渡す。ブラウザ向けには aibow-server が使う:aibow-server の engine でモデルを扱う係(`ServerHost`)と、画面用の HTTP の口(`Handler`。`/aibow/ui/call/{メソッド}` と SSE の `/aibow/ui/events`)。9-1。仕組みは [_docs/api.md](_docs/api.md) の「画面用の口」 |
| `web/` | 画面のビルド結果(`web` パッケージ。ステップ9の 9-2)。vite が `_cmd/aibow/frontend` から `web/dist/app` に書き、デスクトップのアプリと aibow-server の両方が `web.Files()` で埋め込む(写さない)。ビルドしていなければ `web/dist/index.html`(ビルドの仕方だけのページ) |
| `mcp/internal/` | MCP のクライアントとサーバーの両方で使う部分(`meta`: `_meta` の名前) |
| `internal/proc/` | 子プロセスまでまとめて止める仕組み(`tool` の `run_command` と `mcp/client` で使う) |
| `log/` | aibow のログ(`log` パッケージ)。aibow のコードはログをここから出す。土台は wenteasy/log だが、使う人には `slog.Level` と `*slog.Logger` しか見せない。出口はアプリが `Init` で組み、ライブラリとして使う人は `SetLogger(*slog.Logger)` で aibow のログだけ別にできる(決めなければ `slog.Default()`)。詳しくは [log/AGENTS.md](log/AGENTS.md) |
| `_cmd/aibow/` | デスクトップアプリ(Wails v3 + React)。別モジュール |
| `_cmd/aibow-server/` | aibow のサーバーの exe(OpenAI 互換の API。`127.0.0.1` で待ち受ける。ステップ8で `aibow-api` から名前を変えた)。引数・設定ファイル(`server.json`)・環境変数を読み、モデルを読み込んで `api` を動かす。ブラウザの会話(`ui` と `server.db`)も同じポートに載せる(ステップ9)。ルートのモジュールに入れている |
| `_cmd/aibow-mcp/` | MCP のサーバーの exe(標準入出力)。引数を読んで `mcp/server` を起動するだけ。ルートのモジュールに入れている(Wails を使わないため) |
| `_docs/` | 仕組みごとの説明(下の表)と、機能の仕様(`_docs/features/`)。利用者向けの手順は `setup.md`(準備と起動)・`usage.md`(使い方と設定)・`structure.md`(構成とファイル) |
| `TODO.md` | 全体の進み具合とロードマップ |

## 作業内容ごとに読むもの

| 作業内容 | 読むもの |
|---|---|
| 生成・モデルの読み込みと切り替え・トークン化・プロンプトの書式・思考の振り分け・モデルの一覧・`engine` / `format` / `models` のパッケージの分け方 | [_docs/engine.md](_docs/engine.md) |
| ツール呼び出し・エージェントループ・書式の interface(`Format`)・ツールの追加・作業フォルダ・ファイルとコマンドのツール・実行前の確認 | [_docs/tools.md](_docs/tools.md) |
| MCP のクライアント・設定ファイル(`settings.json`)・MCP のサーバーの起動と終了・ツールの扱い(使わない / 確認する / 確認しない)と信頼・自作の MCP サーバー(`mcp/server`・`aibow-mcp`) | [_docs/mcp.md](_docs/mcp.md) |
| OpenAI 互換の API(`api`・`aibow-server`)・リクエストの変換・ツール呼び出しの変換(`tools` ⇄ `tool_calls`)・ストリーミング・思考の返し方・順番待ち | [_docs/api.md](_docs/api.md) |
| 会話の履歴・会話ごとの設定(会話の上限・返事の上限も)・アプリの設定・保存の約束(`Store`)・モデルに渡さない発言・作り直す / 送り直す | [_docs/conversations.md](_docs/conversations.md) |
| 会話の上限・compact(truncate / summary)・メーター | [_docs/compact.md](_docs/compact.md) |
| SQLite の実装・マイグレーション | [store/AGENTS.md](store/AGENTS.md) |
| ログの出し方・出口(`Init`)・ライブラリとして使う人のログ(`SetLogger`)・異常終了の記録 | [log/AGENTS.md](log/AGENTS.md)(決めたこととやり残しは [_docs/features/0008-log.md](_docs/features/0008-log.md)) |
| アプリの起動・`ChatService`・環境変数の読み込み・Go と画面のやり取り | [_cmd/aibow/AGENTS.md](_cmd/aibow/AGENTS.md) |
| 画面(配置・会話の一覧・発言と返事の操作・フォーカス・スクロール・ウィンドウの移動とリサイズ・IME) | [_docs/ui.md](_docs/ui.md) |
| 返事の表示(Markdown・コードの色付け・mermaid の図と書き出し) | [_docs/rendering.md](_docs/rendering.md) |
| バージョン・ローカルデプロイ | [_docs/deploy.md](_docs/deploy.md) |
| 利用者向けの説明(README・準備と起動・使い方と設定・ファイル構成)を直す | README は外部の人向けの入口として短く保ち、詳しい説明は [_docs/setup.md](_docs/setup.md)・[_docs/usage.md](_docs/usage.md)・[_docs/structure.md](_docs/structure.md) に書く |
| アプリを起動して動作を確かめる(共通の手順と確認する項目) | [_docs/testing.md](_docs/testing.md) |
| ユーザーの Windows 環境(worktree)で起動して確かめる | [_docs/env-windows.md](_docs/env-windows.md) |
| クラウドセッション(GUI の無い Linux)で起動して確かめる | [_docs/env-cloud.md](_docs/env-cloud.md) |
| パラメータの調整・実際に動かして分かったことの記録 | [_docs/tuning.md](_docs/tuning.md) |
| 不具合の調査・`npm audit` の警告 | [_docs/known-issues.md](_docs/known-issues.md) |

## 作業の進め方

- 全体の進み具合とロードマップは `TODO.md`、1つ1つの機能の仕様は `_docs/features/<番号>-<名前>.md` にある。作業を始める前に両方を読む
- 機能の仕様は、ユーザーが Chat で相談して決めたものを書いた「発注書」。書かれている決定事項に従い、「やらないこと」は先回りして作らない
- 仕様の「未決事項」は勝手に決めず、案を示してユーザーに確認する。作業中に新しく決めたことは、その仕様のファイルに書き足す
- 着手したら仕様の「状態」と `TODO.md` を 🔧 作業中に、完了の条件を満たしたら ✅ 完了にする
- ステップは順番に進める。先のステップの機能を先回りして入れない
- 仕組みの詳細を書き足すときは、該当する `_docs/*.md` かパッケージの `AGENTS.md` に書く。このファイルには、全体に関わることと読む場所の案内だけを足す

## 技術方針

- **言語は Go**(1.27 以上。標準の `uuid` パッケージを使う)。Python など別言語の実装を持ち込まない
- Go の外部パッケージは次の6つだけ。それ以外は標準ライブラリで書く
  - yzma(github.com/hybridgroup/yzma): llama.cpp の呼び出し。`engine` パッケージだけで使う。llama.cpp との版の組み合わせは [_docs/engine.md](_docs/engine.md)
  - Wails v3: 画面。`_cmd/aibow` だけで使い、ルートの `go.mod` には入れない
  - `modernc.org/sqlite`: 会話の保存。`store` パッケージだけで使う
  - `github.com/pkg/diff`: 書き換えるツールの実行前の確認で見せる差分(diff)の計算。`tool` パッケージだけで使う(0005 の 3-2 でユーザーと決めた)
  - `github.com/modelcontextprotocol/go-sdk`: MCP の公式 Go SDK。`mcp/` の下(`mcp/client`・`mcp/server`)と、それを起動するだけの `_cmd/aibow-mcp` で使う(0006・0007 でユーザーと決めた)
  - `github.com/wenteasy/log`: ログの土台(レベルの段階と、出口の部品 `SimpleHandler` など)。`log` パッケージだけで使い、aibow を使う人には wenteasy/log の型を見せない(2026-10-14、ユーザーの指定。shinte/ikkyoku の log と同じ形)
- C コンパイラなし(`CGO_ENABLED=0`)でビルドできる状態を保つ
- 外部パッケージ(Go・npm とも)を足すときは、理由と代替案を示してユーザーに確認してから足す
- チャットテンプレートなど、学習の対象になる仕組みはライブラリ任せにせず自分で書く
- 設定は `AIBOW_` で始まる環境変数にし、[_docs/usage.md](_docs/usage.md) の表に追加する(今ある環境変数はその表と [_cmd/aibow/AGENTS.md](_cmd/aibow/AGENTS.md))。
  画面で変えられる設定の環境変数は「DB にまだ値が無いときの初期値」で、画面で変えたら DB の値が優先
- 画面で変える設定は DB、手で書く設定(その PC でどう起動するか。MCP のサーバーなど)は設定ファイル `settings.json`、画面で変えるがその PC の人が決めること(ツールの扱い)は `tools.json`([_docs/mcp.md](_docs/mcp.md))。会話をサーバーに置く形にしても手元に残るものを、会話の DB に入れない

## コードの書き方

- コメントは日本語。「何をしているか」より「なぜそうしているか」「ここで何を学べるか」を書く
- 1つの関数に処理を詰め込みすぎず、流れが上から読めるようにする
- エラーは握りつぶさず、原因と対処が分かる日本語のメッセージで画面かログに出す
- 学習用のログ(トークン数、再利用数、速度、終了理由など)は残す。仕組みが観察できることが目的の一部
- ログは `aibow/log`(`log.Infof` / `log.Warnf` / `log.Errorf` など)から出す。標準の `log` や `slog`、wenteasy/log を直接使わない(`_cmd/aibow` も同じ)。レベルは、開発中・学習で観察するものは `Info`(生成の統計・ツールの実行・compact など)、レベルを上げても常に残したいものは `Notice`(起動した版・ログと会話の保存先・計算装置・読み込んだモデル・利用者が変えた設定・確認せずに実行したツール・つながった MCP のサーバー)、注意は `Warn`、失敗は `Error`(以前の「※」はレベルで表す)

## ユーザーとのやり取り

- **コマンドやツールを使うときは、それが何をするものかを必ず説明する**。説明なしにコマンドを並べない
- 新しい概念(例: KV キャッシュ、tool_call、MCP のトランスポート)を導入するときは、短い説明を添える
- ユーザーの環境: Windows、NVIDIA GPU(VRAM 8GB)、メモリ 64GB。シェルは bash(Windows 上の Git Bash など。PowerShell の `$env:` 構文は使えない)。パスは `/d/Go/Projects/aibow` の形で書く
- 主に使うモデルは Qwen3.5(4B / 9B)。ユーザーは Ollama でのモデル管理に慣れている

## 確認方法

変更後は最低限、次を実行してください。

```bash
gofmt -l .                       # 整形が必要なファイルを表示(何も出なければ OK。_cmd も含む)
go test ./...                    # 書式(公式テンプレートとの一致)、思考とツール呼び出しの振り分け、ツール、エージェントループ、Ollama の目録、会話の仕組み、compact、設定の引き継ぎ、SQLite(マイグレーション)のテスト(モデル不要)
(cd _cmd/aibow && wails3 build)  # バインディング生成 → 画面の型チェックとビルド(tsc + vite)→ Go のビルド
go vet ./... ./_cmd/aibow ./_cmd/aibow-mcp ./_cmd/aibow-server  # よくある間違いを静的チェック(./... は _cmd を含まないので並べる)
```

画面のビルド結果は共有のパッケージ `web/`(`web/dist/app`。Git には入れない)に出て、`_cmd/aibow` と `_cmd/aibow-server` が埋め込みます。画面をビルドしていなくても `go vet` / `go build` は通ります(「画面がビルドされていません」と出すだけの `web/dist/index.html` が入る)。画面を確かめるとき・exe を作るときは、先に `wails3 build` で画面をビルドします。

アプリを起動して確かめるときは、**先に必ず [_docs/testing.md](_docs/testing.md) と、今いる環境のファイルを読んでください**。

- ユーザーの Windows 環境: [_docs/env-windows.md](_docs/env-windows.md)。worktree でやってはいけないこと(`lib\` / `models\` を作る、本体へのリンクを張る、ユーザーの DB やアプリに触れる)が書いてあります
- クラウドセッション(GUI の無い Linux): [_docs/env-cloud.md](_docs/env-cloud.md)。画面を server モードで確かめる方法が書いてあります
