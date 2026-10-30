// Package engine は、llama.cpp を直接呼んで文章を生成する部分(モデルの読み込み・生成・トークン数・KV キャッシュ)。
//
// 会話の保存・compact・エージェントループ(ルートの aibow)は知らない。プロンプトを構造(format.Prompt)で受け取り、
// 書式(format パッケージ)を当てて生成し、振り分けた結果(format.Output)と生成の記録(GenerateStats)を返すだけ。
// そのため、会話の仕組みなしで「生成だけ」を使う側(ステップ6の OpenAI 互換 API)からも使える。
package engine

// engine.go: llama.cpp を直接呼んで文章を生成する部分。
//
// Ollama がやっていたことを、ここで自分でやる:
//
//	1. llama.cpp のライブラリ(DLL)を読み込む
//	2. モデル(GGUF ファイル)を読み込み、GPU に載せる
//	3. 会話をモデルの書式(format パッケージ)の文字列にして、トークン(数値の列)に変換する
//	4. トークン列をモデルに通す(decode)→ 次のトークンの確率が出る
//	5. 確率から1つ選ぶ(sample)→ 文字に戻し、書式で思考・答え・ツール呼び出しに振り分けて画面へ → 4 に戻る
//
// llama.cpp の呼び出しには yzma(github.com/hybridgroup/yzma)を使っている。
// yzma は llama.cpp の C の関数を Go からほぼそのまま呼べるようにするだけの薄い層。

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hybridgroup/yzma/pkg/llama"

	"github.com/secondarykey/aibow/format"
	"github.com/secondarykey/aibow/models"
)

// Config はエンジンの設定。_cmd/aibow で環境変数とアプリ設定から作る。
// NCtx と GPULayers は、あとから EnsureContextSize / SetGPULayers で変えられる。
type Config struct {
	LibPath   string // llama.cpp の DLL が入っているフォルダ
	NCtx      int    // 作業領域の大きさ: 一度に扱えるトークン数(プロンプト + 返事)。会話の上限 + 返事の上限(会話ごとに違う)
	GPULayers int    // GPU に載せる層の数。モデルの層数以上なら全部 GPU
	LlamaLog  bool   // llama.cpp 自身のログ(GPU への割り当てなど)を表示するか
}

// GenerateOptions は1回の生成の設定(サンプリングと長さ)。会話ごとの設定から作る。
// 思考の ON/OFF はプロンプトの書き方で決まるので、format.Prompt の方に持つ。
type GenerateOptions struct {
	Temperature float32 // 0 なら greedy(毎回一番点数の高いトークン)
	MaxTokens   int     // 1回の返事で生成するトークン数の上限

	// PresencePenalty は、すでに生成した言葉を選ばれにくくする強さ。0 で無効。
	// 小さいモデルが思考の中で同じことを繰り返し、答えにたどり着かないのを防ぐ。
	PresencePenalty float32
}

// ModelInfo は画面に表示するための、読み込み中のモデルの情報。
type ModelInfo struct {
	ID        string  `json:"id"`         // models.Entry の ID
	Name      string  `json:"name"`       // 表示名
	Desc      string  `json:"desc"`       // llama.cpp が返す説明。例: "qwen35 4B Q4_K - Medium"
	Arch      string  `json:"arch"`       // モデルの種類(GGUF の general.architecture)。例: "qwen35"
	Format    string  `json:"format"`     // 使う書式の名前。空なら未対応の種類のモデル(生成できない)
	ParamsB   float64 `json:"params_b"`   // パラメータ数(十億単位)
	SizeGB    float64 `json:"size_gb"`    // 重みのサイズ
	Layers    int32   `json:"layers"`     // 層の数
	GPULayers int     `json:"gpu_layers"` // GPU に載せる層の数(指定値)
	NCtx      uint32  `json:"n_ctx"`      // 作業領域の大きさ(コンテキスト長)
	LoadSec   float64 `json:"load_sec"`   // 読み込みにかかった秒数

	path string // GGUF ファイルの場所(読み込みに失敗したとき、元に戻すために使う)
}

// GenerateStats は1回の生成の記録。画面の統計行に出す。
// 書式で振り分けた結果(format.Output)とは別に、Generate が返す。
type GenerateStats struct {
	PromptTokens int     `json:"prompt_tokens"` // プロンプトのトークン数
	ReusedTokens int     `json:"reused_tokens"` // そのうち KV キャッシュを再利用できた数
	PromptSec    float64 `json:"prompt_sec"`    // プロンプトを読み込むのにかかった秒数
	GenTokens    int     `json:"gen_tokens"`    // 生成したトークン数(思考 + 答え)
	GenSec       float64 `json:"gen_sec"`       // 生成にかかった秒数
	StopReason   string  `json:"stop_reason"`   // eos / length / context / cancel / stop(書式が決めた位置で止めた)
}

