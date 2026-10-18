# 0012: OpenAI 互換 API

- 状態: 🔧 作業中(2026-10-16 に仕様を書いた。6-0 は済み。6-1・6-2 は済み(2026-10-19))
- ロードマップ: ステップ 6
- 決めた日: 2026-10-16(Chat でユーザーと決めた)
- 前提: 0004(書式の interface `Format`・ツール呼び出しの読み取り)・0005(`tool` パッケージ。0005 の決定事項 11「使う相手が現れたときに分ける」)・0007(別の exe `aibow-mcp` の形・ログ・バージョン)・0008(ログ)

## 目的

aibow の「頭」(llama.cpp で動かすモデルと書式)を、OpenAI 互換の HTTP の API として出し、Cline などの既製のツールからつないで比べる。

```
ステップ5: aibow.exe [ 頭 + 会話の仕組み ] ⇄ MCP ⇄ 自作サーバー [ 手(ツール)]
ステップ6: Cline など [ 会話の仕組み + 手 ] ⇄ HTTP(OpenAI 互換)⇄ aibow-api.exe [ 頭(モデル + 書式)]
```

- ステップ5が「手をどこで動かすか」を切り離したのに対し、ステップ6は「頭をどこで動かすか」を切り離す。TODO.md のクライアント/サーバー構成の **A(モデルだけをサーバーに置く)のサーバー側**にあたる
- 会話の仕組み(履歴の保存・compact・エージェントループ・ツールの実行)は API の側に持たない。それはクライアント(Cline)の仕事

学ぶこと:

- **API はステートレス**:クライアントは毎回、履歴の全部を送ってくる。サーバーは何も覚えないので、前回と同じ先頭部分の KV キャッシュを使い回せるかが速さを決める(`Engine.reuseCache`)。
  Qwen3.5 は途中まで巻き戻せない層(線形アテンション)があるので、クライアントが履歴の途中を書き換えると、全部を読み直すことになる。どれだけ使い回せたかをログで観察する
- **「ネイティブのツール呼び出し」の中身**:API では `tools` を JSON Schema で渡し、`tool_calls` が JSON で返るように見えるが、実際はサーバーがツールの説明をプロンプトに書き、
  モデルが書いた `<tool_call>` を読み取って JSON にしているだけ(aibow がステップ 2-3 から自分でやってきたこと)。それを API の形に包む
- **引数の型の違い**:OpenAI の `tool_calls` の引数は JSON(数は数、真偽は真偽)、Qwen3.5 の書式は値を文字列で書く(`tool.Arg`)。行きと帰りで変換が要る
- **ストリーミング(Server-Sent Events)**:`data: {...}` の行を少しずつ書き、最後に `data: [DONE]` を送る形を、標準の `net/http` で自分で書く
- **compact をどちらがやるか**:履歴を持つのはクライアントなので、上限を超えたら compact せずエラーを返す。クライアントがどう対処するか(Cline の要約など)を観察する
- 既製のコーディングエージェント(Cline)と aibow 自身のエージェントの比べ:プロンプトの長さ・ツールの使い方・4B / 9B での出来

## 決定事項(6-1 の未決事項。Code が案を示し、ユーザーと決めた。2026-10-19)

- **思考**:返事の `reasoning_content`(ストリーミングでは `delta.reasoning_content`)に分けて返す(llama.cpp の server と同じ名前。新しい vLLM は `reasoning`)。
  オン・オフはリクエストの `chat_template_kwargs.enable_thinking`(llama.cpp の server・vLLM と同じ)。書かれていなければ思考あり(アプリの既定と同じ)。
  履歴の `assistant` に `reasoning_content` があれば思考に戻す
