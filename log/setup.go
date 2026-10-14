package log

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	wlog "github.com/wenteasy/log"
)

// Options は Init の設定。
type Options struct {
	// Dir はログファイルを書くフォルダ。空ならファイルには書かない。無ければ作る。
	// ファイルは日ごとに分かれる(Dir/aibow_20261014.log)。前の日のファイルは消さない
	Dir string
	// Console は標準エラー出力にも出すか。wails3 dev や go run で動かすとき(ターミナルで見るとき)に立てる。
	// ⚠️ リリース版(-H windowsgui)には標準エラー出力の行き先が無いので、立てても見えない
	Console bool
}

// FilePrefix はログファイルの名前の頭(aibow_20261014.log)。
const FilePrefix = "aibow"

// Init はログの出口を組み、slog.Default() をそこへ向ける。**アプリの main の最初に 1 回だけ呼ぶ**。
// aibow をライブラリとして使う人は呼ばなくてよい(自分の slog.SetDefault の出口に出る)。
//
// 書式は wenteasy/log の SimpleHandler(1件1行: 2026-10-14T16:00:00+09:00 [INFO  ] メッセージ)。Info 以上を出す。
//
// ⚠️ ログが書けなくてもアプリは動かす。ファイルを作れないときも、出せる出口(コンソール)だけで組み、理由をエラーで返す。
// 返す io.Closer は必ず nil ではない(終わるときに Close する。ファイルを閉じ、閉じたことの区切りの行を書く)。
func Init(opts Options) (io.Closer, error) {
	var sinks []slog.Handler
	var closer io.Closer = nopCloser{}
	var err error
	if opts.Dir != "" {
		var w *wlog.RollingFileWriter
		if w, err = wlog.NewRollingFileWriter(opts.Dir, wlog.Day); err == nil {
			w.SetPrefix(FilePrefix)
			sinks = append(sinks, wlog.NewSimpleHandler(w, LevelInfo))
			closer = w
		} else {
			err = fmt.Errorf("ログのフォルダ %s を作れないので、ログはファイルに残りません: %w", opts.Dir, err)
		}
	}
	if opts.Console {
		sinks = append(sinks, wlog.NewSimpleHandler(os.Stderr, LevelInfo))
	}
	if len(sinks) == 0 {
		// どこにも出せないときは標準エラー出力にする(slog.Default() のまま残すと、標準の log の書式で出る)
		sinks = append(sinks, wlog.NewSimpleHandler(os.Stderr, LevelInfo))
	}
	slog.SetDefault(slog.New(slog.NewMultiHandler(sinks...)))
	return closer, err
}

// FilePath は、今日のログファイルの場所(dir/aibow_20261014.log)。ログや画面に出すためのもの。
// ファイルは最初に書いたときに作られ、日付が変わると次のファイルに切り替わる。
func FilePath(dir string) string {
	return filepath.Join(dir, FilePrefix+"_"+time.Now().Format("20060102")+".log")
}

type nopCloser struct{}

func (nopCloser) Close() error { return nil }
