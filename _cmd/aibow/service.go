package main

// service.go: 画面(frontend)から呼ばれる Go のメソッド。
//
// Wails では、Service として登録した構造体の「大文字で始まるメソッド」を、
// 画面の JavaScript から関数のように呼べる(中身は Wails が仲立ちする RPC)。
//
//	TypeScript: ChatService.Chat(req)      ← wails3 generate bindings が frontend/bindings に作る関数
//	        ↓  Wails が引数を JSON にして Go に渡す
//	Go:         (*ChatService).Chat(ctx, req)
//
// メソッドのコメントと構造体の json タグは、バインディングの TypeScript の型とコメントにもなる。
// Web サーバー版の /api/models・/api/model・/api/chat が、それぞれ Models・SwitchModel・Chat になった。

import (
	"context"
	"log"
	"sync/atomic"

	"aibow"

	"github.com/wailsapp/wails/v3/pkg/application"
)

type ChatService struct {
	engine  *aibow.Engine // 起動に失敗したときは nil
	catalog aibow.ModelCatalog

	// startupErr は起動に失敗した理由(lib が無いなど)。画面に表示する
	startupErr error

	// initial は起動時に読み込むモデル。nil なら読み込まない(画面で選ぶ)。
	initial *aibow.ModelEntry
	// loading は起動時に読み込み中のモデルの名前。読み込み中でなければ nil。
	loading atomic.Pointer[string]

	// quit はアプリの終了時にキャンセルされる。
	// Wails は呼び出し中のメソッドの ctx を、アプリの終了ではキャンセルしない。
	// 生成中にウィンドウを閉じても生成が最後まで続かないよう、自分で止める。
	quit       context.Context
	cancelQuit context.CancelFunc
}

func NewChatService(engine *aibow.Engine, catalog aibow.ModelCatalog, initial *aibow.ModelEntry, startupErr error) *ChatService {
	quit, cancel := context.WithCancel(context.Background())
	return &ChatService{
		engine: engine, catalog: catalog, initial: initial, startupErr: startupErr,
		quit: quit, cancelQuit: cancel,
	}
}

// ServiceStartup はアプリの起動時に Wails から呼ばれる(画面からは呼べない特別な名前)。
//
// モデルの読み込みには数秒〜十数秒かかる。読み終わるまでウィンドウが出ないと
// 起動したのか分からないので、ウィンドウを先に出し、裏で読み込む。
func (s *ChatService) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	if s.initial != nil {
		s.loading.Store(&s.initial.Name)
		go s.loadInitial(*s.initial)
	}
	return nil
}

// loadInitial は起動時のモデルを読み込み、終わったら models イベントで画面に知らせる。
func (s *ChatService) loadInitial(m aibow.ModelEntry) {
	loadAndLog(s.engine, m)
	s.loading.Store(nil)
	application.Get().Event.Emit(modelsEventName)
}

// ServiceShutdown はアプリの終了時に Wails から呼ばれる(画面からは呼べない特別な名前)。
func (s *ChatService) ServiceShutdown() error {
	s.cancelQuit()
	return nil
}

// ---- モデルの一覧と切り替え -------------------------------------------------------------

// modelsEventName は、起動時のモデルの読み込みが終わったことを画面に知らせるイベントの名前。
const modelsEventName = "models"

// ModelsResult は Models の戻り値。JSON のキー名は Web サーバー版と同じ。
type ModelsResult struct {
	Models  []aibow.ModelEntry `json:"models"`
	Current *aibow.ModelInfo   `json:"current"`           // 未読み込みなら null
	Loading string             `json:"loading,omitempty"` // 起動時に読み込み中のモデルの名前
	GPU     bool               `json:"gpu"`
	Error   string             `json:"error,omitempty"` // 起動に失敗した理由。あれば何もできない
}

// Models は使えるモデルの一覧と、今読み込んでいるモデルを返す。
func (s *ChatService) Models() ModelsResult {
	if s.startupErr != nil {
		return ModelsResult{Models: []aibow.ModelEntry{}, Error: s.startupErr.Error()}
	}
	r := ModelsResult{
		Models:  s.catalog.List(),
		Current: s.engine.Info(),
		GPU:     s.engine.HasGPU,
	}
	if name := s.loading.Load(); name != nil {
		r.Loading = *name
	}
	return r
}

