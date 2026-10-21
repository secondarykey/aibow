package client

// models.go: モデルの一覧・読み込み・GPU の層数・作業領域・トークン数(生成以外のやり取り)。

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/secondarykey/aibow/engine"
	"github.com/secondarykey/aibow/format"
	"github.com/secondarykey/aibow/models"
)

// ModelList は GET /models の中身(サーバーで使えるモデルと、サーバーの様子)。
type ModelList struct {
	Models    []models.Entry    // 使えるモデル(Path はサーバーの PC の場所なので来ない)
	GPULayers map[string]int    // モデルの ID → 読み込むときの GPU の層数(サーバーの PC に覚えた値)
	Current   *engine.ModelInfo // 読み込み中のモデル(無ければ nil)
	NCtx      int               // サーバーの作業領域の大きさ(起動のときに決めた大きさで固定)
	GPU       bool              // サーバーで GPU が見つかったか
}

// serverModels は GET /models の JSON(api の serverModels と同じ形)。
type serverModels struct {
	Data []struct {
		models.Entry
		NGPULayers int `json:"n_gpu_layers"`
	} `json:"data"`
	Current *engine.ModelInfo `json:"current"`
	NCtx    int               `json:"n_ctx"`
	GPU     bool              `json:"gpu"`
}

// loadResult は POST /models/load と POST /aibow/gpu-layers の返事。
type loadResult struct {
	Model *engine.ModelInfo `json:"model"`
}

// Models はサーバーで使えるモデルの一覧を聞く。ついでに、読み込み中のモデルと作業領域の大きさを覚え直す。
func (c *Client) Models() (ModelList, error) {
	var sm serverModels
	if err := c.call(http.MethodGet, "/models", nil, &sm, shortTimeout); err != nil {
		return ModelList{}, err
	}
	l := ModelList{Models: []models.Entry{}, GPULayers: map[string]int{}, Current: sm.Current, NCtx: sm.NCtx, GPU: sm.GPU}
	for _, m := range sm.Data {
		l.Models = append(l.Models, m.Entry)
		l.GPULayers[m.ID] = m.NGPULayers
	}
	c.mu.Lock()
	c.info, c.nCtx = sm.Current, sm.NCtx
	c.mu.Unlock()
	return l, nil
}

// LoadModel は、モデルを読み込ませる(読み込み中のモデルと同じなら、サーバーは何もしない)。
// 会話の仕組みが送信の前に呼ぶ(aibow.ModelLoader)。これからの生成は、このモデルで頼む。
func (c *Client) LoadModel(id string) (*engine.ModelInfo, error) {
	var r loadResult
	if err := c.call(http.MethodPost, "/models/load", map[string]string{"model": id}, &r, loadTimeout); err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.info, c.model = r.Model, id
	c.mu.Unlock()
	return r.Model, nil
}

// SetGPULayers は、モデル(空なら読み込み中のモデル)の GPU の層数を変え、サーバーの PC に覚えさせる。
// 読み込み中のモデルならサーバーが読み直し、その情報を返す(読み込んでいないモデルなら nil)。
func (c *Client) SetGPULayers(id string, n int) (*engine.ModelInfo, error) {
	var r loadResult
	body := map[string]any{"model": id, "n_gpu_layers": n}
	if err := c.call(http.MethodPost, "/aibow/gpu-layers", body, &r, loadTimeout); err != nil {
		return nil, err
	}
	if r.Model != nil {
		c.mu.Lock()
		c.info = r.Model
		c.mu.Unlock()
	}
	return r.Model, nil
}

// UnloadModel は、サーバーが読み込んでいるモデルを降ろさせる(サーバーの PC の VRAM を空ける。8-4)。
// 次の生成の前に、会話の仕組みが会話のモデルを読み込ませる(LoadModel)。
func (c *Client) UnloadModel() error {
	if err := c.call(http.MethodPost, "/models/unload", map[string]string{"model": ""}, nil, loadTimeout); err != nil {
		return err
	}
	c.mu.Lock()
	c.info = nil
	c.mu.Unlock()
	return nil
}

// Places は、サーバーのモデルの置き場所(パスはサーバーの PC のもの)。
func (c *Client) Places() ([]models.Place, error) {
	var r placeList
	if err := c.call(http.MethodGet, "/aibow/places", nil, &r, shortTimeout); err != nil {
		return nil, err
	}
	return r.Places, nil
}

