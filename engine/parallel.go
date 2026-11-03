package engine

// parallel.go: シーケンスが2本以上のときの生成(0023)。同時に来た生成を、1回の decode にまとめて進める。
//
// シーケンスが1本なら、Generate がそのまま decode を呼ぶ(engine.go。1つずつ)。2本以上では、decode を回す係(schedule)を1つ置き、
// Generate はプロンプトを係に渡して、生成した文字を受け取るだけにする(決定事項 17):
//
//	Generate(会話 A)─┐                     ┌─ 1回の decode:[A の生成 1 トークン][B のプロンプトの続き 511 トークン]
//	                  ├→ 係(schedule)── 繰り返す
//	Generate(会話 B)─┘                     └─ 確率から A・B それぞれのトークンを選び(サンプラーは生成ごと)、文字を返す
//
// llama.cpp の作業領域はスレッドセーフではないので、作業領域に触る呼び出し(KV キャッシュを捨てる・decode・確率から選ぶ)は全部この係がする。
// 1回の decode に入れるのは、生成中の人の1トークンずつと、プロンプトを読んでいる人の続き(合わせて batchSize まで。決定事項 13)。
// 片方が長いプロンプトを読む間、生成中の人は 1 回の decode が重くなる分だけ遅くなる(段1:生成だけ 15 ms、+511 トークンで 199 ms)。
// これが llama.cpp の server の --parallel(連続バッチ)の中身。この仕様は2本まで(N 本はあとで)。

import (
	"cmp"
	"context"
	"slices"
	"sync"
	"time"

	"github.com/hybridgroup/yzma/pkg/llama"

	"github.com/secondarykey/aibow/format"
)

// job は、係に渡す生成1回分。係だけが書き換え、終わったら done を閉じる(そのあとは Generate が読む)。
type job struct {
	ctx   context.Context
	seq   *sequence
	input []chunk
	opt   GenerateOptions

	// 次にプロンプトのどこを読ませるか(input の i 番目のチャンクの off トークン目)。i == len(input) なら読み終えて生成中
	i, off int
	begun  bool // KV キャッシュの使い回しを決めたか(最初の1回は係が決める。作業領域に触るため)

	sampler llama.Sampler
	parser  format.OutputParser
	u8      utf8Buffer
	buf     []byte

	pending  bool        // 選んだトークン last を、まだモデルに読ませていない
	last     llama.Token //
	fed      []llama.Token
	sample   int32 // 今回の decode で、この生成の確率を取る batch の中の位置(-1 なら取らない)
	start    time.Time
	genStart time.Time
	st       GenerateStats

	text *textQueue
	done chan struct{}
	out  format.Output
	err  error
}

// generateParallel は、シーケンスが2本以上のときの Generate。シーケンスを選び、係に頼んで、文字を受け取る。
func (e *Engine) generateParallel(
	ctx context.Context,
	p format.Prompt,
	opt GenerateOptions,
	onPrompt func(prompt string),
	onThinking, onContent func(text string),
) (format.Output, GenerateStats, error) {
	var st GenerateStats
	// 生成どうしは同時に入る(RLock)。モデルの入れ替え(Lock)とは同時にできない
	if !e.mu.TryRLock() {
		return format.Output{}, st, ErrBusy
	}
	defer e.mu.RUnlock()
	if e.model == 0 {
		return format.Output{}, st, ErrNoModel
	}
	if e.format == nil {
		return format.Output{}, st, format.Unsupported(e.arch)
	}

	prompt := e.format.BuildPrompt(p)
	onPrompt(prompt)
	start := time.Now()
	input, free, err := e.input(p, prompt, false)
	if err != nil {
		return format.Output{}, st, err
	}
	defer free()
	st.PromptTokens = cells(input)
	st.ImageTokens = imageTokens(input)
	st.ProjectorSec, st.ProjectorCPU = e.takeProjectorLoad()
	if err := tooLong(st.PromptTokens, int(llama.NCtxSeq(e.lctx))); err != nil {
		return format.Output{}, st, err
	}

	s := e.takeSeq(input)
	if s == nil {
		// 席(api.Seat)はシーケンスの数だけなので、ふつうはここに来ない
		return format.Output{}, st, ErrBusy
	}
	defer e.releaseSeq(s)

	j := &job{
		ctx: ctx, seq: s, input: input, opt: opt,
		sampler: newSampler(e.vocab, opt.Temperature, opt.PresencePenalty),
		parser:  e.format.NewParser(p),
		buf:     make([]byte, 256),
		sample:  -1,
		start:   start,
		st:      st,
		text:    newTextQueue(),
		done:    make(chan struct{}),
	}
	defer llama.SamplerFree(j.sampler)
	j.st.Seq = int(s.id)
	e.jobs <- j
	j.text.deliver(j.done, onThinking, onContent)
	return j.out, j.st, j.err
}

