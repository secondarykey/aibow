package main

// dialog.go: Wails のファイルダイアログの結果を、OS によらず同じ形にそろえる。
//
// Wails v3.0.0 では、ダイアログを取り消したときの戻り値が OS で違う。
//   - macOS・Linux: ("", nil)
//   - Windows:      ("", エラー)。エラーの文言は "cancelled by user"
// 取り消しは失敗ではないので、Windows を macOS・Linux に合わせて ("", nil) にする。
// そろえないと、取り消しただけで画面に「ダイアログを出せません: cancelled by user」が出る。
// 画面の側は「空なら何もしない」だけにしてあるので、ダイアログの結果は必ず dialogPath を通す。

// dialogCancelledMsg は、Windows でダイアログを取り消したときのエラーの文言
// (Wails の internal/go-common-file-dialog/cfd の ErrorCancelled)。
// internal パッケージなので import できず、errors.Is で見分けられない。文言で比べるしかない。
// Wails を上げたら、Windows で1回取り消して確かめる(文言が変わると、そろえる処理が効かなくなる)。
const dialogCancelledMsg = "cancelled by user"

// isDialogCancelled は、ダイアログの取り消しで返ってきたエラーかを返す。
func isDialogCancelled(err error) bool {
	return err != nil && err.Error() == dialogCancelledMsg
}

// dialogPath は、1つを選ぶダイアログの結果を「取り消し = 空文字・nil」にそろえる。ほかのエラーはそのまま返す。
func dialogPath(path string, err error) (string, error) {
	if isDialogCancelled(err) {
		return "", nil
	}
	return path, err
}
