# 会話の仕組みと設定

ルートの `aibow` パッケージのうち、会話の履歴・会話ごとの設定・アプリの設定・保存の約束の説明です。
決めた経緯は [features/0001-conversations.md](features/0001-conversations.md) にあります。
会話の上限に収める仕組み(compact)は [compact.md](compact.md) に分けています。

## ファイル

| ファイル | 役割 |
|---|---|
| `conversation.go` | 会話の仕組み(`Conversations`)。発言の保存、タイトルの自動付与と名前の変更、生成、答えが出なかった回の理由の文、生成は同時に1つ。ツールを使うループは `agent.go`([tools.md](tools.md)) |
| `store.go` | 保存先の約束(`Store` interface)と、会話・発言・要約の型(`Conversation` / `StoredMessage` / `ReplyStats` / `ToolResult` / `Compaction`) |
| `appsettings.go` | アプリの設定と新しい会話の既定(`app_settings` の読み書き)。モデルの読み込みに関わる設定は `WorkspaceSettings` にまとめる。新しい会話の既定は、プロファイルがあればその値を読む。前の置き場(`conversation_defaults`、もっと古い `history_tokens` / `reply_tokens`・`n_ctx`)は、プロファイル「既定」に引き継ぐためだけに読む。環境変数は DB に値が無いときの初期値 |
| `profile.go` | 設定のプロファイル(`Profile`。0014)。値(`ProfileValues`)、起動したときの引き継ぎ(`PrepareProfiles`)、新しい会話で最初に選ばれるプロファイルの値の変更(`SetDefaults` / `SetDefaultGroup` が使う) |
| `rules.go` | プロファイルの決まり(`ProfileRules`:固定・使えない)の判定。使うときの設定に当てる(`withProfile`)・保存のときに断る(`checkRules`)・固定した項目は保存してある値を残す(`keepStored`) |
| `memstore_test.go` | テスト用のメモリ上の保存先 |
| `conversation_test.go` | 会話の仕組みと設定の引き継ぎのテスト。モデル不要 |
| `profile_test.go` | プロファイルの引き継ぎ(「既定」を作る・会話を紐づける・印の付け直し)のテスト。モデル不要 |
| `rules_test.go` | 決まり(固定した項目はプロファイルの値・あとから固定しても効く・保存のときに断る・使えないツールとグループ)のテスト。モデル不要 |
| `model_test.go` | 会話ごとのモデル(送信の前に読み直す・モデルを写す)とモデルごとの GPU の層数のテスト。モデル不要 |

`Store` の SQLite 実装は [store/AGENTS.md](../store/AGENTS.md) を参照。

## 履歴の持ち方

- 会話履歴は Go(`aibow.Conversations`)が持ち、SQLite に保存する。画面は会話の ID と新しい発言だけを送り、Go が毎回並べ直してモデルに渡す
  (モデルはステートレス。会話の上限を超えたら compact で収める)
- 発言の role は user / assistant / tool。ツールを使うと1回の送信で `user → assistant(呼び出し)→ tool … → assistant` と1件ずつ保存する([tools.md](tools.md))
- 過去のアシスタント発言の思考は保存して画面で見られる。プロンプトに入れるかは書式が決める(Qwen3.5 はツールのループの途中の返事だけ入れ、それ以外は答えだけ)
- システムプロンプトは発言として保存せず、会話の設定から毎回先頭に付ける
- ユーザーが選んで「モデルに渡さない」にした発言(`StoredMessage.Excluded`。0011)は、送信・メーター・今すぐ要約で履歴を読んだところで外す(`forModel`)。
  外したあとの並びで組を数え直す。変えるのは吹き出し1つ分ずつ(`Conversations.SetExcluded`。返事はツールの呼び出しと結果も一緒)で、生成中の会話は変えられない
- 最後の返事を作り直す(`Regenerate`)・最後の発言を直して送り直す(`Resend`)は、送信(`Send`)と入り口だけ違う(`turn`。0011)。
  消す部分(作り直すは最後の発言の後ろ、送り直すは最後の発言から後ろ)を決め、会話の上限に収まるかを確かめてから消す(`Store.DeleteMessages`)
- 答えが空のまま終わった回(思考の途中で打ち切り・停止)は、理由の文を assistant の発言として保存する(質問が宙に浮かないように)。`conversation.go` の `noAnswerNote`。
  ツールを呼んで答えが空の回には付けない(ループの続きで答えるため)

## 設定

