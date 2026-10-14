package log

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// use は、テストのあいだだけ出し先を buf の TextHandler にする(位置を出す)。
func use(t *testing.T, level slog.Level) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	SetLogger(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: level, AddSource: true})))
	t.Cleanup(func() { SetLogger(nil) })
	return buf
}

func TestOutput(t *testing.T) {
	buf := use(t, slog.LevelInfo)
	Info("保存しました", "n", 3)
	Warnf("%d 件を捨てました", 2)
	Debugf("出ない %d", 1)
	got := buf.String()
	for _, want := range []string{
		`level=INFO`, `msg=保存しました n=3`,
		`level=WARN`, `msg="2 件を捨てました"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("%q が無い:\n%s", want, got)
		}
	}
	if strings.Contains(got, "出ない") {
		t.Errorf("Info より下は出さない:\n%s", got)
	}
}

// 記録される位置は、このパッケージ(log.go)ではなく呼んだ側(この log_test.go)
func TestCallerIsTheCaller(t *testing.T) {
	buf := use(t, slog.LevelDebug)
	Info("a")
	Errorf("b %d", 1)
	for line := range strings.Lines(buf.String()) {
		if !strings.Contains(line, "log_test.go:") {
			t.Errorf("位置が呼んだ側になっていない: %s", line)
		}
	}
}

// 設定しなければ slog.Default() へ出す
func TestDefault(t *testing.T) {
	SetLogger(nil)
	buf := &bytes.Buffer{}
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	Infof("既定へ %s", "出す")
	if !strings.Contains(buf.String(), `msg="既定へ 出す"`) {
		t.Errorf("slog.Default() に出ていない: %q", buf.String())
	}
	if Logger() != slog.Default() {
		t.Error("Logger は slog.Default() を返す")
	}
}

// Init(Console)は slog.Default() を wenteasy/log の SimpleHandler(標準エラー出力)に向ける。
// 足した段階(Trace・Notice)は、slog の書き方(INFO+2 など)ではなく名前で出る。
func TestInit(t *testing.T) {
	r, w, perr := os.Pipe()
	if perr != nil {
		t.Fatal(perr)
	}
	oldStderr, oldDefault := os.Stderr, slog.Default()
	os.Stderr = w
	t.Cleanup(func() {
		os.Stderr = oldStderr
		slog.SetDefault(oldDefault)
	})
	SetLogger(nil)

	c, err := Init(Options{Console: true})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	Noticef("aibow から %s", "Notice")
	Trace("Info より下は出ない")
	slog.Warn("slog を直接呼んでも同じ出口")
	w.Close()
	b, _ := io.ReadAll(r)
	got := string(b)
	for _, want := range []string{"[NOTICE] aibow から Notice", "[WARN  ] slog を直接呼んでも同じ出口"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q が無い:\n%s", want, got)
		}
	}
	if strings.Contains(got, "出ない") {
		t.Errorf("Init の既定は Info 以上:\n%s", got)
	}
}

// keepDefault は、テストの終わりに slog.Default() と出し先を戻す。
func keepDefault(t *testing.T) {
	t.Helper()
	old := slog.Default()
	SetLogger(nil)
	t.Cleanup(func() { slog.SetDefault(old) })
}

// Init(Dir)は、Dir の aibow_<日付>.log に書く(リリース版の exe はここにしか残らない)
func TestInitWritesFile(t *testing.T) {
	keepDefault(t)
	dir := filepath.Join(t.TempDir(), "logs") // 無ければ作る
	c, err := Init(Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	Infof("ファイルへ %d", 1)
	slog.Info("slog を直接呼んでも同じファイル")
	Debug("Info より下は出ない")
	if err := c.Close(); err != nil { // 閉じないと Windows では一時フォルダを消せない
		t.Fatal(err)
	}

	b, err := os.ReadFile(FilePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, want := range []string{"[INFO  ] ファイルへ 1", "[INFO  ] slog を直接呼んでも同じファイル"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q が無い:\n%s", want, got)
		}
	}
	if strings.Contains(got, "出ない") {
		t.Errorf("Info 以上だけ:\n%s", got)
	}
}

// フォルダを作れなくても止めない。理由をエラーで返し、出せる出口で続ける(Close できるものを返す)
func TestInitBadDir(t *testing.T) {
	keepDefault(t)
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Init(Options{Dir: filepath.Join(file, "logs")})
	if err == nil || !strings.Contains(err.Error(), "ログはファイルに残りません") {
		t.Errorf("作れない理由を返す: %v", err)
	}
	if c == nil {
		t.Fatal("Close できるものを返す")
	}
	c.Close()
}

// removeOld は今日を含めて keepDays 日分を残し、それより古い aibow_<日付>.log だけを消す
func TestRemoveOld(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 14, 17, 0, 0, 0, time.Local)
	names := map[string]bool{ // ファイル名 → 残るか
		"aibow_20261014.log": true,  // 今日
		"aibow_20261005.log": true,  // 10 日前まで(今日を含めて 10 日分)
		"aibow_20261004.log": false, // 11 日前
		"aibow_20250101.log": false,
		"aibow_abc.log":      true, // 日付でない
		"other_20250101.log": true, // aibow のものではない
		"aibow_20250101.txt": true,
	}
	for name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	removeOld(dir, 10, now)
	for name, keep := range names {
		_, err := os.Stat(filepath.Join(dir, name))
		if got := err == nil; got != keep {
			t.Errorf("%s: 残る %v, want %v", name, got, keep)
		}
	}
}

// Init(KeepDays)は、書き始める前に古いファイルを消す
func TestInitKeepDays(t *testing.T) {
	keepDefault(t)
	dir := t.TempDir()
	old := filepath.Join(dir, FilePrefix+"_20000101.log")
	if err := os.WriteFile(old, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Init(Options{Dir: dir, KeepDays: 10})
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("古いファイルが残っている: %v", err)
	}
}
