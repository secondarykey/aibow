# TODO

aibow 全体の進み具合(入口)。ルールと全体の構成は `AGENTS.md`(仕組みごとの詳細は `_docs/` とパッケージの `AGENTS.md`)、1つ1つの機能の仕様は `_docs/features/` にある。

行は「状態・一言・仕様へのリンク」だけにする(ローカルのモデルにも読める大きさに保つため。0025)。段ごとの経過と決めたことは仕様のファイルに、まだ仕様の無い検討メモは [_docs/backlog.md](_docs/backlog.md) に書く。

状態: ⬜ 未着手 / 🔧 作業中 / ✅ 完了

## ロードマップ

| | ステップ | 仕様 |
|---|---|---|
| ✅ | 1. チャット(llama.cpp を直接動かす、モデル切り替え、思考の表示、Wails v3 の画面) | — |
| ✅ | 2-1. 会話を Go 側に移し、SQLite に保存する。会話は複数。設定を画面から変えられるようにする | [0001-conversations.md](_docs/features/0001-conversations.md) |
| ✅ | 2-2. compact:会話が長くなったら、古い発言を捨てる / 要約する(上限は「会話の上限 + 返事の上限」) | [0002-compact.md](_docs/features/0002-compact.md) |
| ✅ | 2-3. ツール呼び出し:モデルが書く `<tool_call>` を読み取って実行し、結果を返して続きを書かせる(エージェントループ)。最初のツールは現在時刻と計算 | [0004-tools.md](_docs/features/0004-tools.md) |
| ✅ | 3-0. ツールを `tool` パッケージに切り出す(動きは変えない。ステップ4・5 の MCP でツールだけを使うため) | [0005-coding-agent.md](_docs/features/0005-coding-agent.md) |
| ✅ | 3-1. ミニコーディングエージェント:作業フォルダ(会話ごと)と、読むツール(`list_files`・`read_file`)。作業フォルダの外には `os.Root` で出られない | [0005-coding-agent.md](_docs/features/0005-coding-agent.md) |
| ✅ | 3-2. 書くツール(`write_file`・`edit_file`)と、実行前の確認(拒否したら理由をモデルに返して続ける) | [0005-coding-agent.md](_docs/features/0005-coding-agent.md) |
| ✅ | 3-3. コマンド実行(`run_command`。Windows は cmd.exe、それ以外は sh。実行前に確認) | [0005-coding-agent.md](_docs/features/0005-coding-agent.md) |
| ✅ | 4. MCP クライアント:公式の Go SDK で既製の MCP サーバー(filesystem)につなぎ、そのツールを使う。サーバーは `settings.json` の `mcpServers` に書く | [0006-mcp-client.md](_docs/features/0006-mcp-client.md) |
| ✅ | 5. MCP サーバー自作(Go):`tool` のツールを MCP で出し、aibow をつなぎ直す | [0007-mcp-server.md](_docs/features/0007-mcp-server.md) |
| ✅ | 6. OpenAI 互換 API の口(別の exe。Cline などの既製ツールからつないで比べる) | [0012-openai-api.md](_docs/features/0012-openai-api.md) |

ステップは順番に進める。先のステップの機能を先回りして入れない。

## ロードマップ2(組織で使う)

最終目的は組織で使えるようにすること。考え方(切り方・共有・ブラウザに出す方法・ステップごとの論点)は [0013-client-server.md](_docs/features/0013-client-server.md)(検討メモ)。進め方は上のロードマップと同じ。

| | ステップ | 仕様 |
|---|---|---|
| ✅ | 7. 設定のプロファイル化(あとで管理者が配る「共有の作業」に育つ線で作る) | [0014-profiles.md](_docs/features/0014-profiles.md) |
| ✅ | 8. 画面のアプリの生成を aibow-server 越しにする(クライアント/サーバー構成の A。aibow-api を aibow-server に) | [0015-api-client.md](_docs/features/0015-api-client.md) |
| ✅ | 9. ブラウザからも使う:aibow-server が画面を配り、ブラウザから会話する | [0016-browser.md](_docs/features/0016-browser.md) |
| ✅ | 10. 利用者とログイン・会話の持ち主 | [0017-users.md](_docs/features/0017-users.md) |
| ✅ | 11. 共有:管理者・会話の公開範囲・共有の作業 | [0018-sharing.md](_docs/features/0018-sharing.md) |
| ✅ | 12. サーバーの上で動かすツール:12-1 Web のツール・12-2 サーバーの資料のフォルダ(2026-10-26 に完了。GPU の 9B での試しと、ほかの PC からの見え方は確かめていない)・12-3 コマンドと隔離(作らない) | [0024-server-tools.md](_docs/features/0024-server-tools.md) |

## モデルの対応(ほかと分けて扱う)

モデル自身の機能と書式の話。会話の仕組み・サーバー・ツールの話とは分けて扱う(2026-10-26、ユーザーの指示)。着手するときは Chat で相談して仕様を作ってから。

| | やりたいこと | 仕様 |
|---|---|---|
| ⬜ | ほかのモデルの書式に対応する(チャットテンプレートはモデルの系統ごとに違う) | [検討メモ](_docs/backlog.md) |
| ⬜ | 画像を読む(ツールではなくモデル自身の機能) | [検討メモ](_docs/backlog.md) |

## ロードマップの外でやりたいこと

いつ入れるかは未定。着手するときは Chat で相談して仕様(`_docs/features/`)を作ってから。

