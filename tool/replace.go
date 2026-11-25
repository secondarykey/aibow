package tool

// replace.go: edit_file の当て方を、ファイルによらない関数として外に見せる(0045 の決定事項 4)。
//
// 「old を探して new に置き換える」は、ファイルの中身でもメモ帳のノードの本文でも同じ仕組みで済む。
// ほかのアプリが自分の「書き換えるツール」を作るとき、1か所に決まらなければ断る・字下げのずれを合わせる・CRLF を合わせる、を
// 作り直さずに使えるようにする。ファイルを読み書きするのは edit_file(write.go)の側で、ここは文字列だけを扱う。
//
// エラーの文は「ファイル」「read_file」に触れない言い方にする。edit_file はエラーの種類を見て、
// パスと read_file の案内を入れた自分の文を作る(モデルに見える文は 0045 の前と同じ)。

import (
	"errors"
	"fmt"
	"strings"
)

// Replace のエラー。errors.Is で見分ける。
var (
	// ErrNotFound は、old が text の中に無い(字下げを合わせても見つからない)。
	ErrNotFound = errors.New("置き換える文字列が見つかりません")
	// ErrAmbiguous は、old が text の中に2か所以上ある。何か所あったかは *AmbiguousError で取り出せる。
	ErrAmbiguous = errors.New("置き換える文字列が1か所に決まりません")
	// ErrEmptyOld は、old が空。
	ErrEmptyOld = errors.New("置き換える文字列が空です")
	// ErrSameText は、old と new が同じ(置き換えても何も変わらない)。
	ErrSameText = errors.New("置き換える前と後の文字列が同じです")
)

// AmbiguousError は、old が何か所あったか(Count)を持つエラー。errors.Is(err, ErrAmbiguous) も真になる。
// 数を文に入れたい側(edit_file の「N か所あります」)が、errors.As で取り出す
type AmbiguousError struct {
	Count int
}

func (e *AmbiguousError) Error() string {
	return fmt.Sprintf("置き換える文字列が %d か所あります。前後の行を含めて、1か所に決まるようにしてください", e.Count)
}

// Is は errors.Is(err, ErrAmbiguous) を真にする。
func (e *AmbiguousError) Is(target error) bool { return target == ErrAmbiguous }

// Replaced は Replace の結果。
type Replaced struct {
	Text       string // 置き換えた後の全体
	Line       int    // 置き換えた場所の行番号(1 始まり)
	Reindented bool   // old の字下げが text と違ったので、text の字下げに合わせて置き換えたか
}

// Replace は text の中の old を1か所だけ new に置き換える。
//
// old がちょうど1か所に決まらなければエラー(見つからない・2か所以上)。
// そのままでは見つからず、行頭の空白(字下げ)だけがどの行も同じだけ違うなら、text の字下げに合わせて置き換える(editmatch.go)。
// text の改行が CRLF で old が LF だけなら、old と new を CRLF に直してから探す。
func Replace(text, old, new string) (Replaced, error) {
	switch {
	case old == "":
		return Replaced{}, ErrEmptyOld
	case old == new:
		return Replaced{}, ErrSameText
	}
	// 改行が CRLF の中身(Windows で作ったファイルなど)。read_file は CR を見せないので、モデルが書く文字列の改行は LF になる。
	// 中身の書き方に合わせてから探す(合わせないと、複数行の old がいつまでも見つからない)
	if strings.Contains(text, "\r\n") && !strings.Contains(old, "\r\n") {
		old = strings.ReplaceAll(old, "\n", "\r\n")
		new = strings.ReplaceAll(new, "\n", "\r\n")
	}
	switch n := strings.Count(text, old); n {
	case 0:
		// 小さなモデルは字下げのタブを1つ多く(少なく)写しやすい(read_file の行番号の後ろのタブまで写す。tuning.md)。
		// 行頭の空白だけが違い、1か所に決まるなら、中身の字下げに合わせて置き換える。確認のカードの差分に実際の変更が出る
		if m, ok := looseMatch(text, old); ok {
			return Replaced{
				Text:       text[:m.start] + m.reindent(new) + text[m.end:],
				Line:       strings.Count(text[:m.start], "\n") + 1,
				Reindented: true,
			}, nil
		}
		return Replaced{}, ErrNotFound
	case 1:
	default:
		return Replaced{}, &AmbiguousError{Count: n}
	}
	i := strings.Index(text, old)
	return Replaced{
		Text: text[:i] + new + text[i+len(old):],
		Line: strings.Count(text[:i], "\n") + 1,
	}, nil
}
