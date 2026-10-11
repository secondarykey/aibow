// Package aibow は、ローカルLLMを自分で動かして仕組みを学ぶための「中身」の部分。
//
//	画面(_cmd/aibow) ⇄ 関数呼び出し ⇄ aibow ⇄ llama.cpp(DLL) ⇄ GPU
//
// Ollama のような別のサーバーは使わず、aibow の中で llama.cpp を直接動かしている。
// 画面(Wails のウィンドウ)は _cmd/aibow にあり、このパッケージは画面のことを知らない。
// そうしておくと、画面を作り替えても(ブラウザ → Wails のように)ここは変えずに済む。
//
// ファイルの役割:
//
//	engine.go … llama.cpp を呼んで文章を生成する部分
//	prompt.go … 会話をモデル用の書式(ChatML)の文字列にする部分
//	stream.go … 生成された文字を、思考と答えに振り分ける部分
//	models.go … 使えるモデル(models フォルダと Ollama)の一覧を作る部分
package aibow