- **会話の上限を超えたとき**:HTTP 400、`{"error": {"message": "…", "type": "invalid_request_error", "code": "context_length_exceeded"}}`(OpenAI と同じ。クライアントが「長すぎる」と見分けられる)
- **リクエストの `model`**:見ない。読み込み中のモデルをいつも使い、`/v1/models` も読み込み中の1つだけを返す(切り替えは数秒〜十数秒かかり、KV キャッシュも捨てるため)。違う名前が来たら Info のログを出す
- **同時に来たリクエスト**:順番待ちにする(断らない)。待っている間につながりが切れたら列から外す。待った時間はログに出す
- **既定のポート**:8089(llama.cpp の server 8080・vLLM 8000・Ollama 11434・LM Studio 1234 とぶつからない)
- **起動の引数と環境変数**:引数が優先、無ければ環境変数、それも無ければ既定。`-model`(`AIBOW_MODEL`。アプリと同じ。ID・一覧の名前・GGUF のパス。無ければ一覧の先頭)・
  `-ctx`(`AIBOW_API_CTX`。既定 32768。Cline などのシステムプロンプトが長いので、アプリの 16384 より大きく)・`-port`(`AIBOW_API_PORT`。既定 8089)

## 決定事項(ユーザーと決めたこと。2026-10-16)

1. **API は別の exe にする**(`_cmd/aibow-api/`。名前は下の Chat が決めたこと)。画面のアプリの中では動かさない
   - aibow-mcp と同じ形(別の exe・ルートのモジュールに入れる・`main.go` は引数を読んで起動するだけ)。画面のアプリは HTTP サーバーを立てない、という AGENTS.md の方針はそのまま
   - 画面なしで GPU のある PC で動かす、という A の形にそのままなる
   - VRAM 8GB にモデルを2つは載せられないので、**画面のアプリと同時には使わない**(使うときはアプリを閉じる)。アプリの中に入れると、画面の会話と API のリクエストが1つの作業領域を取り合い、KV キャッシュを壊し合うので、その点でも分ける
2. **先にパッケージを分ける(6-0)**:API の exe は「会話の仕組みなしで、生成だけ」を使う。TODO.md の「パッケージの分け方」で、分けるきっかけとして書いていたのがこのとき。
   使う相手(API)が決まっているので、外に見せる名前の線を推測で引かずに済む(0005 の決定事項 11)。3-0(`tool` の切り出し)と同じく、動きは変えない
3. **比べる相手はまず Cline**(ロードマップどおり)。ほかのツール(Continue・aider など)は、Cline で試してから決める

## 決定事項(Chat が話の流れから決めて書いたこと。ユーザーに確かめてもらう)

### 6-0 パッケージを分ける

- 分けるもの(TODO.md の「パッケージの分け方」の「次の候補」のとおり):
  - `models/`:GGUF と Ollama の目録(`models.go`)。何にも依存しない
  - `format/`:チャットテンプレート(`Format`・`OutputParser`・`Message`・`Prompt`・`Output`・`ToolCall`・Qwen3.5 の実装)。`tool` を使う。ルートの `testdata/qwen35/` を `format/testdata/qwen35/` に移す
  - `engine/`:llama.cpp の読み込み・生成・トークン数・KV キャッシュ(`engine.go`・`stream.go`)。`format` と `models` を使う
  - ルートの `aibow` は会話の仕組みに絞る(`Generator` の interface は会話の仕組みの側に残す)
- 型の置き場所の細かい線(例:`Output` が持つ `GenerateStats` をどちらに置くか、`formatFor` を誰が持つか)は Code が引き、この仕様に書き足す。依存の向き(`engine` → `format` → `tool`、ルート → `engine` など)が輪にならないことだけ守る
- 動きは変えない:今のテストがそのまま(移した先で)通ること、`wails3 build`・`go vet` が通ること。`_docs/engine.md`・`_docs/structure.md`・AGENTS.md の構成の表を直す

#### 6-0 で Code が引いた線(2026-10-16。済み)