// Engine は llama.cpp と、今読み込んでいるモデル1つ、その作業領域(コンテキスト)を持つ。
// モデルは LoadModel で入れ替えられ、UnloadModel で降ろせる。
type Engine struct {
	cfg     Config
	Devices []string // llama.cpp が見つけた計算装置
	HasGPU  bool     // GPU が見つかったか

	// 生成とモデルの入れ替えは同時にできない(作業領域が1つしかないため)
	mu    sync.Mutex
	model llama.Model // 0 なら未読み込み
	vocab llama.Vocab // 書き換えるときは mu と vocabMu の両方を持つ
	// format はモデルの書式。未対応の種類のモデルなら nil。vocab と同じく mu と vocabMu の両方で守る
	format format.Format
	arch   string // モデルの種類(未対応のときのエラーに使う)
	lctx   llama.Context

	// vocabMu は、トークン数を数える(CountTokens)ときに、モデルが解放されないようにするためのロック。
	// 語彙(vocab)は生成中も読み取りしかされないので、数えるのは生成と同時にしてよい。
	// mu で守ると、生成中は数えられなくなる(別の会話を開いたときのメーターが出ない)ので、分けている。
	vocabMu sync.RWMutex

	// info は読み込み中のモデルの情報。生成中でも画面から読めるよう、別に持つ。
	info atomic.Pointer[ModelInfo]
	// loading は、入れ替えている先のモデルの ID(入れ替えていなければ nil)。
	// 入れ替えの間は、前のモデルを解放してから読み込むので info が nil になる。降ろした(何も無い)のと見分けるために持つ
	loading atomic.Pointer[string]

	// cached は、今 KV キャッシュに入っているトークン列。
	// KV キャッシュ = モデルが読み終えたトークンの計算結果の置き場。
	// 次の質問のプロンプトが同じところから始まっていれば、そこは計算し直さずに済む。
	cached []llama.Token
}

// 一度に decode するトークン数の上限。長いプロンプトはこの単位に区切って読ませる。
const batchSize = 512

// ErrNoModel は、モデルが読み込まれていないときのエラー。
var ErrNoModel = errors.New("モデルが読み込まれていません。入力欄の下でモデルを選んでください")

// ErrBusy は、生成中やモデルの読み込み中に、別の依頼が来たときのエラー。
var ErrBusy = errors.New("生成中、またはモデルの読み込み中です。終わるまで待つか、停止してください")

