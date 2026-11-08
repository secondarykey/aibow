// Package config は、アプリ(_cmd/aibow)と aibow-server の、その PC に置く設定のファイルと環境変数を読み書きする。
//
//	settings.go     … アプリの手で書く設定ファイル settings.json(aibow-server のつなぎ先 serverUrl・mcpServers など)
//	connection.go   … アプリが画面で決めた aibow-server のつなぎ先とログイン connection.json
//	toolprefs.go    … ツールの扱い(ToolPolicy: 使わない / 確認する / 確認しない)と、その PC の既定 tools.json(aibow-server は server-tools.json)
//	mcp.go          … 画面から足した MCP のサーバー mcp.json と、settings.json に手で書いた分を移す係・画面で変えたものを流す係(MCPConfig)
//	workspaces.go   … ワークスペース(名前の付いた PC のフォルダの組)と、会話の worktree の結び付け workspaces.json
//	server.go       … aibow-server の手で書く設定ファイル server.json(ポート・作業領域・資料のフォルダ・mcpServers など)
//	servermodels.go … aibow-server が書く server-models.json(モデルごとの GPU の層数・足した置き場所・使えないモデル)
//	env.go          … AIBOW_ で始まる環境変数(誤りをまとめて返す)
//
// 設定の置き場所は、性格で分けている:
//
//	画面で変える設定                         … DB(ルートの aibow の appsettings.go と、会話ごとの conversations.settings)
//	手で書く設定                             … settings.json・server.json
//	画面で変えるが、その PC の人が決めること … tools.json・mcp.json・workspaces.json・connection.json・server-models.json
//	環境変数                                 … 画面で変えられる設定では「DB にまだ値が無いときの初期値」。手で書いたファイルの値のほうが強い
//
// ファイルの設定は「その PC でどう起動するか・その PC の人が決めること」で、会話のデータとは性格が違う。
// 会話を aibow-server に置く形(0013・ステップ10)にしても手元に残る。だから会話の仕組み(ルートの aibow)から分けて、ここに置く。
// このパッケージはルートの aibow を import しない(アプリと aibow-server が、会話の仕組みを通さずに読めるように)。
// 会話の仕組みは、ここで読んだもの(ToolPrefs・Workspaces・ToolPolicy)を受け取って使う。
//
// ここは「ファイルや環境変数に何と書いてあるか」を読んで確かめ、画面で変えたものを書くところまで。
// 引数・ファイル・環境変数のどれを使うか(強さの順)は、それぞれの exe が決める(_cmd/aibow の config.go・_cmd/aibow-server の config.go)。
// exe ごとに引数も既定も違うため。
//
// connection.go がルートのモジュールにあるのは、トークンを包む internal/secret を使うため。画面のアプリ(_cmd/aibow)は別のモジュールなので、
// ルートのモジュールの internal/ を直接 import できない(Go の internal は、同じモジュールの中の親からしか使えない)。
//
// 名前を config にしたので、aibow-server の「強さの順で決めた設定」の型は serverConfig にしている(アプリの側の appConfig とそろえて。
// main パッケージに config という型があると、このパッケージの名前とぶつかるため)。
package config
