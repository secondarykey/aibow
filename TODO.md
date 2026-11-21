# TODO

aibow に残っている課題の一覧(入口)。ルールと全体の構成は `AGENTS.md`、1つ1つの機能の仕様は `_docs/features/` にある。

- **済んだものは載せない**。作ったものの経過と決めたことは仕様のファイルに、これまでの歩み(ステップ1〜12)は [DEVELOPMENT.md](DEVELOPMENT.md) の「ロードマップ」にある
- 行は「状態・一言・資料へのリンク」だけにする(ローカルのモデルにも読める大きさに保つため。0025)。仕様の無い課題の中身は [_docs/backlog.md](_docs/backlog.md) に書く
- 着手するときは Chat で相談して仕様(`_docs/features/`)を作ってから。完了したら仕様の「状態」を ✅ にし、ここからは行を消す

状態: ⬜ 未着手 / 🔧 作業中

## 会話とエージェント

| | 課題 | 資料 |
|---|---|---|
| ⬜ | コーディングエージェントとして洗練する(worktree の続き・テストで落ちたら直すループなど) | [検討メモ](_docs/backlog.md#コーディングエージェントとして洗練する) |
| ⬜ | コマンドの出力を途中から見せる(go test などの実行中に、出力を流して見せる) | [検討メモ](_docs/backlog.md#コマンドの出力を途中から見せる) |
| ⬜ | 裏で要約する(返事を書き終えたあと、次に備えて2つ目の作業領域で要約しておく) | [検討メモ](_docs/backlog.md#裏で要約する) |
| 🔧 | ワークスペースの作り方・確認のカード・実行中の表示を見直す | [0049-ui-review.md](_docs/features/0049-ui-review.md) |
| ⬜ | 会話の検索(部分一致・全文検索・意味の近さ) | [0003-search.md](_docs/features/0003-search.md)(検討メモ) |
| ⬜ | KV キャッシュの使い回しを良くする | [検討メモ](_docs/backlog.md#kv-キャッシュの使い回しを良くする) |

## ツールと MCP

| | 課題 | 資料 |
|---|---|---|
| ⬜ | ツールの信頼の続き(サーバーごとにまとめて確認しない・`readOnlyHint`・自作サーバーの二重の確認) | [0010-tool-trust.md](_docs/features/0010-tool-trust.md) の「3段目以降」 |
| ⬜ | 確認のカードの「今後も同じ」がツール全体に効くと分かりにくい(git status を許したつもりで run_command が全部通る)。コマンドごとの許可 | [検討メモ](_docs/backlog.md#確認のカードの今後も同じが何に効くか分かりにくいコマンドごとの許可) |
| ⬜ | MCP の OAuth(ログインが要る url の MCP のサーバー) | [検討メモ](_docs/backlog.md#mcp-の-oauth) |
| ⬜ | ツールと MCP のサーバーをほかのアプリの部品にする(メモ帳から使う) | [0045-as-parts.md](_docs/features/0045-as-parts.md)(相談中) |
| ⬜ | Web で検索する(ページを読むのは組み込みの `fetch_url` で済んだ。残りは探すほう) | [検討メモ](_docs/backlog.md#web-で検索する) |

## aibow-server と組織で使う

| | 課題 | 資料 |
|---|---|---|
| ⬜ | 共有の再検討(アプリからの閲覧・プロジェクトの概念・利用者同士の会話) | [検討メモ](_docs/backlog.md#共有の再検討アプリからの閲覧プロジェクトの概念)・[0018-sharing.md](_docs/features/0018-sharing.md) |
| ⬜ | aibow-server の中央の制御の残り(上限の上限・順番待ちの優先度・自動で降ろす・server.json を画面で) | [検討メモ](_docs/backlog.md#aibow-server-の中央の制御の残り) |
| ⬜ | aibow-server の起動のときのモデル(指定が無い・見つからないときに、最後に読み込んだモデルか既定の印で選ぶ。今は一覧の名前順の先頭) | [検討メモ](_docs/backlog.md#aibow-server-の起動のときのモデル指定が無いとき見つからないとき) |
| ⬜ | 利用者をまとめて作る(インポート) | [検討メモ](_docs/backlog.md#利用者をまとめて作るインポート) |
| ⬜ | 複数のモデルを同時に動かす | [検討メモ](_docs/backlog.md#複数のモデルを同時に動かす) |

## モデルの対応

モデル自身の機能と書式の話。会話の仕組み・サーバー・ツールの話とは分けて扱う(2026-10-26、ユーザーの指示)。対応済みの書式は [_docs/usage.md](_docs/usage.md) の「使えるモデル(書式)」。

| | 課題 | 資料 |
|---|---|---|
| ⬜ | Qwen の新しい版(Qwen3.6・3.8)が Qwen3.5 の書式で動くか確かめる(GGUF の種類の名前とテンプレートを見る) | 未作成 |
| ⬜ | Mistral の書式(ministral-3・mistral 7B。`[INST]`・`[TOOL_CALLS]`。ministral-3 が 8GB に入るかは未確認) | 未作成 |
| ⬜ | Phi-4-mini の書式(Microsoft の 3.8B) | 未作成 |
| ⬜ | gpt-oss の書式(harmony。20b は約 14GB で VRAM 8GB に入らない。メモリと分け合えば動くかは未確認) | 未作成 |
| ⬜ | DeepSeek の書式(V3.x・R1。大きいモデル向けで、この PC では確かめにくい) | 未作成 |
| ⬜ | GLM の書式(4.x。Qwen とは違う XML 風の呼び出し。大きいモデル向け) | 未作成 |
| ⬜ | Llama 4 の書式(Llama 3 とは区切りが変わっている見込み。大きいモデル向け) | 未作成 |
| ⬜ | 画像を作る(後回し。別の画像生成モデルをツールで呼ぶ形) | [検討メモ](_docs/backlog.md#画像を作る後回し) |

## 仕組みの整理

| | 課題 | 資料 |
|---|---|---|
| ⬜ | ログの続き(パッケージごとのレベル・llama.cpp のログ・画面からログを見るなど) | [0008-log.md](_docs/features/0008-log.md) の「やり残し」 |
| ⬜ | ログのレベル分けを見直す(設定を変えたときなどに出すぎる。E4 の「層数の変更」は出なくてよい) | 未作成 |
| ⬜ | パッケージの分け方(手元に残る設定のファイルをルートから分けるか) | [検討メモ](_docs/backlog.md#パッケージの分け方) |

## 確かめていないこと(作ったもの)

完了にしたが、実際の画面・環境では確かめていないもの。確かめたら仕様に書き、ここからは行を消す。

| | 確かめること | 資料 |
|---|---|---|
| ⬜ | 画面のアプリ(serverUrl)での共有・配られたプロファイル・共有の相手の選択・アカウントのタブの使用量 | [0018](_docs/features/0018-sharing.md) の「11-3」・[0031](_docs/features/0031-users-ui.md)・[0033](_docs/features/0033-usage.md) |
| ⬜ | aibow-server を GPU の 9B で:2人同時の生成(ブラウザの画面を2つ並べて。`parallel` 2)・資料のフォルダのツール | [0023-parallel.md](_docs/features/0023-parallel.md)・[0024-server-tools.md](_docs/features/0024-server-tools.md) |
| ⬜ | ほかの PC からつなぐ:本物の Cline・HTTPS・資料のフォルダの見え方 | [0018](_docs/features/0018-sharing.md) の「11-3」・[0024](_docs/features/0024-server-tools.md) |
| ⬜ | アプリのログイン:トークンの期限切れ・証明書を作り直したときの画面(テストだけ) | [0019-app-login.md](_docs/features/0019-app-login.md) |
| ⬜ | 同じ呼び出しの繰り返しを止める仕組みを、llama3.2:3b で | [0028-repeated-calls.md](_docs/features/0028-repeated-calls.md) |
| ⬜ | aibow-mcp の `git_status`・`git_diff` を、Claude Code などほかのクライアントから | [0041-agent-prompt.md](_docs/features/0041-agent-prompt.md) |
| ⬜ | 返事の途中で前のツールの結果を省く compact の画面(吹き出しの「…を省いてモデルに渡しました」・ツールのカードの「省略して渡した」)と、省いても目安を下回らないときに省かない形を 4B で | [0040-compact-in-turn.md](_docs/features/0040-compact-in-turn.md) |
| ⬜ | ボタンの言葉と見た目(削除の形・既定のプロファイルの選択肢で新しい会話の画面の最初の選択が変わる・赤い「削除」・アイコンの付け方)を、アプリとブラウザの画面で | [0050-buttons.md](_docs/features/0050-buttons.md) の「経過」 |
| ⬜ | 信頼するプロキシ:ユーザーのリバースプロキシで X-Forwarded-For を書き、ほかの PC から開くとログの相手がその PC になる(管理の操作の断りは 0046 でどこからでも同じ) | [0044-trusted-proxies.md](_docs/features/0044-trusted-proxies.md) の完了の条件 2 |
