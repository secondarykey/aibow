//go:build unix

package tool

// shell_unix.go: Windows 以外(Linux・macOS)でコマンドを動かすシェル(/bin/sh)と、子プロセスまでまとめて止める仕組み(プロセスグループ)。
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

// processTree は、起動したシェルと、そこから起動された子プロセス(同じプロセスグループに入る)。
type processTree struct {
	cmd *exec.Cmd
}

func startTree(cmd *exec.Cmd) (*processTree, error) {
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &processTree{cmd: cmd}, nil
}

// kill はプロセスグループごと止める。kill に負の PID を渡すと、その番号のプロセスグループ全体に届く
// (Setpgid で起動したので、グループの番号はシェルの PID と同じ)。
func (t *processTree) kill() {
	syscall.Kill(-t.cmd.Process.Pid, syscall.SIGKILL)
}

// close は何もしない。
// Windows では、コマンドが終わった後に残ったプロセス(バックグラウンドで起動したもの)もジョブを閉じて止めるが、
// こちらで終わった後にグループへ kill を送ると、シェルの PID が別のプロセスに使い回されていた場合に、関係ないプロセスを止めてしまう。
// 残ったプロセスが出力のパイプを持っていても、待つのは waitDelay まで
func (t *processTree) close() {}
