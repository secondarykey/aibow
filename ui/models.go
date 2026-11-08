package ui

// models.go: 画面から呼ぶ、モデルのメソッド(一覧と切り替え・モデルを扱う画面)。読み込む・降ろすのは ModelHost(直接か aibow-server 越し)で、ここは画面との中継とログ。

import (
	"cmp"
	"context"
	"errors"

	"github.com/secondarykey/aibow"
	"github.com/secondarykey/aibow/api"
	apiclient "github.com/secondarykey/aibow/api/client"
	"github.com/secondarykey/aibow/engine"
	"github.com/secondarykey/aibow/log"
	"github.com/secondarykey/aibow/models"
)

// ---- モデルの一覧と切り替え -------------------------------------------------------------

// ModelsResult は Models の戻り値。
type ModelsResult struct {
	Models  []models.Entry    `json:"models"`
	Current *engine.ModelInfo `json:"current"`           // 未読み込み(降ろした)なら null
	Loading string            `json:"loading,omitempty"` // 起動時に読み込み中のモデルの名前
	GPU     bool              `json:"gpu"`
	Error   string            `json:"error,omitempty"` // 起動に失敗した理由。あれば何もできない
	Notices []string          `json:"notices"`         // 起動時に伝えること(使われなくなった環境変数など)

	// GPULayers はモデルの ID → 読み込むときの GPU の層数(直接ならアプリの DB、aibow-server 越しならサーバーの PC に覚えた値。8-4)
	GPULayers map[string]int `json:"gpu_layers"`
	// LastModel は最後に読み込んだモデル。降ろしたあと(Current が null)も、モデルの選択にどれを降ろしたかを出すため(0015 の 29)
	LastModel string `json:"last_model,omitempty"`

	// Server は生成を頼んでいる aibow-server のつなぎ先(アプリの中で動かしているときは空。0015)
	Server string `json:"server,omitempty"`
	// ServerError は aibow-server につながらない理由(と対処)。起動したら、アプリを起動し直さずに使える(一覧を取り直せば消える)
	ServerError string `json:"server_error,omitempty"`
	// Manage は、aibow-server の中で、サーバーの PC の事情(読み込む / 降ろす・層の数・置き場所・出どころ)を見せて変えてよい人か
	// (管理者か、サーバーの PC の上から開いた画面。0018 の決定事項 8)。画面はモデルのタブを、アプリの中で動かすときと同じに出す
	Manage bool `json:"manage,omitempty"`
}

// Models は使えるモデルの一覧と、今読み込んでいるモデルを返す。
func (s *Service) Models(ctx context.Context) ModelsResult {
	if s.startupErr != nil {
		return ModelsResult{Models: []models.Entry{}, Error: s.startupErr.Error(), Notices: s.notices}
	}
	r := ModelsResult{Models: []models.Entry{}, Notices: s.notices, Server: s.host.server()}
	l, err := s.host.list(ctx)
	if err != nil {
		// つながらない間は、一覧を取るたびにログを出さない(画面はウィンドウに戻るたびに取り直すため)
		if !s.serverDown.Swap(true) {
			log.Warn(err.Error())
		}
		r.ServerError = err.Error()
		return r
	}
	if s.serverDown.Swap(false) {
		log.Noticef("aibow-server につながりました: %s", s.host.server())
	}
	r.Models, r.Current, r.GPU, r.GPULayers, r.Manage = l.models, l.current, l.gpu, l.layers, l.manage
	// どの書式で動くかを一覧で見えるようにする(0026 の決定事項 17)。aibow-server から受け取った一覧には、サーバーが選んだ名前が入っている
	for i := range r.Models {
		api.FillFormat(&r.Models[i])
	}
	r.LastModel = s.lastModel()
	if name := s.loading.Load(); name != nil {
		r.Loading = *name
	}
	return r
}

