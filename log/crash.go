package log

import (
	"errors"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
)

// CrashFile は異常終了(Go の panic・ランタイムの致命的なエラー)の内容の書き出し先。ログと同じフォルダに置く。
//
// リリース版の exe(-H windowsgui)では、panic の内容は標準エラー出力にしか出ず、どこにも残らない。
// 利用者からは「何も言わずに消えた」にしか見えないので、ファイルにも書かせ、次の起動で「前回は異常終了しました」と伝える。
// 形は shinte/ikkyoku の log の CatchCrash と同じ。
//
// 拾えるのは Go のランタイムを通る異常終了だけ。llama.cpp の C の部分の異常終了(GGML_ASSERT など)は拾えない。
// 画面から呼ばれたメソッドの中の panic は、Wails が recover して Logger(ログファイル)に Error で書くので、ここには来ない。
const CrashFile = FilePrefix + "_crash.log"

// CatchCrash は、以後の異常終了の内容を dir の CrashFile にも書かせる(標準エラー出力にも、これまでどおり出る)。
// 前回の記録が残っていれば aibow_crash_<日時>.log に改名して残し、その場所を返す(= 前回は異常終了した)。
// **Init のあとに 1 回だけ呼ぶ**(アプリの main。ライブラリからは呼ばない)。
//
// ⚠️ 書けなくても起動は止めないこと。エラーを返すだけ。
func CatchCrash(dir string) (previous string, err error) {
	if dir == "" {
		return "", errors.New("ログを書くフォルダが無いので、異常終了の記録を残せません")
	}
	previous = keepPreviousCrash(dir)
	f, err := os.Create(filepath.Join(dir, CrashFile))
	if err != nil {
		return previous, err
	}
	// SetCrashOutput はファイルの記述子を複製して持つので、ここで閉じてよい。
	defer f.Close()
	return previous, debug.SetCrashOutput(f, debug.CrashOptions{})
}

// keepPreviousCrash は中身のある CrashFile を、書かれた時刻の名前(aibow_crash_20261014-173000.log)に改名して返す。
// 空(= 前回は正常に終わった)・無い・改名できないときは空を返す。
//
// ⚠️ 改名すること(消さない・上書きしない)。次の CatchCrash が CrashFile を空で作り直すので、
// そのままにしておくと前回の記録が消える。改名した記録は、古いログを消すとき(removeOld)にも消えない
// (名前が aibow_<日付>.log の形ではないため)。
func keepPreviousCrash(dir string) string {
	path := filepath.Join(dir, CrashFile)
	st, err := os.Stat(path)
	if err != nil || st.Size() == 0 {
		return ""
	}
	name := strings.TrimSuffix(CrashFile, ".log") + "_" + st.ModTime().Format("20060102-150405") + ".log"
	dst := filepath.Join(dir, name)
	if err := os.Rename(path, dst); err != nil {
		return ""
	}
	return dst
}
