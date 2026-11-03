package tool

// glob.go: ファイルの名前の形(glob)で合わせる(list_files の pattern と search_files の glob。0037 の決定事項 9)。
//
// 標準の path.Match は1つの名前(/ を含まない)の中の * ? [abc] しか扱えず、「どの深さでも」を表す ** が無い。
// そこで、パターンとパスを / で区切り、1段ずつ path.Match で合わせ、** の段だけは「0段以上の何でも」として自分で合わせる。
//
// 書き方は2通り:
//
//	/ を含まない(*.go・*_test.go)… どの深さでも、名前だけと比べる(「Go のファイル全部」を一番短く書けるように)
//	/ を含む(**/*.go・cmd/**/main.go)… 作業フォルダからの相対パスと、頭から比べる
//
// 中括弧({a,b})は作らない(決定事項 9)。要るときは2回に分けて呼べばよい。
//
// \ は path.Match のとおり「次の記号をそのまま合わせる」印として扱い、Windows の区切りとして / に直すことはしない。
// 小さなモデルは正規表現の癖で *\.go と書きがちで、これは path.Match ならそのまま main.go に合う。
// / に直すと */.go になって黙って 0 件になる。[id].tsx のような名前を \[id\].tsx で探すのも壊れる。
// 区切りは / で書くよう、ツールの説明で伝える(結果で言い直すことはしない。\ が区切りのつもりか印のつもりかは、結果からは分からないため)。

import (
	"fmt"
	"path"
	"strings"
)

// globPattern は、読み取った glob。
type globPattern struct {
	parts  []string // / で区切ったパターン(byName なら1つ)
	byName bool     // / を含まない(名前だけと比べる)
}

// compileGlob は、モデルが書いた glob を読み取る。書き方の間違い([ が閉じていない、など)はエラー。
func compileGlob(pattern string) (globPattern, error) {
	p := strings.TrimSpace(pattern)
	for strings.HasPrefix(p, "./") {
		p = p[2:]
	}
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return globPattern{}, fmt.Errorf("パターン %q が空です。*.go や **/*_test.go のように書いてください", pattern)
	}
	g := globPattern{parts: strings.Split(p, "/"), byName: !strings.Contains(p, "/")}
	for _, part := range g.parts {
		if part == "**" {
			continue
		}
		// 空の名前と比べて、書き方の間違いだけを確かめる(path.Match はパターン全体を読んでから答える)
		if _, err := path.Match(part, ""); err != nil {
			return globPattern{}, fmt.Errorf("パターン %q を読めません(使えるのは * ? [abc] と、フォルダをまたぐ ** です)", pattern)
		}
	}
	return g, nil
}

// match は、作業フォルダからの相対パス rel(/ 区切り)のファイルが、パターンに合うか。
func (g globPattern) match(rel string) bool {
	if g.byName {
		ok, _ := path.Match(g.parts[0], path.Base(rel))
		return ok
	}
	return matchParts(g.parts, strings.Split(rel, "/"))
}

// matchParts は、パターンの段 pat とパスの段 segs を頭から合わせる。** は0段以上のどの段とも合う。
func matchParts(pat, segs []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			rest := pat[1:]
			if len(rest) == 0 {
				return true // 最後の ** は、残りの段を全部のみこむ
			}
			// ** が何段をのみこむかを、0段から順に試す
			for i := 0; i <= len(segs); i++ {
				if matchParts(rest, segs[i:]) {
					return true
				}
			}
			return false
		}
		if len(segs) == 0 {
			return false
		}
		if ok, _ := path.Match(pat[0], segs[0]); !ok {
			return false
		}
		pat, segs = pat[1:], segs[1:]
	}
	return len(segs) == 0
}
