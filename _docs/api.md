# OpenAI 互換 API(aibow-server)

aibow の「頭」(llama.cpp で動かすモデルと書式)を、OpenAI 互換の HTTP の API として出す仕組みです(ステップ6。仕様は [features/0012-openai-api.md](features/0012-openai-api.md))。
使い方(起動・設定ファイル・環境変数・curl の例)は [usage.md](usage.md) の「aibow のモデルをほかのアプリから使う(aibow-server)」にあります。
ステップ9で、aibow-server はブラウザに画面を配り、ブラウザの会話(会話の仕組みと保存)も持つようになった。その口は下の「画面用の口」。

```
Cline など [ 会話の仕組み + 手 ] ⇄ HTTP(OpenAI 互換。127.0.0.1 だけ)⇄ aibow-server [ 頭(モデル + 書式)]
```

## ファイル

| ファイル | 役割 |
|---|---|
| `api/api.go` | `api` パッケージ。`Server`(道筋・`GET /v1/models`・エラーの形)と、使う生成の interface `Generator`(本物は `*engine.Engine`) |
| `api/types.go` | やり取りする JSON の形と、リクエストを書式の形(`format.Prompt`)にする `toPrompt` |
| `api/tools.go` | ツール呼び出しの変換(`tools` → `tool.Def`、履歴の `tool_calls` → 書式の呼び出し、モデルの呼び出し → `tool_calls`) |
| `api/chat.go` | `POST /v1/chat/completions`。順番待ち・モデルの読み直し・トークン数の確かめ・生成(まとめて / ストリーミング)・学習用のログ。`POST /apply-template`・`POST /aibow/count-tokens` も |
| `api/models.go` | モデルの一覧と読み込み(ステップ8)。使う係の interface `Models`(本物は `_cmd/aibow-server` の `modelManager`)・`GET /models`・`POST /models/load`・`POST /aibow/gpu-layers` |
| `api/places.go` | モデルの置き場所(`GET /aibow/places`・`POST /aibow/places/add`・`POST /aibow/places/remove`)と、表示だけのサーバーの設定(`GET /aibow/settings`。`Settings`)。8-4 |
| `api/seat.go` | 生成の順番待ちの席(`Seat`)。ステップ9で `Server` の外に出し、ブラウザの会話(`ui`)も同じ席を通す(下の「画面用の口」) |
| `api/api_test.go`・`api/aibow_test.go` | 偽物の生成とモデルの係でのテスト(モデル不要) |
| `api/client/` | aibow-server につなぐクライアント(`client` パッケージ。ステップ8)。下の「クライアント(`api/client`)」 |
| `ui/web.go`・`ui/serverhost.go` | 画面用の口(`/aibow/ui/` の下)と、aibow-server の中でブラウザの会話のモデルを扱う係(`ServerHost`)。ステップ9。下の「画面用の口」 |
| `_cmd/aibow-server/` | exe(ステップ8で `aibow-api` から名前を変えた。0015)。`config.go` は設定(強さの順は 引数 > 設定ファイル `server.json` > 環境変数 > 既定)、`models.go` はモデルの係(GPU の層数と画面で足した置き場所を `server-models.json` に覚える)、`main.go` はエンジンとモデルを用意して `api` を `127.0.0.1` で待ち受けるだけ、`browser.go` はブラウザの会話(`server.db` を開き、`ui` の会話の仕組みと画面用の口を作る。ステップ9)。`log.go` はログの出口(`aibow-server_<日付>.log`)、`version` / `appversion.go` はアプリと同じバージョン(`_cmd/aibow/version` の写し) |

依存の向きは `_cmd/aibow-server` → `api` → `engine` → `format` → `tool`。`api` はルートの `aibow`(会話の仕組み)を import しない。
会話の保存・compact・エージェントループ・ツールの実行はクライアントの仕事なので、API の側には持たない。
ブラウザの会話(ステップ9)は別の道で、`_cmd/aibow-server` → `ui` → `aibow`(会話の仕組み)→ `engine`。`api` とは席(`api.Seat`)だけを分け合う。

## 1回のリクエストの流れ(`api/chat.go`)

1. JSON を読み、`toPrompt` で `format.Prompt` にする。読めない発言は 400
   - `content` は文字列か、部品の配列(`[{"type": "text", "text": "…"}]`。Cline はこちら)。文字以外の部品(画像など)は 400
   - `system` と `developer`(OpenAI の新しい名前)は先頭にだけ置ける。先頭に続けて複数あれば `\n\n` でつなぐ。途中にあれば 400(Qwen3.5 の公式テンプレートが途中の system をエラーにするため)
   - `assistant` の `reasoning_content` は思考(`Thinking`)に戻す。プロンプトに入れるかは書式が決める(ツールのループの途中の返事だけ)
   - `assistant` の `tool_calls` は書式の呼び出しに、`tool` の役の発言はツールの結果に戻す(下の「ツール呼び出し」)。`tools` は `tool.Def` にしてモデルに知らせる
2. **順番を待つ**。作業領域が1つなので生成は1つずつ。席が1つのチャンネル(`Server.turn`)で待たせ、断らない。待っている間につながりが切れたら列から外れる
3. リクエストの `model` が読み込み中のモデルと違えば**読み直す**(ステップ8。下の「aibow 専用の口と欄」)。順番待ちの席の中で行うので、生成と取り合わない
4. 書式を当てたトークン数を数え(`CountTokens`)、作業領域に入らなければ **400・`code: context_length_exceeded`**(OpenAI と同じ形)。compact はしない(履歴を持つのはクライアント)。
   読み直すとモデル(語彙と書式)が変わって数も変わるので、順番待ちと読み直しの後に数える(0012 では順番を待つ前に数えていた)。返事の分の空きまでは求めない(足りなくなったら止めて `finish_reason: length`)
5. 生成する。リクエストの context を `Engine.Generate` に渡すので、クライアントがつながりを切ると次のトークンで止まる(`終了理由: cancel`)
6. モデルが書いた呼び出しを `tool_calls` にする(`api/tools.go`)
7. ログ(Info)に1行:まとめて / ストリーミング(aibow のクライアントなら「・aibow」)・モデル・発言の数・ツールの数・順番待ちの秒数・プロンプトのトークン数・**KV キャッシュの再利用**・生成のトークン数と速さ・終了の理由・返した呼び出しの数

