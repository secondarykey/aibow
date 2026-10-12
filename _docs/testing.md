# アプリを起動して確かめる

最低限の確認(`gofmt` / `go test` / `wails3 build` / `go vet`)はトップの `AGENTS.md` にあります。
ここは、実際にアプリを起動して確かめるときの手順と注意です。

## 起動

実際の生成は `_cmd/aibow` で `wails3 dev`(または `wails3 build` してから `wails3 task run`)で起動し、開いたウィンドウで送受信して確認します。

## worktree で起動するとき

worktree には `lib\` / `models\` / `frontend/node_modules` が無い。

- `lib\` / `models\` は `AIBOW_LIB` / `AIBOW_MODELS_DIR` で本体(`/d/Go/Projects/aibow/_cmd/aibow/`)を指す(ユーザーの環境では、worktree のセッション開始時にこの2つが自動で設定される)。
  **worktree に `lib\` / `models\` を作らない・入れ直さない**(yzma の install や GGUF のダウンロードをしない)
- 本体の `lib\` / `models\` へのジャンクションやシンボリックリンクを張らない。worktree の解放時に Git 管理外のファイルが消され、リンク先の本体の実体まで消える
- 会話の DB は、試すときは `AIBOW_DB` を一時フォルダのファイルにする(ユーザーの `%APPDATA%\aibow\aibow.db` に試しの会話を混ぜない)
- `node_modules` は worktree の `_cmd/aibow/frontend` で `npm install` する(ユーザーの環境で本体へのジャンクションが自動で張られている場合は、そのまま使い、`npm install` しない)

## 画面を外から操作する(Windows: CDP)

一時ビルドで `application.Options.Windows.AdditionalBrowserArgs` に `--remote-debugging-port=9222` を足し、
CDP(`http://localhost:9222/json`)で `Runtime.evaluate` する(この変更はコミットしない)。

- ユーザーはふだんから aibow を起動していることがある。一時ビルドでは `application.Options.Windows.WebviewUserDataPath` も一時フォルダにする
  (同じ WebView2 のデータフォルダを起動引数の違うプロセスで使うと、後から起動した方が WebView2 を作れず失敗する: 8007139f)
- 試しに起動したアプリを閉じるときは、**自分が起動したプロセスの PID だけ**を終了させる。`taskkill /IM aibow.exe` のように名前で終了させない(ユーザーのアプリまで閉じてしまう)

## GUI の無い Linux(Wails の server モード + Playwright)

クラウドのセッションなどでは、Wails の server モード(`_cmd/aibow` で `go build -tags server -o <一時フォルダ>/aibow-server .`。リポジトリは変えない)で起動すると
`WAILS_SERVER_PORT` のポートで画面が HTTP で配信され、Playwright などのブラウザから操作して確かめられる(Go のメソッドもイベントも動く)。
server モードは確認用の一時ビルドだけで使う(アプリとしては HTTP サーバーを立てない方針)。

- Linux でビルドするには GTK4 / WebKitGTK 6 の開発用パッケージ(`libgtk-4-dev` / `libwebkitgtk-6.0-dev`)が要る
- Wails が作る `build/linux/aibow.desktop` や、`_cmd/aibow` に出来た実行ファイルはコミットしない

Playwright で確かめるときの注意:

- Chromium はインストール済み(`/opt/pw-browsers/chromium-*/chrome-linux/chrome` を `executablePath` に渡す)。`playwright-core` は一時フォルダに入れる
- ヘッドレスの Chromium は既定でスクロールバーを隠す。スクロールバーを見るときは `ignoreDefaultArgs: ["--hide-scrollbars"]`
- コピーを確かめるときは `grantPermissions(["clipboard-read", "clipboard-write"])` して `navigator.clipboard.readText()` で読む。
  server モードでは Wails の `Clipboard.SetText` は成功を返しても OS のクリップボードに書かない
- 長く生成し続けるモデルが要るとき(スクロールの確認など)は、重みがランダムな極小モデルの語彙に、空白で始まるふつうのトークンを数万個足すと、終わりのトークンが選ばれにくくなる

## GPU や本物のモデルが無い環境

llama.cpp の CPU 版と小さな GGUF(重みがランダムな極小モデルでもよい)で、次までを確認します。

「読み込み → 生成 → 停止 → 2回目の送信で KV キャッシュが再利用される → モデルの切り替え → 壊れたモデルを選んだら元に戻る → 再起動しても会話が残る」

- compact は、会話の上限を小さくして(`AIBOW_HISTORY_TOKENS=1200` など)「truncate で薄くなる → summary で区切りが出る → none で止まる → 再起動しても要約が残る」を確認する。
  生成前の usage イベントのトークン数と、返事の統計のプロンプトのトークン数が一致することも見ておく(数え方がエンジンと同じであることの確認)
- Ollama の保存フォルダは、`manifests\registry.ollama.ai\library\<名前>\<タグ>`(目録 JSON)と `blobs\sha256-<ハッシュ>` を作れば模擬できます(`OLLAMA_MODELS` で指定)
- 出力される文章の中身は確認できないので、思考と答えの振り分けは `stream_test.go` で確認します
