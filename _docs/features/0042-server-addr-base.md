# 0042: aibow-server の待ち受けを addr でまとめて書く・道の頭(base)を付ける

- 状態: ✅ 完了(2026-11-08。下の「確かめたこと」と「確かめていないこと」)
- ロードマップ: ロードマップの外(aibow-server の起動の設定)
- 決めた日: 2026-11-08(ユーザーが Code のセッションで直接頼んだ:「aibow-server の起動を addr 指定、base 指定できるようにしたい。base は / で動いているが、/aibow でも動くようにしたい」。
  下の決定事項のうち 1〜6 は、頼まれた形からエージェントが決めた。違っていれば直す)
- 前提: 0012(aibow-server の引数・設定ファイル・環境変数の強さの順)、0016(ブラウザの会話・画面用の口)、0017(`-host`・ログイン・API キー)、0021(開発用のスクリプト `_cmd/dev`)、0022(パスワードを決めるリンク)

## 目的

aibow-server はこれまで `-host` と `-port` に分けて待ち受けを決め、道は `/` の下に固定だった。

- **addr**:`0.0.0.0:8089` のように1つで書けるようにする(Go の `http.Server.Addr` や、ほかのサーバーの設定と同じ書き方)
- **base**:全部の道を `/aibow/` のような頭の下に載せる。nginx などのリバースプロキシで、ほかのサイトと同じアドレスの下に aibow-server を出すため

学ぶこと:

- **道の頭を外して渡す**:`http.StripPrefix` で頭を外してから中の道に渡すと、中のハンドラは頭を知らなくてよい(`api`・`ui`・`auth` は変えていない)
- **画面は相対パスで呼ぶ**:画面が `/` に置かれる前提で `/aibow/ui/…` と書くと、頭が付いたときに届かない。相対パス(`aibow/ui/…`)なら、画面の置き場所に付いていく。
  vite の `base: "./"` で、ビルドした index.html も `./assets/…` を指す
- **最後の / の意味**:`/aibow` と `/aibow/` では、相対パスの基準が違う(`/aibow` からの `aibow/ui` は `/aibow/ui`)。`ServeMux` は `/aibow/` を登録すると `/aibow` を `/aibow/` へ転送する
- **プロキシの後ろでは、相手のアドレスがプロキシになる**:同じ PC のプロキシ越しの呼び出しは、すべて `127.0.0.1` から来たように見える

## 決定事項

1. **addr は host と port をまとめて書く別の書き方**。引数 `-addr`・設定ファイルの `addr`・環境変数 `AIBOW_SERVER_ADDR`。書いた所(引数・設定ファイル・環境変数)の `host`・`port` の代わりになり、強さの順は今までと同じ
   (引数の `-port` は設定ファイルの `addr` のポートより強い)。`-host`・`-port` は残す
2. **同じ所に addr と host・port の両方を書くとエラー**(どちらが効くかを決めておいても、書いた人が迷うため)
3. **アドレスを書かない `:8089` はポートだけを決めたことにする**。Go の `net.Listen` では `:8089` は全部のネットワークだが、黙って PC の外に出さないよう、アドレスは `127.0.0.1` のまま。全部のネットワークは `0.0.0.0:8089` と書く
4. **base は全部の道に付ける**(画面・`/v1/…`・`/aibow/ui/…`・`/aibow/auth/…` など)。`-base /aibow` なら画面は `/aibow/`、OpenAI 互換の口は `/aibow/v1`、画面用の口は `/aibow/aibow/ui/…`(頭が2つ重なるが、中の道を変えないため)。
   引数 `-base`・設定ファイルの `base`・環境変数 `AIBOW_SERVER_BASE`。既定は頭なし(今までどおり)
5. **base の書き方**:前後の `/` はあってもなくてもよい(`aibow`・`/aibow/` も `/aibow`)。使えるのは英数字と `- _ . ~` と区切りの `/` だけ(`%`・`?`・`#`・空白は URL を壊すため)。空の段・`.`・`..` は断る。
   頭の無い道は「`/aibow/` の下で待ち受けています」の 404(古いつなぎ先のままの Cline などが、理由の分からない 404 にならないように)