## 返すもの

- 答えは `content`、思考は **`reasoning_content`**(llama.cpp の server と同じ名前。OpenAI の標準には思考の欄が無い)
- 思考のオン・オフはリクエストの **`chat_template_kwargs.enable_thinking`**(llama.cpp の server・vLLM と同じ)。書かなければ思考あり(アプリの既定と同じ)
- `finish_reason`:ツール呼び出しを返すときは `tool_calls`。それ以外は、エンジンの `eos`(発言の終わり)・`stop`(書式が決めた位置で止めた)は `stop`、`length`(返事の上限)・`context`(作業領域がいっぱい)は `length`
- `usage`:`prompt_tokens`・`completion_tokens`(思考 + 答え)・`total_tokens` と、`prompt_tokens_details.cached_tokens`(KV キャッシュを使い回せた数。OpenAI の「キャッシュされた入力」と同じ欄)
- `model` は、リクエストの値が使えるモデル(ID・`qwen3.5:4b` のような一覧の名前・GGUF のパス)なら、それを読み込んでから答える(ステップ8。0012 では見なかった)。
  知らない名前なら読み込み中のモデルで答え、Info のログを出す(Cline などがおかしな名前を送ってきても動くように)。返事の `model` は読み込み中のモデルの ID(`ollama:qwen3.5:4b` など)。
  `/v1/models` は使えるモデルを全部返す
- `timings`:生成の時間とトークン数(llama.cpp の server と同じ欄。下の「aibow 専用の口と欄」)。小さいので、いつも返す
- サンプリング:`temperature`・`max_tokens`(`max_completion_tokens` があればそちら)・`presence_penalty`。書かれていなければアプリの既定(0.7・`AIBOW_MAX_TOKENS`・`AIBOW_PRESENCE_PENALTY`)。
  「書かれていない」と「0」を見分けるため、JSON の数の欄はポインタで読む(`temperature: 0` は greedy)
- エラーは OpenAI と同じ `{"error": {"message", "type", "code"}}`。知らないパスは 404、メソッドが違えば 405(Go の `ServeMux` が返す)

## ツール呼び出し(`api/tools.go`)

API では `tools` を JSON Schema で渡し、`tool_calls` が JSON で返るように見えるが、実際はサーバーがツールの説明をプロンプトに書き(書式の仕事。[tools.md](tools.md))、
モデルが書いた `<tool_call>` を読み取って JSON にしているだけ。ここはその JSON と書式の形の間をつなぐ。

```
行き: tools              → tool.Def(parameters は書いた順のまま)
      履歴の tool_calls   → format.ToolCall(arguments の値 → tool.Arg の形。tool.ArgsFromJSON)
      tool の役の発言    → format.Message{Role: "tool"}(書式が <tool_response> で包む)
帰り: format.ToolCall    → tool_calls(tool.Arg の形 → JSON の値。tools の JSON Schema の型を見る)。finish_reason は tool_calls
```

- **引数の型**:aibow の中の引数の値は、書式によらない形(文字列の引数は中身、数などの引数は JSON の文字。[tools.md](tools.md) の「引数の値の持ち方」)で持つ。
  arguments の JSON とは、tools の JSON Schema の型を見て行き来するだけ(Qwen3.5 が整数の引数に書いた `5` は JSON の `5`、配列は `["x", "y"]`)。
  モデルが型に合わない文字(` 5`・`yes`)を書いた値は JSON の文字列で持っているので、そのまま文字列で返る。戻ってきたときも同じ形に戻り、キーの順番も保つ。**往復で1文字も変わらないので、ツールのループで KV キャッシュを前回の生成の分まで使い回せる**
- 定義に合わない呼び出し(知らない名前・引数の間違い・型の合わない値)も、そのまま返す。クライアントのツールの検証でエラーになり、ツールの結果としてモデルに返るので、モデルが直せる。理由は Warn のログ
- 書式が崩れて読み取れなかった呼び出しは、モデルが書いたままを答えの後ろに足して返す(Warn のログ)。aibow のクライアントには答えに足さず、`aibow.broken_tool_calls` で分けて返す(下)
- 書式は呼び出しと結果を順番で対応させる。`tool_call_id` はツールの結果の名前を引くのにだけ使う(Gemma 4 は結果に名前を書く。直前の assistant の `tool_calls` の `id` から引き、引けなければ tool の発言の `name`、それも無ければ書式が順番で組にする。0026 の決定事項 5)。
  aibow のクライアントは、崩れた呼び出し(`id` が無い)の結果も名前が分かるよう、tool の発言に `name` を付けて送る。`tool_choice` は `"none"` だけを見る(ほかは強制できないので `"auto"` と同じ)
- ストリーミングでは、生成が終わってから呼び出し1つにつき1つの塊で、`index` 付きで引数まで全部を送る(書式は呼び出しの部分を生成の後で読み取るため)

## ストリーミング(Server-Sent Events)

`stream: true` のときは、つながりを開いたまま `data: <JSON>` と空行の塊を何回も書く。塊ごとに `http.ResponseController` で Flush する(しないと net/http が溜めてまとめて送る)。

```
data: {"object":"chat.completion.chunk", …, "choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}
data: { … "delta":{"reasoning_content":"考え"} …}       ← 思考
data: { … "delta":{"content":"富士"} …}                 ← 答え(トークンの切れ目で少しずつ)
data: { … "delta":{"tool_calls":[{"index":0,"id":"call_…","type":"function","function":{…}}]} …}  ← 呼び出し(あれば。1つずつ)
data: { … "delta":{},"finish_reason":"stop"}],"timings":{…}}  ← 終了の理由(timings と、aibow のクライアントなら aibow 欄も)
data: { … "choices":[],"usage":{…}}                     ← stream_options.include_usage のときだけ
data: [DONE]
```

