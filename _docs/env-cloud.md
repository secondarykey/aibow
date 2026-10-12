# クラウドセッション(GUI の無い Linux)で確かめる

Claude Code のクラウドセッションなど、画面も GPU も無い Linux でアプリを起動して確かめるときの手順と注意です。
どの環境でも同じ手順と確認する項目は [testing.md](testing.md) にあります。

この環境にはユーザーの `lib\` / `models\` / DB は無い。llama.cpp の CPU 版と小さな GGUF を一時フォルダに用意して使う(リポジトリには入れない)。

## Wails の server モードで起動する

`_cmd/aibow` で `go build -tags server -o <一時フォルダ>/aibow-server .` でビルドして起動すると(リポジトリは変えない)、
`WAILS_SERVER_PORT` のポートで画面が HTTP で配信され、Playwright などのブラウザから操作して確かめられる(Go のメソッドもイベントも動く)。

- server モードは確認用の一時ビルドだけで使う(アプリとしては HTTP サーバーを立てない方針)
- Linux でビルドするには GTK4 / WebKitGTK 6 の開発用パッケージ(`libgtk-4-dev` / `libwebkitgtk-6.0-dev`)が要る
- Wails が作る `build/linux/aibow.desktop` や、`_cmd/aibow` に出来た実行ファイルはコミットしない

## Playwright で操作する

- Chromium はインストール済み(`/opt/pw-browsers/chromium-*/chrome-linux/chrome` を `executablePath` に渡す)。`playwright-core` は一時フォルダに入れる
- ヘッドレスの Chromium は既定でスクロールバーを隠す。スクロールバーを見るときは `ignoreDefaultArgs: ["--hide-scrollbars"]`
- コピーを確かめるときは `grantPermissions(["clipboard-read", "clipboard-write"])` して `navigator.clipboard.readText()` で読む。
  server モードでは Wails の `Clipboard.SetText` は成功を返しても OS のクリップボードに書かない

## 確認用のモデル

- 重みがランダムな極小モデルでよい(文章の中身は無意味になる)
- 長く生成し続けるモデルが要るとき(スクロールの確認など)は、重みがランダムな極小モデルの語彙に、空白で始まるふつうのトークンを数万個足すと、終わりのトークンが選ばれにくくなる
- バイト単位の語彙のモデルでは日本語1文字が約3トークンになるので、要約の指示文が作業領域に入らないことがある([tuning.md](tuning.md))
