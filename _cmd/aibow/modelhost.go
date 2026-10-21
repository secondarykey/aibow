package main

// modelhost.go: モデルを扱う係(0015 の 11)。
//
// 画面のアプリがモデルにすること(一覧・読み込み・GPU の層数・作業領域の確保)と、会話の仕組みに渡す生成の口は、
// 直接(アプリの中の engine)と HTTP 越し(aibow-server。api/client)の2つの形がある。ChatService はどちらかを呼ぶだけにして、
// 画面からはどちらの形でも同じに見せる(8-4 のモデルを扱う画面も、この上に作る)。
//
//	直接       … 今までどおり。GPU の層数はアプリの DB(model_gpu_layers)に覚え、作業領域は会話ごとに作り直す(0009)。
//	             画面で足したモデルの置き場所は手元の model-places.json(modelplaces.go)
//	HTTP 越し  … llama.cpp を読み込まない。GPU の層数と置き場所はサーバーの PC に覚え、作業領域はサーバーが決めた大きさで固定(0015 の 3・9)
//
// 8-4(モデルを扱う画面)で、降ろす・モデルごとの層の数・置き場所を足す / 外す・表示だけの設定を足した。

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/secondarykey/aibow"
	apiclient "github.com/secondarykey/aibow/api/client"
	"github.com/secondarykey/aibow/engine"
	"github.com/secondarykey/aibow/log"
	"github.com/secondarykey/aibow/models"
)

type modelHost interface {
	// generator は会話の仕組みに渡す生成の口(*engine.Engine か *client.Client)
	generator() aibow.Generator
	// list は使えるモデルの一覧と、読み込み中のモデル・GPU があるか。HTTP 越しでつながらなければエラー
	list() (hostModels, error)
	// info は読み込み中のモデル(知らなければ nil)。聞きに行かない
	info() *engine.ModelInfo
	// load はモデルを読み込む。GPU の層数は、そのモデルに覚えた値
	load(id string) (*engine.ModelInfo, error)
	// gpuLayers は、設定の画面の「基本設定」に出す層の数(読み込み中のモデルの値)
	gpuLayers() int
	// setGPULayers は、読み込み中のモデルの層の数を変えて読み直し、覚える。モデルが無ければ nil を返す
	setGPULayers(n int) (*engine.ModelInfo, error)
	// tryContext は、n トークンの作業領域を使えるかを確かめる(直接なら作り直してみる)。作り直したら true
	tryContext(n int) (bool, error)
	// server はつなぎ先(直接なら空)。画面の吹き出しとログに出す
	server() string

	// unload は読み込み中のモデルを降ろす(VRAM を空ける。0015 の 23)。次の送信の前に会話のモデルを読み込む
	unload() error
	// setModelGPULayers は、モデルごとの GPU の層数を変えて覚える。読み込み中のモデルなら読み直す(そのときだけ情報を返す)
	setModelGPULayers(id string, n int) (*engine.ModelInfo, error)
	// places はモデルの置き場所の一覧。addPlace / removePlace は、足した置き場所を足す / 外して、そのあとの一覧を返す
	places() ([]models.Place, error)
	addPlace(path string) ([]models.Place, error)
	removePlace(path string) ([]models.Place, error)
	// settings は表示だけの設定(作業領域・ポート・llama.cpp の場所・最初のモデル。画面からは変えない。0015 の 21)
	settings() (apiclient.ServerSettings, error)
}

// hostModels は list の中身。
type hostModels struct {
	models  []models.Entry
	current *engine.ModelInfo
	gpu     bool
	layers  map[string]int // モデルの ID → 読み込むときの GPU の層数
}

// ---- 直接(アプリの中の engine) --------------------------------------------------------

type directHost struct {
	s       *ChatService // GPU の層数(アプリの DB)を読み書きするため
	eng     *engine.Engine
	catalog models.Catalog // 手で決めた置き場所(環境変数)。足した置き場所は added にあり、catalogNow で合わせる
	added   *placesFile
	missing models.MissingWatch

	// lib と initialModel は、表示だけの設定のため(AIBOW_LIB・AIBOW_MODEL)
	lib, initialModel string
}

func (h *directHost) generator() aibow.Generator { return h.eng }
func (h *directHost) info() *engine.ModelInfo    { return h.eng.Info() }
func (h *directHost) server() string             { return "" }

// catalogNow は、手で決めた置き場所に、画面で足した置き場所を合わせた一覧の係。
func (h *directHost) catalogNow() models.Catalog {
	c := h.catalog
	c.Added = h.added.list()
	return c
}

