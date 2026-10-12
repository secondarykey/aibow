# 会話の上限と compact

会話が長くなったときに、モデルに渡す履歴を会話の上限に収める仕組みの説明です。
決めた経緯と画面の動きは [features/0002-compact.md](features/0002-compact.md) にあります。

## ファイル

| ファイル | 役割 |
|---|---|
| `compact.go` | 会話の上限に収める仕組み(`none` / `truncate` / `summary`)、メーター(`Usage`)、今すぐ要約(`Compact`)と要約だけの停止(`StopSummary`)、要約の指示の文面 |
| `compact_test.go` | compact のテスト。1文字 = 1トークンで数える偽物の `fakeGen` を使う。モデル不要 |

## 上限

- 上限は「会話の上限(`history_tokens`)+ 返事の上限(`reply_tokens`)= 作業領域」。作業領域(`n_ctx`)は aibow が計算して確保する
- 会話の上限に収まっているかは、実際にモデルに渡す文字列(テンプレート込み・要約込み)を `Engine.CountTokens` で数えて判断する。トークン数は保存しない(モデルで変わるため)
- 環境変数は `AIBOW_HISTORY_TOKENS`(会話の上限)と `AIBOW_MAX_TOKENS`(返事の上限)。`AIBOW_CTX` は 0002 で廃止(設定されていたらログとトーストで伝える)

## compact の方式

- どの方式でも DB の発言は消さない
- truncate は保存せず毎回計算する
- summary は `compactions` 表に積み重ね、モデルに渡すのは最新の1件だけ(system の後ろ)
- 外す・要約するのは「ユーザーの発言 + 返事」の組ごと
- none で上限を超える・最新の発言だけで超えるときは、発言を保存せずに生成前に止める
- 送信の途中の要約中だけは `ChatService.StopSummary()` で要約だけを止められる(その回は truncate で返事に進む。仕様の「失敗したとき」)

## 関連

- 実際に動かして分かったこと(KV キャッシュの再利用、要約の指示文の長さなど)は [tuning.md](tuning.md)
- compact の動作確認の手順は [testing.md](testing.md)