| | やりたいこと | 仕様 |
|---|---|---|
| ✅ | ログの仕組み(`aibow/log`。リリース版でもログが見えるように) | [0008-log.md](_docs/features/0008-log.md) |
| ⬜ | ログの続き(0008 のやり残し:パッケージごとのレベル・llama.cpp のログなど) | [0008-log.md](_docs/features/0008-log.md) の「やり残し」 |
| ✅ | 設定のプロファイル化(ロードマップ2のステップ7にした) | [0014-profiles.md](_docs/features/0014-profiles.md) |
| ✅ | 画面の改善(要約中の文を畳む・会話の名前と ID・設定の画面のタブ) | `_docs/ui.md`・[検討メモ](_docs/backlog.md) |
| ✅ | スクロールしたら質問を右上に出す | `_docs/ui.md` の「スクロール」・[検討メモ](_docs/backlog.md) |
| ⬜ | 会話の検索(部分一致・全文検索・意味の近さ) | [0003-search.md](_docs/features/0003-search.md)(検討メモ) |
| ⬜ | 裏で要約する:2つ目の作業領域を作り、返事を書き終えたあとに次に備えて要約しておく(0002 の改良版。送信時の待ち時間をなくす。普通のモデルでは会話側の KV キャッシュを壊さない利点もある) | 未作成 |
| ⬜ | 画像を作る(後回し) | [検討メモ](_docs/backlog.md) |
| ✅ | モデルを扱う画面(設定の画面の「モデル」のタブ。0015 の 8-4 で作った) | [0015-api-client.md](_docs/features/0015-api-client.md) の 8-4 |
| ⬜ | 複数のモデルを同時に動かす | [検討メモ](_docs/backlog.md) |
| ⬜ | 複数の人で GPU を分け合う(ステップ12より後にやる) | [0023-parallel.md](_docs/features/0023-parallel.md) |
| ✅ | ツールの結果が会話の上限に入らないときの立て直し(入る分だけ切って渡す・同じ失敗の繰り返しで止める) | [0025-small-reads.md](_docs/features/0025-small-reads.md) |
| ✅ | TODO.md を短い入口にする(2026-10-26。経過は仕様へ、検討メモは `_docs/backlog.md` へ移した) | [0025-small-reads.md](_docs/features/0025-small-reads.md) |
| ✅ | モデルごとの GPU の層数を DB からファイルに移すか(決めて済んだ) | [0017-users.md](_docs/features/0017-users.md) |
| ✅ | 画面から aibow-server に切り替える(2026-10-29 に完了) | [0019-app-login.md](_docs/features/0019-app-login.md) |
| ✅ | aibow-server の既定をログイン必須にする | [0020-login-default.md](_docs/features/0020-login-default.md) |
| ✅ | aibow-server の開発とビルドの入り口(`_cmd/dev`) | [0021-server-dev.md](_docs/features/0021-server-dev.md) |
| ✅ | パスワードは本人が決める(リンクで決める) | [0022-password-links.md](_docs/features/0022-password-links.md) |
| ⬜ | aibow-server の中央の制御(ステップ11に入れたものの残りの候補) | [検討メモ](_docs/backlog.md) |
| ✅ | 選んだやり取りを捨てる(吹き出しごとに「モデルに渡さない」・作り直す・送り直す) | [0011-exclude-messages.md](_docs/features/0011-exclude-messages.md) |
| ✅ | MCP のツールの信頼(「確認しない」と確認のカードの選択肢) | [0010-tool-trust.md](_docs/features/0010-tool-trust.md) |
| ⬜ | ツールの信頼の続き(0010 のやり残し) | [0010-tool-trust.md](_docs/features/0010-tool-trust.md) の「3段目以降」 |
| ⬜ | MCP の OAuth(ログインが要る url の MCP のサーバー) | [検討メモ](_docs/backlog.md) |
| ✅ | 会話の個別設定(会話の設定の画面) | [0009-conversation-settings.md](_docs/features/0009-conversation-settings.md) |
| ⬜ | 今の組の中の compact(古いツールの結果を省略して続ける) | [検討メモ](_docs/backlog.md) |
| ⬜ | 会話の作業フォルダを PC ごとにする(aibow-server につなぐと、ほかの PC で開いた会話に前の PC のパスが残る) | [検討メモ](_docs/backlog.md) |
| ⬜ | Web で調べる(fetch・検索の MCP のサーバー) | [検討メモ](_docs/backlog.md) |
| ⬜ | コーディングエージェントとして洗練する(最後のほうでやる) | [検討メモ](_docs/backlog.md) |
| ✅ | aibow-server がモデルを読み直している間の一覧 | [0016-browser.md](_docs/features/0016-browser.md) の 9-3 |
| ⬜ | KV キャッシュの使い回しを良くする | `_docs/tuning.md` の「Cline からつないで比べる」「ファイルを読むツール」・[検討メモ](_docs/backlog.md) |
| ⬜ | パッケージの分け方 | [検討メモ](_docs/backlog.md) |
| ✅ | 画面のアプリを aibow-api のクライアントにする(ロードマップ2のステップ8にした) | [0015-api-client.md](_docs/features/0015-api-client.md) |
| ✅ | クライアント/サーバー構成の検討(組織で使う・別の GPU サーバーを使う場合):ロードマップ2にした(2026-10-19) | [0013-client-server.md](_docs/features/0013-client-server.md)(検討メモ) |
