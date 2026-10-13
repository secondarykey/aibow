package tool

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// byOS は、Windows(cmd.exe)とそれ以外(/bin/sh)でコマンドの書き方を変える
func byOS(windows, sh string) string {
	if runtime.GOOS == "windows" {
		return windows
	}
	return sh
}

func runCmd(t *testing.T, tl runCommand, command string) (string, error) {
	t.Helper()
	return tl.Run(context.Background(), Args{"command": command})
}

func TestRunCommand(t *testing.T) {
	dir := workDir(t, map[string]string{"a.txt": "hello\n"})
	tl := runCommand{dir: dir}

	// 作業フォルダがカレント。標準出力とエラー出力はまとめ、終了コードを付ける
	got, err := runCmd(t, tl, byOS("type a.txt", "cat a.txt"))
	if err != nil || got != "hello\n(終了コード 0)" {
		t.Errorf("カレント: %q, %v", got, err)
	}
	// 0 以外の終了コードはエラーではなく結果(テストの失敗などをモデルに読ませる)
	got, err = runCmd(t, tl, byOS("echo bad 1>&2 & exit 3", "echo bad 1>&2; exit 3"))
	if err != nil || !strings.HasPrefix(got, "bad") || !strings.HasSuffix(got, "(終了コード 3)") {
		t.Errorf("終了コード: %q, %v", got, err)
	}
	// 日本語が化けない(Windows では chcp 65001 の後に起動した cmd.exe で動かすため)
	if got, err := runCmd(t, tl, "echo 日本語"); err != nil || !strings.Contains(got, "日本語") {
		t.Errorf("日本語: %q, %v", got, err)
	}
	// 引用符の中の & や | もそのまま(Windows ではコマンドを環境変数で渡しているため、外側の cmd.exe が解釈しない)
	if got, err := runCmd(t, tl, `echo "a & b | c"`); err != nil || !strings.Contains(got, "a & b | c") {
		t.Errorf("引用符: %q, %v", got, err)
	}
	// 標準入力は空なので、入力を待って止まらない
	if got, err := runCmd(t, tl, byOS("findstr x", "cat")); err != nil || !strings.Contains(got, "(終了コード") {
		t.Errorf("標準入力: %q, %v", got, err)
	}
	if got, err := runCmd(t, tl, byOS("rem", "true")); err != nil || got != "(出力なし)\n(終了コード 0)" {
		t.Errorf("出力なし: %q, %v", got, err)
	}

	pv, err := tl.Preview(Args{"command": "  go test ./...  "})
	if err != nil || pv.Note != "コマンド" || pv.Command != "go test ./..." {
		t.Errorf("確認: %+v, %v", pv, err)
	}
	if _, err := tl.Preview(Args{"command": " "}); err == nil {
		t.Error("空のコマンドはエラーのはず")
	}
	if _, err := (runCommand{dir: filepath.Join(dir, "gone")}).Preview(Args{"command": "echo"}); err == nil {
		t.Error("作業フォルダが無ければエラーのはず")
	}
}

// 長い出力は先頭と末尾を残して間を切る
func TestRunCommandLongOutput(t *testing.T) {
	tl := runCommand{dir: workDir(t, nil)}
	got, err := runCmd(t, tl, byOS(
		"for /l %i in (1,1,3000) do @echo line %i",
		`i=1; while [ $i -le 3000 ]; do echo line $i; i=$((i+1)); done`))
	if err != nil || !strings.HasPrefix(got, "line 1\nline 2\n") || !strings.Contains(got, "line 3000\n(終了コード 0)") || !strings.Contains(got, "省略しました") {
		t.Errorf("長い出力: %d 文字 / 先頭 %q / 末尾 %q, %v", len(got), got[:min(40, len(got))], got[max(0, len(got)-60):], err)
	}
	if n := len(got); n > outputHead+outputTail+200 {
		t.Errorf("残しすぎ: %d バイト", n)
	}
}

// 時間の上限を超えたら、シェルだけでなく、そこから起動された子プロセスもまとめて止める。
// 止めきれないと、2秒後に子プロセスが late.txt を作る
func TestRunCommandTimeoutKillsChildren(t *testing.T) {
	dir := workDir(t, nil)
	tl := runCommand{dir: dir, limit: time.Second}
	command := byOS(
		`start /b cmd /c "ping -n 3 127.0.0.1 >nul & echo late> late.txt" & echo started & ping -n 30 127.0.0.1 >nul`,
		`(sleep 2; echo late > late.txt) & echo started; sleep 30`)
	ctx, cancel := context.WithTimeout(context.Background(), tl.TimeLimit())
	defer cancel()
	start := time.Now()
	got, err := tl.Run(ctx, Args{"command": command})
	if err == nil || !strings.Contains(err.Error(), "時間の上限(1 秒)を超えたため止めました") || !strings.Contains(err.Error(), "started") || got != "" {
		t.Errorf("時間切れ: %q, %v", got, err)
	}
	if d := time.Since(start); d > 2500*time.Millisecond {
		t.Errorf("止まるまで %v かかりました(子プロセスが出力のパイプを持ったままになっている)", d)
	}
	time.Sleep(3 * time.Second)
	if _, err := os.Stat(filepath.Join(dir, "late.txt")); err == nil {
		t.Error("子プロセスが止まらずに残っています(late.txt ができた)")
	}
}

// 停止(ctx を止める)でも止まり、その旨を返す
func TestRunCommandStop(t *testing.T) {
	tl := runCommand{dir: workDir(t, nil)}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(500*time.Millisecond, cancel)
	start := time.Now()
	_, err := tl.Run(ctx, Args{"command": byOS("ping -n 30 127.0.0.1 >nul", "sleep 30")})
	if err == nil || !strings.Contains(err.Error(), "停止されたため止めました") || time.Since(start) > 2*time.Second {
		t.Errorf("停止: %v(%v)", err, time.Since(start))
	}
}

// 出力を残す部分の切れ目で、日本語(3バイト)の途中を切らない
func TestOutputBufferCutsAtRunes(t *testing.T) {
	var o outputBuffer
	o.Write([]byte(strings.Repeat("a", outputHead-1) + strings.Repeat("あ", 5000) + "\r\nend\r\n"))
	s := o.String()
	if !strings.HasPrefix(s, strings.Repeat("a", outputHead-1)+"\n…(") || !strings.HasSuffix(s, "あ\nend") || strings.ContainsRune(s, '�') || strings.Contains(s, "?") {
		t.Errorf("先頭 %q / 末尾 %q", s[:min(len(s), outputHead+20)], s[max(0, len(s)-20):])
	}
}