- 会話ごとの設定(システムプロンプト・temperature・思考・presence penalty・compact の方式・作業フォルダ・使う MCP のサーバー)は `conversations.settings`(JSON)
  - `compact` が空の古い会話は `summary` として扱う(`normalized`)。古い JSON の `max_tokens` は読むときに無視される
  - 以前の「ツールを使う」(`tools`)は 0006 で無くした。ツールを使うかは、ツールの扱い(会話の上書き・プロファイル・`tools.json`)と会話で使う MCP のサーバーだけで決める。保存済みの JSON に残っている `tools` は読むときに無視される(OFF だった会話でもツールを使うようになる)
  - `work_dir`(作業フォルダの絶対パス。空なら決めていない)は**新しい会話の既定に入れない**(`asDefaults` で外す)。相対パスは保存しない(`validate`)。
    決めた会話では、ファイルのツール・コマンドのツールと環境の説明がプロンプトに入る(使わないにしたツールは除く。[tools.md](tools.md))
  - `mcp_servers`(この会話で使う MCP のサーバーの名前)と `builtin_off`(この会話で組み込みのツールを使わない。入力欄の上の「この会話のツール」で外したとき)は、
    新しい会話では、プロファイルの「使うツールのグループ」(設定の画面のプロファイルの「ツール」)を写す。settings.json に無いサーバーは無視する(`Conversations.Defaults`)。読むときは空の配列にそろえる(`normalized`)
  - `tool_policies`(この会話だけのツールの扱いの上書き。会話の設定の画面の「ツール」で `on` / `off` を書く)は新しい会話の既定に入れない(`asDefaults` で外す)。知らない値は保存しない(`validate`)。仕組みは [mcp.md](mcp.md)
- 新しい会話は、新しい会話で最初に選ばれるプロファイルの値の写しから始まる(新しい会話の画面で写しを変えると、その設定で作る)。会話はそのプロファイルを覚える(`Conversation.ProfileID`)。詳しくは下の「プロファイル」
- 画面では、既定は設定の画面の「会話」のプロファイル、会話ごとの設定は一覧の各行の歯車で開く会話の設定の画面で変える([features/0009-conversation-settings.md](features/0009-conversation-settings.md))
- 会話の上限・返事の上限(`history_tokens` / `reply_tokens`)も会話ごとの設定(0009)。会話を作るときに既定から写すので、既定を変えても今ある会話は変わらない。
  この機能より前に作った会話(0 のもの)には、起動したときに今の既定の値を写して保存する(`FillMissingLimits`)。作業領域の作り直しは [compact.md](compact.md)
- アプリの設定(GPU の層数・最後に使ったモデル = `WorkspaceSettings`)も `app_settings`。会話の設定に無いものだけを持つ。
  GPU の層数はモデルごと(`model_gpu_layers`。モデルの ID → 層の数。`LayersFor`)で、まだ覚えていないモデルは全体の値(`gpu_layers`)。
  前の版の DB は、今の層の数を最後に使ったモデルの値として覚える(`LoadAppSettings`。0014 の 4・10)
- 画面の設定(`DisplaySettings`。「スクロールしたら、質問を右上に出しておく」= `pin_question` と、明るさ = `theme`(`system` / `light` / `dark`。知らない値は `system`))も `app_settings`。会話によらずアプリ全体で1つで、
  会話の設定にもプロファイルにも入れない(2026-10-19、ユーザーの指示)。生成に関わらないので、変えてもモデルや作業領域は読み直さない(`SetDisplay`。変わった項目だけ保存してログに残す)。
  初めは質問を右上に出す・明るさはシステム(`config.go`。環境変数は作っていない)
- **モデルは会話ごと**(`model`。モデルの ID。0014 の 3)。送信・作り直す・今すぐ要約の前に、読み込んでいるモデルと違えば読み込む(`ensureWorkspace` → `ensureModel`。
  読み込むのはアプリが渡す係 `ModelLoader`。目録とモデルごとの層の数はアプリが持つ)。会話を開いただけでは読み直さない。読み直したら `model_loading` / `model` のイベントで知らせる。
  新しい会話はプロファイルのモデルを写す(プロファイルにモデルが無ければ読み込んでいるモデル)。この機能より前の会話には、起動したときに最後に使ったモデルを写す(`FillMissingModels`)
- 新しい会話で最初に選ばれるプロファイルの ID も `app_settings`(`default_profile`)

## プロファイル(0014)

「新しい会話の既定」に名前を付けたもの。仕様と進み具合は [features/0014-profiles.md](features/0014-profiles.md)。作業の順番の 1〜4(保存と引き継ぎ・会話ごとのモデル・決まり・画面の最初の形)まで済み。

- **値は写し、決まりは参照**。値(`ProfileValues`:システムプロンプト・temperature・思考・presence penalty・compact の方式・会話の上限・返事の上限・モデル・使うツールのグループ)は会話を作るときに写す。
  決まり(`ProfileRules`:固定した項目・使えないツール)は、会話が覚えているプロファイル(`Conversation.ProfileID`)からそのつど引く(`rules.go`)
- ツールごとの「最初は使わない」(`tools_off`)はプロファイルの値。会話を作るときに会話の上書き(`tool_policies` の off)として写す(値は写し)。
  前はこの PC の既定(tools.json の tools の off)で、起動したときに全部のプロファイルと今ある会話の上書きに移して tools.json から消す(`moveToolsOff`。2026-10-20)