ステータスは最初に 200 で送ってしまうので、生成の途中のエラーは `data: {"error": …}` の塊で送る(OpenAI も同じ)。

## aibow 専用の口と欄(ステップ8。0015)

画面のアプリ(aibow のクライアント)が使っているのに、OpenAI の標準に無いもの(トークン数・モデルに渡した文字列・生成の時間・崩れたツール呼び出し・モデルの切り替え)を、
**標準の形を壊さずに**足した。Cline などから見た `/v1/models`・`/v1/chat/completions` の形は変わらない。
意味が同じものは **llama.cpp の server の名前にそろえ**、無いものだけ `/aibow/` と `aibow` 欄に置いた(2026-10-21、ユーザーと決めた)。

### 口(`/v1/` の外)

| 口 | 本文 → 返事 | 順番待ち | 元にしたもの |
|---|---|---|---|
| `GET /models` | → `{"object":"list","data":[{"id","name","source","size_gb","origin","place","arch","status":{"value":"loaded"\|"loading"\|"unloaded"},"n_gpu_layers"}],"current":<読み込み中のモデルの情報 か null>,"loading":<入れ替えている先のモデルの ID。入れ替え中だけ>,"n_ctx","gpu"}` | 待たない | llama.cpp の router の `GET /models`(`status.value`) |
| `POST /models/load` | `{"model"}` → `{"success":true,"model":<読み込んだモデルの情報>}` | 待つ | llama.cpp の router の `POST /models/load` |
| `POST /models/unload` | `{"model"}` → `{"success":true,"model":null}`(VRAM を空ける。8-4) | 待つ | llama.cpp の router の `POST /models/unload` |
| `POST /apply-template` | チャットと同じ本文 → `{"prompt"}`(書式を当てた文字列) | 待たない | llama.cpp の `POST /apply-template` |
| `POST /aibow/count-tokens` | チャットと同じ本文 → `{"n_tokens"}`(書式を当てた文字列のトークン数) | 待たない | aibow だけ(llama.cpp の `/tokenize` は文字列を受け取るので、書式を当てる分まで1回で済むよう別にした) |
| `POST /aibow/gpu-layers` | `{"model","n_gpu_layers"}` → `{"success":true,"model":<読み直したモデルの情報 か null>}` | 待つ | aibow だけ(llama.cpp は起動のときの preset ファイルで決める) |
| `GET /aibow/places` | → `{"places":[{"path","origin","kind":"dir"\|"file","models","missing"}]}` | 待たない | aibow だけ(8-4) |
| `POST /aibow/places/add` | `{"path"}` → 足したあとの `{"places"}`。足せなければ 400 と理由 | 待たない | aibow だけ(8-4) |
| `POST /aibow/places/remove` | `{"path"}` → 外したあとの `{"places"}`。足した置き場所でなければ 400 | 待たない | aibow だけ(8-4) |
| `GET /aibow/settings` | → `{"port","n_ctx","model","lib","models_dir","ollama_dir","config_file","config_found","state_file"}`(表示だけ) | 待たない | aibow だけ(8-4) |

- 入れ替え(`POST /models/load`・会話のモデルでの読み直し・GPU の層数を変えた読み直し)の途中は、前のモデルを解放してから読み込むので `current` が `null` になる。降ろした(何も無い)のと見分けられるよう、入れ替えている先を `loading` で返す(`engine.Loading`。2026-10-22。9-3 で、入れ替えの途中に一覧を取った画面のアプリのモデルが空になったため)
- `n_ctx` はサーバーの作業領域の大きさ。**起動のときに決めた大きさで固定**で、クライアントからは変えられない(共有するサーバーで1人の都合で作り直すと、ほかの人の KV キャッシュが消えるため。0015 の 3)
- `/aibow/gpu-layers` の `model` を書かなければ読み込み中のモデル。読み込み中のモデルなら読み直し、読み直せたら覚える。読み込んでいないモデルは覚えるだけ(次に読み込むときから効く)。
  覚える場所はサーバーの PC の `server-models.json`(GPU の層数は「その PC の人が決めること」なので。アプリの DB の `model_gpu_layers` とは別)
- 読み込み・読み直しは順番待ちの席の中で行う(生成と取り合わない)。数えるのと書式を当てるのは、生成中でもできる(`engine` の `vocabMu`)
- `origin` はモデルがどこから来たか(`models`(models フォルダ)・`ollama`・`added`(画面で足した置き場所。`place` にその置き場所))。同じファイルが2か所から見つかったら、先のほうだけを出す
- `/models/unload` は、`model` が空か読み込み中のモデルなら降ろす。読み込んでいない別のモデルなら何もしない(aibow は1つしか読み込まないため)。
  降ろしたあとのチャットは、`model` のモデルを読み込んでから生成する(`model` を書いていなければ「モデルが読み込まれていません」)
- 置き場所(8-4)は、フォルダか GGUF のファイル1つ。パスは**サーバーの PC のもの**で、足すときにサーバーが「あるか・フォルダか GGUF(先頭が `GGUF`)か・まだ入っていないか」を確かめる(0015 の 25・31)。
  足した置き場所は `server-models.json` の `places` に覚え、手で書く `server.json` は書き換えない(0015 の 22)。models フォルダと Ollama の保存先は外せない(表示だけ)。
  足した置き場所が見つからなくなったら一覧から外し、見つからなくなったときに1回だけ Warn のログを出す。覚えたものからは消さない(つなぎ直せば戻る。0015 の 28)。
  置き場所を足す・外すのは作業領域を使わないので、順番を待たない
- `/aibow/count-tokens`・`/apply-template` がモデルの状態で失敗したときは、`error.code` に `model_not_loaded`(降ろした・まだ読み込んでいない)か `unsupported_format`(書式に未対応の種類のモデル)を入れる。
  `api/client` はこれを `engine.ErrNoModel`・`format.ErrUnsupported` に戻すので、画面のアプリは直接でも HTTP 越しでも同じに「メーターを出さない」にできる(会話を開くのは止めない)
