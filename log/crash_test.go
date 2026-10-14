package log

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
	"time"
)

// 前回の記録を残すこと(改名して返す)・空のファイルは異常終了ではないこと・古いログの片付けで消えないこと
func TestKeepPreviousCrash(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, CrashFile)

	if got := keepPreviousCrash(dir); got != "" {
		t.Fatalf("ファイルが無いのに %q", got)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := keepPreviousCrash(dir); got != "" {
		t.Fatalf("空のファイルで %q(正常に終わった回)", got)
	}

	if err := os.WriteFile(path, []byte("panic: boom\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := keepPreviousCrash(dir)
	if !strings.HasPrefix(filepath.Base(got), FilePrefix+"_crash_") {
		t.Fatalf("改名先 = %q", got)
	}
	if b, err := os.ReadFile(got); err != nil || string(b) != "panic: boom\n" {
		t.Fatalf("改名先の中身 = %q, err=%v", b, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("元の名前が残っている: %v", err)
	}

	removeOld(dir, 1, time.Now().AddDate(1, 0, 0)) // 1 年後に片付けても消えない
	if _, err := os.Stat(got); err != nil {
		t.Fatalf("古いログの片付けで記録が消えた: %v", err)
	}
}

// 本当に異常終了させて、CrashFile に内容が残ることを確かめる。
// テストの実行ファイル自身を、環境変数 AIBOW_CRASH_DIR を付けて起動し直し、その中で CatchCrash してから panic させる。
func TestCatchCrash(t *testing.T) {
	if dir := os.Getenv("AIBOW_CRASH_DIR"); dir != "" {
		if _, err := CatchCrash(dir); err != nil {
			t.Fatal(err)
		}
		go panic("boom") // goroutine の panic は recover されず、プロセスごと落ちる
		time.Sleep(10 * time.Second)
		return
	}

	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestCatchCrash$")
	cmd.Env = append(os.Environ(), "AIBOW_CRASH_DIR="+dir)
	if err := cmd.Run(); err == nil {
		t.Fatal("異常終了するはず")
	}
	b, err := os.ReadFile(filepath.Join(dir, CrashFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "panic: boom") || !strings.Contains(string(b), "goroutine") {
		t.Fatalf("panic の内容とスタックが残る:\n%s", b)
	}

	// 次の起動にあたる CatchCrash で、前回の記録として改名されて返る
	previous, err := CatchCrash(dir)
	t.Cleanup(func() { debug.SetCrashOutput(nil, debug.CrashOptions{}) }) // ファイルを放す(Windows では放さないと一時フォルダを消せない)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(previous); !strings.Contains(string(b), "panic: boom") {
		t.Fatalf("前回の記録 %q: %s", previous, b)
	}
}
