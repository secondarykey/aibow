package api

// models.go: モデルの一覧・読み込み・GPU の層数(ステップ8。0015)。
//
// 0012 では「使うモデルは起動の引数で決め、リクエストの model は見ない」にしていた。
// ステップ8で画面のアプリもここにつなぐので、会話ごとのモデル(0014 の 3)に合わせて、
// リクエストの model が読み込み中のモデルと違えば読み直すようにした。
//
// 口は、意味が同じものは llama.cpp の server(router モード)の名前にそろえ、無いものだけ /aibow/ の下に置く(0015。2026-10-21 にユーザーと決めた):
//
//	GET  /models              使えるモデルの一覧(読み込み中か・GPU の層数つき)。llama.cpp と同じく status.value で状態を返す
//	POST /models/load         {"model"} を明示的に読み込む(送信の前に読み込んで、読み込み中のお知らせとログを出すため)
//	POST /models/unload       {"model"} 読み込み中のモデルを降ろす(VRAM を空ける。8-4)
//	POST /aibow/gpu-layers    {"model", "n_gpu_layers"} モデルごとの GPU の層数を変えて覚える(読み込み中のモデルなら読み直す)
//
// モデルの置き場所と、表示だけのサーバーの設定の口は places.go(8-4)。
//
// 読み込み・読み直しは生成と同じ順番待ちの席の中で行う(作業領域が1つなので、生成と取り合わない)。

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/secondarykey/aibow/engine"
	"github.com/secondarykey/aibow/log"
	"github.com/secondarykey/aibow/models"
)

// Models は、モデルの一覧と読み込みの係。本物は _cmd/aibow-server が作る(GPU の層数をサーバーの PC のファイルに覚える)。
// nil なら、読み込み中のモデル1つだけを扱う(0012 の形)。
type Models interface {
	// List は使えるモデルの一覧
	List() []models.Entry
	// Find は、ID・一覧の名前(qwen3.5:4b)・GGUF のパスからモデルを探す
	Find(name string) (models.Entry, error)
	// Load はモデルを、そのモデルに覚えた GPU の層数で読み込む
	Load(m models.Entry) (*engine.ModelInfo, error)
	// GPULayers は、そのモデルを読み込むときの GPU の層数(覚えていなければ全体の値)
	GPULayers(id string) int
	// SetGPULayers は、モデルの GPU の層数を変えて覚える。読み込み中のモデルなら読み直す(そのときだけ情報を返す)
	SetGPULayers(id string, n int) (*engine.ModelInfo, error)
	// HasGPU は GPU が見つかったか
	HasGPU() bool

	// Unload は読み込み中のモデルを降ろす(VRAM を空ける)。降ろしたら true(読み込んでいなければ false)
	Unload() (bool, error)
	// Places はモデルの置き場所の一覧(手で決めた場所と、画面で足した場所。8-4)
	Places() []models.Place
	// AddPlace は置き場所(フォルダか GGUF のファイル)を足して覚える。path はサーバーの PC のパス。無ければ理由を書いたエラー
	AddPlace(path string) error
	// RemovePlace は足した置き場所を外す(覚えたものから消す)。手で決めた場所は外せない
	RemovePlace(path string) error
}

// ---- JSON の形 ------------------------------------------------------------------------

// serverModels は GET /models の返事。
type serverModels struct {
	Object string        `json:"object"` // いつも "list"
	Data   []serverModel `json:"data"`
	// Current は読み込み中のモデル(無ければ null)。NCtx はサーバーの作業領域の大きさ(起動のときに決めた大きさで固定。0015 の 3)
	Current *engine.ModelInfo `json:"current"`
	NCtx    int               `json:"n_ctx"`
	GPU     bool              `json:"gpu"`
}

// serverModel は一覧のモデル1つ。models.Entry の欄(id・name・source・size_gb)に、状態と GPU の層数を足す。
type serverModel struct {
	models.Entry
	Status     modelStatus `json:"status"`
	NGPULayers int         `json:"n_gpu_layers"`
}

