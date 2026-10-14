package log

import (
	"log/slog"
	"os"

	wlog "github.com/wenteasy/log"
)

// Init はログの出口を組み、slog.Default() をそこへ向ける。**アプリの main の最初に 1 回だけ呼ぶ**。
// aibow をライブラリとして使う人は呼ばなくてよい(自分の slog.SetDefault の出口に出る)。
//
// 今の出口は標準エラー出力だけ(wenteasy/log の SimpleHandler。1件1行: 2026-10-14T16:00:00+09:00 [INFO  ] メッセージ)。
// リリース版(-H windowsgui)には標準エラー出力の行き先が無いので見えない。ファイルに書く・パッケージごとのレベルは、
// ログの仕組みの次の段階でここに足す(TODO.md)。
func Init() {
	slog.SetDefault(slog.New(wlog.NewSimpleHandler(os.Stderr, LevelInfo)))
}
