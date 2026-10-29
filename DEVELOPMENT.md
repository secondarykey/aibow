# 開発する

aibow のコードを読んだり変えたりする人向けの案内です。使ってみるだけなら [README](README.md) を先にどうぞ。

aibow は、ローカル LLM を使ったアプリを**自分で作りながら仕組みを学ぶ**ためのプロジェクトです。
LLM サーバーに頼らず、モデルの読み込みから1トークンずつの生成、会話の管理、ツール呼び出しまでを Go で自作しながら、
「チャット AI の中で何が起きているか」を1段ずつ確かめていきます。
「ローカル LLM でバイブコーディングできるエージェント」と「MCP(クライアントとサーバー)」まで作り、今は組織で使える形に進めています。
完成品を早く作ることより、各段階で何が起きているかが読んで分かることを優先しています。

AI のコーディングエージェントで作業するときの決まりごと(方針・使ってよいライブラリ・コードの書き方)は [AGENTS.md](AGENTS.md) にあります。人が作業するときも同じ決まりです。

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
- **画面**: [Wails v3](https://v3.wails.io/) のデスクトップアプリ(React + TypeScript)。aibow-server がブラウザに配る画面も同じもの
- **保存**: SQLite([modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite))
- **MCP**: 公式の [Go SDK](https://github.com/modelcontextprotocol/go-sdk)
- チャットテンプレートやエージェントループなど、学習の対象になる仕組みはライブラリに任せず自分で書いています

全体の構成とファイルの役割は [_docs/structure.md](_docs/structure.md) にあります。

## 開発モードで動かす

準備(道具・llama.cpp・モデル)は [_docs/setup.md](_docs/setup.md) の「準備」と同じです。そのうえで、アプリのフォルダで開発モードで起動します。

```bash
cd _cmd/aibow
wails3 dev
```
中では次のことが順に行われます。

- Go のコードからバインディング(`frontend/bindings/`。画面から Go のメソッドを呼ぶための TypeScript の関数と型)を作り直す
- Go をビルドする(`bin/aibow.exe`)
- 画面の開発サーバー(Vite)を起動する。画面のコードを保存すると、ウィンドウにすぐ反映される
- アプリを起動する。`_cmd/aibow` の Go のコードを保存すると、ビルドし直して起動し直す

リポジトリのルートの `aibow` パッケージや `engine/` などは見張っていないので、そちらを変えたときは `Ctrl+C` で止めて起動し直してください。
止めるときはウィンドウを閉じます(`wails3 dev` も終わります)。

`wails3 dev` では、ターミナルとファイル `%APPDATA%\aibow\logs\aibow-dev_<日付>.log` の両方に Info 以上のログを出します(生成ごとの統計・ツールの実行・compact など、学習で観察するもの。exe は Notice 以上だけ)。
各行の頭には日時とレベル(`2026-10-14T16:00:00+09:00 [INFO  ]`)が付きますが、下では省いています。

```
会話の保存先: C:\Users\<ユーザー>\AppData\Roaming\aibow\aibow.db
新しい会話の既定: 会話の上限 8192 + 返事の上限 8192 = 作業領域 16384 トークン
計算装置: CUDA0 [GPU] <GPU の名前>(VRAM 空き ○ / ○ GB)
計算装置: CPU [CPU] <CPU の名前>
モデルの置き場所: D:\Go\Projects\aibow\_cmd\aibow\models / Ollama: C:\Users\<ユーザー>\.ollama\models
  ollama   qwen3.5:4b                               ○ GB
  ollama   qwen3.5:9b                               ○ GB
aibow 起動
モデルを読み込み中: qwen3.5:4b (...)
読み込み完了 (○ 秒): <モデルの種類> / ○B パラメータ / ○ GB / ○ 層 / GPU に載せる層: 999 / 作業領域: 16384 トークン
```

依存を増やした・減らしたときは、手で取り直します(`wails3 dev` / `wails3 build` は `node_modules` が無いときだけ自動で `npm install` する)。

```bash
go mod tidy
cd frontend && npm install && cd ..
```
`go mod tidy` は `go.mod` に書いたライブラリを取り、チェックサムを `go.sum` に書きます。`npm install` は画面のパッケージを `frontend/node_modules/` に取ります。
外部のパッケージ(Go・npm とも)を足すときの決まりは [AGENTS.md](AGENTS.md) の「技術方針」にあります。

### worktree などから起動する

Git の worktree など、別の場所にあるコードから起動するときは、`lib\` と `models\` は Git に入っていないので、本体の場所を指定します。
worktree に `lib\` / `models\` を入れ直す必要はありません。

```bash
AIBOW_LIB=/d/Go/Projects/aibow/_cmd/aibow/lib AIBOW_MODELS_DIR=/d/Go/Projects/aibow/_cmd/aibow/models wails3 dev
```

本体の `lib\` / `models\` へのジャンクション(フォルダの別名)は作らないでください。
worktree を消すツールによっては、ジャンクションの先(本体の `lib\` / `models\`)の中身まで消してしまいます。
`frontend/node_modules/` も Git に入っていないので、worktree の `_cmd/aibow/frontend` で `npm install` します。

### aibow-server を開発で動かす

画面を vite の開発用サーバーから配り、aibow-server を `go run` で動かします(画面のソースを保存すると、ブラウザにすぐ出ます)。
リポジトリのフォルダで実行し、ブラウザは `http://127.0.0.1:9246/` を開きます。後ろに aibow-server の引数を付けられます。

```bash
go run ./_cmd/dev server
```

exe を作るときは `go run ./_cmd/dev build` です(画面をビルドしてから `go build` する。`go build` だけだと前にビルドした古い画面が入る)。仕組みは [_docs/features/0021-server-dev.md](_docs/features/0021-server-dev.md)。

## 確かめる

変えたら、最低限これを実行します(何をテストしているかは [AGENTS.md](AGENTS.md) の「確認方法」)。

```bash
gofmt -l .                                       # 整形が必要なファイルを表示(何も出なければ OK)
go test ./...                                    # ルートのモジュールのテスト(モデル不要)
(cd _cmd/aibow && go test .)                     # 画面のアプリのテスト(別モジュールなので ./... に入らない)
(cd _cmd/aibow && wails3 build)                  # バインディング生成 → 画面の型チェックとビルド → Go のビルド
go vet ./... ./_cmd/aibow ./_cmd/aibow-mcp ./_cmd/aibow-server
```

アプリを起動して確かめるときの手順と項目は [_docs/testing.md](_docs/testing.md) にあります。

## 自分の PC にインストールする(local:deploy)

普段使いの場所へ、バージョン管理込みでインストールします。
インストール先などのローカルの設定をリポジトリに残さないよう、`local:deploy` は各自の `_cmd/aibow/Taskfile.local.yml` に書きます。
このファイルは Git に入れません(`.gitignore` 済み)。`Taskfile.yml` が optional で読み込むので、無い環境でも他のタスクはそのまま動きます。

```bash
wails3 task local:deploy
```
`_cmd/aibow` で実行します。`Taskfile.local.yml` の `local:deploy` は、次のことを順に行う作りにしています。

1. 起動中の aibow を終了する(起動中の exe は上書きできないため)
2. 前回インストールしたバージョンのままなら、patch を1つ上げる(0.1.0 → 0.1.1)
3. `version` の値を、`build/config.yml`・`frontend/package.json`・`frontend/package-lock.json`・exe に焼き込むバージョン情報(`build/windows/info.json` など)に反映する
   (`go run _cmd/version.go` → `npm install --package-lock-only` → `wails3 update build-assets -name aibow -binaryname aibow`)
4. `wails3 build`
5. 3 で変わったファイルだけをコミットする(作業中の他の変更は巻き込まない)
6. コミットに `v0.1.1` のようなタグを打つ(push はしない)。どのコミットをインストールしたかが分かる
7. `bin\aibow.exe` と `lib\` をインストール先へコピーする(`lib\` は変わったファイルだけ)

インストール先の `aibow.exe` を起動すると、隣の `lib\` を使います。モデルは Ollama のものが使えます。
GGUF を自分で置く場合は、インストール先に `models\` を作って置くか、`AIBOW_MODELS_DIR` で指定します。

```bash
go run _cmd/version.go -bump
```
patch は自動で上がりますが、minor / major を上げたいときは、**リポジトリのルートで**これを実行してから `local:deploy` します。
上げ方を選ぶと、`version`・`build/config.yml`・`frontend/package.json` が書き換わります。

`Taskfile.local.yml` の変数(インストール先など)を一時的に変えたいときは、`KEY=値` で渡します(`wails3 task` は `--` の後ろの引数を受け取れないため)。

```bash
wails3 task local:deploy DEPLOY_DEST="D:/tmp/aibow"
```

バージョンの扱いの詳しいことは [_docs/deploy.md](_docs/deploy.md) にあります。

## 落ちたときの調べ方

調査の記録は [_docs/known-issues.md](_docs/known-issues.md) にあります。

`GGML_ASSERT(...) failed` と出て落ちたことが一度ありましたが、同じ条件で10回起動し直しても再現しませんでした(原因は未特定)。
もう一度起きたら、llama.cpp 自身のログを出して起動し、落ちる直前のログと、そのとき動いていた他のアプリ(Ollama、ゲームなど)を記録してください。

```bash
AIBOW_LLAMA_LOG=1 wails3 dev 2>&1 | tee aibow.log
```
`2>&1` でエラー出力も含めて、`tee` で画面に出しつつ `aibow.log` にも保存します。

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
| 12. サーバーの上で動かすツール ✅ | ブラウザの会話で Web のツールと資料のフォルダを使う |

進み具合と、ロードマップの外でやりたいことは [TODO.md](TODO.md) にあります。

## ドキュメント

| 読むもの | 内容 |
|---|---|
| [AGENTS.md](AGENTS.md) | 方針・技術の決まりごと・作業の進め方・作業内容ごとに読むもの |
| [TODO.md](TODO.md) | 進み具合とロードマップ(入口) |
| [_docs/structure.md](_docs/structure.md) | 全体の構成とファイルの役割 |
| [_docs/](_docs/) | 仕組みごとの説明(エンジン・会話・compact・ツール・MCP・API・画面など) |
| [_docs/features/](_docs/features/) | 機能ごとの仕様(決めたこと・やらないこと・経過) |
| [_docs/testing.md](_docs/testing.md) | アプリを起動して確かめる手順 |
| [_docs/tuning.md](_docs/tuning.md) | パラメータの調整と、実際に動かして分かったこと |
