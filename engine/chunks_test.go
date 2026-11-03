package engine

import (
	"testing"

	"github.com/hybridgroup/yzma/pkg/llama"
)

func text(ts ...llama.Token) chunk { return textChunk(ts) }

func image(id string, nTok, nPos int) chunk { return chunk{image: id, nTok: nTok, nPos: nPos} }

// KV キャッシュの記録と次のプロンプトの、同じ先頭(0030)。文字はトークン、画像は ID で比べる
func TestCommon(t *testing.T) {
	img := image("a", 768, 32)
	cases := []struct {
		name          string
		cached, next  []chunk
		wantI, wantOf int
	}{
		{"空", nil, []chunk{text(1, 2)}, 0, 0},
		{"文字の途中から違う", []chunk{text(1, 2, 3)}, []chunk{text(1, 2, 9)}, 0, 2},
		{"続きを足しただけ", []chunk{text(1, 2)}, []chunk{text(1, 2, 3, 4)}, 0, 2},
		{"同じ画像の続き", []chunk{text(1), img, text(5, 6, 7)}, []chunk{text(1), img, text(5, 6, 7, 8)}, 2, 3},
		{"画像が違う", []chunk{text(1), img, text(5)}, []chunk{text(1), image("b", 768, 32), text(5)}, 1, 0},
		// 生成したトークンは記録の最後の文字に足してあるので、cached のほうが長い。次の発言で新しい画像が来る
		{"記録のほうが長く、次に画像が来る", []chunk{text(1, 2, 3)}, []chunk{text(1, 2), img, text(4)}, 1, 0},
		{"全部同じ", []chunk{text(1), img, text(5)}, []chunk{text(1), img, text(5)}, 3, 0},
		{"文字のところに画像が来た", []chunk{text(1), text(2)}, []chunk{text(1), img}, 1, 0},
	}
	for _, c := range cases {
		i, off := common(c.cached, c.next)
		if i != c.wantI || off != c.wantOf {
			t.Errorf("%s: (%d, %d)(期待: (%d, %d))", c.name, i, off, c.wantI, c.wantOf)
		}
	}
}

// マスの数と位置の数は別に数える(M-RoPE の画像は、768 マスで 32 位置)
func TestCellsAndPositions(t *testing.T) {
	cs := []chunk{text(1, 2, 3, 4), image("a", 768, 32), text(5, 6)}
	if cells(cs) != 774 || positions(cs) != 38 || imageTokens(cs) != 768 {
		t.Errorf("マス %d・位置 %d・画像 %d", cells(cs), positions(cs), imageTokens(cs))
	}
}