- 依存の向き:ルートの `aibow` → `engine` → `format` → `tool`、`engine` → `models`。ルートは `format` も直接使う(`Message` などを組み立てるため)。輪は無い
- **生成の記録 `GenerateStats` は `engine` に置き、`format.Output` から外した**。`Engine.Generate` は `(format.Output, engine.GenerateStats, error)` を返す。
  トークン数・速さ・終了の理由は書式の話ではなく、`format` に置くと書式が生成の測り方を知ることになるため。`OutputParser.Result()` も振り分けた結果だけを返す(以前は「Stats は空」と断っていた)。
  ルートの `Generator` の interface も同じ形に変えた。保存の形(`ReplyStats` が `GenerateStats` を埋め込む)と JSON は変わらない
- **書式を選ぶ `formatFor` は `format.For(arch)` として `format` に置き、エンジンが呼ぶ**(モデルを読み込んだときに GGUF の種類で選ぶのはエンジンなので)。対応していないときのエラーも `format.Unsupported(arch)` として外に見せる。
  タグの書きかけを数える `partialSuffix` は書式の振り分けだけが使うので `format` に、UTF-8 の切れ目の `utf8Buffer` はエンジンだけが使うので `engine` に分けた
- `Generator` の interface はルートに残した(interface は使う側に置く。本物は `engine.Engine`)
- 名前はパッケージ名と重ねない形にした:`models.Entry`(以前の `ModelEntry`)・`models.Catalog`(`ModelCatalog`)・`models.OllamaDir()`(`OllamaModelsDir`)・`engine.Config`(`EngineConfig`)・`engine.New`(`NewEngine`)。
  `engine.Engine`・`engine.ModelInfo`・`engine.GenerateOptions` / `GenerateStats` / `ErrNoModel` / `ErrBusy` と、`format` の型(`Message` / `Prompt` / `Output` / `ToolCall` / `Format` / `OutputParser`)は名前を変えていない
- 画面:Wails が作るバインディングの置き場所が変わった(`ModelEntry` → `…/aibow/models/index` の `Entry`、`ModelInfo` → `…/aibow/engine`)。
  `…/aibow/models` と書くと、Wails が `aibow` パッケージに作る `models.ts` の方が選ばれるので、`index` まで書く(`useModels.ts`)
- `.gitignore` の `lib/`・`models/` は、どの階層の `models/` も除外していたので、`/_cmd/*/lib/`・`/_cmd/*/models/` にした(新しい `models/` パッケージと、そのバインディングが除外されていた)

### 6-1 文字だけの API

- 名前は `aibow-api`(`_cmd/aibow-api/`)。バージョンは aibow-mcp と同じく、アプリのバージョンを `_cmd/version.go` で書き写す(deploy の `BUILD_INFO_FILES` にも足す)
- 出すもの:`GET /v1/models`・`POST /v1/chat/completions`(`stream: true` と `false` の両方)。HTTP は標準ライブラリの `net/http` で書く(外部パッケージを足さない)
- 待ち受けは **`127.0.0.1` だけ**(PC の外からはつなげない)。外からつなぐこと・API キーは「やらないこと」に置く(クライアント/サーバー構成の相談のときに考える)
- llama.cpp の場所・モデルの場所・GPU の層数は、アプリと同じ環境変数(`AIBOW_LIB`・`AIBOW_MODELS_DIR`・`OLLAMA_MODELS`・`AIBOW_GPU_LAYERS` など)を使う。
  起動時に読み込むモデルと作業領域の大きさは、起動の引数か環境変数で決める(画面の DB は読まない。DB はアプリのもの)
- サンプリング:リクエストの `temperature`・`max_tokens`(または `max_completion_tokens`)・`presence_penalty` を使い、書かれていなければアプリの既定と同じ値にする
- 会話の上限を超えるリクエストは、compact せずにエラーを返す(クライアントが履歴を持っているため)
- クライアントがつながりを切ったら(停止ボタンなど)、生成を止める(リクエストの context を `Engine.Generate` に渡す)
- 学習用のログ:リクエストごとに、メッセージの数・プロンプトのトークン数・**KV キャッシュを使い回せた数**・生成の速さ・終了の理由を Info で出す。
  ログは aibow-mcp にならい、標準エラー出力とファイル(`aibow-api_<日付>.log`)に書く。起動(版・モデル・待ち受けのアドレス)は Notice
