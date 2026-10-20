package main

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/secondarykey/aibow/log"
)

// logPrefix はログファイルの名前の頭(aibow-server_<日付>.log・aibow-server_crash.log)。
// アプリ(aibow_<日付>.log)・aibow-mcp と同じフォルダに置くので、API のものだと分かる別の名前にする
const logPrefix = "aibow-server"

// logKeepDays は、ログファイルを残す日数(今日を含む)。起動のたびに、それより古いものを消す(アプリと同じ)
const logKeepDays = 10

// startLog はログの出口を組む(aibow/log の Init)。形は aibow-mcp(_cmd/aibow-mcp の startLog)にならう。
//
//   - 標準エラー出力:いつも出す。ターミナルで起動して、リクエストごとの行を見ながら試すため
//   - ファイル:アプリと同じフォルダ(%APPDATA%\aibow\logs。AIBOW_LOG_DIR で変えられる)の aibow-server_<日付>.log。
//     ターミナルは流れて消えるので、あとから比べられるようにファイルにも残す
//
// レベルは logLevel で決める(2026-10-21、ユーザーの指定):
//
//	設定ファイルの logLevel > -debug(Debug)> 既定(go run は Info、exe は Notice)
//
// go run は試しながら動かすときなので、リクエストごとの記録(受け付けた・生成の記録。Info)も出す。
// exe でふだん動かすときは、起動・モデルの読み込み・注意とエラー(Notice 以上)だけにする(アプリの exe と同じ)。
// 設定ファイルが -debug より強いのはユーザーの指定(ほかの設定の「引数 > 設定ファイル」とは逆)。
//
// 異常終了(Go の panic など)は同じフォルダの aibow-server_crash.log に残す(log.CatchCrash)。前回の記録が見つかったら Warn で書く。
// ログが書けなくても API は止めない(ログに Warn で書いて続ける)。
func startLog(level slog.Level) io.Closer {
	dir := os.Getenv("AIBOW_LOG_DIR")
	if dir == "" {
		if d, err := os.UserConfigDir(); err == nil {
			dir = filepath.Join(d, "aibow", "logs")
		}
	}
	c, err := log.Init(log.Options{
		Dir:      dir,
		KeepDays: logKeepDays,
		Root:     level,
		Console:  true,
		Prefix:   logPrefix,
	})
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

// logLevel は出すレベル。設定ファイルの logLevel > -debug > 既定(go run は Info、exe は Notice)。
func logLevel(c config, debug bool) slog.Level {
	if l, ok := logLevels[c.LogLevel]; ok && c.LogLevel != "" {
		return l
	}
	if debug {
		return log.LevelDebug
	}
	if isGoRun() {
		return log.LevelInfo
	}
	return log.LevelNotice
}

// logLevels は設定ファイルの logLevel に書ける値。
var logLevels = map[string]slog.Level{
	"debug":  log.LevelDebug,
	"info":   log.LevelInfo,
	"notice": log.LevelNotice,
	"warn":   log.LevelWarn,
	"error":  log.LevelError,
}