// takeSeq は input を読ませるシーケンスを選んで、使用中にする(sequence.go の pickSeq)。空きが無ければ nil。
func (e *Engine) takeSeq(input []chunk) *sequence {
	e.seqMu.Lock()
	defer e.seqMu.Unlock()
	k := pickSeq(e.seqs, input, e.rollback)
	if k < 0 {
		return nil
	}
	e.seqs[k].busy = true
	return e.seqs[k]
}

// releaseSeq はシーケンスを空きに戻す(KV キャッシュは残す。次に同じ会話が来たら使い回す)。
func (e *Engine) releaseSeq(s *sequence) {
	e.seqMu.Lock()
	defer e.seqMu.Unlock()
	s.busy = false
	s.used = time.Now()
}

// schedule は decode を回す係。生成を受け取り、動いている生成がある間は1回ずつ decode して進める。
// jobs が閉じられたら(Engine.Close)終わる。
func (e *Engine) schedule() {
	batch := llama.BatchInit(batchSize, 0, 1)
	defer llama.BatchFree(batch)
	var active []*job
	for {
		if len(active) == 0 {
			j, ok := <-e.jobs
			if !ok {
				return
			}
			active = append(active, j)
		}
		// 待たずに受け取れる分は、次の decode に入れる(片方の生成の途中でも、すぐ混ぜる)
	accept:
		for {
			select {
			case j, ok := <-e.jobs:
				if !ok {
					return
				}
				active = append(active, j)
			default:
				break accept
			}
		}
		active = e.step(&batch, active)
	}
}

// step は1回の decode をする。終わった生成を除いた列を返す。
func (e *Engine) step(batch *llama.Batch, active []*job) []*job {
	// 1. decode の前に:始まったばかりの生成の KV キャッシュを決める・止められたものを終える・画像を読ませる
	live := active[:0]
	for _, j := range active {
		if !j.begun {
			j.begun = true
			j.i, j.off, j.st.ReusedTokens = e.reuseCache(j.seq, j.input)
		}
		if j.ctx.Err() != nil {
			j.finish("cancel", nil)
			continue
		}
		if err := e.feedImages(j); err != nil {
			j.finish("", err)
			continue
		}
		live = append(live, j)
	}

	// 2. batch を作る。生成中の人の1トークンずつを先に取っておき、残りをプロンプトを読んでいる人に。
	// batch の中はシーケンスの番号の順に並べる。作業領域を分け合わない形(KVUnified なし)では、llama.cpp は batch を
	// 「番号が 1 ずつ増えるシーケンスのまとまり」で区切って計算する。逆の順(1 → 0)に並べると2回に分かれ、1回の decode が倍かかった(段4)
	slices.SortFunc(live, func(a, b *job) int { return cmp.Compare(a.seq.id, b.seq.id) })
	batch.Clear()
	budget := batchSize
	for _, j := range live {
		j.fed, j.sample = j.fed[:0], -1
		if j.pending {
			budget--
		}
	}
	for _, j := range live {
		switch {
		case j.pending:
			j.add(batch, j.last, true)
			j.pending = false
		case j.i < len(j.input) && budget > 0:
			budget -= j.addPrompt(batch, budget)
		}
	}
	if batch.NTokens == 0 {
		return live
	}

	// 3. decode。混ざった生成の数を、それぞれの記録に残す(学習用のログ。決定事項 8)
	mixed := 0
	for _, j := range live {
		if len(j.fed) > 0 {
			mixed++
		}
	}
	rc, err := llama.Decode(e.lctx, *batch)
	if err == nil && rc != 0 {
		err = decodeError(rc)
	}
	mem, _ := llama.GetMemory(e.lctx)
	next := live[:0]
	for _, j := range live {
		if len(j.fed) == 0 {
			next = append(next, j)
			continue
		}
		if err != nil {
			e.dropSeq(mem, j.seq) // どこまで読めたか分からないので、このシーケンスの KV キャッシュは捨てる
			j.finish("", err)
			continue
		}
		j.seq.addText(j.fed)
		j.st.Mixed = max(j.st.Mixed, mixed)
		// 4. 確率を取った生成は、次のトークンを選ぶ
		if j.sample >= 0 && !e.choose(j) {
			continue
		}
		next = append(next, j)
	}
	return next
}

// feedImages は、プロンプトの次が画像なら読ませる。画像の埋め込みは文字のトークンと同じ batch に混ぜられないので、画像だけで decode する。
func (e *Engine) feedImages(j *job) error {
	for j.i < len(j.input) && j.input[j.i].image != "" {
		sec, err := e.decodeImage(j.seq, j.input[j.i])
		j.st.EncodeSec += sec
		if err != nil {
			return err
		}
		j.i, j.off = j.i+1, 0
	}
	return nil
}

