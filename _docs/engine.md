# エンジン(llama.cpp の呼び出しとモデル)

ルートの `aibow` パッケージのうち、LLM を動かす部分の説明です。
生成・モデルの読み込みと切り替え・トークン化・プロンプトの書式・思考の振り分け・モデルの一覧を触るときに読みます。

## ファイル

| ファイル | 役割 |
|---|---|
| `aibow.go` | パッケージの説明。ルートは `aibow` パッケージ(ライブラリ)で、Wails を import しない |
| `engine.go` | llama.cpp の呼び出し。モデルの読み込み・入れ替え、作業領域の作り直し、トークン数を数える(`CountTokens`)、KV キャッシュ再利用、decode、サンプリングの生成ループ |
| `models.go` | モデルの一覧(`models\` の *.gguf と Ollama の保存フォルダ)。ID は `file:<パス>` / `ollama:<名前>` |
| `prompt.go` | 会話 → ChatML 文字列(Qwen3.5 の公式チャットテンプレートを Go で書き直したもの) |
| `stream.go` | UTF-8 の切れ目の処理(文字にならないバイトは U+FFFD)と、`</think>` で思考と答えを振り分ける処理 |
| `stream_test.go` | UTF-8 の切れ目、思考の振り分け、テンプレート(`BuildPrompt`)のテスト。モデル不要 |
| `models_test.go` | Ollama の目録のテスト。モデル不要 |

## llama.cpp と yzma

- aibow のプロセスの中で llama.cpp を直接動かす(外部の LLM サーバーは使わない。トップの `AGENTS.md` を参照)
- yzma と llama.cpp はバージョンの組み合わせが決まっている。現在は yzma v1.29.1 + llama.cpp v0.6.0(ビルド b11429)。
  yzma を上げるときは、yzma の README の対応表を確認し、`lib\` の入れ直しも README に書く
- llama.cpp の DLL(`lib\`)とモデル(`models\`)は `_cmd/aibow` に置く(理由は [_cmd/aibow/AGENTS.md](../_cmd/aibow/AGENTS.md))

## モデルの一覧と Ollama

- Ollama が `ollama pull` で保存したファイル(`manifests\` の目録と `blobs\` の GGUF)を aibow が直接読む。Ollama のサーバーや API は使わない
- Ollama のモデルの一部は llama.cpp で読めない独自形式なので、読めない場合はエラーにして元のモデルに戻す
- Ollama の保存先は Ollama と同じ環境変数 `OLLAMA_MODELS` で変えられる

## チャットテンプレート

- ライブラリ任せにせず、`prompt.go` に自分で書く(書式を理解することが学習目的のため)
- 思考の ON/OFF は、プロンプト末尾を `<think>\n` にするか `<think>\n\n</think>\n\n` にするかで切り替える

## 生成とモデルの入れ替え

- 生成とモデルの入れ替えは同時に1つだけ(コンテキストが1つのため)。`Engine.mu` で排他している
  (会話の側でも生成中の会話を1つだけ持つ。[conversations.md](conversations.md))
- 語彙(vocab)は `Engine.vocabMu` でも守っていて、`CountTokens` は生成中でも呼べる(別の会話を開いたときのメーターのため)
- モデルの切り替えは「今のモデルを解放 → 新しいモデルを読み込む」の順(VRAM 8GB に2つは載らないため)。失敗したら元のモデルを読み直す
- GPU の層数の変更(`SetGPULayers`)も同じ仕組みで読み直す。
  会話の上限・返事の上限の変更(`ChatService.SetLimits` → `Engine.SetContextSize`)はコンテキストだけを作り直し、モデルは読み直さない。
  作業領域(`n_ctx`)の決め方は [compact.md](compact.md)

## UTF-8 として正しくない文字列

- llama.cpp のトークン化に UTF-8 として正しくない文字列を渡すと、C++ の例外でプロセスごと落ちることがある
- 生成した文字列(`utf8Buffer`)とトークン化の前(`Engine.tokenize`)で U+FFFD に置き換えている

## 関連

- サンプリングの値(presence penalty など)を変えるときは [tuning.md](tuning.md) の記録を読む
- モデルの読み込みで落ちたときは [known-issues.md](known-issues.md) を見る
