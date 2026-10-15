# 0012: OpenAI 互換 API

- 状態: 🔧 作業中(2026-10-16 に仕様を書いた。6-0 は済み)
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

### 6-2 ツール呼び出しの変換

- リクエストの `tools`(`type: function` の `name`・`description`・`parameters`)を `tool.Def` にして、今の書式(Qwen3.5)でプロンプトに書く
- モデルが書いた `<tool_call>` を、返事の `tool_calls`(`id`・`function.name`・`function.arguments` は JSON の文字列)にする。引数の値は、`tools` の JSON Schema を見て型を変える(`tool.CheckArgs` と同じ考え方)。終了の理由は `tool_calls`
- 履歴に入ってくる `assistant` の `tool_calls` と、`tool` の役の発言(`tool_call_id`)を、今の書式の `Message` に戻す(引数の JSON の値は文字列に戻す)
- 書式が崩れて読み取れなかった呼び出しの扱いは、Code が案を示す(aibow の会話の仕組みは、エラーとしてモデルに返して立て直させている。API ではそれができないので、答えの文字列として返す、など)
- ストリーミングのときの `tool_calls` の送り方(まとめて1回か、少しずつか)は Code が決めてこの仕様に書く

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

- **思考の返し方と、思考のオン・オフの指定**:OpenAI の標準には思考の欄が無い。ほかのサーバー(llama.cpp の server・vLLM・Ollama など)がどうしているか(`reasoning_content` のような欄・リクエストでの切り替え方)を調べて、案を示す
- **会話の上限を超えたときのエラーの形**:HTTP のステータスと、エラーの JSON の中身(クライアントが「長すぎる」と分かる形か)
- **リクエストの `model` の扱い**:読み込んでいるモデルをいつも使う(`model` は見ない)か、指定されたモデルに切り替えるか(切り替えは数秒〜十数秒かかる)
- **同時に来たリクエストの扱い**:生成は1つずつなので、順番待ちにするか、エラーで断るか
- **既定のポートの番号**
- 起動の引数と環境変数の名前(モデル・作業領域の大きさ)

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
