package engine

// chunks.go: プロンプトを「チャンク」の列としてモデルに読ませる部分と、KV キャッシュの使い回し(0030)。
//
// 文字だけのプロンプトは、トークンの列1本。画像を付けると、文字のトークン列と画像が交互に並ぶ列になる:
//
//	文字(<|im_start|>user\n)→ 画像(768 トークン分の埋め込み。<|vision_start|> … <|vision_end|> は mtmd が前後の文字に足す)→ 文字(質問 …)
//
// 画像のチャンクは、トークン(語彙の番号)ではなく、画像エンコーダー(mmproj)が作った埋め込み(ベクトル)を直接モデルに入れる。
// 文字のトークンも、モデルの中では最初に埋め込みに変えられるので、モデルから見れば「埋め込みの列」として同じに扱える。
//
// KV キャッシュの記録(cached)もチャンクの列で持つ。次のプロンプトと先頭から比べ、文字はトークン、画像は ID(中身のハッシュ)で
// 同じところまでを読み直さずに使う(画像はトークンが無いので、トークンでは比べられない)。
//
// 位置(RoPE の位置)にも気をつける。Qwen3.5 は M-RoPE(位置を「時間・縦・横」で持つ)で、画像は縦横の大きいほうの数しか位置を進めない
// (1024×768 の画像は 768 トークンで 32 位置)。KV キャッシュのマスはトークンの数だけ使うので、「マスの数」と「位置」が別の数になる。
// また、画像を読ませたあとの KV キャッシュの位置の最大は画像の始まりの位置のままなので、llama.cpp の位置の自動(最大 + 1)に任せると
// 画像の後ろの文字の位置がずれる(段1で確かめた)。そのため、文字も画像も位置を自分で書いて読ませる(nextPos)。

import (
	"fmt"
	"slices"
	"time"

	"github.com/hybridgroup/yzma/pkg/llama"
	"github.com/hybridgroup/yzma/pkg/mtmd"
)

// chunk は、モデルに読ませる(読ませた)ものの1かたまり。文字のトークン列か、画像1枚。
type chunk struct {
	tokens []llama.Token // 文字のトークン列(画像なら nil)
	image  string        // 画像の ID(文字なら空)
	nTok   int           // KV キャッシュで使うマスの数(文字はトークン数、画像は画像トークン数)
	nPos   int           // 位置をいくつ進めるか(文字はトークン数。M-RoPE の画像は縦横の大きいほう)
	// src は画像を読ませるときの元(mtmd のチャンク)。プロンプトを組み立てた生成の間だけ持つ(KV キャッシュの記録では 0)
	src mtmd.InputChunk
}

func textChunk(tokens []llama.Token) chunk {
	return chunk{tokens: tokens, nTok: len(tokens), nPos: len(tokens)}
}

// cells は KV キャッシュのマスの数(作業領域が足りるかはこれで数える)。
func cells(cs []chunk) int {
	n := 0
	for _, c := range cs {
		n += c.nTok
	}
	return n
}

// positions は位置の数(次に読ませるものの位置)。
func positions(cs []chunk) int {
	n := 0
	for _, c := range cs {
		n += c.nPos
	}
	return n
}

// imageTokens は画像のマスの数(生成の記録に出す)。
func imageTokens(cs []chunk) int {
	n := 0
	for _, c := range cs {
		if c.image != "" {
			n += c.nTok
		}
	}
	return n
}

// common は、KV キャッシュの記録 cached と次のプロンプト next の、同じ先頭の長さ。
// next の i 番目のチャンクの、off トークン目から違う(画像は丸ごと同じか違うかなので、画像のところでは off は 0)。
// i == len(next) なら next は全部 cached に入っている。
func common(cached, next []chunk) (i, off int) {
	for i < len(next) && i < len(cached) {
		c, n := cached[i], next[i]
		if c.image != "" || n.image != "" {
			if c.image != n.image {
				return i, 0
			}
			i++
			continue
		}
		k := 0
		for k < len(c.tokens) && k < len(n.tokens) && c.tokens[k] == n.tokens[k] {
			k++
		}
		if k < len(n.tokens) {
			return i, k
		}
		// next のこのチャンクは全部同じ(cached のほうが長いのは、生成したトークンを足したため)
		i++
		if k < len(c.tokens) {
			return i, 0
		}
	}
	return i, 0
}