// SwitchModel はモデルを切り替える。読み込み終わるまで返らない。
// 失敗したときは元のモデルに戻っている(画面は Models を呼び直して確認する)。
// Go で error を返すと、JavaScript 側では Promise が reject される。
func (s *ChatService) SwitchModel(id string) (*aibow.ModelInfo, error) {
	if s.startupErr != nil {
		return nil, s.startupErr
	}
	m, err := s.catalog.Find(id)
	if err != nil {
		return nil, err
	}
	return loadAndLog(s.engine, m)
}

func loadAndLog(engine *aibow.Engine, m aibow.ModelEntry) (*aibow.ModelInfo, error) {
	log.Printf("モデルを読み込み中: %s (%s)", m.Name, m.Path)
	i, err := engine.LoadModel(m)
	if err != nil {
		log.Printf("※ %v", err)
		return nil, err
	}
	log.Printf("読み込み完了 (%.1f 秒): %s / %.2fB パラメータ / %.2f GB / %d 層 / GPU に載せる層: %d / コンテキスト長: %d",
		i.LoadSec, i.Desc, i.ParamsB, i.SizeGB, i.Layers, i.GPULayers, i.NCtx)
	return i, nil
}

// ---- チャット -------------------------------------------------------------------------

// ChatRequest は画面 → Go。
type ChatRequest struct {
	Messages    []aibow.Message `json:"messages"`
	Think       *bool           `json:"think"`
	Temperature *float64        `json:"temperature"`
}

// chatEventName は、生成の途中経過を画面に送るイベントの名前。
const chatEventName = "chat"

// ChatEvent は Go → 画面。生成の途中経過を Wails のイベントで1つずつ送る。
// (Web サーバー版では SSE の1行が、この1つに当たる)
//
//	prompt   : モデルに渡した文字列(学習用に画面に表示する)
//	thinking : 思考の断片
//	content  : 答えの断片
//	stats    : 統計(トークン数、速度など)
//	error    : エラー
//	done     : 終わり。必ず最後に1回だけ送る
type ChatEvent struct {
	Type    string               `json:"type"`
	Text    string               `json:"text,omitempty"`
	Message string               `json:"message,omitempty"`
	Stats   *aibow.GenerateStats `json:"stats,omitempty"`
}

// Chat は返事を生成する。返事の中身は戻り値ではなく、chat イベントで少しずつ送る。
//
// 終わりの合図も戻り値ではなく done イベントにしている。
// Wails ではメソッドの戻り値とイベントは別の経路で画面に届くので、
// 戻り値が先に着くと、最後のほうの文字がまだ届いていないことがあるため。
//
// 画面で「停止」を押すと、JavaScript 側で Promise の cancel() が呼ばれ、
// Wails がこの ctx をキャンセルする。エンジンはそれを見て生成を止める。
func (s *ChatService) Chat(ctx context.Context, req ChatRequest) {
	emit := func(ev ChatEvent) { application.Get().Event.Emit(chatEventName, ev) }
	defer emit(ChatEvent{Type: "done"})
	if s.startupErr != nil {
		emit(ChatEvent{Type: "error", Message: s.startupErr.Error()})
		return
	}

	// 画面の停止ボタンか、アプリの終了のどちらかで止める
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(s.quit, cancel)
	defer stop()

	think := req.Think == nil || *req.Think
	temp := 0.7
	if req.Temperature != nil {
		temp = *req.Temperature
	}

	st, err := s.engine.Generate(ctx, req.Messages, think, float32(temp),
		func(p string) { emit(ChatEvent{Type: "prompt", Text: p}) },
		func(t string) { emit(ChatEvent{Type: "thinking", Text: t}) },
		func(t string) { emit(ChatEvent{Type: "content", Text: t}) },
	)
	if err != nil {
		log.Printf("エラー: %v", err)
		emit(ChatEvent{Type: "error", Message: err.Error()})
		return
	}

	log.Printf("プロンプト %d トークン(再利用 %d)%.2f 秒 / 生成 %d トークン %.2f 秒(%.1f tok/s)/ 終了理由: %s",
		st.PromptTokens, st.ReusedTokens, st.PromptSec, st.GenTokens, st.GenSec, tokPerSec(st), st.StopReason)
	emit(ChatEvent{Type: "stats", Stats: &st})
}

func tokPerSec(st aibow.GenerateStats) float64 {
	if st.GenSec == 0 {
		return 0
	}
	return float64(st.GenTokens) / st.GenSec
}
