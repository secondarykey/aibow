// Package localconf は、その PC に置く設定のファイル(settings.json・connection.json)を読み書きする。
//
// 設定の置き場所は、性格で分けている:
//
//	画面で変える設定                         … DB(ルートの aibow の appsettings.go と、会話ごとの conversations.settings)
//	手で書く設定                             … settings.json(settings.go。aibow-server のつなぎ先 serverUrl など)
//	画面で変えるが、その PC の人が決めること … tools.json(ツールの扱い)・mcp.json(MCP のサーバー)・workspaces.json(ワークスペース)と、
//	                                           connection.json(connection.go。画面で決めたつなぎ先とログイン)
//
// どれも「その PC でどう起動するか」で、会話のデータとは性格が違う。会話を aibow-server に置く形(0013・ステップ10)にしても手元に残る。
// だから会話の仕組み(ルートの aibow)から分けて、ここに置く。このパッケージはルートの aibow を import しない
// (画面のアプリ・aibow-server が、会話の仕組みを通さずに読めるように)。
//
// tools.json・mcp.json・workspaces.json はまだルートの aibow にある(会話の仕組みが中の非公開のところまで使っているため。
// 分けるときは _docs/backlog.md の「パッケージの分け方」)。
//
// connection.go がルートのモジュールにあるのは、トークンを包む internal/secret を使うため。画面のアプリ(_cmd/aibow)は別のモジュールなので、
// ルートのモジュールの internal/ を直接 import できない(Go の internal は、同じモジュールの中の親からしか使えない)。
package localconf
