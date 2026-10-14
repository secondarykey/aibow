// Package proc は、起動したプロセスと、そこから起動された子プロセスをまとめて止める仕組み。
//
// 使うのは2か所:
//
//	tool/command.go(run_command)… シェル(cmd.exe・/bin/sh)と、そこから起動された go test などを止める
//	mcp/client                   … MCP のサーバー(npx → node など)を、アプリを閉じるときに残さず止める
//
// Windows では、親プロセスを止めても子プロセスは止まらない(Go の Process.Kill は親だけを止める)。
// npx で起動した MCP のサーバーなら、npx.cmd を動かす cmd.exe を止めても、その子の node が残ってしまう。
// そこで Windows ではジョブオブジェクト(proc_windows.go)、それ以外ではプロセスグループ(proc_unix.go)でまとめる。
//
// 3-3 では tool の中にあった。ステップ4で MCP のサーバーにも要ったので、ここに移した。
// internal に置いているのは、aibow のリポジトリの中だけで使う部品だから(外のモジュールからは import できない)。
package proc
