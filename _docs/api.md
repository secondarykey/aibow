# OpenAI 互換 API(aibow-api)

aibow の「頭」(llama.cpp で動かすモデルと書式)を、OpenAI 互換の HTTP の API として出す仕組みです(ステップ6。仕様は [features/0012-openai-api.md](features/0012-openai-api.md))。
使い方(起動・環境変数・curl の例)は [usage.md](usage.md) の「aibow のモデルをほかのアプリから使う(aibow-api)」にあります。

```
Cline など [ 会話の仕組み + 手 ] ⇄ HTTP(OpenAI 互換。127.0.0.1 だけ)⇄ aibow-api [ 頭(モデル + 書式)]
```

## ファイル

| ファイル | 役割 |
|---|---|
| `api/api.go` | `api` パッケージ。`Server`(道筋・`GET /v1/models`・エラーの形)と、使う生成の interface `Generator`(本物は `*engine.Engine`) |
| `api/types.go` | やり取りする JSON の形と、リクエストを書式の形(`format.Prompt`)にする `toPrompt` |
| `api/tools.go` | ツール呼び出しの変換(`tools` → `tool.Def`、履歴の `tool_calls` → 書式の呼び出し、モデルの呼び出し → `tool_calls`) |
| `api/chat.go` | `POST /v1/chat/completions`。トークン数の確かめ・順番待ち・生成(まとめて / ストリーミング)・学習用のログ |
| `api/api_test.go` | 偽物の生成でのテスト(モデル不要) |
| `_cmd/aibow-api/` | exe。`main.go` は引数と環境変数を読み、エンジンとモデルを用意して `api` を `127.0.0.1` で待ち受けるだけ。`log.go` はログの出口(`aibow-api_<日付>.log`)、`version` / `appversion.go` はアプリと同じバージョン(`_cmd/aibow/version` の写し) |

依存の向きは `_cmd/aibow-api` → `api` → `engine` → `format` → `tool`。`api` はルートの `aibow`(会話の仕組み)を import しない。
会話の保存・compact・エージェントループ・ツールの実行はクライアントの仕事なので、API の側には持たない。

## 1回のリクエストの流れ(`api/chat.go`)

1. JSON を読み、`toPrompt` で `format.Prompt` にする。読めない発言は 400
   - `content` は文字列か、部品の配列(`[{"type": "text", "text": "…"}]`。Cline はこちら)。文字以外の部品(画像など)は 400
   - `system` と `developer`(OpenAI の新しい名前)は先頭にだけ置ける。先頭に続けて複数あれば `\n\n` でつなぐ。途中にあれば 400(Qwen3.5 の公式テンプレートが途中の system をエラーにするため)
   - `assistant` の `reasoning_content` は思考(`Thinking`)に戻す。プロンプトに入れるかは書式が決める(ツールのループの途中の返事だけ)
   - `assistant` の `tool_calls` は書式の呼び出しに、`tool` の役の発言はツールの結果に戻す(下の「ツール呼び出し」)。`tools` は `tool.Def` にしてモデルに知らせる
2. 書式を当てたトークン数を数え(`CountTokens`)、作業領域に入らなければ **400・`code: context_length_exceeded`**(OpenAI と同じ形)。compact はしない(履歴を持つのはクライアント)。
   数えるのは生成中でもできる(`engine` の `vocabMu`)ので、順番を待つ前に断れる。返事の分の空きまでは求めない(足りなくなったら止めて `finish_reason: length`)
3. **順番を待つ**。作業領域が1つなので生成は1つずつ。席が1つのチャンネル(`Server.turn`)で待たせ、断らない。待っている間につながりが切れたら列から外れる
4. 生成する。リクエストの context を `Engine.Generate` に渡すので、クライアントがつながりを切ると次のトークンで止まる(`終了理由: cancel`)
5. モデルが書いた呼び出しを `tool_calls` にする(`api/tools.go`)
6. ログ(Info)に1行:まとめて / ストリーミング・発言の数・ツールの数・順番待ちの秒数・プロンプトのトークン数・**KV キャッシュの再利用**・生成のトークン数と速さ・終了の理由・返した呼び出しの数

## 返すもの

- 答えは `content`、思考は **`reasoning_content`**(llama.cpp の server と同じ名前。OpenAI の標準には思考の欄が無い)
- 思考のオン・オフはリクエストの **`chat_template_kwargs.enable_thinking`**(llama.cpp の server・vLLM と同じ)。書かなければ思考あり(アプリの既定と同じ)
- `finish_reason`:ツール呼び出しを返すときは `tool_calls`。それ以外は、エンジンの `eos`(発言の終わり)・`stop`(書式が決めた位置で止めた)は `stop`、`length`(返事の上限)・`context`(作業領域がいっぱい)は `length`
- `usage`:`prompt_tokens`・`completion_tokens`(思考 + 答え)・`total_tokens` と、`prompt_tokens_details.cached_tokens`(KV キャッシュを使い回せた数。OpenAI の「キャッシュされた入力」と同じ欄)
- `model` はリクエストの値を見ず、読み込み中のモデルの ID(`ollama:qwen3.5:4b` など)を返す。違う名前が来たら Info のログを出す。`/v1/models` も読み込み中の1つだけ
- サンプリング:`temperature`・`max_tokens`(`max_completion_tokens` があればそちら)・`presence_penalty`。書かれていなければアプリの既定(0.7・`AIBOW_MAX_TOKENS`・`AIBOW_PRESENCE_PENALTY`)。
  「書かれていない」と「0」を見分けるため、JSON の数の欄はポインタで読む(`temperature: 0` は greedy)