- 確かめ方:`curl` で非ストリーミング・ストリーミングの両方を送る。続けて送ったときに KV キャッシュが使い回されることをログで見る

#### 6-1 で Code が引いた線(2026-10-19。済み)

- 中身は **`api/` パッケージ**(`api`)に置き、`_cmd/aibow-api` は引数と環境変数を読んで起動するだけにした(aibow-mcp と `mcp/server` の関係と同じ)。
  依存は `api` → `engine` → `format`。ルートの `aibow` は import しない。生成は `api.Generator` の interface で受け取る(本物は `*engine.Engine`。テストは偽物)
- 作業領域に入るかは、書式を当てたトークン数(`CountTokens`)で**順番を待つ前に**確かめる(数えるのは生成中でもできるため)。返事の分の空きまでは求めず、足りなくなったら止めて `finish_reason: length`
- `finish_reason`:エンジンの `eos`・`stop` は `stop`、`length`・`context` は `length`
- `usage` に `prompt_tokens_details.cached_tokens`(OpenAI の「キャッシュされた入力」の欄)として KV キャッシュを使い回せた数を入れる。学習用に、ログだけでなく返事でも見える
- 受け付ける発言:`content` は文字列か部品の配列(Cline の形。文字の部品だけ)。`developer` は `system` と同じに扱う。`system` は先頭にだけ置け、続けて複数あればつなぐ(途中にあれば 400。Qwen3.5 の公式テンプレートがエラーにするため)
- 6-2 の前なので、ツール呼び出しは:`tools` は使わずに Warn のログ・`tool` の役と `tool_calls` は 400・ツールを知らせていないのにモデルが書いた呼び出しは返さずに Warn のログ
- ストリーミングの途中のエラーは `data: {"error": …}` の塊で送る(ステータスはもう 200 で送っているため)。`stream_options.include_usage` があれば最後に usage の塊を足す
- Ctrl+C で止めるとき、リクエストの context も終わらせる(`http.Server.BaseContext`)ので、生成中なら次のトークンで止まる
- deploy の `BUILD_INFO_FILES`(`Taskfile.local.yml`。コミットしない)には `../aibow-api/version` を各自で足す

#### 6-1 で動かして分かったこと(2026-10-19。4B・RTX 4060)

- `curl` で `/v1/models`・まとめて・ストリーミング(思考オフ・usage つき)が答えた。パイプが閉じてつながりが切れたとき、3 トークンで止まった(`終了理由: cancel`)。同時に2つ送ると、2つ目は 0.5 秒順番を待って 200 を返した
- **KV キャッシュの再利用は 0 のまま**:新しい user の発言を足したリクエストでは、公式テンプレートが前の返事から思考の部分を外すのでプロンプトが途中で変わり、
  Qwen3.5 は途中まで巻き戻せないので全部を読み直す(画面の会話と同じ性質)。同じリクエストを2回送っても、最後の1トークンと生成した分を捨てるため 0。
  完了の条件の「続けて送ったリクエストで使い回される」は、前回の生成の続きに足すだけのツールのループ(6-2)で確かめる。詳しくは `_docs/api.md`
- 速さ:プロンプト 75 トークンの読み込み 0.3 秒、生成 65〜70 tok/s

### 6-2 ツール呼び出しの変換

- リクエストの `tools`(`type: function` の `name`・`description`・`parameters`)を `tool.Def` にして、今の書式(Qwen3.5)でプロンプトに書く
- モデルが書いた `<tool_call>` を、返事の `tool_calls`(`id`・`function.name`・`function.arguments` は JSON の文字列)にする。引数の値は、`tools` の JSON Schema を見て型を変える(`tool.CheckArgs` と同じ考え方)。終了の理由は `tool_calls`
- 履歴に入ってくる `assistant` の `tool_calls` と、`tool` の役の発言(`tool_call_id`)を、今の書式の `Message` に戻す(引数の JSON の値は文字列に戻す)
- 書式が崩れて読み取れなかった呼び出しの扱いは、Code が案を示す(aibow の会話の仕組みは、エラーとしてモデルに返して立て直させている。API ではそれができないので、答えの文字列として返す、など)
- ストリーミングのときの `tool_calls` の送り方(まとめて1回か、少しずつか)は Code が決めてこの仕様に書く

