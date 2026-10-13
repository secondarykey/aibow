//go:build unix

package tool

// shell_unix.go: Windows 以外(Linux・macOS)でコマンドを動かすシェル(/bin/sh)。
// 子プロセスまでまとめて止める仕組み(プロセスグループ)は internal/proc にある(MCP のサーバーでも使うため)。
//
// どのシェルで動かすかは、この1か所(と shell_windows.go)にまとめている。
// macOS でも動くはずだが、確かめていない(Linux はクラウドセッションと WSL で確かめた)。

import (
	"os/exec"
	"syscall"
)

// ShellNote は、環境の説明(システムプロンプト)に書く、run_command のシェル。
const ShellNote = "/bin/sh"

// shellCommand は、dir をカレントにして command を /bin/sh -c で動かす exec.Cmd を作る。
// 子プロセスもまとめて止められるよう、新しいプロセスグループで起動する(Setpgid)。
func shellCommand(dir, command string) *exec.Cmd {
	cmd := exec.Command("/bin/sh", "-c", command)
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd
}
