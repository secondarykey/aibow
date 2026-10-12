# ユーザーの Windows 環境で確かめる

ユーザーの PC(Windows、NVIDIA GPU)の worktree で作業し、アプリを起動して確かめるときの手順と注意です。
どの環境でも同じ手順と確認する項目は [testing.md](testing.md) にあります。

この環境には、ユーザーがふだん使っている aibow 本体(`/d/Go/Projects/aibow`)とそのアプリ・DB がある。**それらを壊さない・混ぜないこと**が一番の注意点です。

## worktree で起動するとき

worktree には `lib\` / `models\` / `frontend/node_modules` が無い。

- `lib\` / `models\` は `AIBOW_LIB` / `AIBOW_MODELS_DIR` で本体(`/d/Go/Projects/aibow/_cmd/aibow/`)を指す(ユーザーの環境では、worktree のセッション開始時にこの2つが自動で設定される)。
  **worktree に `lib\` / `models\` を作らない・入れ直さない**(yzma の install や GGUF のダウンロードをしない)
- 本体の `lib\` / `models\` へのジャンクションやシンボリックリンクを張らない。worktree の解放時に Git 管理外のファイルが消され、リンク先の本体の実体まで消える
- `node_modules` は worktree の `_cmd/aibow/frontend` で `npm install` する(ユーザーの環境で本体へのジャンクションが自動で張られている場合は、そのまま使い、`npm install` しない)

## 画面を外から操作する(CDP)

一時ビルドで `application.Options.Windows.AdditionalBrowserArgs` に `--remote-debugging-port=9222` を足し、
CDP(`http://localhost:9222/json`)で `Runtime.evaluate` する(この変更はコミットしない)。

- ユーザーはふだんから aibow を起動していることがある。一時ビルドでは `application.Options.Windows.WebviewUserDataPath` も一時フォルダにする
  (同じ WebView2 のデータフォルダを起動引数の違うプロセスで使うと、後から起動した方が WebView2 を作れず失敗する: 8007139f)
- 試しに起動したアプリを閉じるときは、**自分が起動したプロセスの PID だけ**を終了させる。`taskkill /IM aibow.exe` のように名前で終了させない(ユーザーのアプリまで閉じてしまう)