// SwitchModel はモデルを切り替える。読み込み終わるまで返らない。
// 失敗したときは元のモデルに戻っている(画面は Models を呼び直して確認する)。
// Go で error を返すと、JavaScript 側では Promise が reject される。
//
// 読み込めるモデルは1つ(VRAM に1つしか載らない)。画面は設定の画面の「モデル」のタブの「読み込む」でこれを呼ぶ。
// 入力欄の下でモデルを選んだときは呼ばない(会話のモデルを変えるだけ。0014 の「あとから決めたこと(2026-10-31)」)。
// 会話を開いた・選んだだけでは読み直さず、モデルの違う会話に送信するときに読み直す(Conversations.ensureWorkspace。0014 の 3)。
//
// 順番待ちの席があれば(aibow-server の中)、席を取ってから読み込む(生成中のほかの人の作業領域を、途中で入れ替えないように)。
func (s *Service) SwitchModel(ctx context.Context, id string) (*engine.ModelInfo, error) {
	if s.startupErr != nil {
		return nil, s.startupErr
	}
	if s.seat != nil {
		if !s.seat.Take(ctx) {
			return nil, ctx.Err()
		}
		defer s.seat.Release()
	}
	return s.loadModel(id)
}

// loadModel はモデルを読み込み、成功したら「最後に使ったモデル」として保存する(次の起動で読み込む・新しい会話のモデル)。
// GPU に載せる層の数は、そのモデルに覚えた値(覚えていなければ全体の値。0014 の 4)。aibow-server 越しなら、サーバーの PC に覚えた値。
func (s *Service) loadModel(id string) (*engine.ModelInfo, error) {
	if where := s.host.server(); where != "" {
		log.Infof("モデルを読み込み中: %s(aibow-server: %s)", id, where)
	} else {
		log.Infof("モデルを読み込み中: %s", id)
	}
	i, err := s.host.load(id)
	if err != nil {
		log.Error(err.Error())
		return nil, err
	}
	logModel("読み込み完了", i)
	if i.Format == "" {
		log.Warnf("このモデルの書式(種類: %s)には未対応です。送信するとエラーになります", i.Arch)
	}
	s.saveAppSetting(aibow.SettingLastModel, i.ID, func(a *aibow.AppSettings) { a.Workspace.Model = i.ID })
	return i, nil
}

// modelLoader は、会話の仕組みが送信の前に会話のモデルを読み込むときに使う係(aibow.ModelLoader)。
// Service のメソッドにすると画面から呼べるメソッドになってしまうので、別の型にしている。
type modelLoader struct{ s *Service }

func (l modelLoader) LoadModel(id string) (*engine.ModelInfo, error) {
	return l.s.loadModel(id)
}

func logModel(what string, i *engine.ModelInfo) {
	log.Noticef("%s (%.1f 秒): %s / %.2fB パラメータ / %.2f GB / %d 層 / GPU に載せる層: %d / 作業領域: %d トークン / 書式: %s",
		what, i.LoadSec, i.Desc, i.ParamsB, i.SizeGB, i.Layers, i.GPULayers, i.NCtx, cmp.Or(i.Format, "未対応("+i.Arch+")"))
}

// ---- モデルを扱う画面(0015 の 8-4) -------------------------------------------------------
//
// すぐ効く操作だけを画面から変える(読み込む / 降ろす・モデルごとの GPU の層数・置き場所を足す / 外す)。
// 作業領域・ポート・llama.cpp の場所・最初のモデルは表示だけ(0015 の 21)。どちらの形でも同じに見せる(ModelHost)。
// 読み込むのは SwitchModel(会話のモデルは変えない。会話のモデルが違えば、送信の前に読み直す)。

// UnloadModel は読み込み中のモデルを降ろす(VRAM を空ける。ほかのアプリと並べて使うため。0015 の 23)。
// 降ろしたあとに送信すると、送信の前に会話のモデルを読み込む(0014 の 3)。
func (s *Service) UnloadModel(ctx context.Context) error {
	if s.startupErr != nil {
		return s.startupErr
	}
	// aibow-server の中では、席を取ってから(生成中のほかの人の作業領域を入れ替えない。OpenAI 互換の口と同じ)
	if s.seat != nil {
		if !s.seat.Take(ctx) {
			return ctx.Err()
		}
		defer s.seat.Release()
	}
	i := s.host.info()
	if err := s.host.unload(ctx); err != nil {
		log.Error(err.Error())
		return err
	}
	if i != nil {
		log.Noticef("モデルを降ろしました: %s", i.ID)
	}
	return nil
}