// reuseCache は、KV キャッシュの中身と次のプロンプト next の共通部分を残し、残りを捨てる。
// 続きをどこから読ませるか(next の i 番目のチャンクの off トークン目)と、再利用できたマスの数を返す。
//
// 例えば前回のプロンプトの続きに新しい発言が足されただけなら、前回分(画像も)は計算し直さずに済む。
// ただし Qwen3.5 は一部の層が「途中まで巻き戻せない」仕組み(線形アテンション)なので、
// 途中から捨てられない場合は全部捨てて最初から読ませる。
func (e *Engine) reuseCache(next []chunk) (i, off, reused int) {
	i, off = common(e.cached, next)
	// 最後の1トークンは必ず読ませ直す(そうしないと「次の確率」が計算されない)。プロンプトの最後は返事の書き出しの文字なので、文字のチャンク
	if i == len(next) {
		i = len(next) - 1
		off = max(0, next[i].nTok-1)
		if next[i].image != "" {
			off = 0
		}
	}

	keep := slices.Clone(next[:i])
	if off > 0 {
		keep = append(keep, textChunk(next[i].tokens[:off]))
	}
	for k := range keep {
		keep[k].src = 0 // 記録には mtmd のチャンクを持たない(生成が終わると解放するため)
	}
	mem, err := llama.GetMemory(e.lctx)
	if err != nil {
		e.cached = nil
		return 0, 0, 0
	}
	if positions(keep) < positions(e.cached) {
		// 捨てるのは位置で指定する(マスの数ではない。画像があると違う数になる)
		ok, _ := llama.MemorySeqRm(mem, 0, llama.Pos(positions(keep)), -1)
		if !ok {
			llama.MemoryClear(mem, true)
			e.cached = nil
			return 0, 0, 0
		}
	}
	e.cached = keep
	return i, off, cells(keep)
}

// feed は next の i 番目のチャンクの off トークン目から後ろを、モデルに読ませる。読ませた画像のエンコードの秒数を返す。
func (e *Engine) feed(next []chunk, i, off int) (encodeSec float64, err error) {
	for ; i < len(next); i++ {
		c := next[i]
		if c.image != "" {
			sec, err := e.decodeImage(c)
			encodeSec += sec
			if err != nil {
				return encodeSec, err
			}
		} else {
			tokens := c.tokens[off:]
			for k := 0; k < len(tokens); k += batchSize {
				if err := e.decode(tokens[k:min(k+batchSize, len(tokens))]); err != nil {
					return encodeSec, err
				}
			}
		}
		off = 0
	}
	return encodeSec, nil
}

// nextPos は次に読ませるものの位置。
func (e *Engine) nextPos() llama.Pos {
	return llama.Pos(positions(e.cached))
}

// decode はトークン列をモデルに通し、KV キャッシュに追加する。位置は自分で書く(画像の後ろでずれないように)。
func (e *Engine) decode(tokens []llama.Token) error {
	pos := make([]llama.Pos, len(tokens))
	p0 := e.nextPos()
	for k := range pos {
		pos[k] = p0 + llama.Pos(k)
	}
	batch := llama.BatchGetOne(tokens)
	batch.Pos = &pos[0]
	rc, err := llama.Decode(e.lctx, batch)
	if err != nil {
		return err
	}
	if rc != 0 {
		// 1 はキャッシュに空きがない、負の値はそれ以外のエラー
		return fmt.Errorf("decode に失敗しました(戻り値 %d)。コンテキストが足りない可能性があります", rc)
	}
	// 記録の最後が文字なら、そこに足す(生成したトークンも1つずつ足していく)
	if n := len(e.cached); n > 0 && e.cached[n-1].image == "" {
		last := &e.cached[n-1]
		last.tokens = append(last.tokens, tokens...)
		last.nTok += len(tokens)
		last.nPos += len(tokens)
	} else {
		e.cached = append(e.cached, textChunk(slices.Clone(tokens)))
	}
	return nil
}

