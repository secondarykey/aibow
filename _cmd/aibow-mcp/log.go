package main

import (
	"io"
	"os"
	"path/filepath"

	"github.com/secondarykey/aibow/log"
)

// logPrefix はログファイルの名前の頭(aibow-mcp_<日付>.log・aibow-mcp_crash.log)。
// アプリ(aibow_<日付>.log)と同じフォルダに置くので、サーバーのものだと分かる別の名前にする
const logPrefix = "aibow-mcp"

// logKeepDays は、ログファイルを残す日数(今日を含む)。起動のたびに、それより古いものを消す(アプリと同じ)
const logKeepDays = 10

// startLog はログの出口を組む(aibow/log の Init)。形はアプリ(_cmd/aibow の startLog)にならう。
//
//   - 標準エラー出力:いつも出す。クライアント(aibow・Claude Code など)が取り込む。標準出力は MCP のメッセージ専用なので、そこには書かない
//   - ファイル:アプリと同じフォルダ(%APPDATA%\aibow\logs。AIBOW_LOG_DIR で変えられる)の aibow-mcp_<日付>.log。
//     標準エラー出力はクライアントの中に取り込まれて見えにくく、aibow の exe も Info の行をファイルに残さないので、サーバー自身でも残す
//
// レベルは Info(ツールの実行も残す)。アプリの exe が Notice にしているのは生成の統計などで行数が多いためで、
// サーバーはツールを1回呼ぶごとに1行なので Info でも少ない。
//
// サーバーは作業フォルダごとに1つずつ起動されるので、同じ日のファイルに複数のプロセスが書く。
// ファイルは追記(O_APPEND)で開くので壊れないが、どのプロセスの行か分かるよう、各行にプロセスの番号(pid)を付ける。
//
// 異常終了(Go の panic など)は同じフォルダの aibow-mcp_crash.log に残す(log.CatchCrash)。サーバーには画面が無いので、
// 前回の記録が見つかったら、ログに Warn で書く。複数のプロセスが同じ記録のファイルを使うので、どのプロセスの異常終了かは記録の中身で見分ける。
//
// ログが書けなくてもサーバーは止めない(ログに Warn で書いて続ける)。
func startLog() io.Closer {
	dir := os.Getenv("AIBOW_LOG_DIR")
	if dir == "" {
		if d, err := os.UserConfigDir(); err == nil {
			dir = filepath.Join(d, "aibow", "logs")
		}
	}
	c, err := log.Init(log.Options{
		Dir:      dir,
		KeepDays: logKeepDays,
		Root:     log.LevelInfo,
		Console:  true,
		Prefix:   logPrefix,
	})
	log.SetLogger(log.Logger().With("pid", os.Getpid()))
	if dir == "" {
		log.Warn("ログを書くフォルダが決められないので、ログはファイルに残りません。AIBOW_LOG_DIR でフォルダを指定してください")
		return c
	}
	if err != nil {
		log.Warn(err.Error())
		return c
	}
	log.Noticef("ログの書き出し先: %s", log.FilePath(dir))

	previous, err := log.CatchCrash(dir)
	if previous != "" {
		log.Warnf("前回は異常終了しました。記録: %s", previous)
	}
	if err != nil {
		log.Warnf("異常終了の記録を残す準備ができません(起動は続けます): %v", err)
	}
	return c
}