// modelStatus はモデルの状態。llama.cpp の router と同じく {"value": "loaded" | "unloaded"}(読み込み中・失敗は持たない)
type modelStatus struct {
	Value string `json:"value"`
}

// loadRequest は POST /models/load と POST /aibow/gpu-layers の本文。
type loadRequest struct {
	Model      string `json:"model"`
	NGPULayers *int   `json:"n_gpu_layers"` // /aibow/gpu-layers だけで使う
}

// loadResult は POST /models/load と POST /aibow/gpu-layers の返事。llama.cpp と同じ success に、読み込んだモデルの情報を足す。
type loadResult struct {
	Success bool              `json:"success"`
	Model   *engine.ModelInfo `json:"model"` // 読み込んだ(読み直した)モデル。層の数を覚えただけなら null
}

// ---- 口 --------------------------------------------------------------------------------

// handleServerModels は GET /models。
func (s *Server) handleServerModels(w http.ResponseWriter, r *http.Request) {
	info := s.gen.Info()
	out := serverModels{Object: "list", Data: []serverModel{}, Current: info, NCtx: s.nCtx}
	if s.models == nil {
		writeError(w, http.StatusNotFound, "invalid_request_error", "", "このサーバーはモデルの一覧を持っていません")
		return
	}
	out.GPU = s.models.HasGPU()
	for _, m := range s.models.List() {
		st := "unloaded"
		if info != nil && info.ID == m.ID {
			st = "loaded"
		}
		out.Data = append(out.Data, serverModel{Entry: m, Status: modelStatus{Value: st}, NGPULayers: s.models.GPULayers(m.ID)})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleLoad は POST /models/load。読み込み中のモデルと同じなら何もしない。
func (s *Server) handleLoad(w http.ResponseWriter, r *http.Request) {
	req, ok := s.readLoadRequest(w, r)
	if !ok {
		return
	}
	if !s.takeTurn(r) {
		return
	}
	defer s.releaseTurn()
	m, err := s.models.Find(req.Model)
	if err != nil {
		writeError(w, http.StatusNotFound, "invalid_request_error", "model_not_found", err.Error())
		return
	}
	info, err := s.loadModel(m)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server_error", "", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, loadResult{Success: true, Model: info})
}

// handleUnload は POST /models/unload。model が空か読み込み中のモデルなら降ろす。読み込んでいない別のモデルなら何もしない
// (llama.cpp の router は、モデルごとのプロセスを止める口。aibow は1つしか読み込まないので、降ろすのは読み込み中のモデルだけ)。
func (s *Server) handleUnload(w http.ResponseWriter, r *http.Request) {
	req, ok := s.readLoadRequest(w, r)
	if !ok {
		return
	}
	if !s.takeTurn(r) {
		return
	}
	defer s.releaseTurn()
	info := s.gen.Info()
	if info == nil {
		writeJSON(w, http.StatusOK, loadResult{Success: true})
		return
	}
	if req.Model != "" && req.Model != info.ID {
		if m, err := s.models.Find(req.Model); err != nil || m.ID != info.ID {
			log.Infof("モデル %s は読み込んでいないので、降ろしません(読み込み中: %s)", req.Model, info.ID)
			writeJSON(w, http.StatusOK, loadResult{Success: true})
			return
		}
	}
	if _, err := s.models.Unload(); err != nil {
		log.Error(err.Error())
		writeError(w, http.StatusInternalServerError, "server_error", "", err.Error())
		return
	}
	log.Noticef("モデルを降ろしました: %s(VRAM を空けました。次のリクエストで読み込みます)", info.ID)
	writeJSON(w, http.StatusOK, loadResult{Success: true})
}

// handleGPULayers は POST /aibow/gpu-layers。model が空なら読み込み中のモデル。
// GPU の層数は「その PC の人が決めること」なので、サーバーの PC に覚える(0015 の 9)。
func (s *Server) handleGPULayers(w http.ResponseWriter, r *http.Request) {
	req, ok := s.readLoadRequest(w, r)
	if !ok {
		return
	}
	if req.NGPULayers == nil || *req.NGPULayers < 0 {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "", "n_gpu_layers を 0 以上で書いてください")
		return
	}
	if !s.takeTurn(r) {
		return
	}
	defer s.releaseTurn()
	id := req.Model
	if id == "" {
		info := s.gen.Info()
		if info == nil {
			writeError(w, http.StatusBadRequest, "invalid_request_error", "", engine.ErrNoModel.Error())
			return
		}
		id = info.ID
	}
	m, err := s.models.Find(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "invalid_request_error", "model_not_found", err.Error())
		return
	}
	info, err := s.models.SetGPULayers(m.ID, *req.NGPULayers)
	if err != nil {
		log.Error(err.Error())
		writeError(w, http.StatusInternalServerError, "server_error", "", err.Error())
		return
	}
	if info != nil {
		log.Noticef("GPU に載せる層を %d にして読み直しました: %s(%.1f 秒)", *req.NGPULayers, info.ID, info.LoadSec)
	} else {
		log.Noticef("モデル %s の GPU に載せる層を %d として覚えました(次に読み込むときから効きます)", m.ID, *req.NGPULayers)
	}
	writeJSON(w, http.StatusOK, loadResult{Success: true, Model: info})
}

