package ui

// serverhost.go: aibow-server の中で、ブラウザの会話のためにモデルを扱う係(ステップ9。0016)。
//
//	ブラウザ ──HTTP──→ aibow-server [ 画面用の口(web.go)→ Service → ServerHost → engine ]
//	Cline など ──HTTP──→ aibow-server [ OpenAI 互換の口(api)───────────────────→ engine ]   ← 順番待ちの席は1つ(Seat)
//
// 生成は aibow-server の engine を直接使う(HTTP で自分自身に頼まない。0016 の 11)。
// 見せ方と断り方は、aibow-server につないだ画面のアプリ(RemoteHost)と同じ線にそろえる(0016 の 6)。
// ブラウザの人はサーバーの PC の持ち主ではないので、降ろす・GPU の層数・置き場所・出どころ・サーバーの PC のパスは見せも変えもしない。
// 見せるのはモデルの一覧(名前・大きさ・読み込み中か)と作業領域の大きさ。会話のモデルは選べる(席の中で読み直す)。

import (
	"context"

	"github.com/secondarykey/aibow"
	apiclient "github.com/secondarykey/aibow/api/client"
	"github.com/secondarykey/aibow/engine"
	"github.com/secondarykey/aibow/format"
	"github.com/secondarykey/aibow/models"
)

// ServerEngine は ServerHost が使う生成の部分。本物は *engine.Engine。テストでは偽物に差し替える。
// 作業領域の大きさは変えないので、aibow.Generator の EnsureContextSize は持たない(fixedContext が代わりに確かめる)。
type ServerEngine interface {
	Generate(ctx context.Context, p format.Prompt, opt engine.GenerateOptions,
		onPrompt func(prompt string), onThinking, onContent func(text string)) (format.Output, engine.GenerateStats, error)
	CountTokens(p format.Prompt) (int, error)
	Info() *engine.ModelInfo
}

// ServerModels は、aibow-server のモデルの係(_cmd/aibow-server の modelManager。api.Models と同じもの)のうち、ServerHost が使うもの。
type ServerModels interface {
	List() []models.Entry
	Find(name string) (models.Entry, error)
	// Load は、そのモデルにサーバーの PC で覚えた GPU の層数で読み込む
	Load(m models.Entry) (*engine.ModelInfo, error)
	HasGPU() bool
}

// ServerHost は、aibow-server の engine でブラウザの会話のモデルを扱う係。
type ServerHost struct {
	gen    fixedContext
	models ServerModels
	// url は aibow-server のアドレス。ブラウザの画面には、サーバー越しの画面のアプリと同じく「つなぎ先」として出す
	url string
}

// NewServerHost は aibow-server の中で使う係を作る。nCtx はサーバーが起動のときに決めた作業領域の大きさ(0016 の 12)。
func NewServerHost(eng ServerEngine, mm ServerModels, nCtx int, url string) *ServerHost {
	return &ServerHost{gen: fixedContext{ServerEngine: eng, nCtx: nCtx}, models: mm, url: url}
}

func (h *ServerHost) generator() aibow.Generator { return h.gen }
func (h *ServerHost) info() *engine.ModelInfo    { return h.gen.Info() }

// server は aibow-server のアドレス。空でないので、画面はサーバー越しの画面のアプリと同じ見せ方になる(モデルのタブはつなぎ先と作業領域だけ)。
func (h *ServerHost) server() string { return h.url }

// list は、サーバーの PC の置き方(出どころ・置き場所)と層の数を外した一覧(RemoteHost と同じ)。
func (h *ServerHost) list() (hostModels, error) {
	l := h.models.List()
	for i := range l {
		l[i].Origin, l[i].Place = "", ""
	}
	return hostModels{models: l, current: h.gen.Info(), gpu: h.models.HasGPU()}, nil
}

// load は、読み込み中のモデルと違えば読み込む。順番待ちの席は Service が持ってから呼ぶ(送信の前の読み直しも、入力欄の下で選んだときも)。
func (h *ServerHost) load(id string) (*engine.ModelInfo, error) {
	m, err := h.models.Find(id)
	if err != nil {
		return nil, err
	}
	if i := h.gen.Info(); i != nil && i.ID == m.ID {
		return i, nil
	}
	return h.models.Load(m)
}

// gpuLayers は、読み込み中のモデルを読み込んだときの層の数(サーバーの PC の値。RemoteHost と同じ)。
func (h *ServerHost) gpuLayers() int {
	if i := h.gen.Info(); i != nil {
		return i.GPULayers
	}
	return 0
}

// tryContext は、サーバーの作業領域に入るかを確かめるだけ。
func (h *ServerHost) tryContext(n int) (bool, error) { return h.gen.EnsureContextSize(n) }

// 降ろす・GPU の層数・置き場所は、サーバーの PC の事情なのでブラウザからはしない(0016 の 6。口にも出さない。web.go)。
func (h *ServerHost) setGPULayers(int) (*engine.ModelInfo, error) {
	return nil, errServerSide("GPU に載せる層の数")
}
func (h *ServerHost) setModelGPULayers(string, int) (*engine.ModelInfo, error) {
	return nil, errServerSide("GPU に載せる層の数")
}
func (h *ServerHost) unload() error { return errServerSide("モデルを降ろすこと") }
func (h *ServerHost) places() ([]models.Place, error) {
	return nil, errServerSide("モデルの置き場所")
}
func (h *ServerHost) addPlace(string) ([]models.Place, error) {
	return nil, errServerSide("モデルの置き場所")
}
func (h *ServerHost) removePlace(string) ([]models.Place, error) {
	return nil, errServerSide("モデルの置き場所")
}

// settings は作業領域の大きさだけ(会話の上限 + 返事の上限がこれに入る必要があるので、画面に要る。RemoteHost と同じ)。
func (h *ServerHost) settings() (apiclient.ServerSettings, error) {
	return apiclient.ServerSettings{NCtx: h.gen.nCtx}, nil
}

// fixedContext は、作業領域の大きさを変えない生成の口(会話の仕組みに渡す aibow.Generator)。
//
// 会話の仕組みは送信の前に「作業領域を会話の上限 + 返事の上限の大きさにして」と頼む(EnsureContextSize)。
// 画面のアプリの中ならその大きさに作り直すが、aibow-server は Cline などと作業領域を分け合っているので、作り直すとほかの人の
// KV キャッシュが消える。なので入るかを確かめるだけにする(aibow-server につないだ画面のアプリの api/client と同じ。0015 の 3)。
type fixedContext struct {
	ServerEngine
	nCtx int
}

func (g fixedContext) EnsureContextSize(n int) (bool, error) {
	if n > g.nCtx {
		return false, &apiclient.ContextError{Need: n, Have: g.nCtx}
	}
	return false, nil
}
