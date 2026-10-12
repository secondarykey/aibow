# アプリを起動して確かめる

最低限の確認(`gofmt` / `go test` / `wails3 build` / `go vet`)はトップの `AGENTS.md` にあります。
ここは、実際にアプリを起動して確かめるときの、どの環境でも同じ手順と確認する項目です。
環境ごとの手順と注意は、使っている環境のほうだけを読んでください。

- ユーザーの Windows 環境(worktree・CDP): [env-windows.md](env-windows.md)
- クラウドセッション(GUI の無い Linux・server モード・Playwright): [env-cloud.md](env-cloud.md)

## 起動

実際の生成は `_cmd/aibow` で `wails3 dev`(または `wails3 build` してから `wails3 task run`)で起動し、開いたウィンドウで送受信して確認します。

- 会話の DB は、試すときは `AIBOW_DB` を一時フォルダのファイルにする(ユーザーの `%APPDATA%\aibow\aibow.db` に試しの会話を混ぜない)

## 確認する項目

次までを確認します。GPU や本物のモデルが無い環境では、llama.cpp の CPU 版と小さな GGUF(重みがランダムな極小モデルでもよい)で行います。

「読み込み → 生成 → 停止 → 2回目の送信で KV キャッシュが再利用される → モデルの切り替え → 壊れたモデルを選んだら元に戻る → 再起動しても会話が残る」

- compact は、会話の上限を小さくして(`AIBOW_HISTORY_TOKENS=1200` など)「truncate で薄くなる → summary で区切りが出る → none で止まる → 再起動しても要約が残る」を確認する。
  生成前の usage イベントのトークン数と、返事の統計のプロンプトのトークン数が一致することも見ておく(数え方がエンジンと同じであることの確認)
- Ollama の保存フォルダは、`manifests\registry.ollama.ai\library\<名前>\<タグ>`(目録 JSON)と `blobs\sha256-<ハッシュ>` を作れば模擬できます(`OLLAMA_MODELS` で指定)
- 重みがランダムなモデルでは出力される文章の中身は確認できないので、思考と答えの振り分けは `stream_test.go` で確認します