// readLoadRequest は本文を読む。モデルの係が無いサーバーなら 404 を返す。
func (s *Server) readLoadRequest(w http.ResponseWriter, r *http.Request) (loadRequest, bool) {
	var req loadRequest
	if s.models == nil {
		writeError(w, http.StatusNotFound, "invalid_request_error", "", "このサーバーはモデルを切り替えられません")
		return req, false
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "", "リクエストの JSON が読めません: "+err.Error())
		return req, false
	}
	return req, true
}

// loadModel は、読み込み中のモデルと違えば m を読み込む。順番待ちの席を持った状態で呼ぶ。
func (s *Server) loadModel(m models.Entry) (*engine.ModelInfo, error) {
	if info := s.gen.Info(); info != nil && info.ID == m.ID {
		return info, nil
	}
	log.Infof("モデルを読み込み中: %s(GPU に載せる層: %d)", m.ID, s.models.GPULayers(m.ID))
	info, err := s.models.Load(m)
	if err != nil {
		log.Error(err.Error())
		return nil, fmt.Errorf("モデル %s を読み込めません: %w", m.ID, err)
	}
	log.Noticef("モデルを読み込みました: %s(%s・書式 %s・作業領域 %d トークン・GPU %d 層 / %d 層・%.1f 秒)",
		info.ID, info.Desc, info.Format, info.NCtx, min(info.GPULayers, int(info.Layers)), info.Layers, info.LoadSec)
	return info, nil
}

// requestedModel は、チャットのリクエストの model を読み込む(違えば読み直す。0015 の 2)。順番待ちの席を持った状態で呼ぶ。
// 知らない名前なら、読み込み中のモデルを使う(Cline などがおかしな名前を送ってきても動くように)。
func (s *Server) requestedModel(name string) error {
	info := s.gen.Info()
	if name == "" || (info != nil && name == info.ID) {
		return nil
	}
	if s.models == nil {
		log.Infof("リクエストのモデル %q ではなく、読み込み中のモデルを使います(このサーバーはモデルを切り替えません)", name)
		return nil
	}
	m, err := s.models.Find(name)
	if err != nil {
		if info != nil {
			log.Infof("リクエストのモデル %q が見つからないので、読み込み中の %s を使います", name, info.ID)
		}
		return nil
	}
	_, err = s.loadModel(m)
	return err
}

// modelObjects は GET /v1/models の data。モデルの係があれば使えるモデル全部、無ければ読み込み中のモデルだけ。
func (s *Server) modelObjects() []modelObject {
	data := []modelObject{}
	created := s.now().Unix()
	if s.models != nil {
		for _, m := range s.models.List() {
			data = append(data, modelObject{ID: m.ID, Object: "model", Created: created, OwnedBy: "aibow"})
		}
		return data
	}
	if info := s.gen.Info(); info != nil {
		data = append(data, modelObject{ID: info.ID, Object: "model", Created: created, OwnedBy: "aibow"})
	}
	return data
}