#### 6-2 で決めたこと(2026-10-19)

ユーザーと決めたこと(Code が案を示した):

- **書式が崩れて読み取れなかった呼び出し**:モデルが書いたままを、答え(`content`)の後ろに空行を挟んで足して返す。`finish_reason` は今までどおり(`stop` / `length`)。理由は Warn のログ。
  クライアントには崩れた形が見え、Cline などは「ツールを使っていない」とモデルに言い返して立て直させる見込み
- **読み取れたが `tools` の定義に合わない呼び出し**(知らないツール名・引数の名前の間違い・型が合わない値):そのまま `tool_calls` で返す。型を変えられる値は変え、変えられない値は文字列のまま。
  クライアントのツールの検証でエラーになり、それがツールの結果としてモデルに返るので、aibow 自身のループと同じくモデルが直せる。合わない理由(`tool.CheckArgs` のエラー)は Warn のログ

Code が引いた線:

- 変換は `api/tools.go` に置いた。`tools` は `tool.Def` に(`parameters` は書いた順のまま。無ければ引数なし)、`type` が `function` でなければ 400
- **引数の値は、モデルが書いた文字のまま往復させる**:返すときは、JSON Schema の型が文字列でなく、モデルが書いた文字がその型の JSON として読めれば、その文字をそのまま JSON の値にする(`5`・`["x", "y"]`。真偽値は小文字にそろえる)。
  読めなければ文字列のまま。履歴で戻ってきたときは、文字列の値は中身に、それ以外は JSON の文字のまま書式の値に戻す。キーの順番も保つ(`json.Decoder` で1つずつ読む)。
  こうすると書き戻したプロンプトがモデルの書いた文字と一致し、ツールのループで前回の生成の分まで KV キャッシュを使い回せる(Qwen3.5 は途中から巻き戻せないので、1文字違うと全部を読み直す)
- 公式テンプレートの書き方(真偽値は `True`、配列は Python の `json.dumps` の見た目)には合わせなかった。合わせると、モデルが `true` と書いたものが `True` で戻り、使い回しが崩れるため
- `tool_call_id` は読まない(Qwen3.5 の書式は id を使わず、呼び出しと結果の順番で対応させる)。返す id は `call_` + UUID(v7)
- `tool_choice`:`"none"` ならツールを知らせない。`"required"` や特定のツールの指定は書式の上で強制できないので `"auto"` と同じに扱い、Info のログ
- **ストリーミングの `tool_calls` は、生成が終わってから1つにつき1つの塊で、引数まで全部を送る**(`index` 付き)。書式は `<tool_call>` から後ろを持っておいて生成の後で読み取るので、少しずつは送れない。
  OpenAI の形では `arguments` を分けて送ってもよく、クライアントはつなげて読むので、1回で全部でも読める
- ツール呼び出しを返すときの `finish_reason` は `tool_calls`。ログの1行に、知らせたツールの数と返した呼び出しの数を足した

#### 6-2 で動かして分かったこと(2026-10-19。4B)

- `get_weather(city, days: integer)` を渡して「東京の3日間の天気」を頼むと、`{"city":"東京","days":3}`(数は数)で呼び、結果を足して送り直すと表にまとめて答えた(まとめて・ストリーミングとも)
- **KV キャッシュが使い回された**:結果を足したリクエストで、まとめては 430 トークン中 382(= 前回のプロンプト 309 + 生成 73)、ストリーミングは 457 中 409(= 309 + 100)。
  前回の分がちょうど全部使い回されたので、引数の往復で1文字も変わっていないことが分かる。完了の条件の「続けて送ったリクエストで KV キャッシュが使い回される」はこれで確かめた