- 画面のアプリ(`api/client` を使う側)は、`/models/unload`・`/aibow/gpu-layers`・`/aibow/places/*` を使わない。`/aibow/settings` も作業領域しか画面に渡さない(2026-10-22、ユーザーと決めた。0015 の 21)。
  これらはサーバーの PC の事情を変える口で、サーバーの PC の人が curl などで呼ぶもの。誰が呼んでよいか(管理者だけにする)はステップ10で決める
- `/aibow/settings` は表示だけのサーバーの設定(作業領域・ポート・llama.cpp の場所・最初のモデル・設定ファイルの場所)。変えるのは `server.json` と起動し直し(0015 の 21)

### `/v1/chat/completions` に足した欄

```
頼み   "aibow": {"return_prompt": true}                        ← 書けば aibow のクライアント。return_prompt でモデルに渡した文字列も返す
返事   "timings": {"cache_n","prompt_n","prompt_ms","prompt_per_second","predicted_n","predicted_ms","predicted_per_second"}  ← いつも
       "aibow": {"prompt","stop_reason","broken_tool_calls":[{"index","name","raw","error"}]}  ← aibow のクライアントだけ
履歴   assistant の "aibow": {"broken_tool_calls":[…]}         ← 返ってきたものをそのまま送り返す
```

- `timings` は llama.cpp と同じ意味にした:`prompt_n` は今回読み込んだ(KV キャッシュを使い回せなかった)数、`cache_n` は使い回せた数。プロンプトのトークン数は足したもの
- `stop_reason` はエンジンの細かい終了の理由(`eos`・`stop`・`length`・`context`・`cancel`)。`finish_reason` では `length` と `context` を見分けられないため
- `prompt` は長いので頼んだときだけ。ストリーミングなら生成の前に、`choices` が空で `aibow.prompt` だけの塊を1つ送る
- `broken_tool_calls` の `index` は、読み取れた呼び出し(`tool_calls`)と合わせた全体の中の順番。aibow のクライアントは自分のループで「エラーとしてモデルに返して直させる」ので、答えに足さずに分けて返す。
  履歴で送り返すと、`index` の位置に混ぜ戻し、書式はモデルが書いたまま(`raw`)をプロンプトに入れる
- aibow のクライアントには、引数の値を**往復で1文字も変わらない形でだけ** JSON の型の値にする(Cline などには、前後の空白を除いたり `True` を `true` に直したりしても型の値にする)。
  aibow のクライアントは返ってきた値を書式に戻すので、1文字でも変わると KV キャッシュを使い回せなくなるため(0015 の 8)

## クライアント(`api/client`)

画面のアプリが aibow-server に生成を頼むための、HTTP 版の `Generator`(ステップ8の 8-1)。`mcp/client` と並べる形で `api/` の下に置いた。

```
画面のアプリ [ 会話の仕組み → client.Client ] ──HTTP──→ aibow-server [ api.Server → engine ]
```

- `*engine.Engine` と同じメソッド(`Generate`・`CountTokens`・`EnsureContextSize`・`Info`)と、会話のモデルを読み込む `LoadModel` を持つ。
  会話の仕組みの `Generator`・`ModelLoader` をそのまま満たす(ルートの `model_test.go` で型を確かめている)ので、**会話の仕組みのコードは変えずに**相手がネットワークの先に変わる
- ルートの `aibow` は import しない(`api` と同じ)。JSON の形はクライアントの側で自分で持つ(サーバーの型は外に見せていない)

| メソッド | 口 | 中身 |
|---|---|---|
| `Generate` | `POST /v1/chat/completions` | いつもストリーミング・`"aibow": {"return_prompt": true}`。届いた塊を `onThinking` / `onContent` / `onPrompt` に渡し、最後の塊の `timings` と `aibow` から `GenerateStats` と崩れた呼び出しを戻す |
| `CountTokens` | `POST /aibow/count-tokens` | 生成と同じ形の本文で数える(形が違うとトークン数がずれるため) |
| `EnsureContextSize` | (`GET /models` の `n_ctx`) | 作業領域は作り直さず、入るかを確かめるだけ。入らなければ `ContextError`(会話の上限を下げるか、aibow-server の `-ctx` を上げる) |
| `Info` | なし | 最後に知った読み込み中のモデル。呼ばれる回数が多いので聞きに行かず、`LoadModel`・`Models`・生成の返事のモデルが変わったときに取り直す。サーバーが入れ替えの途中なら `null`(会話の仕組みが送信の前に `LoadModel` を呼び、サーバーの席で入れ替えが終わるのを待つ) |
| `Shown` | なし | 画面に見せる読み込み中のモデル(`Models` の `Current` もこれ)。入れ替えの途中(`loading` あり)は、前に知ったモデルのまま。画面のモデルが空になって送信が止められないように、`Info` と分けた(2026-10-22) |
| `LoadModel` | `POST /models/load` | 会話のモデルを読み込ませ、これからの生成でその `model` を頼む |
| `Models`・`SetGPULayers` | `GET /models`・`POST /aibow/gpu-layers` | 画面のモデルの一覧と GPU の層数(8-2 でつなぐ) |

- **会話 → JSON → 会話 で1文字も変えない**:引数の値は `tool.Arg` の形から、送るツールの定義の型を見て JSON にする(`tool.ArgsJSON`。サーバーも同じ型で `tool.ArgsFromJSON` で戻すので、数や `True`・前後の空白も変わらない)。
  0026 より前は値をいつも JSON の文字列で送っていた。前の版のサーバー・クライアントと組み合わせても、書式を当てた文字列は同じになる。
  崩れた呼び出しは `aibow.broken_tool_calls` で順番つきで送り、サーバーがモデルの書いたまま(`raw`)を書式に戻す。
  `client_test.go` で、本物の `api.Server` を `httptest` で立て(生成だけ偽物)、サーバーが受け取った会話と、書式を当てた文字列が直接のときと同じになることを確かめている
