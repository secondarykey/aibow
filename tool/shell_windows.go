//go:build windows

package tool

// shell_windows.go: Windows でコマンドを動かすシェル(cmd.exe)。
// 子プロセスまでまとめて止める仕組み(ジョブオブジェクト)は internal/proc にある(MCP のサーバーでも使うため)。
//
// どのシェルで動かすかは、この1か所(と shell_unix.go)にまとめている。あとからシェルを足すときはここを変える。
//
// cmd.exe を選んだのは、どの Windows にも入っていて、Git Bash を入れていない人でも動くから。
// ただし cmd.exe は、ほかのプログラムと引数の読み方が違う(Go の os/exec の説明にもある)ので、
// コマンドライン全体を自分で組み立てて SysProcAttr.CmdLine に入れる。

import (
	"os"
	"os/exec"
	"syscall"
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