6. **画面は相対パスで口を呼ぶ**。`backendWeb.ts` の `/aibow/ui/…`・`/aibow/auth/…`・`/aibow/admin/…` を `aibow/ui/…` などにし、ビルドの vite の `base` を `"./"` にした(開発用サーバーは `/` のまま)。
   パスワードを決めるリンク(画面が作るもの)は `location.pathname` まで含める。プロキシが頭を外して渡す形(base なしの aibow-server)でも同じ画面で動く
7. **Git Bash が `/aibow` を Windows のパスに書き換える**(`-base /aibow` が `D:/Program Files/Git/aibow` で届いた。2026-11-08 に確かめた)。
   `-base aibow`(先頭の `/` なし)と書けば書き換えられないので、Windows のパスが届いたときは、そう言って止まる
8. **プロキシ越しの呼び出しは直さず、起動のときに注意する**。base を付けて `127.0.0.1` で待ち受けると、同じ PC のプロキシ越しに、サーバーの PC からだけの操作が外に開くことをログの注意で知らせる(下の未決事項 1)

## 作ったもの

- `_cmd/aibow-server/config.go`:`addr`・`base` の欄と引数、`listenLevels`(所ごとに addr を host・port に分ける)・`splitAddr`・`cleanBase`・`config.URL`(つなぐ側が書くアドレス。base 付き)
- `_cmd/aibow-server/browser.go`:`mount`(全部の道を base の下に載せる。頭の無い道は 404)
- `_cmd/aibow-server/main.go`:待ち受けのログ・つなぎ先・Base URL を base 付きに。base を付けて `127.0.0.1` で待ち受けるときの注意
- `_cmd/aibow-server/users.go`:コマンドが出すパスワードを決めるリンクに base を付ける
- `_cmd/dev/main.go`:vite が渡す先に addr・base を反映する(vite は渡す先のパスを頭に足す)
- `_cmd/aibow/frontend`:`backendWeb.ts`(相対パス)・`backend.ts`(リンク)・`vite.config.ts`(`base: "./"`)
- テスト:`TestResolveAddr`・`TestCleanBase`・`TestMount`・`TestLinkBase` に base

## 確かめたこと(2026-11-08)

- 一時フォルダの server.json(`"addr": "127.0.0.1:18089", "base": "/aibow"`)と、引数 `-addr 127.0.0.1:18089 -base aibow` の両方で起動し、ログのつなぎ先・Base URL・画面のアドレスが base 付きで出た
- curl:`/aibow/` は画面(ブラウザの印付き)、`/aibow` は `/aibow/` へ転送、`/aibow/v1/models` はモデルの一覧、`/aibow/assets/…` は画面のファイル、`/` と `/v1/models` は 404(待ち受けている場所を書いた文)
- ブラウザ(Chrome)で `http://127.0.0.1:18089/aibow` を開き、`/aibow/` に転送されて画面が出た。送信して返事が流れた(呼び出しは `/aibow/aibow/ui/call/…`、知らせは SSE)
- `wails3 build` の index.html が `./assets/…` を指し、ビルドした JS に `/assets/`・`/aibow/` で始まる絶対パスが残っていない

## 確かめていないこと

- 本物のリバースプロキシ(nginx など)の後ろ(頭を残して渡す形・頭を外して渡す形)と、SSE の時間切れ
- 画面のアプリ(デスクトップ)を `serverUrl` に `http://…/aibow` を書いてつなぐ(`api/client` は `つなぎ先 + パス` で組むので動く見込み)
- デスクトップのアプリの画面(`wails3 dev` と、ビルドした exe)が `base: "./"` のあとも今までどおり出るか
- `go run ./_cmd/dev server -base aibow`(vite が渡す先に base を足す形)

## 未決事項

1. **プロキシ越しの呼び出しを「サーバーの PC の上から」と見なさないようにするか**。案:(a) 今のまま注意だけ、(b) `X-Forwarded-For`・`Forwarded` が付いた呼び出しは `IsLocal` を偽にする(付けないプロキシもあるので完全ではない)、
   (c) server.json に信頼するプロキシのアドレスを書き、そこから来た呼び出しは `X-Forwarded-For` の相手で見る
2. ログインの cookie の `Path` を base に合わせるか(今は `/`。同じアドレスでほかのアプリと cookie の名前が重ならない限り困らない)