- エラーは OpenAI と同じ `{"error": {"message", "type", "code"}}`。知らないパスは 404、メソッドが違えば 405(Go の `ServeMux` が返す)

## ツール呼び出し(`api/tools.go`)

API では `tools` を JSON Schema で渡し、`tool_calls` が JSON で返るように見えるが、実際はサーバーがツールの説明をプロンプトに書き(書式の仕事。[tools.md](tools.md))、
モデルが書いた `<tool_call>` を読み取って JSON にしているだけ。ここはその JSON と書式の形の間をつなぐ。

```
行き: tools              → tool.Def(parameters は書いた順のまま)
      履歴の tool_calls   → format.ToolCall(arguments の値 → 文字列)
      tool の役の発言    → format.Message{Role: "tool"}(書式が <tool_response> で包む)
帰り: format.ToolCall    → tool_calls(文字列 → JSON の値。tools の JSON Schema の型を見る)。finish_reason は tool_calls
```

- **引数の型**:Qwen3.5 の書式は値をいつも文字列で書く。返すときは、型が文字列でない引数で、モデルが書いた文字がその型の JSON として読めれば、その文字をそのまま JSON の値にする(`5`・`["x", "y"]`)。
  戻ってきたときは、文字列の値は中身に、それ以外は JSON の文字のまま書式に戻す。キーの順番も保つ。**往復で1文字も変わらないので、ツールのループで KV キャッシュを前回の生成の分まで使い回せる**
- 定義に合わない呼び出し(知らない名前・引数の間違い・型の合わない値)も、そのまま返す。クライアントのツールの検証でエラーになり、ツールの結果としてモデルに返るので、モデルが直せる。理由は Warn のログ
- 書式が崩れて読み取れなかった呼び出しは、モデルが書いたままを答えの後ろに足して返す(Warn のログ)
- `tool_call_id` は読まない(書式は呼び出しと結果の順番で対応させる)。`tool_choice` は `"none"` だけを見る(ほかは強制できないので `"auto"` と同じ)
- ストリーミングでは、生成が終わってから呼び出し1つにつき1つの塊で、`index` 付きで引数まで全部を送る(書式は呼び出しの部分を生成の後で読み取るため)

## ストリーミング(Server-Sent Events)

`stream: true` のときは、つながりを開いたまま `data: <JSON>` と空行の塊を何回も書く。塊ごとに `http.ResponseController` で Flush する(しないと net/http が溜めてまとめて送る)。

```
data: {"object":"chat.completion.chunk", …, "choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}
data: { … "delta":{"reasoning_content":"考え"} …}       ← 思考
data: { … "delta":{"content":"富士"} …}                 ← 答え(トークンの切れ目で少しずつ)
data: { … "delta":{"tool_calls":[{"index":0,"id":"call_…","type":"function","function":{…}}]} …}  ← 呼び出し(あれば。1つずつ)
data: { … "delta":{},"finish_reason":"stop"}]}          ← 終了の理由
data: { … "choices":[],"usage":{…}}                     ← stream_options.include_usage のときだけ
data: [DONE]
```

ステータスは最初に 200 で送ってしまうので、生成の途中のエラーは `data: {"error": …}` の塊で送る(OpenAI も同じ)。

## KV キャッシュの再利用(実際に動かして分かったこと。2026-10-19)

API はステートレスで、クライアントは毎回履歴の全部を送ってくる。前回の KV キャッシュ(前回のプロンプト + 生成したトークン)を使い回せるかが速さを決めるが、
**Qwen3.5 では、新しい user の発言を足したリクエストの再利用は 0 になる**(aibow の画面の会話と同じ性質。[tuning.md](tuning.md) の「次の質問では再利用が 0 になる」)。

- 公式テンプレートは、最後の user より前の返事から思考の部分(`<think>…</think>`)を外す。前回の生成は思考の部分のあとに書かれているので、プロンプトがその手前で変わる
- Qwen3.5 は一部の層(線形アテンション)を途中まで巻き戻せないので、共通の先頭(システムプロンプトなど)があっても全部を読み直す
- **同じリクエストを2回送っても 0**:再利用は「最後の1トークンは読ませ直す」ので、前回のプロンプトの最後の1トークンと生成した分を捨てる必要があり、途中から巻き戻すことになる
- 使い回せるのは、前回の生成の続きに足すだけのとき(ツールの結果を返して続きを書かせる。最後の user より後の返事は思考も残るので、前回の生成と一致する)。
  6-2 で確かめた:天気のツールを1回呼んで結果を足したリクエストで、430 トークン中 382(= 前回のプロンプト 309 + 生成 73)を使い回した(ストリーミングも 457 中 409 = 309 + 100)
- 4B・RTX 4060 で、プロンプトの読み込みは 75 トークンで 0.3 秒程度、生成は約 65〜70 tok/s。Cline のように長いシステムプロンプトを毎回読み直すときの待ち時間は、6-3 で測る
