package log

import (
	stdlog "log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// SetLevel は、Init で組んだ出口のレベルを再起動せずに変える。標準の log の行も合わせて変わり、変えたことは Notice で残る(0052)
func TestSetLevel(t *testing.T) {
	keepDefault(t)
	dir := t.TempDir()
	c, err := Init(Options{Dir: dir, Root: LevelNotice})
	if err != nil {
		t.Fatal(err)
	}
	if l, ok := Level(); !ok || l != LevelNotice {
		t.Errorf("Level の初めは Root: %v %v", l, ok)
	}
	Debug("Debug は出ない(1)")

	if !SetLevel(LevelDebug, "") {
		t.Fatal("Init のあとは変えられる")
	}
	Debug("Debug が出る")
	stdlog.Printf("[WebView2] 標準の log の行も Debug で残る")

	// 絞るときは、変える前に Notice で書く(Warn にしたあとでは捨てられる)
	SetLevel(LevelWarn, "admin")
	Notice("Notice は出ない")
	Debug("Debug は出ない(2)")
	Warn("Warn は出る")
	c.Close()

	b, err := os.ReadFile(FilePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, want := range []string{
		"[NOTICE] ログのレベルを Notice から Debug に変えました",
		"[DEBUG ] Debug が出る",
		"[DEBUG ] [WebView2] 標準の log の行も Debug で残る",
		"[NOTICE] ログのレベルを Debug から Warn に変えました(変えた人: admin)",
		"[WARN  ] Warn は出る",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("%q が無い:\n%s", want, got)
		}
	}
	if strings.Contains(got, "出ない") {
		t.Errorf("出さないレベルの行がある:\n%s", got)
	}
}

// Init を呼んでいなければ(ライブラリとして使う人)、SetLevel は何もしない
func TestSetLevelWithoutInit(t *testing.T) {
	keepDefault(t)
	if _, ok := Level(); ok {
		t.Error("Init の前は Level が無い")
	}
	if SetLevel(LevelDebug, "") {
		t.Error("Init の前は変えない")
	}
}

// 名前とレベルの対応は行き来できる。知らない名前は false
func TestParseLevel(t *testing.T) {
	for _, n := range LevelNames {
		l, ok := ParseLevel(n)
		if !ok || LevelName(l) != n {
			t.Errorf("%s: %v %v %s", n, l, ok, LevelName(l))
		}
	}
	if l, ok := ParseLevel(" Trace "); !ok || l != LevelTrace {
		t.Errorf("大文字小文字と前後の空白は問わない: %v %v", l, ok)
	}
	if _, ok := ParseLevel("verbose"); ok {
		t.Error("知らない名前は false")
	}
}

// CurrentFile は、Init でファイルに書くと決まったときだけ、今日のログファイルの絶対パスを返す(0052 の決定事項 21)
func TestCurrentFile(t *testing.T) {
	keepDefault(t)
	if _, ok := CurrentFile(); ok {
		t.Error("Init の前はファイルが無い")
	}
	c, _ := Init(Options{Console: true})
	c.Close()
	if _, ok := CurrentFile(); ok {
		t.Error("Dir が空ならファイルが無い")
	}
	dir := t.TempDir()
	c, err := Init(Options{Dir: dir, Prefix: "aibow-dev"})
	if err != nil {
		t.Fatal(err)
	}
	Notice("書く")
	c.Close()
	p, ok := CurrentFile()
	if !ok || !filepath.IsAbs(p) || filepath.Dir(p) != dir || !strings.HasPrefix(filepath.Base(p), "aibow-dev_") {
		t.Errorf("今日のファイル: %q %v", p, ok)
	}
	if _, err := os.Stat(p); err != nil {
		t.Errorf("書いたファイルがある: %v", err)
	}
}