// New は llama.cpp のライブラリを読み込む。モデルはまだ読み込まない。
func New(cfg Config) (*Engine, error) {
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

// Loading は、入れ替えている先のモデルの ID。入れ替えていなければ空。
// 入れ替えの間は Info が nil になるので、aibow-server はこれを見て「読み直し中」と知らせる(降ろしたのとは違う)。
func (e *Engine) Loading() string {
	if id := e.loading.Load(); id != nil {
		return *id
	}
	return ""
}

// LoadModel はモデルを入れ替える。数秒〜十数秒かかる。GPU に載せる層の数は今の値(Config.GPULayers)のまま。
func (e *Engine) LoadModel(m models.Entry) (*ModelInfo, error) {
	if !e.mu.TryLock() {
		return nil, ErrBusy
	}
	defer e.mu.Unlock()
	return e.replace(m, func() {})
}

// LoadModelWithLayers は、GPU に載せる層の数を gpuLayers にしてモデルを入れ替える。
// 載せられる層の数はモデルの大きさと VRAM で決まるので、画面のアプリはモデルごとに覚えた値で読み込む(0014 の 4)。
// 読めなかったときは、層の数も元に戻して元のモデルを読み直す。
func (e *Engine) LoadModelWithLayers(m models.Entry, gpuLayers int) (*ModelInfo, error) {
	if !e.mu.TryLock() {
		return nil, ErrBusy
	}
	defer e.mu.Unlock()
	old := e.cfg.GPULayers
	e.cfg.GPULayers = gpuLayers
	return e.replace(m, func() { e.cfg.GPULayers = old })
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

// UnloadModel はモデルと作業領域を解放して、VRAM を空ける(ほかのアプリと並べて使うため。0015 の 23)。
// 未読み込みなら何もしない。解放したら true。次に生成するには、また読み込む(会話の仕組みは送信の前に会話のモデルを読み込む)。
func (e *Engine) UnloadModel() (bool, error) {
	if !e.mu.TryLock() {
		return false, ErrBusy
	}
	defer e.mu.Unlock()
	if e.model == 0 {
		return false, nil
	}
	e.unload()
	return true, nil
}

// replace は今のモデルを解放してから m を読み込む。e.mu を持った状態で呼ぶ。
//
// VRAM 8GB に2つのモデルは同時に載らないことが多いので、先に今のモデルを解放してから読み込む。
// 読めなかったときは undo で設定を戻し、元のモデルを読み込み直して戻す。
func (e *Engine) replace(m models.Entry, undo func()) (*ModelInfo, error) {
	e.loading.Store(&m.ID)
	defer e.loading.Store(nil)
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

// EnsureContextSize は作業領域の大きさ(コンテキスト長)を n にする。今と同じ大きさなら何もしない。
// モデル本体(重み)は読み直さず、作業領域だけを作り直す。作り直したら true。
//
// コンテキスト = 推論用の作業領域で、KV キャッシュもここにある。重みとは別に確保されるので、
// 長さを変えるときは作業領域だけ作り直せばよい(重みの読み込みより速い)。
// KV キャッシュは捨てるので、次の送信はプロンプトを最初から読ませることになる。
// 会話ごとに大きさが違うので、会話の仕組みが送信の前に呼ぶ(0009)。
// モデルが未読み込みなら、次に読み込むときから効く(false)。確保できなければ元の大きさに戻してエラーを返す。
func (e *Engine) EnsureContextSize(n int) (bool, error) {
	if !e.mu.TryLock() {
		return false, ErrBusy
	}
	defer e.mu.Unlock()

	if e.cfg.NCtx == n {
		return false, nil
	}
	old := e.cfg.NCtx
	e.cfg.NCtx = n
	if e.model == 0 {
		return false, nil
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
			return false, fmt.Errorf("%w(元の大きさにも戻せなかったので、モデルを解放しました: %v)", err, rerr)
		}
		e.lctx = back
		return false, err
	}
	e.lctx = lctx

	info := *e.info.Load() // 読み込み中の情報を写して、コンテキスト長だけ書き換える
	info.NCtx = llama.NCtx(lctx)
	e.info.Store(&info)
	return true, nil
}

// load はモデルとコンテキストを作る。e.mu を持った状態で呼ぶ。
func (e *Engine) load(m models.Entry) (*ModelInfo, error) {
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

	e.lctx = lctx
	// 書式はモデルの種類で選ぶ。GGUF にはモデルの種類が general.architecture として書かれている
	arch, _ := llama.ModelMetaValStr(model, "general.architecture")
	f := format.For(arch)
	e.vocabMu.Lock()
	e.vocab = llama.ModelGetVocab(model)
	e.format, e.arch = f, arch
	e.vocabMu.Unlock()
	e.cached = nil // 新しい作業領域なので KV キャッシュは空
	info := &ModelInfo{
		ID:        m.ID,
		Name:      m.Name,
		Desc:      llama.ModelDesc(model),
		Arch:      arch,
		ParamsB:   float64(llama.ModelNParams(model)) / 1e9,
		SizeGB:    float64(llama.ModelSize(model)) / (1 << 30),
		Layers:    llama.ModelNLayer(model),
		GPULayers: e.cfg.GPULayers,
		NCtx:      llama.NCtx(lctx),
		LoadSec:   time.Since(start).Seconds(),
		path:      m.Path,
	}
	if f != nil {
		info.Format = f.Name()
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
			"設定の会話の上限・返事の上限か、GPU に載せる層の数を減らしてください", e.cfg.NCtx)
	}
	return lctx, nil
}

// entry は、読み込み中のモデルを読み直すための models.Entry を返す。
func (i *ModelInfo) entry() models.Entry {
	return models.Entry{ID: i.ID, Name: i.Name, Path: i.path}
}

// unload はモデルと作業領域を解放する(VRAM が空く)。e.mu を持った状態で呼ぶ。
func (e *Engine) unload() {
	if e.lctx != 0 {
		llama.Free(e.lctx)
		e.lctx = 0
	}
	if e.model != 0 {
		// 数えている途中のトークン化が終わるのを待ってから、語彙ごとモデルを解放する
		e.vocabMu.Lock()
		e.vocab = 0
		e.format = nil
		llama.ModelFree(e.model)
		e.model = 0
		e.vocabMu.Unlock()
	}
	e.cached = nil
	e.info.Store(nil)
}

// CountTokens は、p に書式を当てた文字列(= 実際にモデルに渡す文字列)のトークン数を数える。
// モデルが読み込まれていないと数えられない。
//
// 同じ文章でも、モデル(の語彙と書式)が変わればトークン数は変わる。会話の仕組みは、上限に収まっているかを
// この数で判断する(数を保存して使い回さず、毎回、今のモデルで数え直す)。
// 特別なトークン(<|im_start|> など)は1トークンとして数える(Generate と同じ数え方)。
func (e *Engine) CountTokens(p format.Prompt) (int, error) {
	e.vocabMu.RLock()
	defer e.vocabMu.RUnlock()
	if e.vocab == 0 {
		return 0, ErrNoModel
	}
	if e.format == nil {
		return 0, format.Unsupported(e.arch)
	}
	return len(e.tokenize(e.format.BuildPrompt(p))), nil
}

// ApplyTemplate は、p に書式を当てた文字列(= 実際にモデルに渡す文字列)を返す。モデルが読み込まれていないと当てられない
// (書式はモデルの種類で決まるため)。aibow-server の POST /apply-template が使う(0015)。
func (e *Engine) ApplyTemplate(p format.Prompt) (string, error) {
	e.vocabMu.RLock()
	defer e.vocabMu.RUnlock()
	if e.vocab == 0 {
		return "", ErrNoModel
	}
	if e.format == nil {
		return "", format.Unsupported(e.arch)
	}
	return e.format.BuildPrompt(p), nil
}

// tokenize は文字列をトークン列にする。特別なトークン(<|im_start|> など)は1トークンとして扱う(parseSpecial=true)。
//
// UTF-8 として正しくないバイト列を渡すと、llama.cpp の中で C++ の例外が投げられてプロセスごと落ちることがある
// (Go 側では受け止められない)。保存済みの発言などに紛れ込んでいても落ちないよう、U+FFFD に置き換えてから渡す。
func (e *Engine) tokenize(text string) []llama.Token {
	return llama.Tokenize(e.vocab, strings.ToValidUTF8(text, replacementChar), true, true)
}

// Close はモデルとライブラリを解放する。
func (e *Engine) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.unload()
	llama.Close()
}

