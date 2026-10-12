package aibow

// engine.go: llama.cpp を直接呼んで文章を生成する部分。
//
// Ollama がやっていたことを、ここで自分でやる:
//
//	1. llama.cpp のライブラリ(DLL)を読み込む
//	2. モデル(GGUF ファイル)を読み込み、GPU に載せる
//	3. 会話を ChatML の文字列にして、トークン(数値の列)に変換する
//	4. トークン列をモデルに通す(decode)→ 次のトークンの確率が出る
//	5. 確率から1つ選ぶ(sample)→ 文字に戻して画面へ → 4 に戻る
//
// llama.cpp の呼び出しには yzma(github.com/hybridgroup/yzma)を使っている。
// yzma は llama.cpp の C の関数を Go からほぼそのまま呼べるようにするだけの薄い層。

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hybridgroup/yzma/pkg/llama"
)

// EngineConfig はエンジンの設定。_cmd/aibow で環境変数とアプリ設定から作る。
// NCtx と GPULayers は、あとから SetContextSize / SetGPULayers で変えられる。
type EngineConfig struct {
	LibPath   string // llama.cpp の DLL が入っているフォルダ
	NCtx      int    // コンテキスト長: 一度に扱えるトークン数(プロンプト + 返事)
	GPULayers int    // GPU に載せる層の数。モデルの層数以上なら全部 GPU
	LlamaLog  bool   // llama.cpp 自身のログ(GPU への割り当てなど)を表示するか
}

// GenerateOptions は1回の生成の設定。会話ごとの設定から作る。
type GenerateOptions struct {
	Think       bool    // 思考させるか
	Temperature float32 // 0 なら greedy(毎回一番点数の高いトークン)
	MaxTokens   int     // 1回の返事で生成するトークン数の上限

	// PresencePenalty は、すでに生成した言葉を選ばれにくくする強さ。0 で無効。
	// 小さいモデルが思考の中で同じことを繰り返し、答えにたどり着かないのを防ぐ。
	PresencePenalty float32
}

// ModelInfo は画面に表示するための、読み込み中のモデルの情報。
type ModelInfo struct {
	ID        string  `json:"id"`         // ModelEntry の ID
	Name      string  `json:"name"`       // 表示名
	Desc      string  `json:"desc"`       // llama.cpp が返す説明。例: "qwen35 4B Q4_K - Medium"
	ParamsB   float64 `json:"params_b"`   // パラメータ数(十億単位)
	SizeGB    float64 `json:"size_gb"`    // 重みのサイズ
	Layers    int32   `json:"layers"`     // 層の数
	GPULayers int     `json:"gpu_layers"` // GPU に載せる層の数(指定値)
	NCtx      uint32  `json:"n_ctx"`      // コンテキスト長
	LoadSec   float64 `json:"load_sec"`   // 読み込みにかかった秒数

	path string // GGUF ファイルの場所(読み込みに失敗したとき、元に戻すために使う)
}

// GenerateStats は1回の生成の記録。画面の統計行に出す。
type GenerateStats struct {
	PromptTokens int     `json:"prompt_tokens"` // プロンプトのトークン数
	ReusedTokens int     `json:"reused_tokens"` // そのうち KV キャッシュを再利用できた数
	PromptSec    float64 `json:"prompt_sec"`    // プロンプトを読み込むのにかかった秒数
	GenTokens    int     `json:"gen_tokens"`    // 生成したトークン数(思考 + 答え)
	GenSec       float64 `json:"gen_sec"`       // 生成にかかった秒数
	StopReason   string  `json:"stop_reason"`   // eos / length / context / cancel
}

// Engine は llama.cpp と、今読み込んでいるモデル1つ、その作業領域(コンテキスト)を持つ。
// モデルは LoadModel で入れ替えられる。
type Engine struct {
	cfg     EngineConfig
	Devices []string // llama.cpp が見つけた計算装置
	HasGPU  bool     // GPU が見つかったか

	// 生成とモデルの入れ替えは同時にできない(作業領域が1つしかないため)
	mu    sync.Mutex
	model llama.Model // 0 なら未読み込み
	vocab llama.Vocab
	lctx  llama.Context

	// info は読み込み中のモデルの情報。生成中でも画面から読めるよう、別に持つ。
	info atomic.Pointer[ModelInfo]

	// cached は、今 KV キャッシュに入っているトークン列。
	// KV キャッシュ = モデルが読み終えたトークンの計算結果の置き場。
	// 次の質問のプロンプトが同じところから始まっていれば、そこは計算し直さずに済む。
	cached []llama.Token
}

