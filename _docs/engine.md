# エンジン(llama.cpp の呼び出しとモデル)

LLM を動かす部分(`engine/`・`format/`・`models/` の3つのパッケージ)の説明です。
生成・モデルの読み込みと切り替え・トークン化・プロンプトの書式・思考の振り分け・モデルの一覧を触るときに読みます。

## ファイル

| ファイル | 役割 |
|---|---|
| `engine/engine.go` | `engine` パッケージ。llama.cpp の呼び出し。モデルの読み込み・入れ替え、作業領域の作り直し、トークン数を数える(`CountTokens`)、KV キャッシュ再利用、decode、サンプリングの生成ループ |
| `engine/chunks.go` | プロンプトをチャンク(文字のトークン列・画像)の列として読ませる・KV キャッシュの記録と使い回し(0030)。下の「画像を読む」 |
| `engine/vision.go` | 画像を読む部品(mmproj)の読み込みと解放、画像の付いたプロンプトを mtmd でチャンクの列にする(0030) |
| `engine/stream.go` | UTF-8 の切れ目の処理(文字にならないバイトは U+FFFD) |
| `engine/sequence.go` | 作業領域の中のシーケンス(KV キャッシュの列)と、生成ごとにどのシーケンスを使うかの選び方(`pickSeq`。0023)。下の「シーケンス」 |
| `engine/parallel.go` | シーケンスが2本以上のときの生成。decode を回す係(`schedule`)が、同時に来た生成を1回の decode にまとめて進める(0023) |
| `models/models.go` | `models` パッケージ。モデルの一覧(`models\` の *.gguf と Ollama の保存フォルダ)。ID は `file:<ファイル名>` / `ollama:<名前>`(以前の `file:<パス>` は `IDOf` で読み替える。0018) |
| `format/format.go` / `format/qwen35.go` / `format/gemma4.go` / `format/llama3.go` / `format/granite.go` / `format/granite42.go` / `format/parts.go` | `format` パッケージ。書式(チャットテンプレート。Qwen3.5・Gemma 4・Llama 3・Granite 4・Granite 4.2。`parts.go` は2つ以上で使う部品)。会話 → 文字列と、生成した文字列 → 思考・答え・ツール呼び出し。説明は [tools.md](tools.md) |
| `engine/stream_test.go` / `format/qwen35_test.go` / `format/gemma4_test.go` | UTF-8 の切れ目 / 書式(公式テンプレートとの一致・思考とツール呼び出しの振り分け)のテスト。モデル不要 |
| `models/models_test.go` | Ollama の目録(projector の層も)のテスト。モデル不要 |
| `engine/chunks_test.go` | KV キャッシュの記録と次のプロンプトの共通の先頭(画像は ID で比べる)・マスの数と位置の数のテスト。モデル不要 |
| `engine/sequence_test.go` | シーケンスの選び方(使える一致・空・一番古いもの)のテスト。モデル不要 |

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
- 目録に `application/vnd.ollama.image.projector` の層があれば、その blob を画像を読む部品(mmproj)として `Entry.Projector` に持つ(qwen3.5:4b / 9b にある。中身は llama.cpp の普通の mmproj。0030 の段1)

## チャットテンプレート

- ライブラリ任せにせず、`format/qwen35.go`・`format/gemma4.go`・`format/llama3.go`・`format/granite.go`・`format/granite42.go` に自分で書く(書式を理解することが学習目的のため)
- 書式は `Format` の interface で、エンジンがモデルの種類(GGUF の `general.architecture`)と、要るならチャットテンプレート(`llama.ModelChatTemplate`)の印で選ぶ
  (`qwen35` → Qwen3.5、`gemma4` → Gemma 4、`llama` でテンプレートに `<|start_header_id|>` があるもの → Llama 3、`granite` でテンプレートに `<|start_of_role|>` と `<tool_call>` があるもの → Granite 4、`<|im_start|>` と `<function=` があるもの → Granite 4.2。`format.ForModel`。0027・0029)。
  `llama` の種類には Llama 3 の書式でないモデル(Hermes 3 の ChatML)もあり、`granite` の種類は版で書式がまったく違う(3.x・4.0/4.1・4.2)ので、種類の名前だけでは選ばない。一覧(`models`)も GGUF の `tokenizer.chat_template` を読んで同じ決まりで選ぶ(`api.FillFormat`)。会話の仕組みは書式を知らない。詳しくは [tools.md](tools.md)。
  対応していない種類のエラーは、対応している書式を一覧から出す(`format.Supported`。0026)
- 思考の ON/OFF は書式ごとに違う:Qwen3.5 はプロンプト末尾を `<think>\n` にするか `<think>\n\n</think>\n\n` にするか、Gemma 4 はシステムの発言の先頭に `<|think|>` を書くか、Granite 4.2 は返事の書き出しを `<think>` と改行にするか `<think></think>` にするか。Llama 3・Granite 4 は思考しない(ON / OFF は無視。画面は「思考する」を押せなくする)
- **bos は書式が書かない**(0026 の決定事項 25)。トークン化は `addSpecial=true`(`Engine.tokenize`)なので、GGUF の `tokenizer.ggml.add_bos_token` が True なら llama.cpp が先頭に1つ足す。
  Gemma 4 は True(`<bos>`)で、テンプレートも先頭に `bos_token` を書くので、書式が文字で書くと2つになる。Qwen3.5 は False で bos のトークンも無い。
  Llama 3.2(Ollama の llama3.2:3b)は `add_bos_token` を書いていないが、llama.cpp は `<|begin_of_text|>` を1つ足す(0027 の段1で確かめた)。
  Granite 4.1 は False(bos は `<|end_of_text|>`)、4.2 は書いていない(bos は `<s>`)が、どちらも llama.cpp は足さず、テンプレートも書かない(0029 の段1で確かめた)。
  語彙だけを読んで(`ModelParams.VocabOnly`)Gemma 4 のプロンプトをトークン化し、bos が先頭の1つだけ・書式の印(`<|turn>`・`<|channel>`・`<|tool_call>`・`<|"|>` など)がどれも1トークンになることを確かめた(2026-10-30)
- 生成した特別なトークンは文字として出す(`TokenToPiece` の special=true)ので、書式の読み取りには `</think>`・`<|channel>`・`<|tool_call>` などがそのまま届く。発言の終わり(Qwen3.5 は `<|im_end|>`、Gemma 4 は `<turn|>`、Granite 4 は `<|end_of_text|>`、Granite 4.2 は `<|im_end|>`)は EOG のトークンなので、届いたところで生成を止める(文字にはしない)

## 生成とモデルの入れ替え

- 生成とモデルの入れ替えは同時にできない(コンテキストが1つのため)。`Engine.mu` で排他している。
  シーケンスが1本(既定)なら生成も1つずつ。2本以上(aibow-server の `-parallel`)なら生成どうしは同時に入り、モデルの入れ替えとだけ排他する(下の「シーケンス」)
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

## シーケンス(0023)

llama.cpp は1つの作業領域(コンテキスト)の中に、番号(seq_id)の付いた複数の KV キャッシュの列を持てる。aibow-server は `-parallel`(既定 1)でその数を決める
(`engine.Config.Seqs`。作業領域を作るときの `NSeqMax`)。デスクトップのアプリは使う人が1人なので1本のまま。

```
作業領域 32768 ─ シーケンス 0(16384)─ 会話 A の KV キャッシュ
               └ シーケンス 1(16384)─ 会話 B の KV キャッシュ   ← 同時に来たら1回の decode にまとめる
```

- **作業領域は等分される**(`KVUnified` で分け合う形は使わない)。1つの会話が使えるのは1本の大きさ(`NCtxSeq`)までで、`ModelInfo.NCtx` も1本あたりを見せる(`Seqs` に本数)。
  VRAM は本数を変えても変わらない(KV キャッシュの合計で決まる)
- **どのシーケンスを使うか**(`pickSeq`):プロンプトの頭が一番長く「使える形で」一致するもの → 空のもの → 一番長く使われていないもの(捨てて使う)。
  会話の ID ではなくプロンプトで選ぶので、OpenAI 互換の口(会話の ID が無い)もブラウザの会話も同じ選び方になる(llama.cpp の server と同じ考え方)。
  「使える一致」は、途中から巻き戻せないモデル(Qwen3.5 などハイブリッド・再帰の層を持つもの。`ModelIsHybrid`・`ModelIsRecurrent`)なら中身が丸ごとプロンプトの頭のときだけ、
  巻き戻せるモデルでも中身の半分以上が一致するときだけ(llama.cpp の server の `--slot-prompt-similarity` の既定 0.5 と同じ。頭の数トークンだけの一致で、ほかの会話のシーケンスを奪わない)
- **捨てる**:Qwen3.5 は途中から捨てられないが、シーケンスを丸ごとなら捨てられる(ほかのシーケンスは残る)。`NRsSeq`(再帰の状態の控え)は decode の回数分しか戻せないので使っていない
- **decode を回す係**(`parallel.go`):2本以上のとき、作業領域に触る呼び出し(KV キャッシュを捨てる・decode・確率から選ぶ)は全部、係(goroutine 1つ)がする(llama.cpp の作業領域はスレッドセーフではない)。
  `Generate` はプロンプトとサンプラーを係に渡し、振り分けた文字を列(`textQueue`)から受け取って画面に送る(遅い相手がいても、係はほかの人の decode を止めない)。
  1回の decode には、生成中の人の1トークンずつと、プロンプトを読んでいる人の続き(合わせて `batchSize` まで)を入れる。画像の埋め込みは文字と混ぜられないので、画像だけで decode する
- **batch の中はシーケンスの番号の順に並べる**:`KVUnified` なしでは、llama.cpp は batch を「番号が 1 ずつ増えるシーケンスのまとまり」で区切って計算する。
  逆の順(1 → 0)に並べると2回に分かれ、1回の decode が倍かかった(0023 の段4。4B で 2本とも 30 tok/s → 並べ直して 64 tok/s)
- **混ぜると答えが少し変わることがある**:混ぜると行列の形が変わって足し算の順番が変わり、logits が最大 0.5 ほど違う。上位2つが近いところで入れ替わることがある(状態が混ざるのではない)。
  temperature 0 でも、1人のときと同じ答えになるとは限らない
- **生成の記録**:`GenerateStats.Seq`(使ったシーケンス)・`Mixed`(この生成の decode に一番多く混ざった生成の数)。aibow-server がログに出す([api.md](api.md) の「席とシーケンス」)
- モデルの入れ替え・層の数・降ろすは、全部の生成が終わってから(席の側で待つ。[api.md](api.md))

## 画像を読む(0030)

仕様と測った数は [features/0030-read-files.md](features/0030-read-files.md)(段1・段2)。今は Qwen3.5(Ollama の qwen3.5)だけ。

- **画像もトークンになる**:画像エンコーダー(mmproj。`clip.projector_type = qwen3vl_merger`)が画像を「画像トークンの埋め込み」にし、文字のトークンと同じ列でモデルが読む。
  32 ピクセル四方で 1 トークン(1024×768 は 768 トークン)。1 枚の上限は `AIBOW_IMAGE_MAX_TOKENS`(既定 1,024。大きい画像は mtmd が縮める)。ツールではなくモデル自身の機能
- 呼び出しは llama.cpp の mtmd(`mtmd.dll`。yzma の `pkg/mtmd`。cgo なし)。`engine.New` で読み、読めなくても文字の会話は止めない(画像を送ったときに理由を出す)
- **mmproj は最初に画像を送ったとき(数えるときも)に読み込む**(画像を使わない人の VRAM を減らさない)。GPU に載せ、載らなければ CPU で読み直す(CPU だとエンコードが 15〜30 倍遅い)。
  読み込んだ回の `GenerateStats` に `ProjectorSec`・`ProjectorCPU` を1回だけ出す。モデルを降ろす・替えるときに一緒に解放する(`unload` → `freeProjector`)
- **書式は印だけを書く**:`format.Message.Images` の画像の場所に `<__media__>`(`format.ImageMarker`)を書く。Qwen3.5 は発言の中で画像を文字より前に置く。
  前後の `<|vision_start|>`・`<|vision_end|>` は mtmd が足す。画像を書ける書式は `WritesImages()` を持ち(`format.ReadsImages`)、持たない書式・mmproj の無いモデルに画像を渡すとエラー(黙って捨てない)
- **チャンクの列**(`chunks.go`):画像があるプロンプトは mtmd の `Tokenize` で「文字のトークン列 / 画像」のチャンクの列にする。画像の中身は llama.cpp が読む(PNG・JPEG など)。
  画像の ID には会話の仕組みが入れた中身のハッシュを使う(`BitmapSetId`)。画像が無ければ今までどおり文字のトークン列1本
- **画像は自分で読ませる**:`EncodeChunk` → `GetOutputEmbd`(トークン数 × `ModelNEmbdInp`)→ 埋め込みの batch(`Embd`)で 512 トークンずつ decode。
  位置は M-RoPE なら1トークンに4つを列ごとに(`pos[i]`・`pos[n+i]`・`pos[2n+i]`・`pos[3n+i]` に t・y・x・z)並べる。llama.cpp の helper と logits が一致することを段1で確かめた。
  yzma には「チャンク1つだけ読ませる」helper が無く、KV キャッシュを使い回して途中から読ませるため、helper(`HelperEvalChunks`)は使わない
- **位置は自分で書く**:画像を読ませたあとの KV キャッシュの位置の最大は画像の始まりの位置のままで、llama.cpp の位置の自動(最大 + 1)だと画像の後ろの文字がずれる。
  文字も `batch.Pos` に位置を書いて読ませる(`decode`。次の位置は記録の位置の合計 `nextPos`)
- **マスと位置は別の数**:M-RoPE の画像は、マスはトークンの数(768)使うが、位置は縦横の大きいほう(32)しか進まない。作業領域が足りるか・`PromptTokens` はマスの数、
  KV キャッシュを途中から捨てる(`MemorySeqRm`)のは位置で指定する
- **KV キャッシュの記録はチャンクの列**(`Engine.cached`):次のプロンプトと先頭から比べ(`common`。文字はトークン、画像は ID)、同じところまでは読み直さない。
  Qwen3.5 は途中まで巻き戻せないので、新しい発言を足すと今までどおり全部読み直しになるが、ツールのループの中では画像も使い回せる(段2:1,383 トークン中 1,360 を再利用・画像のエンコード 0 秒)
- 数える(`CountTokens`)ときは、画像の中身を読まず大きさだけの Bitmap(placeholder)で画像トークン数を数える(mmproj は要る)。mtmd の関数はスレッドセーフでないので、
  mmproj を使うところは `projector.mu` でまとめて守る(数えるのは生成と同時に呼ばれることがある)
- 記録:`GenerateStats` の `ImageTokens`(プロンプトのうち画像の分)・`EncodeSec`(今回エンコードした秒数。使い回した画像は入らない)

## UTF-8 として正しくない文字列

- llama.cpp のトークン化に UTF-8 として正しくない文字列を渡すと、C++ の例外でプロセスごと落ちることがある
- 生成した文字列(`utf8Buffer`)とトークン化の前(`Engine.tokenize`)で U+FFFD に置き換えている

## 関連

- サンプリングの値(presence penalty など)を変えるときは [tuning.md](tuning.md) の記録を読む
- モデルの読み込みで落ちたときは [known-issues.md](known-issues.md) を見る
