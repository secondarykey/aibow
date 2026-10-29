# 準備と起動

aibow をビルドして使い始めるまでの手順です。Windows + NVIDIA GPU を前提にしています。
短い手順は [README](../README.md) にあります。ここではその詳しい説明と、うまく動かないときのことを書きます。
コードを変えながら動かす(開発モード)・自分の PC にインストールする・worktree から起動する、は [DEVELOPMENT.md](../DEVELOPMENT.md) にあります。

## 準備(最初の1回だけ)

bash(Git Bash など)で、アプリのフォルダ `_cmd/aibow` に移動して作業します。
llama.cpp の DLL(`lib\`)とモデル(`models\`)は、モデルを動かすこのアプリの持ち物なので、アプリのフォルダに置きます。

```bash
cd aibow/_cmd/aibow
```
以降のコマンドは、すべてこのフォルダで実行します。

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

### 2. llama.cpp の取得

```bash
go run github.com/hybridgroup/yzma@v1.29.1 install --lib lib --processor cuda
```
yzma(aibow が llama.cpp を呼ぶのに使うライブラリ)に付属しているインストーラーを、インストールせずにその場で1回だけ実行します。aibow のビルドとは無関係の、準備用の道具です。

- `go run パッケージ@バージョン` … 指定したプログラムをダウンロードしてビルドし、そのまま実行する
- `install` … llama.cpp のビルド済み DLL を GitHub からダウンロードする
- `--lib lib` … 置き場所。相対パスはコマンドを実行したフォルダが基準なので、`_cmd\aibow\lib\` にできる
- `--processor cuda` … NVIDIA GPU 用のビルドを選ぶ。CUDA の実行に必要な DLL(cudart)も一緒に入る。GPU が無ければ `cpu`(遅くなる)

`go run パッケージ@バージョン` は今いるフォルダの `go.mod` / `go.work` を見ないので、`_cmd/aibow` で実行しても問題ありません(aibow のソースが無いフォルダでも動きます)。
バージョン(`@v1.29.1`)は、ルートの `go.mod` に書いてある yzma のバージョンと合わせてください。yzma と llama.cpp は組み合わせが決まっています。
今は yzma v1.29.1 → llama.cpp v0.6.0(llama.cpp のビルド b11429)で、`--version` を付けなければ yzma がこの版を入れます([engine.md](engine.md) の「llama.cpp と yzma」)。

Go が無いときは、llama.cpp のリリースページ(https://github.com/ggml-org/llama.cpp/releases/tag/b11429)から zip を手で落として `_cmd\aibow\lib\` に展開しても同じです。
yzma が NVIDIA GPU 用に落とすのは次の2つです(GPU が無ければ `llama-b11429-bin-win-cpu-x64.zip` だけ)。

- `llama-b11429-bin-win-cuda-13.4-x64.zip` … llama.cpp の本体
- `cudart-llama-bin-win-cuda-13.4-x64.zip` … CUDA の実行に要る DLL(cudart・cublas)
aibow-server につないで使うだけの PC には要りません(下の「aibow-server を使い始める」)。

### 3. モデルの用意

次のどちらでも構いません。両方あれば、両方とも画面の一覧に出ます。

**A. Ollama で管理する**

```bash
ollama pull qwen3.5:4b
```
Ollama がモデルをダウンロードして保存します。すでに pull 済みなら何もしなくて構いません。
aibow は Ollama の保存フォルダ(既定は `C:\Users\<ユーザー>\.ollama\models`、環境変数 `OLLAMA_MODELS` があればそちら)を見に行きます。
Ollama は起動していなくて構いません(保存したファイルを aibow が直接読みます)。

注意: Ollama のモデルの中には、Ollama 独自の形式になっていて llama.cpp では読めないものがあります。
読めなかった場合は画面にエラーが出て、元のモデルに戻ります。その場合は B の方法で GGUF を用意してください。

**B. GGUF ファイルを自分で置く**

GGUF は llama.cpp 用のモデルファイルの形式です。ダウンロードして `_cmd\aibow\models\` フォルダに置くだけです。例えば Qwen3.5 4B なら、次の URL をブラウザで開くとダウンロードが始まります(約3GB)。

https://huggingface.co/bartowski/Qwen_Qwen3.5-4B-GGUF/resolve/main/Qwen_Qwen3.5-4B-Q4_K_M.gguf

`Q4_K_M` は「重みを4ビット程度に圧縮したもの」という意味で、元のサイズの約1/4になり、VRAM 8GB に余裕で収まります。
ほかのフォルダに置いたモデルは、設定の画面の「モデル」のタブで置き場所を足すと一覧に出ます。

## ビルドと起動

```bash
wails3 build
```
画面(React)をビルドして埋め込み、アイコン付きの実行ファイル `bin\aibow.exe` を作ります。
画面のパッケージ(`frontend/node_modules/`)が無ければ、先に自動で `npm install` します。初回は Go と npm のダウンロードがあるので時間がかかります。

```bash
./bin/aibow.exe
```
起動します(`wails3 task run` でも同じ)。ウィンドウが出たら、入力欄の下でモデルを選んで話しかけます。
モデルはウィンドウが出てから裏で読み込むので、読み終わるまでは送信できません。止めるときはウィンドウを閉じます(VRAM も解放されます)。

- `lib\` と `models\` は**カレントフォルダから**探します。`_cmd/aibow` で起動してください。
  `bin\aibow.exe` をエクスプローラーからダブルクリックするとカレントフォルダが `bin\` になり、`lib\` が見つかりません(起動できない理由がウィンドウに出ます)。
  どこからでも起動したいときは、`AIBOW_LIB` / `AIBOW_MODELS_DIR` を絶対パスで設定しておきます(下の「環境変数」)
- 会話・設定は `%APPDATA%\aibow\` に置きます(`aibow.db`・`settings.json` など。何をどこに置くかは [usage.md](usage.md) の「環境変数の効き方」)
- `bin\aibow.exe` はコンソールを持たないので、ログはファイル `%APPDATA%\aibow\logs\aibow_<日付>.log` に書きます(起動した版・使ったモデル・注意・エラーなど、常に残したいもの(NOTICE 以上)だけ。日ごとに分かれ、10 日分を残して古いものは起動時に消します。場所は `AIBOW_LOG_DIR` で変えられます)。
  アプリが異常終了したときは、同じフォルダの `aibow_crash_<日時>.log` に内容が残り、次の起動で画面に知らせます。起動できないなど、うまく動かないときはこのファイルを見てください

画面のビルド結果はリポジトリのルートの `web/dist/app` にでき、aibow-server(ブラウザから使うとき)にも同じものが入ります。

### 環境変数(セットアップに関わるもの)

llama.cpp とモデルは、既定ではカレントフォルダの `lib\`・`models\` を探します。ほかの場所に置いたときや、ほかのフォルダから起動するときは、環境変数で場所を指します。

| 環境変数 | 既定 | 内容 |
|---|---|---|
| `AIBOW_LIB` | `lib` | llama.cpp の DLL のフォルダ |
| `AIBOW_MODELS_DIR` | `models` | GGUF を置くフォルダ |
| `OLLAMA_MODELS` | `%USERPROFILE%\.ollama\models` | Ollama の保存フォルダ(Ollama 自身と同じ変数) |
| `AIBOW_MODEL` | 最後に使ったモデル | 起動のときに読み込むモデル(例: `ollama:qwen3.5:4b`) |
| `AIBOW_GPU_LAYERS` | `999`(全部) | GPU に載せる層の数。VRAM に入らないときに減らす |
| `AIBOW_DB`・`AIBOW_LOG_DIR` | `%APPDATA%\aibow\` の下 | 会話の DB とログの場所(試すときは一時フォルダにすると、ふだんの会話と混ざらない) |
| `AIBOW_SERVER_URL` | なし | 画面のアプリを aibow-server につなぐ(画面の「サーバー」のタブでもつなげる) |

- **読むのは起動のときだけ**です。変えたら起動し直します
- **画面で変えられる設定**(GPU の層数・会話の上限など)の環境変数は、まだ画面で変えていないときの初期値です。画面で一度変えると、画面の値が優先されます
- **aibow-server のつなぎ先**は、`settings.json` の `serverUrl` > `AIBOW_SERVER_URL` > 画面で決めたもの、の順に強く、手で書いてあれば画面では変えられません

全部の環境変数と効き方は [usage.md](usage.md) の「環境変数の効き方」と表にあります。

### うまく動かないとき

- ウィンドウに「起動できません: llama.cpp のフォルダが見つかりません: …」と出る場合は、表示されたフォルダに `lib\` がありません。
  `_cmd/aibow` で起動しているか、`AIBOW_LIB` の指定が正しいかを確認してください。環境変数の値が不正なときも、同じようにウィンドウに理由が出ます。
  aibow-server につないで使う PC なら `lib\` は要りません。この表示のまま設定の画面の「サーバー」のタブでつなぎ、起動し直します([usage.md](usage.md) の「画面のアプリを aibow-server につなぐ」)。
- ログの `計算装置` に GPU が出てこない場合は、GPU 用ではない llama.cpp が入っているか、ドライバーが古い可能性があります(準備の2の `--processor cuda` を確認)。
- Ollama のモデルが一覧に出てこない場合は、ログの `Ollama:` の場所が正しいか確認してください。違う場所に保存しているなら、環境変数 `OLLAMA_MODELS` で指定します。
- Linux で起動すると `bwrap: setting up uid map: Permission denied` と出て落ちる場合は、画面に使う WebKitGTK が隔離(sandbox)を作れていません
  (Ubuntu 23.10 以降は、AppArmor で一般の利用者が名前空間を作るのを制限している)。`WEBKIT_DISABLE_SANDBOX_THIS_IS_DANGEROUS=1` を付けて起動すると動きます(2026-10-29 に確かめた)。
- 起動時に落ちる(`GGML_ASSERT(...) failed` など)場合は、[known-issues.md](known-issues.md) の記録と、[DEVELOPMENT.md](../DEVELOPMENT.md) の「落ちたときの調べ方」を見てください。

## aibow-server を使い始める(ステップ10・11)

aibow-server は、リポジトリのフォルダで `go run ./_cmd/dev build` で作ります(画面をビルドしてから exe を作る。`go build` だけだと古い画面が入る)。
要るのは Go と Node.js で、wails3 は要りません。起動の引数と設定ファイルは [usage.md](usage.md) の「aibow-server」にあります。

初めて起動した aibow-server の利用者は `local` の1人だけで(パスワードは空)、**管理者は一人もいません。既定の admin も作りません**
(決まったパスワードの管理者がいると、変え忘れたまま外に出したときに誰でも入れてしまうため。最初の管理者は、サーバーの PC でコマンドを打てる人が決める)。
aibow-server は**既定でログインが要る**ので(0020)、最初にサーバーの PC で管理者を作ります。詳しくは [usage.md](usage.md) の「ログインと利用者」にあります。
管理者がいない間は、起動のときのログとブラウザのログインの画面に「管理者がいません」と出ます。

1. 管理者にする人を作る:`aibow-server user add <名前>`(本人がパスワードを決める招待のリンクを表示する。1 時間・一度だけ。0022)
2. 管理者にする:`aibow-server user admin <名前>`
3. aibow-server を起動する(1・2 の前に起動していてもよい)
4. 1 のリンクをブラウザで開き、パスワード(12 文字以上)を決める(そのままログインする)。設定の画面の「利用者」のタブで、ほかの人を作って招待のリンクを渡す

- 1 と 2 は aibow-server を動かしたままでも打てる。設定ファイルを `-config` で指しているときは、コマンドにも同じ `-config` を付ける
- リンクの期限が切れた・パスワードを忘れて入れなくなったときは、`aibow-server user passwd <名前>` で決め直すリンクを出す(出した時点で今のパスワードは使えなくなる)
- 自分のパスワードは、設定の画面の「基本設定」の「パスワード」で変える
- 管理者がいなくても動く(サーバーの PC からはモデルなどをいつでも変えられ、利用者はコマンドで扱える)
- 1人で自分の PC だけで使うなら、`server.json` に `"login": false`(または `-login=false`)を書くとログインなしになる(利用者は `local` だけ。127.0.0.1 の外で待ち受けるときは効かない)
- 画面のアプリがアプリの中でモデルを動かしている間は、aibow-server を同時に動かさない(VRAM にモデルを2つ載せられないため)
- ブラウザの会話で Web のツール(MCP のサーバー)や資料のフォルダを使うときは、`server.json` の `mcpServers`・`folders` に書く(任意。ステップ12。
  書き方と、管理者が決めること・気をつけることは [usage.md](usage.md) の「ブラウザの会話のツール」「資料のフォルダ」)

## ほかの PC から使えるようにする(aibow-server。ステップ10)

aibow-server を同じ網のほかの PC から使うときの準備です。手順の詳細は [usage.md](usage.md) の「ログインと利用者」「ほかの PC から使う」にあります。

1. 証明書を作る:`aibow-server cert <ほかの PC からつなぐときのアドレス>`(設定ファイルの隣に `server-cert.pem` と `server-key.pem` ができ、証明書のフィンガープリントが表示される)
2. `server.json` に `"host": "0.0.0.0"` と `tlsCert`・`tlsKey` を書いて起動する(外で待ち受けると、ログインが必ず要る。起動のときのログにも証明書のフィンガープリントが出る)
3. 利用者を作る:上の「aibow-server を使い始める」と同じ(管理者は、コマンドかブラウザの「利用者」のタブで、ほかの人を作る)
4. 使う人に、招待のリンク(1 時間)と証明書のフィンガープリントを渡す。本人はリンクを開いてパスワードを決める
   - 画面のアプリ:設定の画面の「サーバー」のタブでアドレスを入れ、出てきたフィンガープリントが渡されたものと同じなら信頼して、名前と決めたパスワードでログインする(0019。API キーと証明書のファイルは要らない。つなぐだけの PC には `lib\`・`models\` は要らない)
   - Cline など(OpenAI 互換のクライアント):API キー(`aibow-server user key <名前>`。このときだけ表示する)と、証明書のファイル(`server-cert.pem`。秘密鍵は渡さない)も渡す
   - 画面のアプリを `settings.json` に手で書いてつなぐときも、`serverKey` に API キー、`serverCert` に証明書のファイルを書く

- ブラウザで初めて開くと、自己署名の証明書なので「この接続ではプライバシーが保護されません」と警告が出る。サーバーの PC で作った証明書かを確かめて先へ進む
- Windows では、初めて外で待ち受けるときにファイアウォールの許可を聞かれる(プライベートネットワークだけを許可する)