// list は一覧を作る。見つからない足した置き場所は一覧に出さず、見つからなくなったときに1回だけ Warn のログを出す(0015 の 28)。
func (h *directHost) list() (hostModels, error) {
	c := h.catalogNow()
	lost, found := h.missing.Check(c)
	logPlaces(lost, found)
	l := hostModels{models: c.List(), current: h.eng.Info(), gpu: h.eng.HasGPU, layers: map[string]int{}}
	h.s.appMu.Lock()
	for _, m := range l.models {
		l.layers[m.ID] = h.s.app.Workspace.LayersFor(m.ID)
	}
	h.s.appMu.Unlock()
	return l, nil
}

// load は、そのモデルに覚えた層の数(覚えていなければ全体の値。0014 の 4)で読み込む。
func (h *directHost) load(id string) (*engine.ModelInfo, error) {
	m, err := h.catalogNow().Find(id)
	if err != nil {
		return nil, err
	}
	h.s.appMu.Lock()
	layers := h.s.app.Workspace.LayersFor(m.ID)
	h.s.appMu.Unlock()
	return h.eng.LoadModelWithLayers(m, layers)
}

func (h *directHost) gpuLayers() int {
	h.s.appMu.Lock()
	defer h.s.appMu.Unlock()
	if i := h.eng.Info(); i != nil {
		return h.s.app.Workspace.LayersFor(i.ID)
	}
	return h.s.app.Workspace.GPULayers
}

// setGPULayers は読み直し、読み込み中のモデルの値として DB に覚える。モデルが無ければ、まだ覚えていないモデルに使う全体の値を変える。
func (h *directHost) setGPULayers(n int) (*engine.ModelInfo, error) {
	i, err := h.eng.SetGPULayers(n)
	if err != nil {
		return nil, err
	}
	if i == nil {
		h.s.saveAppSetting(aibow.SettingGPULayers, n, func(a *aibow.AppSettings) { a.Workspace.GPULayers = n })
		return nil, nil
	}
	h.rememberLayers(i.ID, n)
	return i, nil
}

// setModelGPULayers は、読み込み中のモデルなら読み直して覚え、ほかのモデルなら覚えるだけ(次に読み込むときから効く)。
func (h *directHost) setModelGPULayers(id string, n int) (*engine.ModelInfo, error) {
	if i := h.eng.Info(); i != nil && i.ID == id {
		return h.setGPULayers(n)
	}
	if _, err := h.catalogNow().Find(id); err != nil {
		return nil, err
	}
	h.rememberLayers(id, n)
	return nil, nil
}

// rememberLayers は、モデルの GPU の層数をアプリの DB に覚える(0014 の 4。DB からファイルに移すかは TODO に記録だけした)。
func (h *directHost) rememberLayers(id string, n int) {
	h.s.appMu.Lock()
	layers := maps.Clone(h.s.app.Workspace.ModelGPULayers)
	h.s.appMu.Unlock()
	if layers == nil {
		layers = map[string]int{}
	}
	layers[id] = n
	h.s.saveAppSetting(aibow.SettingModelGPULayers, layers, func(a *aibow.AppSettings) { a.Workspace.ModelGPULayers = layers })
}

func (h *directHost) tryContext(n int) (bool, error) {
	changed, err := h.eng.EnsureContextSize(n)
	if err != nil {
		return false, fmt.Errorf("上限を下げるか、GPU に載せる層の数を減らしてください: %w", err)
	}
	return changed, nil
}

func (h *directHost) unload() error {
	_, err := h.eng.UnloadModel()
	return err
}

func (h *directHost) places() ([]models.Place, error) { return h.catalogNow().Places(), nil }

// addPlace は置き場所を確かめて(あるか・フォルダか GGUF か・まだ入っていないか)、model-places.json に覚える。
func (h *directHost) addPlace(path string) ([]models.Place, error) {
	c := h.catalogNow()
	abs, err := c.CheckPlace(path)
	if err != nil {
		return nil, err
	}
	if err := h.added.set(append(c.Added, abs)); err != nil {
		return nil, err
	}
	log.Noticef("モデルの置き場所を足しました: %s", abs)
	return h.places()
}