- **決まりの当て方**(扱いを決めるのは Go の1か所。画面は結果を出すだけ):
  - `Conversations.Get` / `List`(と送信の途中の読み直し)は、会話のプロファイルを読んで設定に当てる(`withProfile`)。固定した項目(`rules.fixed`。`Fix…` の名前)はプロファイルの値になる。
    会話に保存してある値は書き換えない(使うときだけ。固定を外すと会話の値に戻る。2026-10-20、ユーザーと決めた)。決まりは設定の保存しない欄(`profile`)に入り、ツールの扱い(`policyOf`)が見る
  - 保存のとき(`UpdateSettings`・`Create`・`SetToolPolicy`)は `checkRules` で断る:固定した項目をプロファイルの値から変える・使えないツールの上書きを足す / 変える(消すのはよい)・使えないグループを新しく選ぶ。
    固定した項目は保存してある値のまま残す(`keepStored`)
  - 新しい会話の既定(`Defaults`)では、使えないグループを選ばない
  - 画面には `ChatService.Conversation` の `profile`(会話のプロファイル)と、`ToolStates(会話の ID, 設定)` の `from: "profile"` で返す
- この PC に依存しないものだけを入れる。作業フォルダ・GPU の層数・MCP のサーバーの起動のしかた(`settings.json`)・ツールごとの確認の扱い(`tools.json`)は入れない
- 保存は `profiles` の表(値と決まりは JSON)。会話の `profile_id` は外部キーで、使っている会話があるプロファイルは消せない(`Store.DeleteProfile` が `ErrProfileInUse`)。消したいときは「しまう」(`archived`)
- **起動したときの引き継ぎ**(`Conversations.PrepareProfiles`。`SetRemoteTools`・`SetToolPrefs` のあとに1回):
  - プロファイルが1つも無ければ、今の既定(`LoadAppSettings` が読んだ値。DB が空なら環境変数の初期値)・最後に使ったモデル(`last_model`)・
    `tools.json` の `notDefault`(最初から使わないグループ)から「既定」を作り、`default_profile` の印を付ける。決まりは無し
  - 印のプロファイルが無い・しまってあるときは、しまっていない最初のプロファイルに付け直す
  - プロファイルを持たない会話(この機能より前の会話)を、印のプロファイルに紐づける。中身は実際に既定の写しなので、動きは変わらない
- プロファイルのモデルは、プロファイルの値を保存したとき(設定の画面で変える・会話の設定の画面の「プロファイルに保存」)だけ変わる。入力欄の下でモデルを選んでも変わらない(選ぶのは会話のモデル)
- プロファイルの名前は重ねない(しまったものとも。作る・変えるときに断る。`checkName`)
- **プロファイルの管理**(`profile.go`):`Profiles`(使っている会話の数・印つき)・`CreateProfile`・`UpdateProfile`(名前・値・決まり)・`SetProfileArchived`(しまう / 戻す。印のものはしまえない)・
  `DeleteProfile`(使っている会話が無いときだけ。印のものは消せない)・`SetDefaultProfile`(印を付け替える。しまったものは選べない)・`DefaultsOf`(プロファイルから作るときの設定)・
  `CreateIn`(選んだプロファイルから会話を作る。しまったものからは作れない)・`SaveToProfile` / `SaveAsProfile`(会話の今の設定で上書き / 新しいプロファイル。使うツールのグループも写す)
- `SetDefaults` / `SetDefaultGroup` は印のプロファイルを書き換える(ライブラリとして使うとき向け。画面は `UpdateProfile` を使う)。前の置き場(`conversation_defaults`・`tools.json` の `notDefault`)には書かない
- `PrepareProfiles` の前(ライブラリとして使うとき)は、`NewConversations` に渡した既定から作った仮のプロファイル(保存していない)。既定は保存できず、会話はプロファイルを持たずに作られる
- 画面で変えられる設定(新しい会話の既定の会話の上限・返事の上限・presence penalty と、GPU の層数)の環境変数は「DB にまだ値が無いときの初期値」で、画面で変えたら DB の値が優先

## 保存

- 発言の保存では会話を丸ごと書き戻さない(`Store.TouchConversation` で更新日時と空のときだけのタイトル)。生成中に画面で変えた名前や設定を古い値で上書きしないため
- ID は `uuid.NewV7()`(作成順に並ぶ)。発言の順番は ID の並びで決める(順番用の列は持たない)
- 返事の無い質問を残さない:送信は、会話のモデルを読み込み、書式に対応しているか・会話の上限に収まるか(`checkFits`)を確かめてから発言を保存する。
  そこで止まったら発言は保存しない(書式に未対応のモデル(gemma4 など)は、MCP のサーバーを起動する前に止める。0015)。
  画面は保存されなかった送信の吹き出しを消して文を入力欄に戻し、新しい会話の画面から送ったなら、送る直前に作った空の会話も消す([ui.md](ui.md))

## 生成の排他

- 生成中の会話は1つだけ(`Conversations.generating`)。生成中の会話は削除できない
- エンジンの側でも `Engine.mu` で生成とモデルの入れ替えを排他している([engine.md](engine.md))
