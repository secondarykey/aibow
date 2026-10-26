# 0021: aibow-server の開発とビルドの入り口(画面のビルドを忘れない)

- 状態: ✅ 完了(2026-10-26。確かめたことは下の「確かめたこと」)
- ロードマップ: ロードマップの外
- 決めた日: 2026-10-26(Code でユーザーと決めた)
- 前提: 0016(aibow-server が画面を埋め込んで配る。画面のビルド結果 `web/dist/app` は Git に入れない)

## 目的

**aibow-server を開発で動かすとき・exe を作るときに、画面(`_cmd/aibow/frontend`)のビルドを忘れられないようにする**。
今は「先に画面をビルドしてから `go run` / `go build`」の2段で、画面をビルドし直し忘れると古い画面が埋め込まれる(2026-10-26、「利用者」のタブが出なかった。古いと注意を出すより、間違えられない形にしたい、というユーザーの考え)。
画面のアプリの `wails3 dev` のように、1つのコマンドで画面と aibow-server が揃って動くようにする。

## 決定事項(ユーザーと決めたこと。2026-10-26)

1. **入り口は Go のスクリプト**(`_cmd/version.go` と同じく、Go と標準ライブラリだけ。要るのは Go と Node.js)。`go run ./_cmd/dev <コマンド>`
2. **開発中の画面は vite の開発用サーバーから配る**(`wails3 dev` と同じ考え。保存するとブラウザにすぐ出る。画面のビルドは要らない)
3. **exe を作る入り口も作る**:画面をビルドしてから aibow-server の exe を作る(`go build` だけで作ると古い画面が入る、をなくす)

## 決定事項(Code が決めたこと)

4. コマンドは2つ:
   - `go run ./_cmd/dev server [aibow-server の引数...]`:vite の開発用サーバー(`127.0.0.1:9246`。画面のアプリの `wails3 dev` の 9245 とぶつからないように)と、`go run ./_cmd/aibow-server` を一緒に起動する。
     ブラウザは vite のほう(`http://127.0.0.1:9246/`)を開く。Ctrl+C で両方止める
   - `go run ./_cmd/dev build [-o 出力先]`:`npm run build` で画面をビルドしてから `go build ./_cmd/aibow-server` で exe を作る
5. **aibow-server には開発のためのコードを足さない**。vite の側で、`/aibow/` の呼び出しを aibow-server に渡し(vite の proxy)、ブラウザの印(`<meta name="aibow-browser">`)を index.html に足す。
   どちらも、スクリプトが環境変数 `AIBOW_BROWSER_DEV`(aibow-server のアドレス)を渡したときだけ効く(`wails3 dev` の画面には効かない)
6. aibow-server のアドレスは、引数の `-port`・`-host`・`-tls-cert`、無ければ設定ファイル(`-config` か `%APPDATA%\aibow\server.json`)の `port`・`host`・`tlsCert` から決める(既定 `http://127.0.0.1:8089`)
7. `lib`・`models` の場所は、どこにも書いていなければ `_cmd/aibow/lib`・`_cmd/aibow/models` にする(環境変数 `AIBOW_LIB`・`AIBOW_MODELS_DIR` として渡す。環境変数は引数と設定ファイルより弱いので、書いた値が勝つ)
8. `node_modules` が無ければ、先に `npm install` する
9. OpenAI 互換の口(Cline など)と画面のアプリは、今までどおり aibow-server(`:8089`)に直接つなぐ(vite を通すのはブラウザの画面だけ)

## 確かめたこと(2026-10-26)

- `go run ./_cmd/dev server -config <一時フォルダ>/server.json`(port 18090・ログインあり・CPU):vite(9246)と aibow-server が起動し、
  `http://127.0.0.1:9246/` の index.html にブラウザの印が入り、`/aibow/auth/me` が aibow-server に渡る。
  Chrome で開くと管理者の画面に「利用者」のタブが出て、知らせ(SSE)も vite を通してつながる。`SettingsView.tsx` のタブの名前を変えると、読み直さずに画面に出た(HMR)
- スクリプトのプロセスを強制終了しても、vite と aibow-server が残らない(どちらも Windows のジョブオブジェクトに入れてある。`internal/proc`)
- `go run ./_cmd/dev build -o <一時フォルダ>/aibow-server.exe`:`npm run build` のあとに `go build` し、作った画面の時刻を出す
- 見ていないこと:ターミナルで Ctrl+C を押したときの止まり方(このセッションからはコンソールに Ctrl+C を送れない)・HTTPS の aibow-server に渡すとき・mac / Linux

## やらないこと

- Go のコードが変わったら aibow-server を自動で起動し直す(Ctrl+C して打ち直す)
- バインディング(`frontend/bindings`)を作り直す(Go のメソッドを変えたら、今までどおり `wails3 generate bindings` か `wails3 build`。Git に入っている)
- 配布の仕組み(ビルド済みの exe を配る)。`build` はその土台になる

## 経過(TODO.md から移した。2026-10-26)

TODO.md を短い入口にしたとき(0025)、TODO.md の行に書き足してきた経過と決めたことを、そのまま移した。

### aibow-server の開発とビルドの入り口(`_cmd/dev`)

aibow-server の開発とビルドの入り口(2026-10-26、ユーザーの要望。画面のビルドを忘れて古い画面が埋め込まれたため。**2026-10-26 に完了**):`go run ./_cmd/dev server`(画面は vite の開発用サーバーから配り、aibow-server は go run)・`go run ./_cmd/dev build`(画面をビルドしてから exe を作る)