// SetModelGPULayers は、モデルごとの GPU の層数を変えて覚える。読み込み中のモデルなら読み直し、その情報を返す(ほかのモデルなら nil。
// 次に読み込むときから効く)。直接ならアプリの DB、aibow-server 越しならサーバーの PC に覚える(0015 の 9)。
func (s *Service) SetModelGPULayers(ctx context.Context, id string, n int) (*engine.ModelInfo, error) {
	if s.startupErr != nil {
		return nil, s.startupErr
	}
	if n < 0 {
		return nil, errors.New("GPU に載せる層の数は 0 以上にしてください")
	}
	if s.seat != nil {
		if !s.seat.Take(ctx) {
			return nil, ctx.Err()
		}
		defer s.seat.Release()
	}
	i, err := s.host.setModelGPULayers(ctx, id, n)
	if err != nil {
		log.Error(err.Error())
		return nil, err
	}
	if i != nil {
		logModel("GPU に載せる層を変えて読み直しました", i)
	} else {
		log.Noticef("モデル %s の GPU に載せる層を %d として覚えました(次に読み込むときから効きます)", id, n)
	}
	return i, nil
}

// SetModelEnabled は、モデルを使えるかを決める(aibow-server の管理者がモデルのタブで。0018 の決定事項 13)。
// 使えないモデルは一覧に出さず、頼まれても読み込まない。使えなくしたのが読み込み中のモデルなら降ろすので、席を取ってから行う。
func (s *Service) SetModelEnabled(ctx context.Context, id string, enabled bool) error {
	if s.startupErr != nil {
		return s.startupErr
	}
	if s.seat != nil {
		if !s.seat.Take(ctx) {
			return ctx.Err()
		}
		defer s.seat.Release()
	}
	if err := s.host.setModelEnabled(ctx, id, enabled); err != nil {
		log.Warn(err.Error())
		return err
	}
	return nil
}

// ModelPlaces はモデルの置き場所の一覧(models フォルダ・Ollama・足した置き場所。aibow-server 越しならサーバーの PC のもの)。
func (s *Service) ModelPlaces(ctx context.Context) ([]models.Place, error) {
	if s.startupErr != nil {
		return nil, s.startupErr
	}
	return s.host.places(ctx)
}

// AddModelPlace は置き場所(フォルダか GGUF のファイル)を足し、足したあとの一覧を返す。
// aibow-server 越しなら、path はサーバーの PC のパスで、サーバーがあるかを確かめる(0015 の 31)。
func (s *Service) AddModelPlace(ctx context.Context, path string) ([]models.Place, error) {
	if s.startupErr != nil {
		return nil, s.startupErr
	}
	l, err := s.host.addPlace(ctx, path)
	if err != nil {
		log.Warn(err.Error())
	}
	return l, err
}

// RemoveModelPlace は足した置き場所を外し、外したあとの一覧を返す。手で決めた置き場所は外せない(0015 の 26)。
func (s *Service) RemoveModelPlace(ctx context.Context, path string) ([]models.Place, error) {
	if s.startupErr != nil {
		return nil, s.startupErr
	}
	l, err := s.host.removePlace(ctx, path)
	if err != nil {
		log.Warn(err.Error())
	}
	return l, err
}

// ModelSettingsResult は ModelSettings の戻り値。
type ModelSettingsResult struct {
	// Server はつなぎ先(直接なら空)
	Server string `json:"server,omitempty"`
	// Settings は表示だけの設定。直接なら作業領域は 0(会話ごと)・ポートは 0(待ち受けない)・設定ファイルは空(環境変数で変える)。
	// aibow-server 越しなら作業領域だけ(サーバーの PC のパスなどは渡さない。0015 の 21)
	Settings apiclient.ServerSettings `json:"settings"`
}

// ModelSettings は、表示だけの設定(作業領域・ポート・llama.cpp の場所・最初のモデル・モデルの置き場所)を返す。
// 画面からは変えない(aibow-server 越しなら server.json で変えて起動し直す。直接なら環境変数。0015 の 21)。
func (s *Service) ModelSettings() (ModelSettingsResult, error) {
	if s.startupErr != nil {
		return ModelSettingsResult{}, s.startupErr
	}
	v, err := s.host.settings()
	return ModelSettingsResult{Server: s.host.server(), Settings: v}, err
}
