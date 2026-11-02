package engine

import (
	"testing"
	"time"

	"github.com/hybridgroup/yzma/pkg/llama"
)

// toks は 1..n のようなトークン列を作る(テスト用)。
func toks(from, n int) []llama.Token {
	out := make([]llama.Token, n)
	for k := range out {
		out[k] = llama.Token(from + k)
	}
	return out
}

func textOf(ts ...[]llama.Token) []chunk {
	var all []llama.Token
	for _, t := range ts {
		all = append(all, t...)
	}
	return []chunk{textChunk(all)}
}

func TestUsable(t *testing.T) {
	system := toks(1, 100) // システムの発言とツールの説明(会話どうしで同じ)
	convA := textOf(system, toks(1000, 50))
	nextA := textOf(system, toks(1000, 50), toks(2000, 20)) // A の続き(前回の分 + 新しい発言)
	nextB := textOf(system, toks(3000, 30))                 // 新しい会話 B(頭のシステムの発言だけ同じ)

	cases := []struct {
		name     string
		cached   []chunk
		next     []chunk
		rollback bool
		want     int
	}{
		{"続きなら全部使える", convA, nextA, false, 150},
		{"巻き戻せるモデルは途中まで使える", convA, nextB, true, 100},
		{"巻き戻せないモデルは途中で違えば 0", convA, nextB, false, 0},
		{"空なら 0", nil, nextB, false, 0},
		// 同じプロンプトをもう一度:最後の1トークンは読ませ直すので、巻き戻せないモデルでは使えない(0012 の 6-1)
		{"同じプロンプト・巻き戻せない", convA, convA, false, 0},
		{"同じプロンプト・巻き戻せる", convA, convA, true, 149},
	}
	for _, c := range cases {
		if got := usable(c.cached, c.next, c.rollback); got != c.want {
			t.Errorf("%s: usable = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestPickSeq(t *testing.T) {
	system := toks(1, 100)
	now := time.Now()
	seqA := func() *sequence {
		return &sequence{id: 0, cached: textOf(system, toks(1000, 50)), used: now.Add(-time.Minute)}
	}
	seqB := func() *sequence { return &sequence{id: 1, cached: textOf(system, toks(3000, 30)), used: now} }
	nextA := textOf(system, toks(1000, 50), toks(2000, 20))
	nextC := textOf(system, toks(5000, 10)) // 3つ目の会話

	// 会話 A の続きは、A の入っているシーケンス(B のほうが新しくても)
	if got := pickSeq([]*sequence{seqA(), seqB()}, nextA, false); got != 0 {
		t.Errorf("A の続き: %d, want 0", got)
	}
	if got := pickSeq([]*sequence{seqB(), seqA()}, nextA, false); got != 1 {
		t.Errorf("A の続き(並びが逆): %d, want 1", got)
	}

	// 3つ目の会話:空があれば空、無ければ一番長く使われていないもの(A)を捨てて使う
	empty := &sequence{id: 1}
	if got := pickSeq([]*sequence{seqA(), empty}, nextC, false); got != 1 {
		t.Errorf("空があるとき: %d, want 1", got)
	}
	if got := pickSeq([]*sequence{seqA(), seqB()}, nextC, false); got != 0 {
		t.Errorf("空が無いとき: %d, want 0(一番古い A)", got)
	}
	if got := pickSeq([]*sequence{seqB(), seqA()}, nextC, false); got != 1 {
		t.Errorf("空が無いとき(並びが逆): %d, want 1(一番古い A)", got)
	}

	// 巻き戻せるモデルでは、システムの発言の分の一致があるので、新しい会話でも一致が長いほう(同じなら古いほう)
	if got := pickSeq([]*sequence{seqB(), seqA()}, nextC, true); got != 1 {
		t.Errorf("巻き戻せるモデル: %d, want 1(一致は同じ 100 なので古い A)", got)
	}

	// 使っているものは選ばない
	a := seqA()
	a.busy = true
	if got := pickSeq([]*sequence{a, seqB()}, nextA, false); got != 1 {
		t.Errorf("A が使用中: %d, want 1", got)
	}
	b := seqB()
	b.busy = true
	if got := pickSeq([]*sequence{a, b}, nextA, false); got != -1 {
		t.Errorf("全部使用中: %d, want -1", got)
	}
}

func TestPlanKeepsLastToken(t *testing.T) {
	cached := textOf(toks(1, 10))
	keep, i, off := plan(cached, cached)
	if cells(keep) != 9 || i != 0 || off != 9 {
		t.Errorf("plan(同じ) = %d マス, i=%d off=%d; want 9, 0, 9", cells(keep), i, off)
	}
}
