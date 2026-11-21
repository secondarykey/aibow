package log

import (
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
)

// 起動したあとにレベルを変える(0052)。
//
// ここで学べること:slog の Handler はレベルを決まった値ではなく slog.Leveler で受け取れる。
// slog.LevelVar(あとから値を変えられるレベル)を渡しておけば、出口(ファイル・標準エラー出力)を組み直さずに、
// 値を1つ変えるだけで全部の出口のレベルが変わる。wenteasy/log の SimpleHandler も slog.Leveler を受け取る。
//
// 変えたレベルは保存しない。再起動すると、起動したときのレベル(Options.Root)に戻る(0052 の決定事項 2)。

// levelVar は Init で組んだ出口のレベル。nil なら Init を呼んでいない(aibow がライブラリとして使われている)。
// 画面から呼ぶメソッドと送信中の goroutine の両方から触るので、差し替えは atomic にする(LevelVar の中身もそれ自体 atomic)
var levelVar atomic.Pointer[slog.LevelVar]

// LevelNames は、画面・aibow-server の server.json の logLevel・aibow-mcp の -log-level で使えるレベルの名前(細かい順)。
// 名前とレベルの対応はここだけに置く(0052 の決定事項 13)
var LevelNames = []string{"trace", "debug", "info", "notice", "warn", "error"}

var levelsByName = map[string]slog.Level{
	"trace":  LevelTrace,
	"debug":  LevelDebug,
	"info":   LevelInfo,
	"notice": LevelNotice,
	"warn":   LevelWarn,
	"error":  LevelError,
}

// ParseLevel は LevelNames の名前をレベルにする。大文字小文字は問わない。知らない名前は false
// (黙って Info に倒すと、書き間違えたことに気づけないため。呼ぶ側が書ける値を添えて断る)。
func ParseLevel(name string) (slog.Level, bool) {
	l, ok := levelsByName[strings.ToLower(strings.TrimSpace(name))]
	return l, ok
}

// LevelName はレベルの名前(LevelNames のどれか)を返す。段階の間の値は、それより下で一番近い段階の名前になる。
func LevelName(l slog.Level) string {
	switch {
	case l < LevelDebug:
		return "trace"
	case l < LevelInfo:
		return "debug"
	case l < LevelNotice:
		return "info"
	case l < LevelWarn:
		return "notice"
	case l < LevelError:
		return "warn"
	default:
		return "error"
	}
}

// displayName はログの文に書く名前(Notice のように頭だけ大文字。ログの行の [NOTICE] と同じものだと分かるように)。
func displayName(l slog.Level) string {
	n := LevelName(l)
	return strings.ToUpper(n[:1]) + n[1:]
}

// Level は、Init で組んだ出口の今のレベルを返す。Init を呼んでいなければ false(レベルは使う人の出し先が決める)。
func Level() (slog.Level, bool) {
	lv := levelVar.Load()
	if lv == nil {
		return 0, false
	}
	return lv.Level(), true
}

// SetLevel は、Init で組んだ出口のレベルを変える。Init を呼んでいなければ何もせず false を返す
// (ライブラリとして使う人の出し先のレベルは、その人が決める。0052 の決定事項 11)。
//
// 標準の log から来る行(Wails の中の WebView2 の行など)のレベルも同じ値に合わせる。Init と同じく、
// 出すレベルで記録しないと、レベルを上げたときにその行を捨ててしまう。
//
// 変えたことは Notice で残す(利用者が変えた設定は Notice。0052 の決定事項 12)。by は誰が変えたか(aibow-server の管理者など。空なら書かない)。
// 書くのは出るほうのレベルのとき:細かくするときは変えたあと、絞るときは変える前(Warn にしてから Notice で書くと捨てられる)。
// どちらも Notice より上(Warn → Error など)なら残らない。
func SetLevel(l slog.Level, by string) bool {
	lv := levelVar.Load()
	if lv == nil {
		return false
	}
	old := lv.Level()
	if old == l {
		return true
	}
	msg := fmt.Sprintf("ログのレベルを %s から %s に変えました", displayName(old), displayName(l))
	if by != "" {
		msg += "(変えた人: " + by + ")"
	}
	// ⚠️ output は直接呼ぶ(Notice を挟まない)。記録される位置を SetLevel を呼んだ側にするため(log.go の callerSkip)
	if l > old {
		output(LevelNotice, msg, "", nil, nil)
	}
	lv.Set(l)
	slog.SetLogLoggerLevel(l)
	if l < old {
		output(LevelNotice, msg, "", nil, nil)
	}
	return true
}
