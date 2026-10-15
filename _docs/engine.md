# エンジン(llama.cpp の呼び出しとモデル)

LLM を動かす部分(`engine/`・`format/`・`models/` の3つのパッケージ)の説明です。
生成・モデルの読み込みと切り替え・トークン化・プロンプトの書式・思考の振り分け・モデルの一覧を触るときに読みます。

## ファイル

| ファイル | 役割 |
|---|---|
| `engine/engine.go` | `engine` パッケージ。llama.cpp の呼び出し。モデルの読み込み・入れ替え、作業領域の作り直し、トークン数を数える(`CountTokens`)、KV キャッシュ再利用、decode、サンプリングの生成ループ |
| `engine/stream.go` | UTF-8 の切れ目の処理(文字にならないバイトは U+FFFD) |
| `models/models.go` | `models` パッケージ。モデルの一覧(`models\` の *.gguf と Ollama の保存フォルダ)。ID は `file:<パス>` / `ollama:<名前>` |
| `format/format.go` / `format/qwen35.go` | `format` パッケージ。書式(チャットテンプレート)。会話 → 文字列と、生成した文字列 → 思考・答え・ツール呼び出し。説明は [tools.md](tools.md) |
| `engine/stream_test.go` / `format/qwen35_test.go` | UTF-8 の切れ目 / 書式(公式テンプレートとの一致・思考とツール呼び出しの振り分け)のテスト。モデル不要 |
| `models/models_test.go` | Ollama の目録のテスト。モデル不要 |

## パッケージの分け方と依存の向き

会話の仕組み(ルートの `aibow`)なしで「生成だけ」を使えるよう、LLM を動かす部分を3つに分けている(0012 の 6-0)。
ステップ6の OpenAI 互換 API(`aibow-api`)は、`engine` と `format` だけを使う。

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

- ライブラリ任せにせず、`format/qwen35.go` に自分で書く(書式を理解することが学習目的のため)
- 書式は `Format` の interface で、エンジンがモデルの種類(GGUF の `general.architecture`)で選ぶ。会話の仕組みは書式を知らない。詳しくは [tools.md](tools.md)
- 思考の ON/OFF は、プロンプト末尾を `<think>\n` にするか `<think>\n\n</think>\n\n` にするかで切り替える

## 生成とモデルの入れ替え

- 生成とモデルの入れ替えは同時に1つだけ(コンテキストが1つのため)。`Engine.mu` で排他している
  (会話の側でも生成中の会話を1つだけ持つ。[conversations.md](conversations.md))
- 語彙(vocab)は `Engine.vocabMu` でも守っていて、`CountTokens` は生成中でも呼べる(別の会話を開いたときのメーターのため)
- モデルの切り替えは「今のモデルを解放 → 新しいモデルを読み込む」の順(VRAM 8GB に2つは載らないため)。失敗したら元のモデルを読み直す
- GPU の層数の変更(`SetGPULayers`)も同じ仕組みで読み直す。
  作業領域の大きさの変更(`Engine.EnsureContextSize`。同じ大きさなら何もしない)はコンテキストだけを作り直し、モデルは読み直さない。
  会話ごとに大きさが違うので、会話の仕組みが送信の前に呼ぶ。画面で上限を「適用」したときも `ChatService.TryLimits` から呼び、VRAM に入るかを確かめる(0009)。
  作業領域(`n_ctx`)の決め方は [compact.md](compact.md)

## UTF-8 として正しくない文字列

- llama.cpp のトークン化に UTF-8 として正しくない文字列を渡すと、C++ の例外でプロセスごと落ちることがある
- 生成した文字列(`utf8Buffer`)とトークン化の前(`Engine.tokenize`)で U+FFFD に置き換えている

## 関連

- サンプリングの値(presence penalty など)を変えるときは [tuning.md](tuning.md) の記録を読む
- モデルの読み込みで落ちたときは [known-issues.md](known-issues.md) を見る
