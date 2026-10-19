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

## wails3 が無いとき

- バインディングは `_cmd/aibow` で `CGO_ENABLED=0 go run github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.26 generate bindings -f '' -clean=true -ts -i` で作り直せる
  (GTK が無いので cgo を切る。Go の版の違いなどの警告は出るが、コミット済みのものと同じ結果になることを確かめた。2026-10-12)
- 画面だけの変更なら、`_cmd/aibow/frontend` で `npx tsc --noEmit` と `npx vite build` で型チェックとビルドを確かめられる(`wails3 build` の画面の部分と同じ)
- `go vet ./_cmd/aibow` と `wails3 build` は GTK が無いと通らない。Go の画面側を変えたときは、server モードのビルド(上)か、ユーザーの Windows で確かめてもらう

## Go を動かさずに画面の部品を確かめる

- `_cmd/aibow/frontend/.harness/`(`.gitignore` の `.*` で Git に入らない)に、部品(`RenderedText` や `ChatView` など)にダミーのデータを渡して描くだけの
  `index.html` と `main.tsx` を置き、`npx vite build .harness --outDir <一時フォルダの絶対パス> --base ./` でビルドして、Playwright で開く。終わったら `.harness/` を消す
  - `--outDir` は `.harness` からの相対パスになるので、**必ず絶対パスで書く**(相対で書いてリポジトリの外に出力してしまったことがある)
- ダウンロードは `page.waitForEvent("download")`、時刻は `page.clock.install()` で固定・早送りして確かめられる
- 画面全体(`App.tsx`)も `frontend/dist` を開けば描けるが、Go の呼び出しはエラーになる(モデルの一覧のエラーが出る)
- ビルドしたものは `file://` で開くと、モジュールの script が読み込まれず何も描かれない。一時フォルダで小さな HTTP サーバー
  (`python3 -m http.server` か、Playwright のスクリプトの中で `node:http`)を立てて開く(2026-10-19)
- Go を呼ぶ部品(`ProfileTools`・`ConversationTools` など。設定の画面ごと描くときも)は、Playwright の `page.route("**/wails/**", …)` で Go の返事を作ると描ける。
  どのメソッドも `[]` を返すと、配列でない値を返すメソッド(`ToolPrefs` など)で落ちて画面ごと消える。そのメソッドだけは、リクエストに入るメソッドの ID
  (`frontend/bindings/.../chatservice.ts` の `$Call.ByID(…)` の数)で見分けて、形の合う値を返す(2026-10-19)

## Playwright で操作する

- Chromium はインストール済み(`/opt/pw-browsers/chromium-*/chrome-linux/chrome` を `executablePath` に渡す)。`playwright-core` は一時フォルダに入れる
- ヘッドレスの Chromium は既定でスクロールバーを隠す。スクロールバーを見るときは `ignoreDefaultArgs: ["--hide-scrollbars"]`
- `p.click("text=会話")` のような文字での指定は部分一致なので、「会話に戻る」など別のボタンを押してしまう。タブなどはクラスや `role` で指定する
- ライト / ダークは `newContext({ colorScheme: "dark" })` か `page.emulateMedia({ colorScheme })` で OS の設定を切り替えられる(明るさが「システム」のときに追いかけるかを見る)
- コピーを確かめるときは `grantPermissions(["clipboard-read", "clipboard-write"])` して `navigator.clipboard.readText()` で読む。
  server モードでは Wails の `Clipboard.SetText` は成功を返しても OS のクリップボードに書かない

## 確認用のモデル

- 重みがランダムな極小モデルでよい(文章の中身は無意味になる)
- 長く生成し続けるモデルが要るとき(スクロールの確認など)は、重みがランダムな極小モデルの語彙に、空白で始まるふつうのトークンを数万個足すと、終わりのトークンが選ばれにくくなる
- バイト単位の語彙のモデルでは日本語1文字が約3トークンになるので、要約の指示文が作業領域に入らないことがある([tuning.md](tuning.md))
