package log

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
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