// removePlace は足した置き場所を外す。手で決めた置き場所(AIBOW_MODELS_DIR・OLLAMA_MODELS)は外せない(0015 の 26)。
func (h *directHost) removePlace(path string) ([]models.Place, error) {
	c := h.catalogNow()
	p, ok := c.IsAdded(path)
	if !ok {
		return nil, fmt.Errorf("%s は画面で足した置き場所ではないので外せません(models フォルダと Ollama の保存先は環境変数 AIBOW_MODELS_DIR・OLLAMA_MODELS で変えます)", path)
	}
	if err := h.added.set(slices.DeleteFunc(c.Added, func(s string) bool { return s == p })); err != nil {
		return nil, err
	}
	log.Noticef("モデルの置き場所を外しました: %s", p)
	return h.places()
}

// settings は、直接の形の表示だけの設定。作業領域は会話ごと(会話の上限 + 返事の上限)なので 0、ポートは無い(待ち受けない)。
func (h *directHost) settings() (apiclient.ServerSettings, error) {
	return apiclient.ServerSettings{
		Model: h.initialModel, Lib: absPath(h.lib),
		ModelsDir: absPath(h.catalog.ModelsDir), OllamaDir: h.catalog.OllamaDir,
	}, nil
}

// logPlaces は、見つからなくなった置き場所と、また見つかった置き場所をログに出す(0015 の 28)。
func logPlaces(lost, found []string) {
	for _, p := range lost {
		log.Warnf("モデルの置き場所が見つからないので、そのモデルを一覧に出しません: %s(覚えたまま残します。つなぎ直せば一覧に戻ります)", p)
	}
	for _, p := range found {
		log.Noticef("モデルの置き場所がまた見つかりました: %s", p)
	}
}

// ---- HTTP 越し(aibow-server) ------------------------------------------------------------

type remoteHost struct {
	c *apiclient.Client
}

func (h *remoteHost) generator() aibow.Generator { return h.c }
func (h *remoteHost) info() *engine.ModelInfo    { return h.c.Info() }
func (h *remoteHost) server() string             { return h.c.URL() }

func (h *remoteHost) list() (hostModels, error) {
	l, err := h.c.Models()
	if err != nil {
		return hostModels{}, err
	}
	return hostModels{models: l.Models, current: l.Current, gpu: l.GPU, layers: l.GPULayers}, nil
}

// load はサーバーに読み込ませる。GPU の層数はサーバーの PC に覚えた値(0015 の 9)。
func (h *remoteHost) load(id string) (*engine.ModelInfo, error) {
	return h.c.LoadModel(id)
}

// gpuLayers は、サーバーが読み込み中のモデルを読み込んだときの層の数(サーバーの PC の値)。
func (h *remoteHost) gpuLayers() int {
	if i := h.c.Info(); i != nil {
		return i.GPULayers
	}
	return 0
}

// setGPULayers はサーバーに読み直させ、サーバーの PC に覚えさせる。アプリの DB の model_gpu_layers は使わない(消さない。0015 の 9)。
func (h *remoteHost) setGPULayers(n int) (*engine.ModelInfo, error) {
	if h.c.Info() == nil {
		return nil, errors.New("aibow-server が読み込んでいるモデルが分かりません。先にモデルを選んでください")
	}
	return h.c.SetGPULayers("", n)
}

// setModelGPULayers はサーバーに覚えさせる(読み込み中のモデルならサーバーが読み直す)。
func (h *remoteHost) setModelGPULayers(id string, n int) (*engine.ModelInfo, error) {
	return h.c.SetGPULayers(id, n)
}

// tryContext は、サーバーの作業領域に入るかを確かめるだけ(作り直しは頼めない。0015 の 3)。
func (h *remoteHost) tryContext(n int) (bool, error) {
	return h.c.EnsureContextSize(n)
}

// unload は、aibow-server 越しではしない(2026-10-22、ユーザーと決めた)。会話に結びつかない降ろすは、サーバーを使っている
// ほかの利用者と取り合いになるだけなので。サーバーの PC で VRAM を空けたいときは、aibow-server の POST /models/unload を手で呼ぶ
func (h *remoteHost) unload() error {
	return errors.New("aibow-server につないでいるときは、画面からモデルを降ろせません(ほかの利用者が使っているモデルも降ろしてしまうため)。" +
		"サーバーの PC で aibow-server の POST /models/unload を呼んでください")
}

// places・addPlace・removePlace のパスはサーバーの PC のもの(0015 の 31)。足すときはサーバーがあるかを確かめる。
func (h *remoteHost) places() ([]models.Place, error)                 { return h.c.Places() }
func (h *remoteHost) addPlace(path string) ([]models.Place, error)    { return h.c.AddPlace(path) }
func (h *remoteHost) removePlace(path string) ([]models.Place, error) { return h.c.RemovePlace(path) }

func (h *remoteHost) settings() (apiclient.ServerSettings, error) { return h.c.Settings() }
