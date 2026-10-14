// Package log は aibow のログ。**aibow のコードはここの関数でログを出す**(標準の log や slog を直接使わない)。
//
//	log.Info("保存しました", "path", p)          // slog と同じ「メッセージ + キーと値」
//	log.Infof("%d 件を読みました", n)           // printf の形
//
// 土台は github.com/wenteasy/log(レベルの段階と、Init で組む出口の部品)。
// ただし aibow を使う人には wenteasy/log の型を見せない。レベルは slog.Level、出し先は *slog.Logger のまま:
//
//   - アプリ(_cmd/aibow など)は main の最初に Init を呼び、slog.Default() をここの出口に向ける
//   - aibow をライブラリとして使う人は Init を呼ばなくてよい。ログは slog.Default() に出るので、
//     自分の slog.SetDefault の出口に集まる。aibow のログだけ別にしたいときは SetLogger で *slog.Logger を渡す
//
// ここで学べること:ライブラリはログの書式・出し先を決めつけず、*slog.Logger を受け取れるようにしておく。
// そうしておけば、使う側は標準の slog だけで、自分のログと aibow のログをまとめて扱える。
package log

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"sync/atomic"
	"time"

	wlog "github.com/wenteasy/log"
)

// slog の段階(Debug / Info / Warn / Error)に Trace と Notice を足したもの。
// 値は wenteasy/log と同じ(型は slog.Level なので、使う側は wenteasy/log を知らなくてよい)。
const (
	LevelTrace  = wlog.LevelTrace
	LevelDebug  = wlog.LevelDebug
	LevelInfo   = wlog.LevelInfo
	LevelNotice = wlog.LevelNotice
	LevelWarn   = wlog.LevelWarn
	LevelError  = wlog.LevelError
)

// logger は SetLogger で決めた出し先。nil なら slog.Default()。
// 送信中の goroutine からも読むので、差し替えは atomic にする。
var logger atomic.Pointer[slog.Logger]

// SetLogger は aibow のログの出し先を決める。nil を渡すと slog.Default() に戻る。
// aibow をライブラリとして使う人が、aibow のログだけを別の出し先・別のレベルにしたいときに使う。
func SetLogger(l *slog.Logger) {
	logger.Store(l)
}

// Logger は今の出し先を返す(SetLogger で決めていなければ slog.Default())。
// SDK など、*slog.Logger を受け取るものに渡すときに使う。
func Logger() *slog.Logger {
	if l := logger.Load(); l != nil {
		return l
	}
	return slog.Default()
}

// Enabled は今の出し先がそのレベルを出すかを返す。引数を組み立てるのが重いときに、先に確かめる。
func Enabled(level slog.Level) bool {
	return Logger().Enabled(context.Background(), level)
}

func Trace(msg string, args ...any)  { output(LevelTrace, msg, "", nil, args) }
func Debug(msg string, args ...any)  { output(LevelDebug, msg, "", nil, args) }
func Info(msg string, args ...any)   { output(LevelInfo, msg, "", nil, args) }
func Notice(msg string, args ...any) { output(LevelNotice, msg, "", nil, args) }
func Warn(msg string, args ...any)   { output(LevelWarn, msg, "", nil, args) }
func Error(msg string, args ...any)  { output(LevelError, msg, "", nil, args) }

func Tracef(format string, args ...any)  { output(LevelTrace, "", format, args, nil) }
func Debugf(format string, args ...any)  { output(LevelDebug, "", format, args, nil) }
func Infof(format string, args ...any)   { output(LevelInfo, "", format, args, nil) }
func Noticef(format string, args ...any) { output(LevelNotice, "", format, args, nil) }
func Warnf(format string, args ...any)   { output(LevelWarn, "", format, args, nil) }
func Errorf(format string, args ...any)  { output(LevelError, "", format, args, nil) }

// callerSkip は、記録する位置(ログを出した場所)までの段数(runtime.Callers → output → 公開関数 → 呼び出し元)。
//
// ⚠️ output は公開関数から**直接**呼ぶこと。間に関数を挟むと、記録される位置が全部このパッケージになる
// (slog.HandlerOptions.AddSource や、wenteasy/log の PackageLevelHandler が見る位置)。
// wenteasy/log の Info などを包まないのも同じ理由(あちらも段数で数えているので、包むと位置がここになる。
// それに、あちらは slog.Default() に固定で書くので SetLogger が効かなくなる)。
const callerSkip = 3

func output(level slog.Level, msg, format string, fargs, attrs []any) {
	l := Logger()
	ctx := context.Background()
	if !l.Enabled(ctx, level) {
		return // 出さないレベルなら、書式を組み立てる手間もかけない
	}
	if format != "" {
		msg = fmt.Sprintf(format, fargs...)
	}
	var pcs [1]uintptr
	runtime.Callers(callerSkip, pcs[:])
	r := slog.NewRecord(time.Now(), level, msg, pcs[0])
	r.Add(attrs...)
	_ = l.Handler().Handle(ctx, r)
}
