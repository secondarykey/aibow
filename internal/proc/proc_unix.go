//go:build unix

package proc

// proc_unix.go: Windows 以外(Linux・macOS)で子プロセスまでまとめて止める仕組み(プロセスグループ)。
//
// 新しいプロセスグループで起動すると(Setpgid)、そこから起動された子プロセスも同じグループに入る。
// 止めるときはグループごと止める。macOS でも動くはずだが、確かめていない(Linux はクラウドセッションと WSL で確かめた)。

import (
	"os/exec"
	"syscall"
)

// Tree は、起動したプロセスと、そこから起動された子プロセス(同じプロセスグループに入る)。
type Tree struct {
	cmd *exec.Cmd
}

// Prepare は、起動する前の cmd を、新しいプロセスグループで起動するようにする。
func Prepare(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// Start はコマンドを起動する(Prepare か、自分で Setpgid を付けておくこと)。
func Start(cmd *exec.Cmd) (*Tree, error) {
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &Tree{cmd: cmd}, nil
}

// Attach は、別の部品(MCP の SDK など)が起動したプロセスをまとめる(起動する前に Prepare しておくこと)。
func Attach(cmd *exec.Cmd) (*Tree, error) {
	return &Tree{cmd: cmd}, nil
}

// Kill はプロセスグループごと止める。kill に負の PID を渡すと、その番号のプロセスグループ全体に届く
// (Setpgid で起動したので、グループの番号は起動したプロセスの PID と同じ)。
func (t *Tree) Kill() {
	syscall.Kill(-t.cmd.Process.Pid, syscall.SIGKILL)
}

// Close は何もしない。
// Windows では、終わった後に残ったプロセス(バックグラウンドで起動したもの)もジョブを閉じて止めるが、
// こちらで終わった後にグループへ kill を送ると、PID が別のプロセスに使い回されていた場合に、関係ないプロセスを止めてしまう。
func (t *Tree) Close() {}
