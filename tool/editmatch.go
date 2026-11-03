package tool

// editmatch.go: edit_file の old_string が、行頭の空白(字下げ)だけ違うときに見つける。
//
// Qwen3.5 4B は、read_file の結果(「行番号 + タブ + 行」)から old_string を写すとき、
// 字下げのタブを1つ多く(ときには少なく)書くことが多かった(7 回のうち 4 回。tuning.md の「コーディング向けのツールと fetch_url を 4B で試す」)。
// エラーの文で「行番号の後ろのタブは含めません」と伝えても直らず、同じ失敗を繰り返すか、ファイルを丸ごと書き直した。
//
// そこで、そのままでは見つからないときだけ、行ごとに行頭の空白を除いて比べる。
// 緩めるのは「どの行も同じだけずれている」ときだけにする(タブが1つ多い、など)。
// 行ごとにずれ方が違うなら、モデルが字下げを書き換えたつもりかもしれず、どう合わせるか決められないので、今までどおり見つからない扱いにする。
// new_string も同じだけずらすので、モデルが書いたとおりの相対的な字下げ(1段深くする、など)は保たれる。

import "strings"

// loose は、字下げを合わせて見つけた場所と、new_string の直し方。
type loose struct {
	start, end int    // ファイルの中で置き換える範囲(バイト)
	extra      string // ずれの分の空白
	more       bool   // true ならモデルの字下げが extra だけ深い(new_string から削る)。false なら浅い(足す)
}

// looseMatch は、行頭の空白を除けば old と同じ行の並びがファイルの中に1か所だけあり、
// どの行のずれも同じなら、その場所を返す。old は行の頭から始まり、行の終わり(か改行)で終わるものだけを扱う。
func looseMatch(text, old string) (loose, bool) {
	trailNL := strings.HasSuffix(old, "\n")
	olds := strings.Split(strings.TrimSuffix(old, "\n"), "\n")
	lines := strings.SplitAfter(text, "\n")
	var found []loose
	offset := 0
	for s := range lines {
		if s+len(olds) <= len(lines) {
			if m, ok := matchAt(lines[s:s+len(olds)], olds, trailNL); ok {
				m.start += offset
				m.end += offset
				found = append(found, m)
			}
		}
		offset += len(lines[s])
	}
	if len(found) != 1 {
		return loose{}, false
	}
	return found[0], true
}

// matchAt は、ファイルの行 lines(改行つき)が old の行 olds と字下げだけ違うかを見る。
// 返す範囲は lines の頭からの位置。
func matchAt(lines, olds []string, trailNL bool) (loose, bool) {
	var m loose
	decided := false
	end := 0
	for i, o := range olds {
		f := strings.TrimSuffix(lines[i], "\n")
		fi, fb := splitIndent(f)
		oi, ob := splitIndent(o)
		if fb != ob {
			return loose{}, false
		}
		if i < len(olds)-1 || trailNL {
			if !strings.HasSuffix(lines[i], "\n") {
				return loose{}, false
			}
			end += len(lines[i])
		} else {
			end += len(f)
		}
		if strings.TrimSpace(fb) == "" { // 空の行は字下げを比べない
			continue
		}
		extra, more, ok := indentDiff(fi, oi)
		if !ok {
			return loose{}, false
		}
		if !decided {
			m.extra, m.more, decided = extra, more, true
		} else if extra != m.extra || more != m.more {
			return loose{}, false
		}
	}
	if !decided || m.extra == "" {
		return loose{}, false // 字下げが同じなら、違いは本文にある(そのままの比べ方で見つからなかった)
	}
	m.end = end
	return m, true
}

// splitIndent は行を、行頭の空白とそれ以外に分ける。
func splitIndent(s string) (indent, body string) {
	body = strings.TrimLeft(s, " \t")
	return s[:len(s)-len(body)], body
}

// indentDiff は、ファイルの字下げ fi とモデルの字下げ oi の違いを返す。
// 片方がもう片方の頭に空白を足した形のときだけ ok(タブとスペースを入れ替えた、などは扱わない)。
func indentDiff(fi, oi string) (extra string, more bool, ok bool) {
	switch {
	case strings.HasPrefix(oi, fi):
		return oi[len(fi):], true, true
	case strings.HasPrefix(fi, oi):
		return fi[len(oi):], false, true
	}
	return "", false, false
}

// reindent は new_string の各行を、old_string と同じだけずらす(空の行はそのまま)。
func (m loose) reindent(repl string) string {
	ls := strings.Split(repl, "\n")
	for i, l := range ls {
		indent, body := splitIndent(l)
		if strings.TrimSpace(body) == "" {
			continue
		}
		if m.more {
			// モデルの字下げは extra だけ深い。足りない行(モデルが浅く書いた行)はそのまま
			ls[i] = strings.TrimPrefix(indent, m.extra) + body
		} else {
			ls[i] = m.extra + l
		}
	}
	return strings.Join(ls, "\n")
}