// 一度に decode するトークン数の上限。長いプロンプトはこの単位に区切って読ませる。
const batchSize = 512

// ErrBusy は、生成中やモデルの読み込み中に、別の依頼が来たときのエラー。
var ErrBusy = errors.New("生成中、またはモデルの読み込み中です。終わるまで待つか、停止してください")

// NewEngine は llama.cpp のライブラリを読み込む。モデルはまだ読み込まない。
func NewEngine(cfg EngineConfig) (*Engine, error) {
	if err := llama.Load(cfg.LibPath); err != nil {
		return nil, fmt.Errorf("llama.cpp のライブラリを読み込めません(AIBOW_LIB=%q): %w", cfg.LibPath, err)
	}
	if !cfg.LlamaLog {
		llama.LogSet(llama.LogSilent())
	}
	// Init で、lib フォルダにある計算用のバックエンド(ggml-cpu.dll / ggml-cuda.dll など)が読み込まれる
	if err := llama.Init(); err != nil {
		return nil, fmt.Errorf("llama.cpp の初期化に失敗しました: %w", err)
	}
	e := &Engine{cfg: cfg}
	e.Devices, e.HasGPU = listDevices()
	return e, nil
}

// listDevices は llama.cpp が見つけた計算装置(CPU / GPU)を一覧にする。
// GPU 用の DLL が入っていない、ドライバーが古い、などで GPU が見つからないと CPU だけになる。
func listDevices() (names []string, hasGPU bool) {
	for i := uint64(0); i < llama.GGMLBackendDeviceCount(); i++ {
		dev := llama.GGMLBackendDeviceGet(i)
		typ := llama.GGMLBackendDevType(dev)
		name := fmt.Sprintf("%s [%s] %s", llama.GGMLBackendDeviceName(dev), typ, llama.GGMLBackendDeviceDescription(dev))
		if typ == llama.GGMLBackendDeviceTypeGPU {
			hasGPU = true
			free, total := llama.GGMLBackendDeviceMemory(dev)
			name += fmt.Sprintf("(VRAM 空き %.1f / %.1f GB)", float64(free)/(1<<30), float64(total)/(1<<30))
		}
		names = append(names, name)
	}
	return names, hasGPU
}

// Info は読み込み中のモデルの情報を返す。未読み込みなら nil。
func (e *Engine) Info() *ModelInfo {
	return e.info.Load()
}

// LoadModel はモデルを入れ替える。数秒〜十数秒かかる。
func (e *Engine) LoadModel(m ModelEntry) (*ModelInfo, error) {
	if !e.mu.TryLock() {
		return nil, ErrBusy
	}
	defer e.mu.Unlock()
	return e.replace(m, func() {})
}

// SetGPULayers は GPU に載せる層の数を変え、今のモデルを読み込み直す。
// 層を GPU と CPU のどちらに置くかはモデルの読み込み時に決まるので、読み直すしかない。
// モデルが未読み込みなら、次に読み込むときから効く。
func (e *Engine) SetGPULayers(n int) (*ModelInfo, error) {
	if !e.mu.TryLock() {
		return nil, ErrBusy
	}
	defer e.mu.Unlock()

	old := e.cfg.GPULayers
	e.cfg.GPULayers = n
	prev := e.info.Load()
	if prev == nil {
		return nil, nil
	}
	return e.replace(prev.entry(), func() { e.cfg.GPULayers = old })
}

// replace は今のモデルを解放してから m を読み込む。e.mu を持った状態で呼ぶ。
//
// VRAM 8GB に2つのモデルは同時に載らないことが多いので、先に今のモデルを解放してから読み込む。
// 読めなかったときは undo で設定を戻し、元のモデルを読み込み直して戻す。
func (e *Engine) replace(m ModelEntry, undo func()) (*ModelInfo, error) {
	prev := e.info.Load()
	e.unload()

	info, err := e.load(m)
	if err != nil {
		undo()
		if prev != nil {
			if _, rerr := e.load(prev.entry()); rerr != nil {
				err = fmt.Errorf("%w(元のモデルにも戻せませんでした: %v)", err, rerr)
			}
		}
		return nil, err
	}
	return info, nil
}

