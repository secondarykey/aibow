// Package log は、aibow のライブラリ(ルートの aibow・tool・store・mcp/client など)がログを出す口。
//
// aibow のパッケージの中では、ログはすべてこのパッケージから出す(標準の log や slog を直接使わない)。
// 出し先は *slog.Logger で、ライブラリを使う側が SetLogger で決める:
//
//	log.SetLogger(slog.New(slog.NewJSONHandler(os.Stderr, nil))) // aibow のログだけを別の出し先へ
//
// 設定しなければ、書くたびに slog.Default() へ出す。アプリが slog.SetDefault で出口を決めれば、
// aibow のログもアプリのログと同じところに集まる(画面のアプリ _cmd/aibow は github.com/wenteasy/log のハンドラを使う)。
//
// ここで学べること:ライブラリはログの設定(書式・出し先・レベル)を持たず、*slog.Logger を受け取るだけにする。
// そうしておけば、使う側は標準の slog だけで、自分のログとライブラリのログをまとめて扱える。
// なので、このパッケージは wenteasy/log を import しない(あちらはアプリが使うもの)。
//
// Info などは slog と同じ「メッセージ + キーと値」の形、Infof などが printf の形。
package log

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"sync/atomic"
	"time"
)

// logger は SetLogger で決めた出し先。nil なら slog.Default()。
// 送信中の goroutine からも読むので、差し替えは atomic にする。
var logger atomic.Pointer[slog.Logger]

// SetLogger は aibow のログの出し先を決める。nil を渡すと slog.Default() に戻る。
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

func Debug(msg string, args ...any) { output(slog.LevelDebug, msg, args) }
func Info(msg string, args ...any)  { output(slog.LevelInfo, msg, args) }
func Warn(msg string, args ...any)  { output(slog.LevelWarn, msg, args) }
func Error(msg string, args ...any) { output(slog.LevelError, msg, args) }

func Debugf(format string, args ...any) { outputf(slog.LevelDebug, format, args) }
func Infof(format string, args ...any)  { outputf(slog.LevelInfo, format, args) }
func Warnf(format string, args ...any)  { outputf(slog.LevelWarn, format, args) }
func Errorf(format string, args ...any) { outputf(slog.LevelError, format, args) }

// callerSkip は、記録する位置(ログを出した場所)までの段数(runtime.Callers → output → 公開関数 → 呼び出し元)。
//
// slog.Logger.Info などを関数で包むと、記録される位置が全部この log.go になってしまう
// (slog.HandlerOptions.AddSource や、wenteasy/log の PackageLevelHandler が見る位置)。
// そこで呼び出し元を自分で取り、Record を作ってハンドラに渡す。
// output / outputf は公開関数から**直接**呼ぶこと。間に関数を挟むと段数がずれる。
const callerSkip = 3

func output(level slog.Level, msg string, args []any) {
	l := Logger()
	ctx := context.Background()
	if !l.Enabled(ctx, level) {
		return
	}
	var pcs [1]uintptr
	runtime.Callers(callerSkip, pcs[:])
	r := slog.NewRecord(time.Now(), level, msg, pcs[0])
	r.Add(args...)
	_ = l.Handler().Handle(ctx, r)
}

func outputf(level slog.Level, format string, args []any) {
	l := Logger()
	ctx := context.Background()
	if !l.Enabled(ctx, level) {
		return // 出さないレベルなら、書式を組み立てる手間もかけない
	}
	var pcs [1]uintptr
	runtime.Callers(callerSkip, pcs[:])
	r := slog.NewRecord(time.Now(), level, fmt.Sprintf(format, args...), pcs[0])
	_ = l.Handler().Handle(ctx, r)
}