// AddPlace は、サーバーの PC の path を置き場所に足させる。サーバーが、そのパスがあるかを確かめる(0015 の 31)。返事は足したあとの一覧。
func (c *Client) AddPlace(path string) ([]models.Place, error) {
	var r placeList
	if err := c.call(http.MethodPost, "/aibow/places/add", map[string]string{"path": path}, &r, shortTimeout); err != nil {
		return nil, err
	}
	return r.Places, nil
}

// RemovePlace は、足した置き場所を外させる。返事は外したあとの一覧。
func (c *Client) RemovePlace(path string) ([]models.Place, error) {
	var r placeList
	if err := c.call(http.MethodPost, "/aibow/places/remove", map[string]string{"path": path}, &r, shortTimeout); err != nil {
		return nil, err
	}
	return r.Places, nil
}

// placeList は /aibow/places の返事。
type placeList struct {
	Places []models.Place `json:"places"`
}

// ServerSettings は、表示だけのサーバーの設定(api.Settings と同じ形。画面からは変えない。0015 の 21)。
type ServerSettings struct {
	Port        int    `json:"port"`
	NCtx        int    `json:"n_ctx"`
	Model       string `json:"model"`
	Lib         string `json:"lib"`
	ModelsDir   string `json:"models_dir"`
	OllamaDir   string `json:"ollama_dir"`
	ConfigFile  string `json:"config_file"`
	ConfigFound bool   `json:"config_found"`
	StateFile   string `json:"state_file"`
}

// Settings は、表示だけのサーバーの設定を聞く。
func (c *Client) Settings() (ServerSettings, error) {
	var r ServerSettings
	err := c.call(http.MethodGet, "/aibow/settings", nil, &r, shortTimeout)
	return r, err
}

// Info は、最後に知った読み込み中のモデル(知らなければ nil)。聞きに行かない(呼ばれる回数が多いため)。
func (c *Client) Info() *engine.ModelInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.info
}

// EnsureContextSize は、n トークンがサーバーの作業領域に入るかを確かめる。作り直しは頼めないので、いつも false(0015 の 3)。
//
// 手元の engine は会話ごとに作業領域を作り直していた(0009)が、サーバーを共有すると、1人の都合で作り直すと
// ほかの人の KV キャッシュが消える。そのため作業領域はサーバーが起動のときに決めた大きさで固定し、ここは入るかを確かめるだけにした。
func (c *Client) EnsureContextSize(n int) (bool, error) {
	c.mu.Lock()
	nCtx := c.nCtx
	c.mu.Unlock()
	if nCtx == 0 {
		l, err := c.Models()
		if err != nil {
			return false, err
		}
		nCtx = l.NCtx
	}
	if n > nCtx {
		return false, &ContextError{Need: n, Have: nCtx}
	}
	return false, nil
}

// ContextError は、会話の上限 + 返事の上限がサーバーの作業領域に入らないときのエラー。
type ContextError struct {
	Need, Have int
}

// HasRemedy は、対処まで書いてあること(会話の仕組みが「GPU の層を減らして」を書き足さないための約束)。
func (e *ContextError) HasRemedy() bool { return true }

func (e *ContextError) Error() string {
	return fmt.Sprintf("会話の上限 + 返事の上限(%d トークン)が、aibow-server の作業領域(%d トークン)に入りません。"+
		"会話の上限か返事の上限を下げるか、aibow-server を -ctx(設定ファイルの ctx)で大きくして起動し直してください", e.Need, e.Have)
}

// IsContextError は err が ContextError か。
func IsContextError(err error) bool {
	var ce *ContextError
	return errors.As(err, &ce)
}

// CountTokens は、p に書式を当てた文字列のトークン数をサーバーに数えさせる(書式はサーバーにあるため)。
// メーターと compact の判断に使う。数えるのはサーバーが読み込んでいるモデル。
func (c *Client) CountTokens(p format.Prompt) (int, error) {
	req, err := newChatRequest(p)
	if err != nil {
		return 0, err
	}
	var r struct {
		NTokens int `json:"n_tokens"`
	}
	if err := c.call(http.MethodPost, "/aibow/count-tokens", req, &r, shortTimeout); err != nil {
		return 0, err
	}
	return r.NTokens, nil
}