// decodeImage は画像のチャンクを読ませる。エンコードにかかった秒数を返す。
//
//  1. 画像エンコーダー(mmproj)で、画像を画像トークンの埋め込みにする(EncodeChunk。GPU でも数百ミリ秒かかる、いちばん重いところ)
//  2. 埋め込みを、トークンの代わりに batch に入れて decode する(llama_batch の embd)。位置は1トークンに4つ(M-RoPE の時間・縦・横・予備)を、
//     列ごとに並べる(pos[i]・pos[n+i]・pos[2n+i]・pos[3n+i])。並びは llama.cpp の helper と同じ t・y・x・z(段1で logits が一致することを確かめた)
//
// llama.cpp の helper(mtmd_helper_eval_chunks)も同じことをしているが、チャンクの列を丸ごと読ませるものしか yzma に無い。
// KV キャッシュを使い回して途中のチャンクから読ませるため、ここで自分で書く。
func (e *Engine) decodeImage(c chunk) (float64, error) {
	var embd []float32
	var mrope, nonCausal bool
	start := time.Now()
	err := e.withProjector(func(mctx mtmd.Context) error {
		if err := mtmd.EncodeChunk(mctx, c.src); err != nil {
			return fmt.Errorf("画像をエンコードできません: %w", err)
		}
		out, err := mtmd.GetOutputEmbd(mctx, int32(c.nTok)*llama.ModelNEmbdInp(e.model))
		if err != nil {
			return err
		}
		embd = slices.Clone(out) // 次のエンコードで書き換わるので写す
		mrope, nonCausal = mtmd.DecodeUseMRope(mctx), mtmd.DecodeUseNonCausal(mctx, c.src)
		return nil
	})
	sec := time.Since(start).Seconds()
	if err != nil {
		return sec, err
	}
	if nonCausal {
		// 画像の中で前後を見合うモデル(Gemma 3 など)は、画像全体を1回で読ませる必要がある。今は Qwen3.5 だけなので作っていない
		return sec, fmt.Errorf("この画像エンコーダーの読ませ方(non-causal)にはまだ対応していません")
	}

	nEmbd := int(llama.ModelNEmbdInp(e.model))
	it := mtmd.InputChunkGetTokensImage(c.src)
	p0 := e.nextPos()
	for off := 0; off < c.nTok; off += batchSize {
		m := min(batchSize, c.nTok-off)
		var pos []llama.Pos
		if mrope {
			pos = make([]llama.Pos, m*4)
			for k := 0; k < m; k++ {
				dp := mtmd.ImageTokensGetDecoderPos(it, p0, uint64(off+k))
				pos[k], pos[m+k], pos[2*m+k], pos[3*m+k] = llama.Pos(dp.T), llama.Pos(dp.Y), llama.Pos(dp.X), llama.Pos(dp.Z)
			}
		} else {
			pos = make([]llama.Pos, m)
			for k := range pos {
				pos[k] = p0 + llama.Pos(off+k)
			}
		}
		var batch llama.Batch
		batch.NTokens = int32(m)
		batch.Embd = &embd[off*nEmbd]
		batch.Pos = &pos[0]
		rc, err := llama.Decode(e.lctx, batch)
		if err != nil {
			return sec, err
		}
		if rc != 0 {
			return sec, fmt.Errorf("画像の decode に失敗しました(戻り値 %d)。コンテキストが足りない可能性があります", rc)
		}
	}
	e.cached = append(e.cached, chunk{image: c.image, nTok: c.nTok, nPos: c.nPos})
	return sec, nil
}
