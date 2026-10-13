//go:build windows

package proc

// proc_windows.go: Windows で子プロセスまでまとめて止める仕組み(ジョブオブジェクト)。
//
// Windows では、親プロセス(cmd.exe)を止めても子プロセス(go test が起動したテストなど)は止まらない。
// しかも子プロセスが出力のパイプを持ったままなので、Wait も子が終わるまで返らない(試したら、ping を止めるのに 14 秒待った)。
// そこで、起動したプロセスをジョブオブジェクト(プロセスのまとまり。子プロセスも自動で同じジョブに入る)に入れ、止めるときはジョブごと止める。
// ジョブには「ハンドルを閉じたら中のプロセスを全部止める」を付けておく。
// 終わった後に残ったプロセス(バックグラウンドで起動したもの)も、aibow が落ちたときも、残らない。
//
// ジョブに入れるのは起動した直後(Go の exec にはジョブに入れて起動する口が無い)。
// その間(マイクロ秒)に cmd.exe が子プロセスを起動することはまず無い(cmd.exe が起動して行を読むだけでミリ秒かかる)。
// taskkill /T /F は使わない(別のプロセスを起動するうえ、親が先に終わった孫を見つけられない)。

import (
	"fmt"
	"os/exec"
	"syscall"
	"unsafe"
)

// createNoWindow は、コンソールの窓を出さずに起動するフラグ(aibow は窓のアプリなので、無いと黒い窓が一瞬出る)。
const createNoWindow = 0x08000000

// Tree は、起動したプロセスと、そこから起動された子プロセスをまとめたもの。
type Tree struct {
	cmd *exec.Cmd
	job syscall.Handle
}

var (
	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObjectW         = kernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObject  = kernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJobObject = kernel32.NewProc("AssignProcessToJobObject")
	procTerminateJobObject       = kernel32.NewProc("TerminateJobObject")
)

// jobObjectExtendedLimitInformation は Windows の JOBOBJECT_EXTENDED_LIMIT_INFORMATION(64 ビットでの並び)。
type jobObjectExtendedLimitInformation struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
	IoInfo                  [6]uint64 // IO_COUNTERS
	ProcessMemoryLimit      uintptr
	JobMemoryLimit          uintptr
	PeakProcessMemoryUsed   uintptr
	PeakJobMemoryUsed       uintptr
}

const (
	jobObjectExtendedLimitInformationClass = 9
	jobObjectLimitKillOnJobClose           = 0x2000
	processSetQuota                        = 0x0100
	processTerminate                       = 0x0001
)

// Prepare は、起動する前の cmd に、まとめて止めるための設定をする(Windows ではコンソールの窓を出さない設定だけ)。
// SysProcAttr を自分で作るとき(run_command の cmd.exe)は使わず、そちらで CreationFlags を付ける。
func Prepare(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createNoWindow
}

// Start はコマンドを起動し、ジョブオブジェクトに入れる。
func Start(cmd *exec.Cmd) (*Tree, error) {
	job, err := newJob()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		syscall.CloseHandle(job)
		return nil, err
	}
	t := &Tree{cmd: cmd, job: job}
	if err := t.assign(); err != nil {
		cmd.Process.Kill()
		t.Close()
		return nil, err
	}
	return t, nil
}

// Attach は、別の部品(MCP の SDK など)が起動したプロセスを、起動した直後にジョブオブジェクトに入れる。
func Attach(cmd *exec.Cmd) (*Tree, error) {
	job, err := newJob()
	if err != nil {
		return nil, err
	}
	t := &Tree{cmd: cmd, job: job}
	if err := t.assign(); err != nil {
		t.Close()
		return nil, err
	}
	return t, nil
}

func newJob() (syscall.Handle, error) {
	h, _, err := procCreateJobObjectW.Call(0, 0)
	if h == 0 {
		return 0, fmt.Errorf("ジョブオブジェクトを作れません: %w", err)
	}
	info := jobObjectExtendedLimitInformation{LimitFlags: jobObjectLimitKillOnJobClose}
	if r, _, err := procSetInformationJobObject.Call(h, jobObjectExtendedLimitInformationClass,
		uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info)); r == 0 {
		syscall.CloseHandle(syscall.Handle(h))
		return 0, fmt.Errorf("ジョブオブジェクトを設定できません: %w", err)
	}
	return syscall.Handle(h), nil
}

func (t *Tree) assign() error {
	p, err := syscall.OpenProcess(processSetQuota|processTerminate, false, uint32(t.cmd.Process.Pid))
	if err != nil {
		return fmt.Errorf("起動したプロセスを開けません: %w", err)
	}
	defer syscall.CloseHandle(p)
	if r, _, err := procAssignProcessToJobObject.Call(uintptr(t.job), uintptr(p)); r == 0 {
		return fmt.Errorf("プロセスをジョブオブジェクトに入れられません: %w", err)
	}
	return nil
}

// Kill はプロセスと子プロセスをまとめて止める。
func (t *Tree) Kill() {
	procTerminateJobObject.Call(uintptr(t.job), 1)
}

// Close はジョブのハンドルを閉じる。中にまだプロセスが残っていれば、ここで止まる(KILL_ON_JOB_CLOSE)。
func (t *Tree) Close() {
	if t.job != 0 {
		syscall.CloseHandle(t.job)
		t.job = 0
	}
}
