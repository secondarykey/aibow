package log

import (
	"bytes"
	"compress/gzip"
	"io"
	stdlog "log"
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
	t.Cleanup(func() {
		slog.SetDefault(old)
		slog.SetLogLoggerLevel(slog.LevelInfo)
		prefix = FilePrefix // Init(Prefix)で変えたファイルの名前の頭を戻す
	})
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

// Init(KeepDays)は、今日を含めて KeepDays 日分を残し、それより古い aibow_<日付>.log だけを消す。
// 消すのは wenteasy/log の RollingFileWriter(SetKeep)で、最初に書いたときに別の goroutine で動くので、消えるまで待つ
// (待つ仕組みは wenteasy/log のテストの中にしか無い)。名前の形の細かい決まりは wenteasy/log のテストが確かめている。
// ここでは aibow が渡す日数の境目と、aibow のほかのファイルに触らないことを確かめる
func TestInitKeepDays(t *testing.T) {
	keepDefault(t)
	dir := t.TempDir()
	day := func(n int) string { return time.Now().AddDate(0, 0, -n).Format("20060102") }
	names := map[string]bool{ // ファイル名 → 残るか
		"aibow_" + day(0) + ".log":  true,  // 今日(これから書く)
		"aibow_" + day(9) + ".log":  true,  // 9 日前まで(今日を含めて 10 日分)
		"aibow_" + day(10) + ".log": false, // 10 日前
		"aibow_20000101.log":        false,
		"aibow_abc.log":             true, // 日付でない
		"aibow-dev_20000101.log":    true, // 頭が違う(wails3 dev のログ)
		"other_20000101.log":        true, // aibow のものではない
		"aibow_20000101.txt":        true,
	}
	var gone []string
	for name, keep := range names {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if !keep {
			gone = append(gone, path)
		}
	}
	c, err := Init(Options{Dir: dir, KeepDays: 10})
	if err != nil {
		t.Fatal(err)
	}
	Info("書くと片付けが始まる")
	c.Close()

	waitRemoved(t, gone...)
	for name, keep := range names {
		if !keep {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s が消えた: %v", name, err)
		}
	}
}

// waitRemoved は、paths が消えるまで待つ(片付けは別の goroutine で動くため)。期限を過ぎたら失敗にする
func waitRemoved(t *testing.T, paths ...string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for _, p := range paths {
		for {
			_, err := os.Stat(p)
			if os.IsNotExist(err) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("古いファイルが消えない: %s(%v)", p, err)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

// Init(CompressDays)は、今日を含めて CompressDays 日分を .log のまま残し、それより古いものを .log.gz にする。
// KeepDays より古いものは消す(圧縮しない)。圧縮は「.gz を作ってから元の .log を消す」順なので、元が消えるのを待てば .gz はできている
func TestInitCompressDays(t *testing.T) {
	keepDefault(t)
	dir := t.TempDir()
	day := func(n int) string {
		return filepath.Join(dir, "aibow_"+time.Now().AddDate(0, 0, -n).Format("20060102")+".log")
	}
	yesterday, older, oldest := day(1), day(2), day(60)
	for _, p := range []string{yesterday, older, oldest} {
		if err := os.WriteFile(p, []byte("記録 "+filepath.Base(p)+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	c, err := Init(Options{Dir: dir, KeepDays: 60, CompressDays: 2})
	if err != nil {
		t.Fatal(err)
	}
	Info("書くと片付けが始まる")
	c.Close()

	waitRemoved(t, older, oldest)
	if _, err := os.Stat(yesterday); err != nil {
		t.Errorf("昨日のログは .log のまま残す: %v", err)
	}
	if _, err := os.Stat(oldest + ".gz"); !os.IsNotExist(err) {
		t.Errorf("KeepDays より古いものは圧縮せずに消す: %v", err)
	}
	f, err := os.Open(older + ".gz")
	if err != nil {
		t.Fatalf("おとといのログが .log.gz になっていない: %v", err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(zr)
	if want := "記録 " + filepath.Base(older) + "\n"; err != nil || string(b) != want {
		t.Errorf("圧縮したものの中身 = %q, %v(want %q)", b, err, want)
	}
}

// Init(Prefix)は、別の頭のファイル(aibow-mcp_<日付>.log)に書き、古いものを消すときもアプリのファイルには触らない
// (同じフォルダに MCP のサーバー aibow-mcp のログを置くため)
func TestInitPrefix(t *testing.T) {
	keepDefault(t)
	dir := t.TempDir()
	app := filepath.Join(dir, FilePrefix+"_20000101.log") // アプリの古いログ(サーバーは消さない)
	mine := filepath.Join(dir, "aibow-mcp_20000101.log")  // サーバーの古いログ(消す)
	for _, p := range []string{app, mine} {
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	c, err := Init(Options{Dir: dir, KeepDays: 10, Prefix: "aibow-mcp"})
	if err != nil {
		t.Fatal(err)
	}
	Info("サーバーのログ")
	c.Close()

	if got := filepath.Base(FilePath(dir)); !strings.HasPrefix(got, "aibow-mcp_") {
		t.Errorf("FilePath = %s", got)
	}
	if got := CrashFile(); got != "aibow-mcp_crash.log" {
		t.Errorf("CrashFile = %s", got)
	}
	b, err := os.ReadFile(FilePath(dir))
	if err != nil || !strings.Contains(string(b), "サーバーのログ") {
		t.Errorf("ファイルに書かれていない: %q %v", b, err)
	}
	waitRemoved(t, mine) // サーバーの古いログは消える(片付けは別の goroutine なので待つ)
	if _, err := os.Stat(app); err != nil {
		t.Errorf("アプリのログを消した: %v", err)
	}
}

// Init(Root)は、そのレベルより下を出さない(exe は Notice)
func TestInitRoot(t *testing.T) {
	keepDefault(t)
	dir := t.TempDir()
	c, err := Init(Options{Dir: dir, Root: LevelNotice})
	if err != nil {
		t.Fatal(err)
	}
	Info("Info は出ない")
	Notice("Notice は出る")
	Warn("Warn も出る")
	stdlog.Printf("[WebView2] 標準の log は捨てない")
	c.Close()
	b, err := os.ReadFile(FilePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if strings.Contains(got, "出ない") || !strings.Contains(got, "[NOTICE] Notice は出る") || !strings.Contains(got, "[WARN  ] Warn も出る") ||
		!strings.Contains(got, "[NOTICE] [WebView2] 標準の log は捨てない") {
		t.Errorf("Notice 以上だけ:\n%s", got)
	}
}