### 6-3 Cline からつないで比べる

- Cline の「OpenAI Compatible」のプロバイダーで `http://127.0.0.1:<ポート>/v1` につなぐ
- Cline が `tools` を送ってくるか(ネイティブのツール呼び出し)、プロンプトに XML でツールを書く以前の形かを、ログで確かめて記録する。
  どちらになるかは調べても分からなかった(下の「調べた結果」)。XML の形なら、6-1 だけで動く見込み
- 比べること:Cline のプロンプトの長さ(会話の上限に収まるか。「Use Compact Prompt」の有無)・同じ頼みごと(ファイルを読んで直す、など)を aibow 自身でやったときとの出来と速さ・4B と 9B。`_docs/tuning.md` に書く

## やらないこと(先回りしない)

- 画面のアプリの中で API を動かす(決定事項 1)
- PC の外からつなぐ・API キー・複数の人で使う(クライアント/サーバー構成の相談のときに考える)
- `/v1/completions`(古い形)・`/v1/embeddings`・Responses API・画像
- API の側で compact する・会話を保存する
- 画面のアプリを aibow-api のクライアントにする(`Generator` の HTTP 版。クライアント/サーバー構成の A を最後までやる形)。面白いが、ロードマップの外として TODO.md に書く
- ほかのモデルの書式(今は Qwen3.5 だけ。TODO.md の「ほかのモデルの書式に対応する」)

## 未決事項(Code は勝手に決めず、案を示してユーザーに確認する)

- (6-1 の6つは決めた。上の「決定事項(6-1 の未決事項)」)

## 調べた結果(2026-10-16。Chat が調べた)

- Cline は v3.35 から、ツールの定義を JSON Schema で API に直接渡す「ネイティブのツール呼び出し」を既定にした。対象は Claude 4 以降・Gemini 2.5・GPT-5 などの一部のモデルとプロバイダーで、
  それ以外は以前の形(プロンプトに XML でツールを書く)のまま([Cline v3.35](https://cline.bot/blog/cline-v3-35))。
  **「OpenAI Compatible」のプロバイダーでどちらになるかは未確認**(ブログに書かれていない)
- Cline の公式ドキュメントは、ローカルのモデルには設定の「Use Compact Prompt」を有効にするよう勧めている([Running Models Locally](https://docs.cline.bot/running-models-locally/overview))。プロンプトの長さの数値は未確認

## 完了の条件

- 6-0:`models/`・`format/`・`engine/` に分かれ、動きが変わらない(テスト・`wails3 build`・`go vet` が通る)。ドキュメントの構成の表が今の形
- `aibow-api` を起動し、`curl` で `/v1/models` と `/v1/chat/completions`(ストリーミングあり・なし)が答える。つながりを切ると生成が止まる
- 続けて送ったリクエストで KV キャッシュが使い回されることが、ログで見える
- `tools` を送ると、モデルの `<tool_call>` が `tool_calls`(引数は JSON の型)で返る。ツールの結果を入れた履歴を送ると、続きを書く
- Cline からつないで、ファイルを読む・直すくらいの頼みごとが通る(通らなければ、どこで詰まったかを記録する)。比べた結果を `_docs/tuning.md` に書く
- `_docs/usage.md`(aibow-api の起動と Cline からのつなぎ方・環境変数)・`_docs/setup.md`(必要なら)・AGENTS.md(構成の図と表・`_cmd/aibow-api`)を直す。仕組みの説明は新しく `_docs/api.md` に書く

## 作業の順番

1. **6-0 パッケージを分ける**(動きは変えない)
2. **6-1 文字だけの API**(`aibow-api`・`/v1/models`・`/v1/chat/completions`・ストリーミング・ログ)。未決事項の「思考」「上限を超えたとき」「model」「同時のリクエスト」「ポート」はここで案を示す
3. **6-2 ツール呼び出しの変換**
4. **6-3 Cline からつないで比べる**(6-1 のあとでも試せる。Cline が XML の形なら、6-2 の前に一度つないでみてもよい)