- `LoadModel` を呼ばずに生成するとき(会話のモデルが、最後に知った読み込み中のモデルと同じ)は、そのモデルを `model` で頼む。空で頼むとサーバーが今のモデルで答えるので、そのあとでほかの人が入れ替えていると、違うモデルで返事ができてしまうため
- 停止ボタン(ctx を止める)はつながりを切る。サーバーはそれを見て生成を止める。クライアントは届いた分を `StopReason: cancel` で返す(エラーにしない。engine と同じ)
- つながらないとき(起動していない・止めた)は `ErrUnreachable`(つなぎ先と「aibow-server を起動してください」を添える)。サーバーのエラーは `APIError`(`code` で見分けられる)

## 画面用の口(ブラウザの会話。ステップ9。0016)

aibow-server は、ブラウザの画面のために会話の仕組みと保存も持つ(OpenAI 互換の口はステートレスのまま)。

```
ブラウザ ──HTTP──→ /aibow/ui/  → ui.Service → 会話の仕組み(ツールはここ)→ engine ┐
Cline など ──HTTP──→ /v1/ など → api.Server ──────────────────────────→ engine ┴─ 順番待ちの席は1つ(api.Seat)
                                  会話の保存: server.db(server.json の隣。画面のアプリの aibow.db とは別)
```

デスクトップのアプリで Wails が仲立ちしていること(メソッドの呼び出し・Go からのイベント・停止)を、HTTP に自分で書いたもの。形を Wails にそろえた。

| | デスクトップ(Wails) | 画面用の口 |
|---|---|---|
| 呼び出し | `ChatService.Send(id, text)` | `POST /aibow/ui/call/Send`、本文は引数の JSON の配列 `["<id>", "<text>"]`。返事は戻り値の JSON(無ければ `null`) |
| エラー | Promise が reject | Go の `error` は 500、引数が読めない・数が違うは 400、ブラウザで呼べないメソッドは 404。形は `{"error": {"message", "type"}}`(OpenAI 互換の口と同じ) |
| Go からの知らせ | Wails のイベント `chat`・`models` | `GET /aibow/ui/events`(SSE)。`event: chat` / `data: ChatEvent の JSON`。最初に `: connected`、何も無いときは 15 秒ごとに `: ping` |
| 停止 | Promise の `cancel()` → ctx が止まる | fetch を止める → つながりが切れて、リクエストの ctx が止まる |

- 呼び出しは `reflect` で `ui.Service` のメソッドを名前から探す(Wails のバインディングも、実行のときは同じことをしている)。最初の引数が `context.Context` なら、リクエストの ctx を渡す
- **誰の呼び出しか**は、aibow-server が `ui.Handler` に渡す係(`ui.Who`。`browser.go` の `who`)で決め、メソッドに渡す ctx に入れる(`aibow.WithUser`。0017 の 12)。
  会話・プロファイル・表示の設定は、その人のものだけが見える([conversations.md](conversations.md) の「持ち主」)。決められなければ 401(SSE も同じ)。
  ログインが要る形(既定。0020)ならログインした人(下の「ログイン」)、要らない形(`login: false`)ならいつもログインなしの形の利用者(`aibow.LocalUser`。名前 `local`)
- 呼べるのは、ブラウザで使うメソッドだけ(`ui/web.go` の `browserMethods`)。ツールの扱いの既定を変えるもの(`SetToolPolicy`・`SetGroupPolicy`・`ToolPrefs`・`LoadToolList` など。
  既定は管理者が決める。0024 の決定事項 3)と、GPU の層数・降ろす・置き場所(サーバーの PC の事情)は口に出さない(0016 の 6)
- 送信(`Send`・`Regenerate`・`Resend`・`Compact`)は生成が終わるまで返らない長い呼び出し。経過は SSE の `chat` で届き、終わりは `done` で判定する(戻り値とは別の道で届くので。Wails と同じ)。
  書き込みの時間の上限は付けていない
- SSE の `chat`(生成の途中経過)は、その会話の持ち主がつないでいる画面にだけ送る(同じ人の別のタブ・別の PC には送る。0017 の 7)。`models` は全員に送る。
  送り切れずに 1024 件溜まったら、そのつながりを切る(黙って捨てると、答えの途中が抜けたまま気づけないため)
- **席**:送信の全体(会話のモデルの読み直し・要約・返事)の間、OpenAI 互換の口と同じ席を持つ。生成1回ごとに取り直すと、その間に Cline などがモデルを入れ替えてしまうため。
  ふさがっていれば `notice`(「ほかの生成が終わるのを待っています…」)を送ってから待つ。入力欄の下でモデルを選んだとき(`SwitchModel`)も席を取ってから読み込む
- **ツールと確認の往復**(0024 の 12-1a):ブラウザの会話のツールはサーバーの PC で動く。今は組み込みの時刻・計算だけ(会話に保存した作業フォルダは使わない。`Conversations.IgnoreWorkDir`)。
  書き換えるツールなど、実行の前に確認するときは、確認のカードが SSE の `chat`(`type: confirm`)で届き、答えは `POST /aibow/ui/call/ConfirmTool` で返す。会話の仕組みは答えが来るまでループを止めて待つ(Wails の中で済んでいたことを、知らせと呼び出しに分けただけ)
  - 待っている間に送信の呼び出しが切れたら(タブを閉じた・読み直した)、停止と同じ(「停止されたため実行していません」)
  - 知らせ(SSE)だけが切れてつなぎ直したら、画面は `PendingConfirm` で待っている確認を取り直し、カードを出し直す(切れている間のイベントは届かないため)
  - 待っている間も席は持ったまま。ただし 5 分答えがなければ送信を止めて席を返す(`ui.confirmLimit`。ほかの人と Cline をずっと待たせないため)
  - ほかの人の会話の確認は、見えも答えられもしない。利用者は会話の設定で「確認しない」を足せない(足せるのは確認のカードの「この会話では今後聞かない」だけ)
  - ツールの実行のログには、利用者の名前と会話が付く(`ツール calculate(...)(bob の会話 …)`)
  - MCP のサーバー(`server.json` の `mcpServers`)と管理者の既定(`server-tools.json`)は [mcp.md](mcp.md) の「aibow-server の MCP のサーバー」。
    既定を見る `ToolPrefs`・`ToolDefaults`・`BuiltinTools` は誰でも呼べ(既定のファイルの場所は管理者にだけ返す)、変える `SetToolPolicy`・`SetGroupPolicy`・`LoadToolList` は管理者だけ(ほかの人は 403)
  - 資料のフォルダ(0024 の 12-2):`Folders` は `server.json` の `folders` の名前だけを返す。会話の詳細(`Conversation`)の `folder` はその会話で使えるフォルダの名前。
    パスはどの返事にも入れない。資料のフォルダのある会話では、コマンドのツール(`run_command`)をモデルにも画面にも出さない(`ui.noCommand`・`forBrowser`)
