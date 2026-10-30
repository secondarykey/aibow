# エンジン(llama.cpp の呼び出しとモデル)

LLM を動かす部分(`engine/`・`format/`・`models/` の3つのパッケージ)の説明です。
生成・モデルの読み込みと切り替え・トークン化・プロンプトの書式・思考の振り分け・モデルの一覧を触るときに読みます。

## ファイル

| ファイル | 役割 |
|---|---|
| `engine/engine.go` | `engine` パッケージ。llama.cpp の呼び出し。モデルの読み込み・入れ替え、作業領域の作り直し、トークン数を数える(`CountTokens`)、KV キャッシュ再利用、decode、サンプリングの生成ループ |
| `engine/stream.go` | UTF-8 の切れ目の処理(文字にならないバイトは U+FFFD) |
| `models/models.go` | `models` パッケージ。モデルの一覧(`models\` の *.gguf と Ollama の保存フォルダ)。ID は `file:<ファイル名>` / `ollama:<名前>`(以前の `file:<パス>` は `IDOf` で読み替える。0018) |
| `format/format.go` / `format/qwen35.go` / `format/gemma4.go` / `format/llama3.go` / `format/parts.go` | `format` パッケージ。書式(チャットテンプレート。Qwen3.5・Gemma 4・Llama 3。`parts.go` は2つ以上で使う部品)。会話 → 文字列と、生成した文字列 → 思考・答え・ツール呼び出し。説明は [tools.md](tools.md) |
| `engine/stream_test.go` / `format/qwen35_test.go` / `format/gemma4_test.go` | UTF-8 の切れ目 / 書式(公式テンプレートとの一致・思考とツール呼び出しの振り分け)のテスト。モデル不要 |
| `models/models_test.go` | Ollama の目録のテスト。モデル不要 |

## パッケージの分け方と依存の向き

会話の仕組み(ルートの `aibow`)なしで「生成だけ」を使えるよう、LLM を動かす部分を3つに分けている(0012 の 6-0)。
ステップ6の OpenAI 互換 API(`aibow-server`)は、`engine` と `format` だけを使う。

```
ルートの aibow(会話の仕組み)──→ engine ──→ format ──→ tool
                    │              └──→ models
                    └──→ format
```

- `models`:何にも依存しない。ファイルを探すだけで、llama.cpp も読まない。型は `models.Entry`(一覧の1件)・`models.Catalog`(一覧を作る)・`models.OllamaDir()`
- `format`:`tool` だけを使う。llama.cpp も会話の保存も知らない。会話の仕組みとエンジンの間で受け渡す形(`Message` / `Prompt` / `Output` / `ToolCall`)もここ。
  モデルの種類に合う書式は `format.For(arch)`、対応していないときのエラーは `format.Unsupported(arch)`
- `engine`:`format` と `models` を使う。型は `engine.Engine` / `engine.Config` / `engine.New` と、生成の設定 `GenerateOptions`・生成の記録 `GenerateStats`・モデルの情報 `ModelInfo`
- **生成の記録(`GenerateStats`)は `format.Output` に入れず、`Engine.Generate` が別に返す**(`(format.Output, engine.GenerateStats, error)`)。
  トークン数・速さ・終了の理由は書式の話ではなく、`format` が `engine` を import すると輪になるため
- `Generator`(会話の仕組みが使う生成の interface)はルートに残す。interface は使う側に置く(本物は `engine.Engine`、テストでは偽物)

## llama.cpp と yzma

- aibow のプロセスの中で llama.cpp を直接動かす(外部の LLM サーバーは使わない。トップの `AGENTS.md` を参照)
- yzma と llama.cpp はバージョンの組み合わせが決まっている。現在は yzma v1.29.1 + llama.cpp v0.6.0(ビルド b11429)。
  yzma を上げるときは、yzma の README の対応表を確認し、`lib\` の入れ直しも [setup.md](setup.md) に書く
- llama.cpp の DLL(`lib\`)とモデル(`models\`)は `_cmd/aibow` に置く(理由は [_cmd/aibow/AGENTS.md](../_cmd/aibow/AGENTS.md))

## モデルの一覧と Ollama

- Ollama が `ollama pull` で保存したファイル(`manifests\` の目録と `blobs\` の GGUF)を aibow が直接読む。Ollama のサーバーや API は使わない
- Ollama のモデルの一部は llama.cpp で読めない独自形式なので、読めない場合はエラーにして元のモデルに戻す
- Ollama の保存先は Ollama と同じ環境変数 `OLLAMA_MODELS` で変えられる

## チャットテンプレート

- ライブラリ任せにせず、`format/qwen35.go`・`format/gemma4.go` に自分で書く(書式を理解することが学習目的のため)
- 書式は `Format` の interface で、エンジンがモデルの種類(GGUF の `general.architecture`)と、要るならチャットテンプレート(`llama.ModelChatTemplate`)の印で選ぶ
  (`qwen35` → Qwen3.5、`gemma4` → Gemma 4、`llama` でテンプレートに `<|start_header_id|>` があるもの → Llama 3。`format.ForModel`。0027)。
  `llama` の種類には Llama 3 の書式でないモデル(Hermes 3 の ChatML)もあるので、種類の名前だけでは選ばない。一覧(`models`)も GGUF の `tokenizer.chat_template` を読んで同じ決まりで選ぶ(`api.FillFormat`)。会話の仕組みは書式を知らない。詳しくは [tools.md](tools.md)。
  対応していない種類のエラーは、対応している書式を一覧から出す(`format.Supported`。0026)
- 思考の ON/OFF は書式ごとに違う:Qwen3.5 はプロンプト末尾を `<think>\n` にするか `<think>\n\n</think>\n\n` にするか、Gemma 4 はシステムの発言の先頭に `<|think|>` を書くか。Llama 3 は思考しない(ON / OFF は無視。画面は「思考する」を押せなくする)
- **bos は書式が書かない**(0026 の決定事項 25)。トークン化は `addSpecial=true`(`Engine.tokenize`)なので、GGUF の `tokenizer.ggml.add_bos_token` が True なら llama.cpp が先頭に1つ足す。
  Gemma 4 は True(`<bos>`)で、テンプレートも先頭に `bos_token` を書くので、書式が文字で書くと2つになる。Qwen3.5 は False で bos のトークンも無い。
  Llama 3.2(Ollama の llama3.2:3b)は `add_bos_token` を書いていないが、llama.cpp は `<|begin_of_text|>` を1つ足す(0027 の段1で確かめた)。
  語彙だけを読んで(`ModelParams.VocabOnly`)Gemma 4 のプロンプトをトークン化し、bos が先頭の1つだけ・書式の印(`<|turn>`・`<|channel>`・`<|tool_call>`・`<|"|>` など)がどれも1トークンになることを確かめた(2026-10-30)
- 生成した特別なトークンは文字として出す(`TokenToPiece` の special=true)ので、書式の読み取りには `</think>`・`<|channel>`・`<|tool_call>` などがそのまま届く。発言の終わり(Qwen3.5 は `<|im_end|>`、Gemma 4 は `<turn|>`)は EOG のトークンなので、届いたところで生成を止める(文字にはしない)

## 生成とモデルの入れ替え

- 生成とモデルの入れ替えは同時に1つだけ(コンテキストが1つのため)。`Engine.mu` で排他している
  (会話の側でも生成中の会話を1つだけ持つ。[conversations.md](conversations.md))
- 語彙(vocab)は `Engine.vocabMu` でも守っていて、`CountTokens` は生成中でも呼べる(別の会話を開いたときのメーターのため)
- モデルの切り替えは「今のモデルを解放 → 新しいモデルを読み込む」の順(VRAM 8GB に2つは載らないため)。失敗したら元のモデルを読み直す
- GPU の層数の変更(`SetGPULayers`)も同じ仕組みで読み直す。画面のアプリはモデルごとに層の数を覚えていて、その値で読み込む(`LoadModelWithLayers`。読めなければ層の数も戻す。0014 の 4)。
  層の数を覚える場所は、モデルを動かす PC(「その PC の人が決めること」なので):アプリの中で動かすときはアプリの DB(`model_gpu_layers`)、
  aibow-server で動かすときはサーバーの PC の `server-models.json`(`POST /aibow/gpu-layers`。0015 の 9。[api.md](api.md))。
  画面では設定の「モデル」のタブで、モデルごとに変える(読み込んでいないモデルは覚えるだけ。0015 の 8-4)。aibow-server で動かすときは画面からは変えず、サーバーの PC で `POST /aibow/gpu-layers` を呼ぶ。
- モデルを降ろす(`UnloadModel`)は、モデルと作業領域を解放して VRAM を空けるだけ(0015 の 8-4)。次に生成するときは、会話の仕組みが送信の前に会話のモデルを読み込む。
  起動のときのモデルを指定していなければ、一覧の先頭から書式に対応している最初のモデルを選ぶ。種類は `models.Arch` が GGUF の先頭(メタデータの `general.architecture`)だけを読んで調べる(重みは読み込まない)。
  モデルの置き場所(`models.Catalog` の `Added`。画面で足したフォルダか GGUF のファイル)は `models/places.go`。覚える場所はアプリの `model-places.json` か aibow-server の `server-models.json`
  どのモデルを読み込むかは会話ごと(会話の仕組みが送信の前に `aibow.ModelLoader` で読み込む。[conversations.md](conversations.md))。
  作業領域の大きさの変更(`Engine.EnsureContextSize`。同じ大きさなら何もしない)はコンテキストだけを作り直し、モデルは読み直さない。
  会話ごとに大きさが違うので、会話の仕組みが送信の前に呼ぶ。画面で上限を「適用」したときも `ChatService.TryLimits` から呼び、VRAM に入るかを確かめる(0009)。
  作業領域(`n_ctx`)の決め方は [compact.md](compact.md)

## UTF-8 として正しくない文字列

- llama.cpp のトークン化に UTF-8 として正しくない文字列を渡すと、C++ の例外でプロセスごと落ちることがある
- 生成した文字列(`utf8Buffer`)とトークン化の前(`Engine.tokenize`)で U+FFFD に置き換えている

## 関連

- サンプリングの値(presence penalty など)を変えるときは [tuning.md](tuning.md) の記録を読む
- モデルの読み込みで落ちたときは [known-issues.md](known-issues.md) を見る