// Generate は会話の続き(アシスタントの返事)を生成する。
// 文字が出るたびに onThinking / onContent が呼ばれる。返事全体は、書式で振り分けた形(format.Output)と、
// 生成の記録(GenerateStats)で返す。失敗したときも、そこまでの記録は返す。
// ctx がキャンセルされる(画面で「停止」が押される)と途中で止まる。
func (e *Engine) Generate(
	ctx context.Context,
	p format.Prompt,
	opt GenerateOptions,
	onPrompt func(prompt string),
	onThinking, onContent func(text string),
) (format.Output, GenerateStats, error) {
	var st GenerateStats
	if !e.mu.TryLock() {
		return format.Output{}, st, ErrBusy
	}
	defer e.mu.Unlock()
	if e.model == 0 {
		return format.Output{}, st, ErrNoModel
	}
	if e.format == nil {
		return format.Output{}, st, format.Unsupported(e.arch)
	}

	// 3. 会話 → 文字列(書式を当てる)→ トークン列
	prompt := e.format.BuildPrompt(p)
	onPrompt(prompt)
	tokens := e.tokenize(prompt)
	st.PromptTokens = len(tokens)

	nCtx := int(llama.NCtx(e.lctx))
	if len(tokens) >= nCtx {
		// 会話の仕組み(compact.go)が会話の上限に収めてから渡すので、ふつうはここに来ない
		return format.Output{}, st, fmt.Errorf("会話が長すぎます(%d トークン / 作業領域 %d)。新しい会話で始め直すか、設定の会話の上限を増やしてください", len(tokens), nCtx)
	}

	// 4. プロンプトをモデルに読ませる。KV キャッシュに残っている部分は飛ばす。
	start := time.Now()
	st.ReusedTokens = e.reuseCache(tokens)
	for i := st.ReusedTokens; i < len(tokens); i += batchSize {
		end := min(i+batchSize, len(tokens))
		if err := e.decode(tokens[i:end]); err != nil {
			return format.Output{}, st, err
		}
	}
	st.PromptSec = time.Since(start).Seconds()

	// 5. 1トークンずつ選んで、文字にして、またモデルに読ませる、の繰り返し
	sampler := newSampler(e.vocab, opt.Temperature, opt.PresencePenalty)
	defer llama.SamplerFree(sampler)

	var u8 utf8Buffer
	parser := e.format.NewParser(p)
	send := func(th, c string) {
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

		send(parser.Push(u8.Push(e.tokenBytes(tok, &buf))))
		st.GenTokens++

		// 書式が「ここで止める」と言ったら止める(例: ツールの結果をモデルが自分で書き始めた)
		if parser.Stop() {
			st.StopReason = "stop"
			break
		}

		// 選んだトークンをモデルに読ませて、その次の確率を計算させる
		if err := e.decode([]llama.Token{tok}); err != nil {
			return format.Output{}, st, err
		}
	}
	send(parser.Push(u8.Flush()))
	send(parser.Flush())
	st.GenSec = time.Since(start).Seconds()
	return parser.Result(), st, nil
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
