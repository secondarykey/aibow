package tool

// confirm.go: 実行する前にユーザーの確認が要るツール(ファイルを書き換えるツール。3-3 のコマンドも)。
//
// ツールの守り方は2種類ある。ファイルのツールは os.Root で「作業フォルダの外に出られない」ことを仕組みで守れるが、
// 作業フォルダの中で何を書き換えるかは縛れない。モデルへの入力(読んだファイルの中身も含む)に
// 「このファイルを消して書き直して」と書いてあれば、モデルはそれに従うかもしれない。
// そこで、何かを書き換えるツールは、実行する前にユーザーに中身を見せて確かめる。
//
// 確認を待つ・答えを受け取るのは会話の仕組み(ルートの agent.go)の仕事。ここは「何を見せるか」を作るだけ。

import (
	"strings"

	"github.com/pkg/diff"
)

// Confirmable は、実行する前にユーザーの確認が要るツール。
type Confirmable interface {
	Tool
	// Preview は、確認の画面に出す内容を作る(実行はしない)。
	// エラーなら確認せずに、そのエラーをモデルに返す(置き換える場所が見つからない、など。ユーザーに聞くまでもない)
	Preview(args Args) (Preview, error)
}

// Preview は確認の画面に出す内容。
type Preview struct {
	Path string `json:"path"` // 対象(作業フォルダからの相対パス)。コマンドでは空
	Note string `json:"note"` // 何をするか(「上書き」「新しく作る」「書き換え」「コマンド」)
	Diff string `json:"diff"` // 変更の差分(unified diff。画面は ```diff のコードブロックで出す)。大きすぎるときは空

	Command string `json:"command,omitempty"` // 実行するコマンド(run_command)
}

// diffMaxLines は差分を作るファイルの大きさの上限(前後の行数の合計)。
// 差分の計算(Myers の方法)は、変わった行が多いほど時間がかかる。丸ごと書き直した大きなファイルでは数秒かかることがあるので、
// 上限を超えたら差分を作らない(画面には「大きいため差分は出していません」と出す)
const diffMaxLines = 5000

// unifiedDiff は before → after の差分を、git diff と同じ unified 形式で返す(前後 3 行の文脈と、行番号の @@ 付き)。
//
// 差分の計算は github.com/pkg/diff(Myers の方法)に任せている。
// diff.Text は、文字列の代わりに nil を渡すとファイル名からファイルを読むので、必ず文字列を渡す
// (作業フォルダの外のファイルを読ませないため)。
func unifiedDiff(path, before, after string) string {
	if strings.Count(before, "\n")+strings.Count(after, "\n") > diffMaxLines {
		return ""
	}
	var b strings.Builder
	from := "a/" + path
	if before == "" {
		from = "/dev/null" // 新しく作るファイル(git diff と同じ書き方)
	}
	if err := diff.Text(from, "b/"+path, before, after, &b); err != nil {
		return ""
	}
	return b.String()
}
