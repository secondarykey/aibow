//go:build windows

package tool

// shell_windows.go: Windows でコマンドを動かすシェル(cmd.exe)と、子プロセスまでまとめて止める仕組み(ジョブオブジェクト)。
//
// どのシェルで動かすかは、この1か所(と shell_unix.go)にまとめている。あとからシェルを足すときはここを変える。
//
// cmd.exe を選んだのは、どの Windows にも入っていて、Git Bash を入れていない人でも動くから。
// ただし cmd.exe は、ほかのプログラムと引数の読み方が違う(Go の os/exec の説明にもある)ので、
// コマンドライン全体を自分で組み立てて SysProcAttr.CmdLine に入れる。

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"unsafe"
)

// ShellNote は、環境の説明(システムプロンプト)に書く、run_command のシェル。
// モデルは Linux のコマンド(ls・cat・grep)を書きがちなので、cmd.exe の書き方を添える
const ShellNote = "cmd.exe (use dir, type, findstr; ls, cat and grep are not available)"

const createNoWindow = 0x08000000 // コンソールの窓を出さずに起動する(aibow は窓のアプリなので、無いと黒い窓が一瞬出る)

// shellCommand は、dir をカレントにして command を cmd.exe で動かす exec.Cmd を作る。
//
// 文字化けを防ぐため、外側の cmd.exe でコードページを UTF-8(65001)にしてから、内側の cmd.exe でコマンドを動かす:
//
//		cmd.exe /d /v:on /s /c "chcp 65001 >nul && cmd.exe /d /s /c "!AIBOW_COMMAND!""
//
//	  - 日本語の Windows の cmd.exe は、組み込みのコマンド(echo・dir・type のエラーなど)を Shift_JIS で出す。
//	    同じ cmd.exe の中で chcp 65001 しても変わらず(試した)、chcp した後に起動した cmd.exe から UTF-8 になる。
//	    go や git はもともと UTF-8 で出す。cmd.exe 自身のメッセージは英語になる(「The system cannot find the file specified.」)
//	  - コマンドは環境変数 AIBOW_COMMAND に入れ、遅延展開(/v:on の !変数!)で渡す。遅延展開は外側の cmd.exe が行を読んだ後に行うので、
//	    コマンドの中の " & | > を外側の cmd.exe が解釈しない(コマンドラインに直接埋め込むと、"a & b" のような引用符の中の & で行が分かれてしまった)。
//	    内側の cmd.exe は、コマンドをふつうのコマンドラインとして読む(for %i や %PATH% も、cmd.exe で打つときと同じ)
//	  - /d は自動実行(AutoRun のレジストリ)を読まない。/s /c "…" は、最初と最後の引用符だけを外して、その間をそのまま実行する
func shellCommand(dir, command string) *exec.Cmd {
	cmd := exec.Command(os.Getenv("ComSpec"))
	if cmd.Path == "" || cmd.Err != nil {
		cmd = exec.Command("cmd.exe")
	}
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "AIBOW_COMMAND="+command)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CmdLine:       `cmd.exe /d /v:on /s /c "chcp 65001 >nul && cmd.exe /d /s /c "!AIBOW_COMMAND!""`,
		CreationFlags: createNoWindow,
	}
	return cmd
}

// processTree は、起動したシェルと、そこから起動された子プロセスをまとめたもの。
//
// Windows では、親プロセス(cmd.exe)を止めても子プロセス(go test が起動したテストなど)は止まらない。
// しかも子プロセスが出力のパイプを持ったままなので、Wait も子が終わるまで返らない(試したら、ping を止めるのに 14 秒待った)。
// そこで、シェルをジョブオブジェクト(プロセスのまとまり。子プロセスも自動で同じジョブに入る)に入れ、止めるときはジョブごと止める。
// ジョブには「ハンドルを閉じたら中のプロセスを全部止める」を付けておく。
// コマンドが終わった後に残ったプロセス(バックグラウンドで起動したもの)も、aibow が落ちたときも、残らない。
//
// ジョブに入れるのは起動した直後(Go の exec にはジョブに入れて起動する口が無い)。
// その間(マイクロ秒)に cmd.exe が子プロセスを起動することはまず無い(cmd.exe が起動して行を読むだけでミリ秒かかる)。
type processTree struct {
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

// startTree はコマンドを起動し、ジョブオブジェクトに入れる。
func startTree(cmd *exec.Cmd) (*processTree, error) {
	job, err := newJob()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		syscall.CloseHandle(job)
		return nil, err
	}
	t := &processTree{cmd: cmd, job: job}
	if err := t.assign(); err != nil {
		cmd.Process.Kill()
		t.close()
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

func (t *processTree) assign() error {
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

// kill はシェルと子プロセスをまとめて止める。
func (t *processTree) kill() {
	procTerminateJobObject.Call(uintptr(t.job), 1)
}

// close はジョブのハンドルを閉じる。中にまだプロセスが残っていれば、ここで止まる(KILL_ON_JOB_CLOSE)。
func (t *processTree) close() {
	if t.job != 0 {
		syscall.CloseHandle(t.job)
		t.job = 0
	}
}