// SetContextSize はコンテキスト長を変える。モデル本体(重み)は読み直さず、作業領域だけを作り直す。
//
// コンテキスト = 推論用の作業領域で、KV キャッシュもここにある。重みとは別に確保されるので、
// 長さを変えるときは作業領域だけ作り直せばよい(重みの読み込みより速い)。
// KV キャッシュは捨てるので、次の送信はプロンプトを最初から読ませることになる。
// モデルが未読み込みなら、次に読み込むときから効く。
func (e *Engine) SetContextSize(n int) (*ModelInfo, error) {
	if !e.mu.TryLock() {
		return nil, ErrBusy
	}
	defer e.mu.Unlock()

	old := e.cfg.NCtx
	e.cfg.NCtx = n
	if e.model == 0 {
		return nil, nil
	}

	// 先に今の作業領域を解放する(VRAM に新旧2つ分は載らないことがあるため)
	llama.Free(e.lctx)
	e.lctx = 0
	e.cached = nil

	lctx, err := e.newContext()
	if err != nil {
		e.cfg.NCtx = old
		back, rerr := e.newContext()
		if rerr != nil {
			// 元の長さでも作れないなら、モデルごと解放して「未読み込み」にする
			e.unload()
			return nil, fmt.Errorf("%w(元のコンテキスト長にも戻せなかったので、モデルを解放しました: %v)", err, rerr)
		}
		e.lctx = back
		return nil, err
	}
	e.lctx = lctx

	info := *e.info.Load() // 読み込み中の情報を写して、コンテキスト長だけ書き換える
	info.NCtx = llama.NCtx(lctx)
	e.info.Store(&info)
	return &info, nil
}

// load はモデルとコンテキストを作る。e.mu を持った状態で呼ぶ。
func (e *Engine) load(m ModelEntry) (*ModelInfo, error) {
	start := time.Now()

	// NGpuLayers が「何層を VRAM に置くか」。
	// VRAM に入りきらないときは、この数を減らすと残りはメインメモリ(CPU)で計算される。
	mp := llama.ModelDefaultParams()
	mp.NGpuLayers = int32(e.cfg.GPULayers)
	model, err := llama.ModelLoadFromFile(m.Path, mp)
	if err != nil || model == 0 {
		return nil, fmt.Errorf("モデル %s を読み込めません。llama.cpp が対応していない形式の可能性があります"+
			"(Ollama のモデルの一部は Ollama 独自の形式です)。AIBOW_LLAMA_LOG=1 で詳しい理由が出ます", m.Name)
	}

	e.model = model
	lctx, err := e.newContext()
	if err != nil {
		llama.ModelFree(model)
		e.model = 0
		return nil, err
	}

	e.lctx, e.vocab = lctx, llama.ModelGetVocab(model)
	e.cached = nil // 新しい作業領域なので KV キャッシュは空
	info := &ModelInfo{
		ID:        m.ID,
		Name:      m.Name,
		Desc:      llama.ModelDesc(model),
		ParamsB:   float64(llama.ModelNParams(model)) / 1e9,
		SizeGB:    float64(llama.ModelSize(model)) / (1 << 30),
		Layers:    llama.ModelNLayer(model),
		GPULayers: e.cfg.GPULayers,
		NCtx:      llama.NCtx(lctx),
		LoadSec:   time.Since(start).Seconds(),
		path:      m.Path,
	}
	e.info.Store(info)
	return info, nil
}

// newContext は今のモデルにコンテキストを作る。e.mu を持った状態で呼ぶ。
//
// コンテキスト = 推論用の作業領域。KV キャッシュもここに確保される。
// NCtx を大きくするほど長い会話を扱えるが、その分 VRAM を使う。
func (e *Engine) newContext() (llama.Context, error) {
	cp := llama.ContextDefaultParams()
	cp.NCtx = uint32(e.cfg.NCtx)
	cp.NBatch = batchSize
	cp.NUbatch = batchSize
	lctx, err := llama.InitFromModel(e.model, cp)
	if err != nil || lctx == 0 {
		return 0, fmt.Errorf("コンテキスト(長さ %d)を作れません。VRAM が足りない可能性があります。"+
			"設定のコンテキスト長か、GPU に載せる層の数を減らしてください", e.cfg.NCtx)
	}
	return lctx, nil
}