- **作業領域**:起動のときに決めた大きさで固定。会話の上限 + 返事の上限が入るかを確かめるだけで、作り直さない(`ui.ServerHost`。aibow-server につないだ画面のアプリと同じ `ContextError`)
- **見せるもの**は、aibow-server につないだ画面のアプリと同じ:モデルの一覧(名前・大きさ・読み込み中か)・作業領域の大きさ・つなぎ先(aibow-server のアドレス)。Cline などのためにモデルを入れ替えている途中は、前のモデルを見せ続ける(`ServerHost`。api/client の `Shown` と同じ)。出どころ・置き場所・層の数・サーバーの PC のパスは見せない
- 実際に動かして確かめたこと(2026-10-22。qwen3.5:4b・9b):Cline の形のリクエストを 9B で送っている間にブラウザから送ると、「待っています」が届き、
  Cline の返事のあとで会話のモデル(4B)を読み直して答えた。aibow-server を起動し直しても会話が残っていた

## KV キャッシュの再利用(実際に動かして分かったこと。2026-10-19)

API はステートレスで、クライアントは毎回履歴の全部を送ってくる。前回の KV キャッシュ(前回のプロンプト + 生成したトークン)を使い回せるかが速さを決めるが、
**Qwen3.5 では、新しい user の発言を足したリクエストの再利用は 0 になる**(aibow の画面の会話と同じ性質。[tuning.md](tuning.md) の「次の質問では再利用が 0 になる」)。

- 公式テンプレートは、最後の user より前の返事から思考の部分(`<think>…</think>`)を外す。前回の生成は思考の部分のあとに書かれているので、プロンプトがその手前で変わる
- Qwen3.5 は一部の層(線形アテンション)を途中まで巻き戻せないので、共通の先頭(システムプロンプトなど)があっても全部を読み直す
- **同じリクエストを2回送っても 0**:再利用は「最後の1トークンは読ませ直す」ので、前回のプロンプトの最後の1トークンと生成した分を捨てる必要があり、途中から巻き戻すことになる
- 使い回せるのは、前回の生成の続きに足すだけのとき(ツールの結果を返して続きを書かせる。最後の user より後の返事は思考も残るので、前回の生成と一致する)。
  6-2 で確かめた:天気のツールを1回呼んで結果を足したリクエストで、430 トークン中 382(= 前回のプロンプト 309 + 生成 73)を使い回した(ストリーミングも 457 中 409 = 309 + 100)
- 4B・RTX 4060 で、プロンプトの読み込みは 75 トークンで 0.3 秒程度、生成は約 65〜70 tok/s。Cline のように長いシステムプロンプトを毎回読み直すときの待ち時間は、6-3 で測る

## ログイン(ブラウザの画面。ステップ10の 10-1。0017)

ブラウザの画面にログインを要るようにする(既定。`-login=false`・`server.json` の `"login": false`・`AIBOW_SERVER_LOGIN=0` でログインなし。0020)。仕組みは `auth` パッケージ。
ログインできる管理者がまだいなければ、`GET /aibow/auth/me` が `"setup": true` を返し、ログインの画面が管理者の作り方を出す(起動のときにも注意を出す)。

```
ブラウザ ─ POST /aibow/auth/login {"name","password"} ─→ auth(パスワードを確かめる)→ セッションを作る → Set-Cookie: aibow_session=<合言葉>
ブラウザ ─ POST /aibow/ui/call/…(cookie 付き)─→ auth.Middleware(合言葉 → 利用者を ctx に)→ who(ui.Who)→ aibow.WithUser → ui.Service
```

| 口 | 中身 |
|---|---|
| `GET /aibow/auth/me` | `{"login": ログインが要るか, "name": ログインしている人}`。画面は最初にこれを聞き、要るのにしていなければログインの画面を出す |
| `POST /aibow/auth/login` | 本文 `{"name", "password"}`(JSON だけ)。合っていれば cookie を渡して `{"login": true, "name"}`、違えば 401「名前かパスワードが違います」 |
| `POST /aibow/auth/logout` | セッションを消して cookie を消す |
| `POST /aibow/auth/link/check` | 本文 `{"token"}`。パスワードを決めるリンクの持ち主 `{"name"}`(0022)。使えない・期限切れは 400 |
| `POST /aibow/auth/link` | 本文 `{"token", "password"}`。リンクでパスワードを決め、cookie を渡して `{"login": true, "name"}`(そのままログイン)。ほかのセッションとアプリ用のトークンは消える |
| `POST /aibow/auth/password` | 本文 `{"current", "password"}`(ログインしている本人)。パスワードを変え、新しい cookie を渡す。今のパスワードの間違いはログインと一緒に数える |

- **認証と認可**:`auth` は「この人は誰か」(認証)だけを決める。「何をしてよいか」(認可)は、今は「自分のものだけ」で、保存先が持ち主で分ける([conversations.md](conversations.md) の「持ち主」)。管理者などの認可はステップ11
- **パスワード**は保存しない。塩を付けて PBKDF2(HMAC-SHA256)を 60 万回重ねた値を `users.password` に保存する(`pbkdf2-sha256$回数$塩$値`)。
  パスワードは本人が決める(0022。12 文字以上)。`aibow-server user add|passwd` と管理者の画面は、決めるリンク(`/#reset=<合言葉>`。1 時間・一度だけ・`password_links` には SHA-256 だけ)を出す。
  合言葉は URL の `#` の後ろなので、サーバーのアクセスに乗らない(画面が読んで本文で送る)
