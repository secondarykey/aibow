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
	// ファイルは日ごとに分かれる(Dir/aibow_20261014.log)
	Dir string
	// KeepDays は Dir に残す日数(今日を含む)。それより古いログファイルを消す。0 なら消さない。
	// 消すのは wenteasy/log の RollingFileWriter(SetKeep)で、ファイルを開いたとき(最初に書いたとき・日付が変わって切り替えたとき)に
	// 別の goroutine で動く。何日も動かし続ける aibow-server でも、日が変わるたびに片付く
	KeepDays int
	// Root は出すレベル(これより下は出さない)。ゼロ値は Info。名前は ikkyoku の log に揃えた。
	// アプリは dev で Info(開発中・学習で観察するもの)、配る exe で Notice(常に残したいもの)を渡す
	Root slog.Level
	// Console は標準エラー出力にも出すか。wails3 dev や go run で動かすとき(ターミナルで見るとき)に立てる。
	// ⚠️ リリース版(-H windowsgui)には標準エラー出力の行き先が無いので、立てても見えない
	Console bool
	// Prefix はログファイルの名前の頭(Prefix_20261014.log・Prefix_crash.log)。空なら FilePrefix(aibow)。
	// 同じフォルダに書くほかのプログラム(MCP のサーバー aibow-mcp)は、別の頭にしてアプリのログと混ぜない
	// (同じファイルに2つのプロセスが書くと、行が入り混じって読みにくい。古いものを消すときも、頭の違うファイルには触らない)
	Prefix string
}

// FilePrefix はログファイルの名前の頭の既定(aibow_20261014.log)。
const FilePrefix = "aibow"

// prefix は Init で決めたログファイルの名前の頭(FilePath・CatchCrash が使う)。Init はアプリの main で 1 回だけ呼ぶので、そこで決めて変えない
var prefix = FilePrefix

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
	prefix = FilePrefix
	if opts.Prefix != "" {
		prefix = opts.Prefix
	}
	if opts.Dir != "" {
		var w *wlog.RollingFileWriter
		if w, err = wlog.NewRollingFileWriter(opts.Dir, wlog.Day); err == nil {
			w.SetPrefix(prefix)
			// 片付けるのは <prefix>_<日付>.log(と .log.gz)にぴったり合うものだけ。異常終了の記録(<prefix>_crash.log・
			// <prefix>_crash_<日時>.log)や頭の違うファイル(aibow-dev_…・aibow-mcp_…)には触らない
			w.SetKeep(opts.KeepDays)
			// 消せなかったことは残す(ログのために止めはしない)。wenteasy/log は w のロックを持たずに呼ぶので、ここから同じ w へ書いてよい
			w.SetErrorHandler(func(err error) {
				Warnf("古いログファイルを片付けられませんでした(%s): %v", opts.Dir, err)
			})
			sinks = append(sinks, wlog.NewSimpleHandler(w, opts.Root))
			closer = w
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

// FilePath は、今日のログファイルの場所(dir/aibow_20261014.log。頭は Init の Options.Prefix)。ログや画面に出すためのもの。
// ファイルは最初に書いたときに作られ、日付が変わると次のファイルに切り替わる。
func FilePath(dir string) string {
	return filepath.Join(dir, prefix+"_"+time.Now().Format("20060102")+".log")
}

type nopCloser struct{}

func (nopCloser) Close() error { return nil }