// entry は、読み込み中のモデルを読み直すための ModelEntry を返す。
func (i *ModelInfo) entry() ModelEntry {
	return ModelEntry{ID: i.ID, Name: i.Name, Path: i.path}
}

// unload はモデルと作業領域を解放する(VRAM が空く)。e.mu を持った状態で呼ぶ。
func (e *Engine) unload() {
	if e.lctx != 0 {
		llama.Free(e.lctx)
		e.lctx = 0
	}
	if e.model != 0 {
		llama.ModelFree(e.model)
		e.model = 0
	}
	e.cached = nil
	e.info.Store(nil)
}

// Close はモデルとライブラリを解放する。
func (e *Engine) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.unload()
	llama.Close()
}

// Generate は会話の続き(アシスタントの返事)を生成する。
// 文字が出るたびに onThinking / onContent が呼ばれる。
// ctx がキャンセルされる(画面で「停止」が押される)と途中で止まる。
func (e *Engine) Generate(
	ctx context.Context,
	messages []Message,
	opt GenerateOptions,
	onPrompt func(prompt string),
	onThinking, onContent func(text string),
) (GenerateStats, error) {
	var st GenerateStats
	if !e.mu.TryLock() {
		return st, ErrBusy
	}
	defer e.mu.Unlock()
	if e.model == 0 {
		return st, errors.New("モデルが読み込まれていません。入力欄の下でモデルを選んでください")
	}

	// 3. 会話 → 文字列 → トークン列
	prompt := buildPrompt(messages, opt.Think)
	onPrompt(prompt)
	tokens := llama.Tokenize(e.vocab, prompt, true, true)
	st.PromptTokens = len(tokens)

	nCtx := int(llama.NCtx(e.lctx))
	if len(tokens) >= nCtx {
		return st, fmt.Errorf("会話が長すぎます(%d トークン / 上限 %d)。新しい会話で始め直すか、設定のコンテキスト長を増やしてください", len(tokens), nCtx)
	}

	// 4. プロンプトをモデルに読ませる。KV キャッシュに残っている部分は飛ばす。
	start := time.Now()
	st.ReusedTokens = e.reuseCache(tokens)
	for i := st.ReusedTokens; i < len(tokens); i += batchSize {
		end := min(i+batchSize, len(tokens))
		if err := e.decode(tokens[i:end]); err != nil {
			return st, err
		}
	}
	st.PromptSec = time.Since(start).Seconds()

	// 5. 1トークンずつ選んで、文字にして、またモデルに読ませる、の繰り返し
	sampler := newSampler(e.vocab, opt.Temperature, opt.PresencePenalty)
	defer llama.SamplerFree(sampler)

	var u8 utf8Buffer
	split := newThinkSplitter(opt.Think)
	emit := func(text string) {
		th, c := split.Push(text)
		if th != "" {
			onThinking(th)
		}
		if c != "" {
			onContent(c)
		}
	}

	start = time.Now()
	st.StopReason = "length"
	buf := make([]byte, 256)
	for st.GenTokens < opt.MaxTokens {
		if ctx.Err() != nil {
			st.StopReason = "cancel"
			break
		}
		if len(e.cached) >= nCtx {
			st.StopReason = "context"
			break
		}

		// 直前に decode した結果(次のトークンの確率)から1つ選ぶ
		tok := llama.SamplerSample(sampler, e.lctx, -1)

		// <|im_end|> など「発言の終わり」を表すトークンが出たら終了
		if llama.VocabIsEOG(e.vocab, tok) {
			st.StopReason = "eos"
			break
		}

		emit(u8.Push(e.tokenBytes(tok, &buf)))
		st.GenTokens++

		// 選んだトークンをモデルに読ませて、その次の確率を計算させる
		if err := e.decode([]llama.Token{tok}); err != nil {
			return st, err
		}
	}
	emit(u8.Flush())
	if th, c := split.Flush(); th != "" || c != "" {
		if th != "" {
			onThinking(th)
		}
		if c != "" {
			onContent(c)
		}
	}
	st.GenSec = time.Since(start).Seconds()
	return st, nil
}

