# 準備と起動

aibow をビルドして起動するまでの手順です。Windows + NVIDIA GPU を前提にしています。

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
Go のバージョンを表示します。標準の `uuid` パッケージ(会話や発言の ID を作る)を使うため、`go.mod` に `go 1.27.0` と書いてあります。
手元の Go が古い場合でも、Go が必要なバージョンを自動でダウンロードして使います(Go 1.21 以降の機能)。

```bash
node --version
```
Node.js のバージョンを表示します。画面のビルドに使う Vite が **Node.js 20.19 以上(または 22.12 以上)** を必要とします。
画面(React)のパッケージを入れる npm は Node.js に付いてきます。入っていなければ https://nodejs.org/ から入れてください。

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
画面で使うパッケージ(React、Vite、Wails の JavaScript 用ランタイム、アイコンの react-icons、Markdown を HTML にする marked、コードの色付けの highlight.js、図の mermaid など)を `frontend/node_modules/` にダウンロードします。
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

リポジトリのルートの `aibow` パッケージや `engine/` などは見張っていないので、そちらを変えたときは `Ctrl+C` で止めて起動し直してください。
初回は Go のライブラリのダウンロードとビルドがあるので、ウィンドウが出るまでしばらくかかります。

起動すると、ターミナルに次のようなログが出ます(Wails 自身のログも混ざります)。各行の頭には日時とレベル(`2026-10-14T16:00:00+09:00 [INFO  ]`)が付きますが、下では省いています。

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

ウィンドウが開きます。モデルはウィンドウが出てから裏で読み込むので、読み終わるまでは送信できません。
止めるときはウィンドウを閉じます(VRAM も解放され、`wails3 dev` も終わります)。生成中に閉じても、生成を止めてから終了します。

うまく動かないとき:

- ウィンドウに「起動できません: llama.cpp のフォルダが見つかりません: …」と出る場合は、表示されたフォルダに `lib\` がありません。
  `_cmd/aibow` で起動しているか、`AIBOW_LIB` の指定が正しいかを確認してください。環境変数の値が不正なときも、同じようにウィンドウに理由が出ます。
- `計算装置` に GPU が出てこない場合は、GPU 用ではない llama.cpp が入っているか、ドライバーが古い可能性があります(準備の3の `--processor cuda` を確認)。
- Ollama のモデルが一覧に出てこない場合は、ログの `Ollama:` の場所が正しいか確認してください。違う場所に保存しているなら、環境変数 `OLLAMA_MODELS` で指定します。

### 実行ファイルを作る

```bash
wails3 build
```
画面をビルドして埋め込み、アイコン付きの実行ファイル `bin\aibow.exe` を作ります。コンソールは出ません。
画面のビルド結果はリポジトリのルートの `web/dist/app` にでき、aibow-server(ブラウザから使うとき)にも同じものが入ります。aibow-server は、このあとでビルドします([usage.md](usage.md) の「ブラウザから使う」)。

```bash
wails3 task run
```
`bin\aibow.exe` を、`_cmd/aibow` をカレントフォルダにして起動します。`bin\aibow.exe` をエクスプローラーからダブルクリックすると
カレントフォルダが `bin\` になり、`lib\` が見つかりません。その場合は、起動できない理由がウィンドウに表示されます
(`AIBOW_LIB` / `AIBOW_MODELS_DIR` を絶対パスで設定しておけば、どこから起動しても動きます)。
`bin\aibow.exe` はコンソールを持たないので、ログはファイル `%APPDATA%\aibow\logs\aibow_<日付>.log` に書きます(起動した版・使ったモデル・注意・エラーなど、常に残したいもの(NOTICE 以上)だけ。生成ごとの統計などは `wails3 dev` のターミナルで見ます。日ごとに分かれ、10 日分を残して古いものは起動時に消します。アプリが異常終了したときは、同じフォルダの `aibow_crash_<日時>.log` に内容が残り、次の起動で画面に知らせます。場所は `AIBOW_LOG_DIR` で変えられます)。
起動できないなど、うまく動かないときはこのファイルを見てください。`wails3 dev` で起動したときは、ファイルには書かずターミナルに出します。

### 自分の PC にインストールする(local:deploy)

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

### 起動時に落ちた場合

調査の記録は [known-issues.md](known-issues.md) にあります。

`GGML_ASSERT(...) failed` と出て落ちたことが一度ありましたが、同じ条件で10回起動し直しても再現しませんでした(原因は未特定)。
もう一度起きたら、llama.cpp 自身のログを出して起動し、落ちる直前のログと、そのとき動いていた他のアプリ(Ollama、ゲームなど)を記録してください。

```bash
AIBOW_LLAMA_LOG=1 wails3 dev 2>&1 | tee aibow.log
```
`2>&1` でエラー出力も含めて、`tee` で画面に出しつつ `aibow.log` にも保存します。

### worktree などから起動する

Git の worktree など、別の場所にあるコードから起動するときは、`lib\` と `models\` は Git に入っていないので、本体の場所を指定します。
worktree に `lib\` / `models\` を入れ直す必要はありません。

```bash
AIBOW_LIB=/d/Go/Projects/aibow/_cmd/aibow/lib AIBOW_MODELS_DIR=/d/Go/Projects/aibow/_cmd/aibow/models wails3 dev
```

本体の `lib\` / `models\` へのジャンクション(フォルダの別名)は作らないでください。
worktree を消すツールによっては、ジャンクションの先(本体の `lib\` / `models\`)の中身まで消してしまいます。
`frontend/node_modules/` も Git に入っていないので、worktree の `_cmd/aibow/frontend` で `npm install` します。

## aibow-server をログインありで使い始める(ステップ10・11)

初めて起動した aibow-server の利用者は `local` の1人だけで(パスワードは空)、**管理者は一人もいません。既定の admin も作りません**
(決まったパスワードの管理者がいると、変え忘れたまま外に出したときに誰でも入れてしまうため。最初の管理者は、サーバーの PC でコマンドを打てる人が決める)。
ログインありで使うときは、最初にサーバーの PC で管理者を作ります。詳しくは [usage.md](usage.md) の「ログインと利用者」にあります。

1. 管理者にする人を作る:`aibow-server user add <名前>`(パスワードをこのときだけ表示する)
2. 管理者にする:`aibow-server user admin <名前>`
3. `server.json` に `"login": true` を書いて(または `-login` を付けて)起動する
4. ブラウザで aibow-server の画面を開いてその人でログインし、設定の画面の「利用者」のタブで、ほかの人を作る

- 1 と 2 は aibow-server を動かしたままでも打てる。設定ファイルを `-config` で指しているときは、コマンドにも同じ `-config` を付ける
- 管理者がいなくても動く(サーバーの PC からはモデルなどをいつでも変えられ、利用者はコマンドで扱える)

## ほかの PC から使えるようにする(aibow-server。ステップ10)

aibow-server を同じ網のほかの PC から使うときの準備です。手順の詳細は [usage.md](usage.md) の「ログインと利用者」「ほかの PC から使う」にあります。

1. 証明書を作る:`aibow-server cert <ほかの PC からつなぐときのアドレス>`(設定ファイルの隣に `server-cert.pem` と `server-key.pem` ができ、証明書の指紋が表示される)
2. `server.json` に `"host": "0.0.0.0"` と `tlsCert`・`tlsKey` を書いて起動する(外で待ち受けると、ログインが必ず要る。起動のときのログにも証明書の指紋が出る)
3. 利用者を作る:上の「aibow-server をログインありで使い始める」と同じ(管理者は、コマンドかブラウザの「利用者」のタブで、ほかの人を作る)
4. 使う人に、アドレス・パスワード・証明書の指紋を渡す
   - 画面のアプリ:設定の画面の「サーバー」のタブでアドレスを入れ、出てきた指紋が渡されたものと同じなら信頼して、名前とパスワードでログインする(0019。API キーと証明書のファイルは要らない)
   - Cline など(OpenAI 互換のクライアント):API キー(`aibow-server user key <名前>`。このときだけ表示する)と、証明書のファイル(`server-cert.pem`。秘密鍵は渡さない)も渡す
   - 画面のアプリを `settings.json` に手で書いてつなぐときも、`serverKey` に API キー、`serverCert` に証明書のファイルを書く

- ブラウザで初めて開くと、自己署名の証明書なので「この接続ではプライバシーが保護されません」と警告が出る。サーバーの PC で作った証明書かを確かめて先へ進む
- Windows では、初めて外で待ち受けるときにファイアウォールの許可を聞かれる(プライベートネットワークだけを許可する)