- **セッション**:ステートレスな OpenAI 互換の口(毎回全部を送る)と違い、サーバーが「誰がログインしているか」を覚える。合言葉(32 バイトのでたらめな値)を cookie で渡し、server.db には SHA-256 だけを保存する。
  期限は最後に使ってから 30 日(1日以上たってから使うと延ばし、cookie の期限も延ばす)。server.db にあるので、起動し直しても切れない
- **cookie**:`HttpOnly`(画面のスクリプトから読めない)・`SameSite=Strict`(ほかのサイトからのリクエストには付かない)・HTTPS なら `Secure`
- ログインが要らない形では、`/aibow/auth/login` は 400 で断り、`me` は `{"login": false}`
- OpenAI 互換の口と aibow 専用の口は、下の「API キーと外で待ち受け」で守る(10-2)

## API キーと外で待ち受け(ステップ10の 10-2。0017)

```
Cline・画面のアプリ ─ Authorization: Bearer aibow_… ─→ auth.Keys.RequireKey(キー → 利用者を ctx に)→ api.Server
ほかの PC ──HTTPS(自己署名の証明書)──→ aibow-server(-host 0.0.0.0・tlsCert・tlsKey)
```

- **API キー**:利用者ごとに1本(`aibow-server user key <名前>`)。server.db には SHA-256 だけ。OpenAI の API と同じく `Authorization: Bearer <キー>` で毎回送る(サーバーは覚えない。ステートレス)。
  無い・違うは 401(`{"error": {"code": "invalid_api_key", …}}`)
- **要るとき**:`127.0.0.1` の外で待ち受けるとき、または `127.0.0.1` でもログインが要る設定のとき。OpenAI 互換の口と aibow 専用の口の全部(`_cmd/aibow-server` の `KeysRequired`)。
  ログインなし(`login: false`)の `127.0.0.1` だけなら、キーなしで通す
- **サーバーの PC か管理者だけの口**:`POST /models/unload`・`/aibow/gpu-layers`・`/aibow/places/add`・`/aibow/places/remove` は、`127.0.0.1`・`::1` からの呼び出しと、
  管理者の API キーの呼び出しだけ(ほかは 403・`code: local_only`。`api` の `pcSide`)。サーバーの PC と、ほかの人が使っているモデルに関わるため。
  管理者は 0018 の 11-0 で足した(下の「管理者」)
- **待ち受けのアドレス**:`-host`(既定 `127.0.0.1`)。ループバックの外ならログインを必ず要るようにする(認証が無いまま LAN に出さない)
- **HTTPS**:`tlsCert`・`tlsKey` を書くと `ServeTLS`(TLS 1.2 以上)。証明書なしで外に出すときは、警告して HTTP で動かす。ログインの cookie には `Secure` が付く
- **自己署名の証明書**(`aibow-server cert`):誰も保証していない証明書なので、つなぐ側が「この証明書を信頼する」と決める。
  アプリは `serverCert`(`api/client` の `TrustCert`。OS の認証局に足すだけなので、ほかの証明書のサーバーにはつながらない)、curl は `--cacert`、Node.js は `NODE_EXTRA_CA_CERTS`、ブラウザは警告の画面で先へ進む

## 管理者(ステップ11の 11-0。0018)

```
aibow-server user admin <名前>(コマンドだけ)→ server.db の users.admin(DB の版 10)
                    │
  ┌─────────────────┼──────────────────────────────────────────────┐
  OpenAI 互換の口・aibow 専用の口   画面用の口(/aibow/ui/)              管理者の口(/aibow/admin/)
  API キーの持ち主(auth.IsAdmin)  ui.Caller{Admin, Local}(who)       ブラウザでログインした管理者だけ
  → PC の事情を変える口を通す       → モデルのタブを全部見せる(pcMethods) → 利用者の管理
```

- **誰が管理者か**は `auth.User.Admin`。決めるのは `aibow-server user admin <名前>` / `user unadmin <名前>` だけ(画面からは決めない。0018 の決定事項 6)。
  一人もいなくてもよい(127.0.0.1 からはいつも PC の事情を変えられ、コマンドで戻せるため。決定事項 16)。ログインの様子(`/aibow/auth/me`・`/aibow/auth/key`)に `admin` を返す
- **認可を決める場所**:`api` は auth を import せず、aibow-server が「管理者か」を決める係(`Server.SetAdmin(auth.IsAdmin)`)を渡す。
  画面用の口は、aibow-server の `who` が呼んだ人(`ui.Caller`。ID・管理者か・サーバーの PC の上からか)を決めて ctx に入れる。
  `ui.Handler` は PC の事情を変えるメソッド(`pcMethods`:`UnloadModel`・`SetModelGPULayers`・`ModelPlaces`・`AddModelPlace`・`RemoveModelPlace`・`SetModelEnabled`)を、
  `Caller.ManagesPC()`(管理者か、サーバーの PC の上から)でなければ 403 で断り、`ServerHost` も同じことを確かめる(口の作りを間違えても漏れないように)
- **管理者の口**(`_cmd/aibow-server/admin.go`。ブラウザでログインした管理者だけ。ほかは 401 / 403):
  `GET /aibow/admin/users`(一覧。パスワードと API キーは、あるかだけ)・`POST /aibow/admin/users`(本文 `{"name"}`。返事にパスワード)・
  `POST /aibow/admin/users/{名前}/password`・`POST /aibow/admin/users/{名前}/key`(返事に新しいパスワード / キー。一度だけ)・`DELETE /aibow/admin/users/{名前}`(自分と `local` は消せない)。
  処理は user コマンドと同じ関数(`createUser`・`renewPassword`・`renewKey`・`deleteUser`)。返事は `Cache-Control: no-store`
- **モデルの ID**:GGUF は `file:<ファイル名>`(以前は `file:<パス>`。サーバーの PC のパスが `/v1/models` や共有した会話に出ないように。0018 の決定事項 15)。
  同じ名前のファイルは先の置き場所だけを出し、後ろは Warn のログ(`models.Catalog.Hidden`)。以前の形の ID は `models.IDOf` で読み替える
  (会話・プロファイル・アプリの設定は読むときだけで DB は書き換えない。`server-models.json` の層の数のキーは、読み込むときに書き換える)