// decode はトークン列をモデルに通し、KV キャッシュに追加する。
func (e *Engine) decode(tokens []llama.Token) error {
	rc, err := llama.Decode(e.lctx, llama.BatchGetOne(tokens))
	if err != nil {
		return err
	}
	if rc != 0 {
		// 1 はキャッシュに空きがない、負の値はそれ以外のエラー
		return fmt.Errorf("decode に失敗しました(戻り値 %d)。コンテキストが足りない可能性があります", rc)
	}
	e.cached = append(e.cached, tokens...)
	return nil
}

// reuseCache は、KV キャッシュの中身と新しいプロンプトの共通部分を残し、
// 残りを捨てる。再利用できたトークン数を返す。
//
// 例えば前回のプロンプトの続きに新しい発言が足されただけなら、前回分は計算し直さずに済む。
// ただし Qwen3.5 は一部の層が「途中まで巻き戻せない」仕組み(線形アテンション)なので、
// 途中から捨てられない場合は全部捨てて最初から読ませる。
func (e *Engine) reuseCache(tokens []llama.Token) int {
	n := 0
	for n < len(e.cached) && n < len(tokens) && e.cached[n] == tokens[n] {
		n++
	}
	// 最後の1トークンは必ず読ませ直す(そうしないと「次の確率」が計算されない)
	n = min(n, len(tokens)-1)

	mem, err := llama.GetMemory(e.lctx)
	if err != nil {
		return 0
	}
	if n < len(e.cached) {
		ok, _ := llama.MemorySeqRm(mem, 0, llama.Pos(n), -1)
		if !ok {
			llama.MemoryClear(mem, true)
			n = 0
		}
		e.cached = e.cached[:n]
	}
	return n
}

// tokenBytes はトークンを元のバイト列に戻す。
// special=true にしているので "</think>" のような特別なトークンも文字として出てくる。
func (e *Engine) tokenBytes(tok llama.Token, buf *[]byte) []byte {
	n := llama.TokenToPiece(e.vocab, tok, *buf, 0, true)
	if n < 0 { // バッファが足りないときは必要なサイズが負の値で返ってくる
		*buf = make([]byte, -n)
		n = llama.TokenToPiece(e.vocab, tok, *buf, 0, true)
	}
	return (*buf)[:n]
}

// newSampler は「次のトークンの選び方」を組み立てる。
//
// モデルが出すのは、語彙の全トークン(Qwen3.5 なら約25万個)それぞれの点数。
// そこから1つ選ぶまでに、次のフィルターを順に通す(サンプラーチェーン):
//
//	penalties : 直近に生成したトークンの点数を下げる(presence penalty)
//	top-k     : 点数の高い上位 k 個だけ残す
//	top-p     : 確率の高い順に足して p を超えるまでの候補だけ残す
//	temp      : 確率の差を強調(<1)するか、ならす(>1)か
//	dist      : 残った候補から確率に従ってランダムに1つ選ぶ
//
// temperature が 0 のときは、常に一番点数の高いものを選ぶ(greedy)。毎回同じ答えになるが、
// 小さいモデルでは同じ文を繰り返しやすい。
// top-k / top-p の値は、Qwen が推奨している値に合わせている。
func newSampler(vocab llama.Vocab, temperature, presence float32) llama.Sampler {
	chain := llama.SamplerChainInit(llama.SamplerChainDefaultParams())

	// presence penalty: 直近 penaltyLastN 個の生成トークンに1回でも出てきたトークンは、
	// 点数から presence を引く(何回出たかは問わない)。数えるのは今回生成したトークンだけで、
	// プロンプト(質問や過去の会話)は数えない。
	if presence > 0 {
		llama.SamplerChainAdd(chain, llama.SamplerInitPenalties(
			llama.VocabNTokens(vocab), penaltyLastN, 1.0, 0, presence))
	}

	if temperature <= 0 {
		llama.SamplerChainAdd(chain, llama.SamplerInitGreedy())
		return chain
	}
	llama.SamplerChainAdd(chain, llama.SamplerInitTopK(20))
	llama.SamplerChainAdd(chain, llama.SamplerInitTopP(0.95, 1))
	llama.SamplerChainAdd(chain, llama.SamplerInitTemp(temperature))
	llama.SamplerChainAdd(chain, llama.SamplerInitDist(llama.DefaultSeed))
	return chain
}

// penaltyLastN は presence penalty の対象にする直近の生成トークン数。
const penaltyLastN = 256