// add は batch にトークンを1つ足す(このシーケンスの次の位置。logits なら、そこで次のトークンの確率を出させる)。
func (j *job) add(batch *llama.Batch, tok llama.Token, logits bool) {
	pos := j.seq.nextPos() + llama.Pos(len(j.fed))
	batch.Add(tok, pos, []llama.SeqId{j.seq.id}, logits)
	if logits {
		j.sample = batch.NTokens - 1
	}
	j.fed = append(j.fed, tok)
}

// addPrompt は、プロンプトの続きを最大 budget トークン batch に足す。足した数を返す。
// 次が画像なら、そこで止める(画像は次の回の feedImages で読ませる)。プロンプトの最後のトークンでは確率を出させる。
func (j *job) addPrompt(batch *llama.Batch, budget int) int {
	n := 0
	for n < budget && j.i < len(j.input) && j.input[j.i].image == "" {
		tokens := j.input[j.i].tokens
		k := min(len(tokens)-j.off, budget-n)
		for x := 0; x < k; x++ {
			last := j.i == len(j.input)-1 && j.off+x == len(tokens)-1
			j.add(batch, tokens[j.off+x], last)
		}
		n += k
		j.off += k
		if j.off == len(tokens) {
			j.i, j.off = j.i+1, 0
		}
	}
	return n
}

// choose は、decode で出た確率から次のトークンを選ぶ(シーケンスが1本のときの Generate のループの中身と同じ順番)。
// 終わったら false。
func (e *Engine) choose(j *job) bool {
	if j.genStart.IsZero() {
		// プロンプトを読み終えた。ここから生成
		j.st.PromptSec = time.Since(j.start).Seconds()
		j.genStart = time.Now()
	}
	switch {
	case j.ctx.Err() != nil:
		j.finish("cancel", nil)
		return false
	case j.st.GenTokens >= j.opt.MaxTokens:
		j.finish("length", nil)
		return false
	case cells(j.seq.cached) >= int(llama.NCtxSeq(e.lctx)):
		j.finish("context", nil)
		return false
	}
	tok := llama.SamplerSample(j.sampler, e.lctx, j.sample)
	// <|im_end|> など「発言の終わり」を表すトークンが出たら終了
	if llama.VocabIsEOG(e.vocab, tok) {
		j.finish("eos", nil)
		return false
	}
	j.text.push(j.parser.Push(j.u8.Push(e.tokenBytes(tok, &j.buf))))
	j.st.GenTokens++
	// 書式が「ここで止める」と言ったら止める(例: ツールの結果をモデルが自分で書き始めた)
	if j.parser.Stop() {
		j.finish("stop", nil)
		return false
	}
	j.last, j.pending = tok, true
	return true
}

// finish は生成を終える。err が無ければ、振り分けた結果を残す。
func (j *job) finish(reason string, err error) {
	if !j.genStart.IsZero() {
		j.st.GenSec = time.Since(j.genStart).Seconds()
	} else if j.st.PromptSec == 0 {
		j.st.PromptSec = time.Since(j.start).Seconds()
	}
	j.st.StopReason, j.err = reason, err
	if err == nil {
		j.text.push(j.parser.Push(j.u8.Flush()))
		j.text.push(j.parser.Flush())
		j.out = j.parser.Result()
	}
	close(j.done)
}

// textQueue は、係が振り分けた文字を Generate に渡す列。
// 係が画面への送信(SSE・HTTP の書き込み)を待たないように、係は列に足すだけにし、送るのは Generate の側でする
// (遅い相手が1人いても、ほかの人の生成を止めない)。
type textQueue struct {
	mu    sync.Mutex
	items []piece
	ready chan struct{}
}

type piece struct {
	thinking bool
	text     string
}

func newTextQueue() *textQueue { return &textQueue{ready: make(chan struct{}, 1)} }

// push は思考と答えの文字を足す(Generate の send と同じく、思考が先)。
func (q *textQueue) push(thinking, content string) {
	if thinking == "" && content == "" {
		return
	}
	q.mu.Lock()
	if thinking != "" {
		q.items = append(q.items, piece{thinking: true, text: thinking})
	}
	if content != "" {
		q.items = append(q.items, piece{text: content})
	}
	q.mu.Unlock()
	select {
	case q.ready <- struct{}{}:
	default:
	}
}

// deliver は、done が閉じるまで、足された文字を順に onThinking / onContent に渡す。
func (q *textQueue) deliver(done <-chan struct{}, onThinking, onContent func(string)) {
	send := func() {
		q.mu.Lock()
		items := q.items
		q.items = nil
		q.mu.Unlock()
		for _, p := range items {
			if p.thinking {
				onThinking(p.text)
			} else {
				onContent(p.text)
			}
		}
	}
	for {
		select {
		case <-q.ready:
			send()
		case <-done:
			send()
			return
		}
	}
}
