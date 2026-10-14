package log

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	wlog "github.com/wenteasy/log"
)

// Options は Init の設定。
type Options struct {
	// Dir はログファイルを書くフォルダ。空ならファイルには書かない。無ければ作る。
	// ファイルは日ごとに分かれる(Dir/aibow_20261014.log)
	Dir string
	// KeepDays は Dir に残す日数(今日を含む)。Init のときに、それより古いログファイルを消す。0 なら消さない
	KeepDays int
	// Root は出すレベル(これより下は出さない)。ゼロ値は Info。名前は ikkyoku の log に揃えた。
	// アプリは dev で Info(開発中・学習で観察するもの)、配る exe で Notice(常に残したいもの)を渡す
	Root slog.Level
	// Console は標準エラー出力にも出すか。wails3 dev や go run で動かすとき(ターミナルで見るとき)に立てる。
	// ⚠️ リリース版(-H windowsgui)には標準エラー出力の行き先が無いので、立てても見えない
	Console bool
}

// FilePrefix はログファイルの名前の頭(aibow_20261014.log)。
const FilePrefix = "aibow"

// Init はログの出口を組み、slog.Default() をそこへ向ける。**アプリの main の最初に 1 回だけ呼ぶ**。
// aibow をライブラリとして使う人は呼ばなくてよい(自分の slog.SetDefault の出口に出る)。
//
// 書式は wenteasy/log の SimpleHandler(1件1行: 2026-10-14T16:00:00+09:00 [INFO  ] メッセージ)。Options.Root 以上を出す。
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
			sinks = append(sinks, wlog.NewSimpleHandler(w, opts.Root))
			closer = w
			if opts.KeepDays > 0 {
				removeOld(opts.Dir, opts.KeepDays, time.Now())
			}
		} else {
			err = fmt.Errorf("ログのフォルダ %s を作れないので、ログはファイルに残りません: %w", opts.Dir, err)
		}
	}
	if opts.Console {
		sinks = append(sinks, wlog.NewSimpleHandler(os.Stderr, opts.Root))
	}
	if len(sinks) == 0 {
		// どこにも出せないときは標準エラー出力にする(slog.Default() のまま残すと、標準の log の書式で出る)
		sinks = append(sinks, wlog.NewSimpleHandler(os.Stderr, opts.Root))
	}
	slog.SetDefault(slog.New(slog.NewMultiHandler(sinks...)))
	// 標準の log で書かれたもの(Wails の中の WebView2 の [WebView2] … など)は、SetDefault のあと slog の出口に流れる。
	// 標準の log はレベルを持たないので、出すレベル(Root)で記録する(既定の Info のままだと、Root が Notice の exe で
	// WebView2 の失敗の記録まで捨ててしまう)
	slog.SetLogLoggerLevel(opts.Root)
	return closer, err
}

// FilePath は、今日のログファイルの場所(dir/aibow_20261014.log)。ログや画面に出すためのもの。
// ファイルは最初に書いたときに作られ、日付が変わると次のファイルに切り替わる。
func FilePath(dir string) string {
	return filepath.Join(dir, FilePrefix+"_"+time.Now().Format("20060102")+".log")
}

// removeOld は dir の古いログファイル(aibow_<日付>.log)を消し、今日を含めて keepDays 日分を残す。
//
// 日付はファイル名から読む(更新時刻は見ない。コピーや展開で変わるため)。
// 名前の形が違うファイル(利用者が置いたもの・ほかのアプリのもの)は消さない。消せなくても何もしない(ログのために止めない)。
func removeOld(dir string, keepDays int, now time.Time) {
	names, err := filepath.Glob(filepath.Join(dir, FilePrefix+"_*.log"))
	if err != nil {
		return
	}
	y, m, d := now.Date()
	oldest := time.Date(y, m, d-(keepDays-1), 0, 0, 0, 0, now.Location()) // 残す一番古い日
	for _, path := range names {
		date := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), FilePrefix+"_"), ".log")
		t, err := time.ParseInLocation("20060102", date, now.Location())
		if err != nil || !t.Before(oldest) {
			continue
		}
		os.Remove(path)
	}
}

type nopCloser struct{}

func (nopCloser) Close() error { return nil }
