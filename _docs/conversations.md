# 会話の仕組みと設定

ルートの `aibow` パッケージのうち、会話の履歴・会話ごとの設定・アプリの設定・保存の約束の説明です。
決めた経緯は [features/0001-conversations.md](features/0001-conversations.md) にあります。
会話の上限に収める仕組み(compact)は [compact.md](compact.md) に分けています。

## ファイル

| ファイル | 役割 |
|---|---|
| `conversation.go` | 会話の仕組み(`Conversations`)。発言の保存、タイトルの自動付与と名前の変更、生成、答えが出なかった回の理由の文、生成は同時に1つ。ツールを使うループは `agent.go`([tools.md](tools.md)) |
| `store.go` | 保存先の約束(`Store` interface)と、会話・発言・要約の型(`Conversation` / `StoredMessage` / `ReplyStats` / `ToolResult` / `Compaction`) |
| `appsettings.go` | アプリの設定と新しい会話の既定(`app_settings` の読み書き)。モデルの読み込みに関わる設定は `WorkspaceSettings` にまとめる(プロファイルに移す塊)。会話の上限・返事の上限の古い置き場(`history_tokens` / `reply_tokens`)は、新しい会話の既定に引き継ぐためだけに読む。環境変数は DB に値が無いときの初期値。古い `n_ctx` の引き継ぎ |
| `memstore_test.go` | テスト用のメモリ上の保存先 |
| `conversation_test.go` | 会話の仕組みと設定の引き継ぎのテスト。モデル不要 |

`Store` の SQLite 実装は [store/AGENTS.md](../store/AGENTS.md) を参照。

## 履歴の持ち方

- 会話履歴は Go(`aibow.Conversations`)が持ち、SQLite に保存する。画面は会話の ID と新しい発言だけを送り、Go が毎回並べ直してモデルに渡す
  (モデルはステートレス。会話の上限を超えたら compact で収める)
- 発言の role は user / assistant / tool。ツールを使うと1回の送信で `user → assistant(呼び出し)→ tool … → assistant` と1件ずつ保存する([tools.md](tools.md))
- 過去のアシスタント発言の思考は保存して画面で見られる。プロンプトに入れるかは書式が決める(Qwen3.5 はツールのループの途中の返事だけ入れ、それ以外は答えだけ)
- システムプロンプトは発言として保存せず、会話の設定から毎回先頭に付ける
- ユーザーが選んで「モデルに渡さない」にした発言(`StoredMessage.Excluded`。0011)は、送信・メーター・今すぐ要約で履歴を読んだところで外す(`forModel`)。
  外したあとの並びで組を数え直す。変えるのは吹き出し1つ分ずつ(`Conversations.SetExcluded`。返事はツールの呼び出しと結果も一緒)で、生成中の会話は変えられない
- 答えが空のまま終わった回(思考の途中で打ち切り・停止)は、理由の文を assistant の発言として保存する(質問が宙に浮かないように)。`conversation.go` の `noAnswerNote`。
  ツールを呼んで答えが空の回には付けない(ループの続きで答えるため)

## 設定

- 会話ごとの設定(システムプロンプト・temperature・思考・presence penalty・compact の方式・作業フォルダ・使う MCP のサーバー)は `conversations.settings`(JSON)
  - `compact` が空の古い会話は `summary` として扱う(`normalized`)。古い JSON の `max_tokens` は読むときに無視される
  - 以前の「ツールを使う」(`tools`)は 0006 で無くした。ツールを使うかは、設定の画面の「ツール」(`tools.json`)と会話で使う MCP のサーバーだけで決める。保存済みの JSON に残っている `tools` は読むときに無視される(OFF だった会話でもツールを使うようになる)
  - `work_dir`(作業フォルダの絶対パス。空なら決めていない)は**新しい会話の既定に入れない**(`asDefaults` で外す)。相対パスは保存しない(`validate`)。
    決めた会話では、ファイルのツール・コマンドのツールと環境の説明がプロンプトに入る(使わないにしたツールは除く。[tools.md](tools.md))
  - `mcp_servers`(この会話で使う MCP のサーバーの名前)は、保存する新しい会話の既定には入れない(`asDefaults` で外す)。新しい会話では、設定の画面の「ツール」で使うにしているサーバーを最初から選ぶ(`Conversations.Defaults`)。読むときは空の配列にそろえる(`normalized`)
  - `builtin_off`(この会話で組み込みのツールを使わない。入力欄の上の「この会話のツール」で外したとき)は新しい会話の既定に入れない(新しい会話では使う)
  - `tool_policies`(この会話だけのツールの扱いの上書き。会話の設定の画面の「ツール」で `on` / `off` を書く)は新しい会話の既定に入れない(`asDefaults` で外す)。知らない値は保存しない(`validate`)。仕組みは [mcp.md](mcp.md)
- 新しい会話は `app_settings` の既定の写しから始まる(新しい会話の画面で写しを変えると、その設定で作る)
- 画面では、既定は設定の画面の「新しい会話の既定」、会話ごとの設定は一覧の各行の歯車で開く会話の設定の画面で変える([features/0009-conversation-settings.md](features/0009-conversation-settings.md))
- 会話の上限・返事の上限(`history_tokens` / `reply_tokens`)も会話ごとの設定(0009)。会話を作るときに既定から写すので、既定を変えても今ある会話は変わらない。
  この機能より前に作った会話(0 のもの)には、起動したときに今の既定の値を写して保存する(`FillMissingLimits`)。作業領域の作り直しは [compact.md](compact.md)
- アプリの設定(GPU の層数・最後に使ったモデル = `WorkspaceSettings`)も `app_settings`。会話の設定に無いものだけを持つ
- 画面で変えられる設定(新しい会話の既定の会話の上限・返事の上限・presence penalty と、GPU の層数)の環境変数は「DB にまだ値が無いときの初期値」で、画面で変えたら DB の値が優先

## 保存

- 発言の保存では会話を丸ごと書き戻さない(`Store.TouchConversation` で更新日時と空のときだけのタイトル)。生成中に画面で変えた名前や設定を古い値で上書きしないため
- ID は `uuid.NewV7()`(作成順に並ぶ)。発言の順番は ID の並びで決める(順番用の列は持たない)

## 生成の排他

- 生成中の会話は1つだけ(`Conversations.generating`)。生成中の会話は削除できない
- エンジンの側でも `Engine.mu` で生成とモデルの入れ替えを排他している([engine.md](engine.md))
