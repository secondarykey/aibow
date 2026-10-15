// Package aibow は、ローカルLLMを自分で動かして仕組みを学ぶための「会話の仕組み」の部分。
//
//	画面(_cmd/aibow) ⇄ 関数呼び出し ⇄ aibow(会話の仕組み)⇄ engine ⇄ llama.cpp(DLL) ⇄ GPU
//
// Ollama のような別のサーバーは使わず、aibow の中で llama.cpp を直接動かしている。
// 画面(Wails のウィンドウ)は _cmd/aibow にあり、このパッケージは画面のことを知らない。
// そうしておくと、画面を作り替えても(ブラウザ → Wails のように)ここは変えずに済む。
//
// 生成そのものは別のパッケージに分けてある。会話の仕組みなしで「生成だけ」を使う側(ステップ6の OpenAI 互換 API)からも使えるようにするため:
//
//	engine/ … llama.cpp を呼んで文章を生成する部分(モデルの読み込み・トークン数・KV キャッシュ)
//	format/ … 会話をモデルの書式の文字列にし、生成した文字を思考・答え・ツール呼び出しに振り分ける部分
//	models/ … 使えるモデル(models フォルダと Ollama)の一覧を作る部分
//
// このパッケージに残るのは、会話(Conversations)・エージェントループ・compact・設定・保存の約束(Store)。
// 生成は Generator の interface を通して使う(本物は engine.Engine)。
package aibow
