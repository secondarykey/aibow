package main

// modelhost.go: モデルを扱う係(0015 の 11)。
//
// 画面のアプリがモデルにすること(一覧・読み込み・GPU の層数・作業領域の確保)と、会話の仕組みに渡す生成の口は、
// 直接(アプリの中の engine)と HTTP 越し(aibow-server。api/client)の2つの形がある。ChatService はどちらかを呼ぶだけにして、
// 画面からはどちらの形でも同じに見せる(8-4 のモデルを扱う画面も、この上に作る)。
//
//	直接       … 今までどおり。GPU の層数はアプリの DB(model_gpu_layers)に覚え、作業領域は会話ごとに作り直す(0009)
//	HTTP 越し  … llama.cpp を読み込まない。GPU の層数はサーバーの PC に覚え、作業領域はサーバーが決めた大きさで固定(0015 の 3・9)

import (
	"errors"
	"fmt"
	"maps"

	"github.com/secondarykey/aibow"
	apiclient "github.com/secondarykey/aibow/api/client"
	"github.com/secondarykey/aibow/engine"
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
}

// hostModels は list の中身。
type hostModels struct {
	models  []models.Entry
	current *engine.ModelInfo
	gpu     bool
}

// ---- 直接(アプリの中の engine) --------------------------------------------------------

type directHost struct {
	s       *ChatService // GPU の層数(アプリの DB)を読み書きするため
	eng     *engine.Engine
	catalog models.Catalog
}

func (h *directHost) generator() aibow.Generator { return h.eng }
func (h *directHost) info() *engine.ModelInfo    { return h.eng.Info() }
func (h *directHost) server() string             { return "" }

func (h *directHost) list() (hostModels, error) {
	return hostModels{models: h.catalog.List(), current: h.eng.Info(), gpu: h.eng.HasGPU}, nil
}

// load は、そのモデルに覚えた層の数(覚えていなければ全体の値。0014 の 4)で読み込む。
func (h *directHost) load(id string) (*engine.ModelInfo, error) {
	m, err := h.catalog.Find(id)
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
	h.s.appMu.Lock()
	layers := maps.Clone(h.s.app.Workspace.ModelGPULayers)
	h.s.appMu.Unlock()
	if layers == nil {
		layers = map[string]int{}
	}
	layers[i.ID] = n
	h.s.saveAppSetting(aibow.SettingModelGPULayers, layers, func(a *aibow.AppSettings) { a.Workspace.ModelGPULayers = layers })
	return i, nil
}

func (h *directHost) tryContext(n int) (bool, error) {
	changed, err := h.eng.EnsureContextSize(n)
	if err != nil {
		return false, fmt.Errorf("上限を下げるか、GPU に載せる層の数を減らしてください: %w", err)
	}
	return changed, nil
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
	return hostModels{models: l.Models, current: l.Current, gpu: l.GPU}, nil
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

// tryContext は、サーバーの作業領域に入るかを確かめるだけ(作り直しは頼めない。0015 の 3)。
func (h *remoteHost) tryContext(n int) (bool, error) {
	return h.c.EnsureContextSize(n)
}