- **使えるモデル**:管理者がモデルのタブで決め、`server-models.json` の `disabled` に「使えないモデル」の ID を覚える(新しく増えたモデルは使える。決定事項 17)。
  使えないモデルは `/v1/models`・`GET /models`・画面の一覧に出さず(管理者の一覧にだけ印 `disabled` 付きで出す)、`Find` は `*models.DisabledError` を返す。
  OpenAI 互換の口は知らないモデルと同じ 404 `model_not_found`(読み込み中のモデルで代わりに答えない)、aibow 専用の `/models/load` は 404 `model_disabled`、
  会話の送信は注意で断る(`ChatEvent.Caution`。勝手にほかのモデルに替えない)。使えなくしたのが読み込み中のモデルなら降ろす。
  一覧の外の GGUF をパスで読み込ませるのは起動のとき(`-model` など)だけ。つないでくる人には、パスで好きなファイルを読ませない

## 会話の共有と管理者が読む(ステップ11の 11-2。0018)

- 画面用の口に `SetConversationShare`・`SharedConversations`・`UserConversations`・`UserNames`・`ReadConversation`・`CopyShared` を足した(`browserMethods`)。
  誰が読めるかは保存先(`store`)が決め、管理者かも保存先が利用者の表で確かめる(`UserConversations` は管理者だけ)
- 保存の口(`/aibow/store/`)にも、`aibow.Store` に足した同じメソッドが出る(`store/remote` の `Client`)。画面のアプリ(serverUrl)でも、共有された会話を読む・写す・自分の会話を共有するができる
- 管理者が共有されていない会話を読むと、`ui.Service.ReadConversation` が Notice のログに残す(誰が・誰の・どの会話を)
- OpenAI 互換の口は会話を持たない(履歴はクライアントが毎回送る)ので、共有と関係しない

## 保存の口(画面のアプリの会話をサーバーに置く。ステップ10の 10-3。0017)

画面のアプリを aibow-server につなぐと、生成だけでなく会話の保存もサーバーに頼む(表の C)。ループとツールはアプリの PC で動くので、HTTP を越えるのは画面の呼び出しではなく、会話の仕組みが保存先に頼む操作(`aibow.Store` のメソッド)。

```
画面のアプリ [ 会話の仕組み・ループ・ツール ] ─┬─ 生成:api/client ──────────→ /v1/chat/completions など ─→ engine
                                              └─ 保存:store/remote.Client ─→ /aibow/store/{メソッド} ──→ server.db(ブラウザの会話と同じ)
```

- 形は画面用の口と同じ:`POST /aibow/store/{メソッド}`(`ListConversations`・`AddMessage` など `aibow.Store` と `aibow.ConversationLocks` のメソッド)。本文は引数の JSON の配列(ctx は除く)、返事は戻り値(error は除く)の JSON の配列
- エラーは `{"error": {"message", "type", "code"}}`。`code` は `not_found`・`profile_not_found`・`profile_in_use`・`busy`(ほかの画面で生成中)・`no_user`。アプリの側で同じエラーに戻すので、会話の仕組みは直接のときと同じに見分けられる
- 誰のものか:API キーが要る形ならキーの持ち主、要らない形ならログインなしの形の利用者(`local`)。アプリは利用者を送らない
- アプリが書く「アプリ全体の設定」(最後に使ったモデルなど)は、その人の設定(`app.` で始まるキー)として置く(サーバー全体の設定を、ほかの人のアプリが書き換えないように)
- 同じ server.db なので、アプリで作った会話を、同じ人がブラウザで開ける
- **生成中の印**:同じ会話にブラウザとアプリ(または2台のアプリ)から同時に送ったら、後から来たほうを `busy`(409)で断る。印は送信の間だけ付け、期限 2 分・30 秒ごとに延ばす(`lock.go`)

## そのほかの口(ステップ10の 10-4)

- `GET /aibow/auth/key`(10-4):API キーの持ち主の名前(`{"login": true, "name"}`。ブラウザの `/aibow/auth/me` と同じ形)。画面のアプリが、誰としてつないでいるかを一覧の下に出すのに使う。キーの要らない形なら `{"login": false}`
## アプリのログイン(0019)

```
画面のアプリ ─ POST /aibow/auth/token {"name","password","device"} ─→ auth(パスワードを確かめる)→ アプリ用のトークン(aibowapp_…)
画面のアプリ ─ Authorization: Bearer aibowapp_… ─→ auth.Keys.RequireKey(API キーと同じ口。トークン → 利用者を ctx に)
```

| 口 | 中身 |
|---|---|
| `POST /aibow/auth/token` | 本文 `{"name", "password", "device"}`(JSON だけ。device は PC の名前)。合っていれば `{"token", "name", "admin", "expiresAt"}`、違えば 401。間違いの回数はブラウザのログインと一緒に数える(5回で 15 分)。ログインの要らない形では 400 |
| `POST /aibow/auth/token/revoke` | `Authorization: Bearer <トークン>` のトークンを消す(アプリのログアウト)。無くても 200 |

- **トークン**:期限は作ってから 10 日で固定(ブラウザのセッションと違って延ばさない)。PC ごとに1本(利用者 + PC の名前)で、同じ PC でログインし直すと前のものは消える。server.db の `app_tokens` には SHA-256 だけ。
  パスワードを作り直すと、ブラウザのセッションと一緒に消える
- **使える口**:API キーと同じ(`KeysRequired` の口の全部と `/aibow/auth/key`)。期限が切れたら 401 で `code` が `token_expired`(アプリはもう一度ログインするよう出す)
- **証明書のフィンガープリント**:HTTPS で起動したときと `cert` のコマンドで、証明書の SHA-256 のフィンガープリント(`AB:CD:…`。`auth.Fingerprint`)を出す。画面のアプリは初めてつなぐときに同じ形で出し、人が比べて信頼する(`api/client` の `ProbeServer`・`PinCert`)

- `GET /favicon.ico` は、画面の係が黙って 404 を返す(ブラウザが自分で取りに来るので、OpenAI 互換の口に落として「キーが無い」の警告を出さないように)
