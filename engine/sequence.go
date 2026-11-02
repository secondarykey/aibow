package engine

// sequence.go: 作業領域の中のシーケンス(KV キャッシュの列)と、生成ごとにどのシーケンスを使うかの選び方(0023)。
//
// llama.cpp は1つの作業領域(コンテキスト)の中に、番号(seq_id)の付いた複数の KV キャッシュの列を持てる。
// 作業領域の大きさはシーケンスの数で等分される(32768 を 2 本なら 1 本 16384。段1 で確かめた)。
//
//	作業領域 32768 ─ シーケンス 0(16384)─ 会話 A の KV キャッシュ
//	               └ シーケンス 1(16384)─ 会話 B の KV キャッシュ
//
// 会話ごとに自分の KV キャッシュが残るので、2人が交互に使っても読み直しが減る。
// どのシーケンスを使うかは、プロンプトの頭が一番長く「使える形で」一致するもので選ぶ(llama.cpp の server と同じ考え方。決定事項 7・14)。
// OpenAI 互換の口には会話の ID が無いので、どの道も同じ選び方にそろえている。

import (
	"time"

	"github.com/hybridgroup/yzma/pkg/llama"
)

// sequence は作業領域の中の1本の KV キャッシュの列。
type sequence struct {
	id llama.SeqId
	// cached は、今このシーケンスの KV キャッシュに入っているものの記録(文字のトークン列と画像の ID の列。chunks.go)。
	// 次のプロンプトが同じところから始まっていれば、そこは計算し直さずに済む。
	cached []chunk
	used   time.Time // 最後に使い終わった時刻(空きが無いとき、一番古いものを捨てて使う)
	busy   bool      // 生成に使っている途中(Engine.seqMu で守る。シーケンスが2本以上のときだけ使う)
}

// plan は、KV キャッシュの記録 cached に次のプロンプト next を読ませるとき、残す分(keep)と、
// 続きを読ませる位置(next の i 番目のチャンクの off トークン目から)を決める。KV キャッシュには触らない。
//
// 最後の1トークンは必ず読ませ直す(そうしないと「次の確率」が計算されない)。プロンプトの最後は返事の書き出しの文字なので、文字のチャンク。
func plan(cached, next []chunk) (keep []chunk, i, off int) {
	i, off = common(cached, next)
	if i == len(next) {
		i = len(next) - 1
		off = max(0, next[i].nTok-1)
		if next[i].image != "" {
			off = 0
		}
	}
	keep = append([]chunk(nil), next[:i]...)
	if off > 0 {
		keep = append(keep, textChunk(next[i].tokens[:off]))
	}
	for k := range keep {
		keep[k].src = 0 // 記録には mtmd のチャンクを持たない(生成が終わると解放するため)
	}
	return keep, i, off
}

// usable は、cached を next に使い回せるマスの数。
// 途中から巻き戻せないモデル(rollback が false。Qwen3.5 などハイブリッド・再帰の層を持つもの)は、
// 残す分が cached の全部のときだけ使える(途中から捨てようとすると、llama.cpp が断って全部を捨てることになる。段1)。
func usable(cached, next []chunk, rollback bool) int {
	keep, _, _ := plan(cached, next)
	if !rollback && positions(keep) < positions(cached) {
		return 0
	}
	return cells(keep)
}

// pickSeq は next を読ませるシーケンスの番号を選ぶ。空きが無ければ -1。
//
//  1. 使える一致が一番長いもの(同じなら、使い終わったのが古いほう)
//  2. 一致が無ければ、空(KV キャッシュに何も入っていない)のもの
//  3. それも無ければ、一番長く使われていないもの(中身を捨てて使う)
//
// 一致を「使える一致」で比べるのは、システムの発言とツールの説明が同じなだけの新しい会話が、
// 途中から巻き戻せないモデルで、ほかの会話のシーケンスを奪わないようにするため(決定事項 14)。
func pickSeq(seqs []*sequence, next []chunk, rollback bool) int {
	best, bestN := -1, 0
	empty, oldest := -1, -1
	for k, s := range seqs {
		if s.busy {
			continue
		}
		if n := usable(s.cached, next, rollback); n > bestN || (n == bestN && n > 0 && s.used.Before(seqs[best].used)) {
			best, bestN = k, n
		}
		if len(s.cached) == 0 && empty < 0 {
			empty = k
		}
		if oldest < 0 || s.used.Before(seqs[oldest].used) {
			oldest = k
		}
	}
	switch {
	case best >= 0:
		return best
	case empty >= 0:
		return empty
	default:
		return oldest
	}
}
