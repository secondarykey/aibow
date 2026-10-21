package api

// places.go: モデルの置き場所と、表示だけのサーバーの設定(0015 の 8-4)。
//
// llama.cpp の server には無い口なので /aibow/ の下に置く(0015 の 30):
//
//	GET  /aibow/places          置き場所の一覧(models フォルダ・Ollama・足した置き場所)
//	POST /aibow/places/add      {"path"} 置き場所(フォルダか GGUF のファイル)を足す。返事は足したあとの一覧
//	POST /aibow/places/remove   {"path"} 足した置き場所を外す。返事は外したあとの一覧
//	GET  /aibow/settings        表示だけのサーバーの設定(作業領域・ポート・llama.cpp の場所・最初のモデル・設定ファイル)
//
// パスはサーバーの PC のもの。足すときはサーバーがそのパスがあるかを確かめ、無ければ理由を書いたエラーを返す(0015 の 31)。
// 足した置き場所は server-models.json(サーバーが書くファイル)に覚え、手で書く server.json は書き換えない(0015 の 22)。
//
// 置き場所を足す・外すのは生成と取り合わない(作業領域を使わない)ので、順番待ちの席は取らない。
// 読み込み中のモデルの置き場所を外しても、読み込んだモデルはそのまま使える(一覧から消えるだけ)。

import (
	"encoding/json"
	"net/http"

	"github.com/secondarykey/aibow/log"
	"github.com/secondarykey/aibow/models"
)

// Settings は、表示だけのサーバーの設定(画面から変えない。server.json で変え、aibow-server の起動し直しが要る。0015 の 21)。
// 作業領域を変えるとつないでいる全員の KV キャッシュが消え、ポートを変えると画面のアプリのつながりが切れるので、
// 「誰が変えてよいか」と一緒にステップ10で考える。
type Settings struct {
	Port      int    `json:"port"`       // 待ち受けのポート
	NCtx      int    `json:"n_ctx"`      // 作業領域の大きさ
	Model     string `json:"model"`      // 起動のときに読み込むモデル(空なら一覧の先頭から、書式に対応している最初のモデル)
	Lib       string `json:"lib"`        // llama.cpp の DLL のフォルダ
	ModelsDir string `json:"models_dir"` // models フォルダ(手で決める置き場所)
	OllamaDir string `json:"ollama_dir"` // Ollama の保存フォルダ
	// ConfigFile は手で書く設定ファイル(server.json)の場所。ConfigFound はそのファイルがあったか(無ければ環境変数か既定で動いている)
	ConfigFile  string `json:"config_file"`
	ConfigFound bool   `json:"config_found"`
	StateFile   string `json:"state_file"` // aibow-server が書くファイル(server-models.json)
}

// SetSettings は、表示だけのサーバーの設定を決める(_cmd/aibow-server が起動のときに呼ぶ)。作業領域の大きさは New で渡した値。
func (s *Server) SetSettings(v Settings) {
	v.NCtx = s.nCtx
	s.settings = v
}

// placeList は /aibow/places の返事。
type placeList struct {
	Places []models.Place `json:"places"`
}

// placeRequest は /aibow/places/add・/aibow/places/remove の本文。
type placeRequest struct {
	Path string `json:"path"`
}

// handlePlaces は GET /aibow/places。
func (s *Server) handlePlaces(w http.ResponseWriter, r *http.Request) {
	if s.models == nil {
		writeError(w, http.StatusNotFound, "invalid_request_error", "", "このサーバーはモデルの置き場所を持っていません")
		return
	}
	writeJSON(w, http.StatusOK, placeList{Places: s.models.Places()})
}

// handleAddPlace は POST /aibow/places/add。
func (s *Server) handleAddPlace(w http.ResponseWriter, r *http.Request) {
	req, ok := s.readPlaceRequest(w, r)
	if !ok {
		return
	}
	if err := s.models.AddPlace(req.Path); err != nil {
		log.Warn(err.Error())
		writeError(w, http.StatusBadRequest, "invalid_request_error", "", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, placeList{Places: s.models.Places()})
}

// handleRemovePlace は POST /aibow/places/remove。
func (s *Server) handleRemovePlace(w http.ResponseWriter, r *http.Request) {
	req, ok := s.readPlaceRequest(w, r)
	if !ok {
		return
	}
	if err := s.models.RemovePlace(req.Path); err != nil {
		log.Warn(err.Error())
		writeError(w, http.StatusBadRequest, "invalid_request_error", "", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, placeList{Places: s.models.Places()})
}

// readPlaceRequest は本文を読む。モデルの係が無いサーバーなら 404 を返す。
func (s *Server) readPlaceRequest(w http.ResponseWriter, r *http.Request) (placeRequest, bool) {
	var req placeRequest
	if s.models == nil {
		writeError(w, http.StatusNotFound, "invalid_request_error", "", "このサーバーはモデルの置き場所を持っていません")
		return req, false
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "", "リクエストの JSON が読めません: "+err.Error())
		return req, false
	}
	return req, true
}

// handleSettings は GET /aibow/settings。
func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	v := s.settings
	v.NCtx = s.nCtx
	writeJSON(w, http.StatusOK, v)
}
