// Package ui は、画面(frontend)から呼ばれる Go のメソッド(画面向けの処理)。
//
// デスクトップのアプリ(_cmd/aibow)は、Service を ChatService に埋め込んで Wails に登録する。
// Wails では、登録した構造体の「大文字で始まるメソッド」を、画面の JavaScript から関数のように呼べる(中身は Wails が仲立ちする RPC)。
// 埋め込んだ型のメソッドも ChatService のメソッドとして見える。
//
//	TypeScript: ChatService.Send(id, text)  ← wails3 generate bindings が frontend/bindings に作る関数
//	        ↓  Wails が引数を JSON にして Go に渡す
//	Go:         (*ChatService).Send(ctx, id, text) = (*ui.Service).Send
//
// メソッドのコメントと構造体の json タグは、バインディングの TypeScript の型とコメントにもなる。
//
// このパッケージは Wails を import しない(0016 の 9)。ブラウザから使うときは、aibow-server が同じ Service を画面用の HTTP の口に載せる(9-1)。
// Wails に頼るところは、画面へ知らせる係(Notifier)としてアプリから受け取る。ダイアログ(フォルダを選ぶなど)はアプリの ChatService に残す。
//
// 会話の履歴や設定を持つのは aibow パッケージの Conversations で、Service は画面との中継をするだけ。
//
// 会話・プロファイル・表示の設定は人ごと(0017)。誰のものかは、メソッドの最初の引数の ctx で運ぶ(aibow.WithUser)。
// ctx に利用者を入れるのは呼ぶ側の一番外(aibow-server の HTTP の層)で、Service は受け取った ctx をそのまま会話の仕組みに渡す。
// デスクトップのアプリは利用者が1人なので入れない(手元の DB が、利用者の無い ctx をその1人のものとして扱う。store.SQLite.OneUser)。
// Wails も、最初の引数が context.Context のメソッドには呼び出しの ctx を渡し、画面の関数の引数には出さない。
package ui

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"
	"uuid"

	"github.com/secondarykey/aibow"
	apiclient "github.com/secondarykey/aibow/api/client"
	"github.com/secondarykey/aibow/engine"
	"github.com/secondarykey/aibow/format"
	"github.com/secondarykey/aibow/log"
	"github.com/secondarykey/aibow/mcp/client"
	"github.com/secondarykey/aibow/models"
	"github.com/secondarykey/aibow/tool"
)

// 画面に知らせるイベントの名前。
const (
	// ChatEventName は、生成の途中経過を画面に送るイベントの名前。中身は aibow.ChatEvent
	ChatEventName = "chat"
	// ModelsEventName は、起動時のモデルの読み込みが終わったことを画面に知らせるイベントの名前
	ModelsEventName = "models"
)

// Notifier は、Go から画面に知らせる係。デスクトップでは Wails のイベント、ブラウザでは SSE(9-1)。
type Notifier interface {
	// Chat は chat イベント(生成の途中経過)を送る
	Chat(ev aibow.ChatEvent)
	// ModelLoaded は models イベント(起動時のモデルの読み込みが終わった)を送る
	ModelLoaded()
}

// Seat は生成の順番待ちの席(本物は aibow-server の api.Seat)。
// aibow-server の中のブラウザの会話は、OpenAI 互換の口(Cline など)と同じ席を通して生成とモデルの読み込みをする(0016 の 11)。
type Seat interface {
	// Take は席が空くまで待って取る。待っている間に ctx が終わったら false
	Take(ctx context.Context) bool
	// TryTake は、空いていれば取る(待たない)
	TryTake() bool
	Release()
}

// Options は New に渡すもの。
type Options struct {
	// Host はモデルを扱う係(NewDirectHost・NewRemoteHost・NewServerHost)。起動に失敗したときは nil
	Host  ModelHost
	Store aibow.Store
	App   aibow.AppSettings
	// Initial は起動時に読み込むモデル。nil なら読み込まない(画面で選ぶ)
	Initial *models.Entry
	// Local は組み込みのツール、MCP は settings.json の MCP のサーバー(書いてなければ nil)、Prefs はツールの扱いの PC の既定(tools.json)
	Local *client.Local
	MCP   *client.Manager
	Prefs *aibow.ToolPrefs
	// Version はアプリのバージョン(画面の左下に出す)
	Version string
	// Notices は起動時に画面のトーストで伝えること。StartupErr は起動に失敗した理由(画面に表示する)
	Notices    []string
	StartupErr error
	// Notify は画面に知らせる係
	Notify Notifier

	// Seat は生成の順番待ちの席。nil なら待たない(デスクトップのアプリ。モデルを使うのはこのアプリだけか、待つのは aibow-server の側)
	Seat Seat
	// WithoutTools は、会話でツールを使わない(aibow-server の中のブラウザの会話。0016 の 7)。
	// Local を渡さないだけだと、会話の仕組みは組み込みのツールを直接使うので、空のツールの係を渡す
	WithoutTools bool
}

// noTools は、ツールを1つも出さない係(aibow.LocalTools)。
type noTools struct{}

func (noTools) Tools(string) []tool.Tool { return nil }

type Service struct {
	// host はモデルを扱う係(直接か aibow-server 越し。modelhost.go)。起動に失敗したときは nil
	host  ModelHost
	store aibow.Store          // 起動に失敗したときは nil
	convs *aibow.Conversations // 起動に失敗したときは nil

	// serverDown は、aibow-server につながらなかったか(つながらない間、一覧を取るたびにログを出さないため)
	serverDown atomic.Bool

	// startupErr は起動に失敗した理由(lib が無いなど)。画面に表示する
	startupErr error

	// mcp は settings.json の MCP のサーバー(書いてなければ nil)。prefs はツールの扱いの PC の既定(tools.json)
	mcp   *client.Manager
	prefs *aibow.ToolPrefs

	// version はアプリのバージョン(_cmd/aibow/version。画面の左下に出す)
	version string

	// app はアプリ全体の設定(作業領域に関わる設定)。画面で変えると DB にも保存する。
	// 表示の設定と新しい会話の既定は人ごとなので、ここの値は使わず、そのつど ctx の利用者のものを読む(AppSettings)
	appMu sync.Mutex
	app   aibow.AppSettings

	// notices は起動時に画面のトーストで伝えること(使われなくなった環境変数など)
	notices []string

	// initial は起動時に読み込むモデル。nil なら読み込まない(画面で選ぶ)。
	initial *models.Entry
	// loading は起動時に読み込み中のモデルの名前。読み込み中でなければ nil。
	loading atomic.Pointer[string]

	// notify は画面に知らせる係(Wails のイベントか SSE)
	notify Notifier
	// seat は生成の順番待ちの席(nil なら待たない)
	seat Seat

	// quit はアプリの終了時にキャンセルされる。
	// Wails は呼び出し中のメソッドの ctx を、アプリの終了ではキャンセルしない。
	// 生成中にウィンドウを閉じても生成が最後まで続かないよう、自分で止める。
	quit       context.Context
	cancelQuit context.CancelFunc
}

// New は Service を作る。起動と終了のときに呼ぶものは、戻り値の Lifecycle にある。
func New(o Options) (*Service, Lifecycle) {
	quit, cancel := context.WithCancel(context.Background())
	s := &Service{
		host: o.Host, app: o.App, initial: o.Initial, notices: o.Notices, startupErr: o.StartupErr, notify: o.Notify, seat: o.Seat,
		quit: quit, cancelQuit: cancel, mcp: o.MCP, prefs: o.Prefs, version: o.Version,
	}
	if d, ok := o.Host.(*DirectHost); ok {
		d.s = s // 直接の形は、GPU の層数をアプリの DB(Service が持つ設定)に覚える
	}
	if o.StartupErr == nil {
		st, app := o.Store, o.App
		s.store = st
		// 会話の仕組みから見ると、生成の口の相手が手元の engine か aibow-server かが変わるだけ(0015)
		s.convs = aibow.NewConversations(st, o.Host.generator(), app)
		// 組み込みのツールは aibow の中の自作の MCP サーバー越しに呼ぶ(0007 の 5-4)
		if o.WithoutTools {
			s.convs.SetLocalTools(noTools{})
		} else if o.Local != nil { // nil の *Local を interface に入れると nil に見えなくなる(MCP と同じ)
			s.convs.SetLocalTools(o.Local)
		}
		if o.MCP != nil { // nil の *Manager を interface に入れると nil に見えなくなるので、確かめてから渡す
			s.convs.SetRemoteTools(o.MCP)
		}
		s.convs.SetToolPrefs(o.Prefs)
		// 送信の前に会話のモデルを読み込む係(0014 の 3)。モデルごとの GPU の層数で読み込む
		s.convs.SetModelLoader(modelLoader{s})
		// ここから下の引き継ぎは、利用者を分ける前に作った会話・プロファイルのためのもの。それはログインなしの形の利用者のもの(DB の版 6)なので、その人として行う(0017)
		ctx := aibow.WithUser(context.Background(), aibow.LocalUser)
		// 新しい会話の既定はプロファイルの値。プロファイルより前の DB なら、今の既定からプロファイル「既定」を作り、今ある会話を紐づける(0014)。
		// ツールのグループを引き継ぐので、MCP のサーバーとツールの扱いを渡したあとに呼ぶ
		if notes, err := s.convs.PrepareProfiles(ctx); err != nil {
			log.Error(err.Error())
		} else {
			for _, n := range notes {
				log.Notice(n)
			}
		}
		// 会話の上限・返事の上限を会話ごとにする前に作った会話には、今の既定の値を写す(既定を変えても今ある会話は変わらない、の約束。0009)
		if n, err := s.convs.FillMissingLimits(ctx); err != nil {
			log.Error(err.Error())
		} else if n > 0 {
			d := app.Defaults
			log.Noticef("会話の上限・返事の上限を持たない %d 個の会話に、今の既定(%d + %d)を入れました", n, d.HistoryTokens, d.ReplyTokens)
		}
		// 会話ごとのモデルにする前に作った会話には、最後に使ったモデルを写す(0014 の 10)
		if n, err := s.convs.FillMissingModels(ctx, app.Workspace.Model); err != nil {
			log.Error(err.Error())
		} else if n > 0 {
			log.Noticef("モデルを持たない %d 個の会話に、最後に使ったモデル %s を入れました", n, app.Workspace.Model)
		}
	}
	return s, Lifecycle{s}
}

// Lifecycle は、起動と終了のときに呼ぶ係。
// Service のメソッドにすると画面から呼べるメソッドになってしまうので、別の型にしている(modelLoader と同じ)。
type Lifecycle struct{ s *Service }

// Start は起動のときに呼ぶ(デスクトップでは Wails の ServiceStartup から)。
//
// モデルの読み込みには数秒〜十数秒かかる。読み終わるまでウィンドウが出ないと
// 起動したのか分からないので、ウィンドウを先に出し、裏で読み込む。
func (l Lifecycle) Start() {
	s := l.s
	if s.initial != nil {
		s.loading.Store(&s.initial.Name)
		go s.loadInitial(*s.initial)
	}
}

// Stop は終了のときに呼ぶ(デスクトップでは Wails の ServiceShutdown から)。生成中なら止める。
func (l Lifecycle) Stop() {
	l.s.cancelQuit()
}

// loadInitial は起動時のモデルを読み込み、終わったら models イベントで画面に知らせる。
// aibow-server 越しのときは呼ばない(サーバーが起動のときに読み込んでいる。会話のモデルが違えば送信の前に読み直す。0015 の 12)。
func (s *Service) loadInitial(m models.Entry) {
	s.loadModel(m.ID)
	s.loading.Store(nil)
	s.notify.ModelLoaded()
}

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
}

// Models は使えるモデルの一覧と、今読み込んでいるモデルを返す。
func (s *Service) Models() ModelsResult {
	if s.startupErr != nil {
		return ModelsResult{Models: []models.Entry{}, Error: s.startupErr.Error(), Notices: s.notices}
	}
	r := ModelsResult{Models: []models.Entry{}, Notices: s.notices, Server: s.host.server()}
	l, err := s.host.list()
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
	r.Models, r.Current, r.GPU, r.GPULayers = l.models, l.current, l.gpu, l.layers
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
// 読み込めるモデルは1つ(VRAM に1つしか載らない)。画面は入力欄の下でモデルを選んだとき、会話のモデルを変えてからこれを呼ぶ。
// 会話を開いただけでは読み直さず、モデルの違う会話に送信するときに読み直す(Conversations.ensureWorkspace。0014 の 3)。
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

// ---- アプリ全体の設定 -------------------------------------------------------------------

// AppSettings はアプリの設定(GPU の層数・最後に使ったモデル)と、画面の設定・新しい会話の既定の設定(会話の上限・返事の上限も入る)を返す。
// GPU の層数は、読み込んでいるモデルの値(モデルごとに覚えている。0014 の 4。aibow-server 越しならサーバーの PC の値)。
// 画面の設定と新しい会話の既定は、ctx の利用者のもの(0017 の 6)。
// 設定の画面はこれを出して変える(SetGPULayers)。
func (s *Service) AppSettings(ctx context.Context) (aibow.AppSettings, error) {
	if s.startupErr != nil {
		return aibow.AppSettings{}, s.startupErr
	}
	layers := s.host.gpuLayers() // 直接の形は appMu を使うので、持つ前に聞く
	s.appMu.Lock()
	a := s.app
	s.appMu.Unlock()
	a.Workspace.GPULayers = layers
	var err error
	if a.Display, err = aibow.LoadDisplay(ctx, s.store, InitialDisplay()); err != nil {
		return aibow.AppSettings{}, err
	}
	if a.Defaults, err = s.convs.Defaults(ctx); err != nil {
		return aibow.AppSettings{}, err
	}
	return a, nil
}

// TryLimits は、会話の上限 + 返事の上限の大きさの作業領域を、今の作業領域として作り直してみる(モデルは読み直さない)。
// 画面は、会話の設定の画面・新しい会話の既定で上限を「適用」するときに呼び、確保できたら設定を保存する
// (VRAM に入らないことを、送信したときではなく変えたときに分かるようにするため。0009)。
// 作り直したら true。確保できなければ元の大きさに戻してエラーを返す。モデルが未読み込みなら、次に読み込むときの大きさだけ変わる。
// aibow-server 越しなら作り直さず、サーバーの作業領域に入るかを確かめるだけ(0015 の 3。VRAM の確認はサーバーの起動と読み直しのとき)。
func (s *Service) TryLimits(history, reply int) (bool, error) {
	if s.startupErr != nil {
		return false, s.startupErr
	}
	if history <= 0 || reply <= 0 {
		return false, errors.New("会話の上限と返事の上限は 1 以上にしてください")
	}
	n := history + reply
	changed, err := s.host.tryContext(n)
	if err != nil {
		log.Error(err.Error())
		return false, fmt.Errorf("作業領域(会話の上限 %d + 返事の上限 %d = %d トークン)を確保できません。%w", history, reply, n, err)
	}
	if changed {
		log.Noticef("会話の上限 %d + 返事の上限 %d = 作業領域 %d トークンに作り直しました(モデルは読み直していません)", history, reply, n)
	}
	return changed, nil
}

// SetGPULayers は GPU に載せる層の数を変え、今のモデルを読み込み直す。
// 失敗したときは元の層の数で読み直している。モデルが未読み込みなら nil を返す。
// 値は読み込んでいるモデルの値として覚える(モデルごと。0014 の 4)。モデルが未読み込みなら、まだ覚えていないモデルに使う全体の値を変える。
// aibow-server 越しなら、サーバーが読み直し、サーバーの PC に覚える(0015 の 9)。
func (s *Service) SetGPULayers(n int) (*engine.ModelInfo, error) {
	if s.startupErr != nil {
		return nil, s.startupErr
	}
	i, err := s.host.setGPULayers(n)
	if err != nil {
		log.Error(err.Error())
		return nil, err
	}
	if i != nil {
		logModel("GPU に載せる層を変えて読み直しました", i)
	}
	return i, nil
}

// ---- モデルを扱う画面(0015 の 8-4) -------------------------------------------------------
//
// すぐ効く操作だけを画面から変える(読み込む / 降ろす・モデルごとの GPU の層数・置き場所を足す / 外す)。
// 作業領域・ポート・llama.cpp の場所・最初のモデルは表示だけ(0015 の 21)。どちらの形でも同じに見せる(ModelHost)。
// 読み込むのは SwitchModel(会話のモデルは変えない。会話のモデルが違えば、送信の前に読み直す)。

// UnloadModel は読み込み中のモデルを降ろす(VRAM を空ける。ほかのアプリと並べて使うため。0015 の 23)。
// 降ろしたあとに送信すると、送信の前に会話のモデルを読み込む(0014 の 3)。
func (s *Service) UnloadModel() error {
	if s.startupErr != nil {
		return s.startupErr
	}
	i := s.host.info()
	if err := s.host.unload(); err != nil {
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
func (s *Service) SetModelGPULayers(id string, n int) (*engine.ModelInfo, error) {
	if s.startupErr != nil {
		return nil, s.startupErr
	}
	if n < 0 {
		return nil, errors.New("GPU に載せる層の数は 0 以上にしてください")
	}
	i, err := s.host.setModelGPULayers(id, n)
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

// ModelPlaces はモデルの置き場所の一覧(models フォルダ・Ollama・足した置き場所。aibow-server 越しならサーバーの PC のもの)。
func (s *Service) ModelPlaces() ([]models.Place, error) {
	if s.startupErr != nil {
		return nil, s.startupErr
	}
	return s.host.places()
}

// AddModelPlace は置き場所(フォルダか GGUF のファイル)を足し、足したあとの一覧を返す。
// aibow-server 越しなら、path はサーバーの PC のパスで、サーバーがあるかを確かめる(0015 の 31)。
func (s *Service) AddModelPlace(path string) ([]models.Place, error) {
	if s.startupErr != nil {
		return nil, s.startupErr
	}
	l, err := s.host.addPlace(path)
	if err != nil {
		log.Warn(err.Error())
	}
	return l, err
}

// RemoveModelPlace は足した置き場所を外し、外したあとの一覧を返す。手で決めた置き場所は外せない(0015 の 26)。
func (s *Service) RemoveModelPlace(path string) ([]models.Place, error) {
	if s.startupErr != nil {
		return nil, s.startupErr
	}
	l, err := s.host.removePlace(path)
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

// ---- プロファイル(0014) -------------------------------------------------------------------
//
// プロファイルは名前の付いた「新しい会話の既定」。値は会話を作るときに写し、決まり(固定・使えない)は会話からそのつど見に行く。
// 扱いを決めるのは aibow(Conversations)で、ここは画面との中継とログだけ。

// Profiles は、プロファイルの一覧(しまったものも。使っている会話の数・新しい会話で最初に選ばれるか)。
func (s *Service) Profiles(ctx context.Context) ([]aibow.ProfileInfo, error) {
	if s.startupErr != nil {
		return nil, s.startupErr
	}
	return s.convs.Profiles(ctx)
}

// CreateProfile は、新しいプロファイルを作る(設定の画面の「新しいプロファイル」)。
func (s *Service) CreateProfile(ctx context.Context, name string, values aibow.ProfileValues, rules aibow.ProfileRules) (aibow.Profile, error) {
	if s.startupErr != nil {
		return aibow.Profile{}, s.startupErr
	}
	p, err := s.convs.CreateProfile(ctx, name, values, rules)
	if err == nil {
		log.Noticef("プロファイル「%s」を作りました", p.Name)
	}
	return p, err
}

// UpdateProfile は、プロファイルの名前・値・決まりを変える。値は今ある会話に効かず、決まりは今ある会話にもすぐ効く。
func (s *Service) UpdateProfile(ctx context.Context, p aibow.Profile) error {
	if s.startupErr != nil {
		return s.startupErr
	}
	if err := s.convs.UpdateProfile(ctx, p); err != nil {
		return err
	}
	log.Noticef("プロファイル「%s」を変えました(固定 %v・使えない %+v)", p.Name, p.Rules.Fixed, p.Rules.Off)
	return nil
}

// SetProfileArchived は、プロファイルをしまう / 戻す。
func (s *Service) SetProfileArchived(ctx context.Context, id uuid.UUID, archived bool) error {
	if s.startupErr != nil {
		return s.startupErr
	}
	if err := s.convs.SetProfileArchived(ctx, id, archived); err != nil {
		return err
	}
	log.Noticef("プロファイル %s をしまう: %v", id, archived)
	return nil
}

// DeleteProfile は、使っている会話が無いプロファイルを消す。
func (s *Service) DeleteProfile(ctx context.Context, id uuid.UUID) error {
	if s.startupErr != nil {
		return s.startupErr
	}
	if err := s.convs.DeleteProfile(ctx, id); err != nil {
		return err
	}
	log.Noticef("プロファイル %s を消しました", id)
	return nil
}

// SetDefaultProfile は、新しい会話で最初に選ばれるプロファイルを変える。
func (s *Service) SetDefaultProfile(ctx context.Context, id uuid.UUID) error {
	if s.startupErr != nil {
		return s.startupErr
	}
	if err := s.convs.SetDefaultProfile(ctx, id); err != nil {
		return err
	}
	if p, err := s.convs.DefaultProfile(ctx); err == nil {
		log.Noticef("新しい会話で最初に選ばれるプロファイル: %s", p.Name)
	}
	return nil
}

// ProfileDefaults は、プロファイル id から新しい会話を作るときの設定(新しい会話の画面でプロファイルを選んだとき)。
func (s *Service) ProfileDefaults(ctx context.Context, id uuid.UUID) (aibow.ConversationSettings, error) {
	if s.startupErr != nil {
		return aibow.ConversationSettings{}, s.startupErr
	}
	return s.convs.DefaultsOf(ctx, id)
}

// SaveToProfile は、会話の今の設定でプロファイルの値を上書きする(会話の設定の画面の「プロファイルに保存」)。
func (s *Service) SaveToProfile(ctx context.Context, conv, profile uuid.UUID) error {
	if s.startupErr != nil {
		return s.startupErr
	}
	if err := s.convs.SaveToProfile(ctx, conv, profile); err != nil {
		return err
	}
	log.Noticef("会話 %s の設定を、プロファイル %s に保存しました", conv, profile)
	return nil
}

// SaveAsProfile は、会話の今の設定で新しいプロファイルを作る(会話の設定の画面の「新しいプロファイルとして保存」)。
func (s *Service) SaveAsProfile(ctx context.Context, conv uuid.UUID, name string) (aibow.Profile, error) {
	if s.startupErr != nil {
		return aibow.Profile{}, s.startupErr
	}
	p, err := s.convs.SaveAsProfile(ctx, conv, name)
	if err == nil {
		log.Noticef("会話 %s の設定から、プロファイル「%s」を作りました", conv, p.Name)
	}
	return p, err
}

// SetDisplay は画面の設定(質問を右上に出すか・明るさ)を変える。生成には関わらないので、モデルは読み直さない。
// 設定は ctx の利用者のもの(0017 の 6)。変わった項目だけ保存し、ログに残す。
func (s *Service) SetDisplay(ctx context.Context, d aibow.DisplaySettings) error {
	if s.startupErr != nil {
		return s.startupErr
	}
	if err := d.Validate(); err != nil {
		return err
	}
	old, err := aibow.LoadDisplay(ctx, s.store, InitialDisplay())
	if err != nil {
		return err
	}
	if d.PinQuestion != old.PinQuestion {
		if err := aibow.SaveUserSetting(ctx, s.store, aibow.SettingPinQuestion, d.PinQuestion); err != nil {
			return err
		}
		log.Noticef("スクロールしたときに質問を右上に出す: %v", d.PinQuestion)
	}
	if d.Theme != old.Theme {
		if err := aibow.SaveUserSetting(ctx, s.store, aibow.SettingTheme, d.Theme); err != nil {
			return err
		}
		log.Noticef("画面の明るさ: %s", d.Theme)
	}
	return nil
}

// saveAppSetting はアプリ全体の設定を1つ保存する。
// 保存に失敗しても、変えた設定は今の起動中は効いているので、ログに出すだけにする。
// アプリ全体の設定は利用者によらないので、呼び出しの ctx は渡さない(モデルの読み込みは、会話の仕組みからも ctx なしで頼まれる)。
func (s *Service) saveAppSetting(key string, v any, apply func(*aibow.AppSettings)) {
	s.appMu.Lock()
	defer s.appMu.Unlock()
	apply(&s.app)
	if err := aibow.SaveAppSetting(context.Background(), s.store, key, v); err != nil {
		log.Error(err.Error())
	}
}

// Version はアプリのバージョン(画面の左下に出す)。起動に失敗していても返す。
func (s *Service) Version() string {
	return s.version
}

// ---- MCP のサーバーとツールの扱い ------------------------------------------------------

// MCPServers は、会話ごとに使うものを選ぶ MCP のサーバーの一覧(settings.json に書いたもの全部)。書いてなければ空。
// 設定の画面の「ツール」のチェックは「新しい会話で最初から選ぶか」だけなので、チェックを外したものもここに出す。
func (s *Service) MCPServers() []client.ServerInfo {
	if s.mcp == nil {
		return []client.ServerInfo{}
	}
	return s.mcp.Infos()
}

// AllMCPServers は settings.json に書いた MCP のサーバー全部(設定の画面の「ツール」に出す)。
func (s *Service) AllMCPServers() []client.ServerInfo {
	if s.mcp == nil {
		return []client.ServerInfo{}
	}
	return s.mcp.Infos()
}

// BuiltinTools は組み込みのツール(aibow の中で動くツール)の一覧(設定の画面の「ツール」に出す)。
func (s *Service) BuiltinTools() []aibow.KnownTool {
	return aibow.BuiltinTools()
}

// ToolDefaults は、会話の上書きが無いときのツールの扱い(PC の既定と決まりの既定。設定の画面の「ツール」に出す)。
// この PC の既定なので、プロファイルの決まりは当てない。
func (s *Service) ToolDefaults() ([]aibow.ToolGroupState, error) {
	if s.startupErr != nil {
		return nil, s.startupErr
	}
	return s.convs.ToolStates(aibow.ConversationSettings{}), nil
}

// SetGroupPolicy は、グループ(空なら組み込み、そうでなければ MCP のサーバー)の全部のツールの確認の扱いの PC の既定を変える
// (確認する ask / 確認しない allow / 決めていない 空。tools.json に書く)。
func (s *Service) SetGroupPolicy(group string, policy aibow.ToolPolicy) error {
	if err := s.prefs.SetGroupPolicy(group, policy); err != nil {
		return err
	}
	log.Noticef("ツールのグループ %s の扱いの PC の既定: %s", cmp.Or(group, "組み込み"), policyLabel(policy))
	return nil
}

// ToolStates は、会話 id の設定 settings でのツールの扱い(会話の設定の画面に出す)。
// 画面がまだ保存していない設定でも見られるよう、設定を受け取る。会話の ID は、プロファイルの決まり(使えない)を引くために使う
// (空なら新しい会話 = 新しい会話で最初に選ばれるプロファイル。0014)。
func (s *Service) ToolStates(ctx context.Context, id uuid.UUID, settings aibow.ConversationSettings) ([]aibow.ToolGroupState, error) {
	if s.startupErr != nil {
		return nil, s.startupErr
	}
	return s.convs.ConversationToolStates(ctx, id, settings)
}

// ToolPrefs は、ツールの扱いの PC の既定(tools.json)と、サーバーを起動したときに見えたツールの一覧。
// 新しい会話で最初から使うグループはプロファイルの値なので、設定の画面はプロファイルから出す(ここの notDefault は引き継ぐ前の値)。
func (s *Service) ToolPrefs() aibow.ToolPrefsView {
	return s.prefs.View()
}

// LoadToolList は、MCP のサーバーを一時的に起動してツールの一覧を受け取り、tools.json に覚える(設定の画面で、一覧が無いときと「読み直す」)。
func (s *Service) LoadToolList(ctx context.Context, server string) error {
	if s.startupErr != nil {
		return s.startupErr
	}
	return s.convs.LoadToolList(ctx, server)
}

// SetToolPolicy は、ツールの扱いの PC の既定を変える(tools.json に書く)。policy が空なら決めていない状態に戻す。
// 次の送信から効く(プロンプトに入るツールが変わるので、画面はメーターを数え直す)。
func (s *Service) SetToolPolicy(name string, policy aibow.ToolPolicy) error {
	if err := s.prefs.SetToolPolicy(name, policy); err != nil {
		return err
	}
	log.Noticef("ツール %s の扱いの PC の既定: %s", name, policyLabel(policy))
	return nil
}

func policyLabel(p aibow.ToolPolicy) string {
	switch p {
	case aibow.ToolOff:
		return "使わない"
	case aibow.ToolAsk:
		return "確認する"
	case aibow.ToolAllow:
		return "確認しない"
	case aibow.ToolOn:
		return "使う"
	}
	return "決めていない"
}

// ---- 会話 -------------------------------------------------------------------------------

// Conversations は会話の一覧を、更新の新しい順に返す。
func (s *Service) Conversations(ctx context.Context) ([]aibow.Conversation, error) {
	if s.startupErr != nil {
		return nil, s.startupErr
	}
	return s.convs.List(ctx)
}

// ConversationDetail は会話1つと、その発言。
type ConversationDetail struct {
	Conversation aibow.Conversation    `json:"conversation"`
	Messages     []aibow.StoredMessage `json:"messages"`
	Compactions  []aibow.Compaction    `json:"compactions"` // 要約(古い順)。画面の区切りに使う
	Generating   bool                  `json:"generating"`  // この会話で返事を生成中か
	Usage        *aibow.Usage          `json:"usage"`       // メーター。モデルが読み込まれていなければ null
	// Profile は、この会話のプロファイル(名前・値・決まり。0014)。画面は、固定した項目を押せなくして理由を添えるのに使う。
	// プロファイルを持たない会話なら null
	Profile *aibow.Profile `json:"profile"`
}

// Conversation は会話1つと、その発言を古い順に返す。
func (s *Service) Conversation(ctx context.Context, id uuid.UUID) (ConversationDetail, error) {
	if s.startupErr != nil {
		return ConversationDetail{}, s.startupErr
	}
	c, ms, err := s.convs.Get(ctx, id)
	if err != nil {
		return ConversationDetail{}, err
	}
	comps, err := s.convs.Compactions(ctx, id)
	if err != nil {
		return ConversationDetail{}, err
	}
	g := s.convs.Generating()
	u, err := s.Usage(ctx, id)
	if err != nil {
		return ConversationDetail{}, err
	}
	p, err := s.convs.ProfileOf(ctx, c)
	if err != nil {
		return ConversationDetail{}, err
	}
	return ConversationDetail{Conversation: c, Messages: ms, Compactions: comps, Generating: g != nil && *g == id, Usage: u, Profile: p}, nil
}

// Usage は、会話がモデルに渡す文字列で、会話の上限をどれだけ使っているか(メーター)を返す。
// トークン数は今のモデルで数えるので、モデルが読み込まれていなければ null を返す。
// 画面は、モデル・上限・会話の設定を変えたあとに呼び直す。
func (s *Service) Usage(ctx context.Context, id uuid.UUID) (*aibow.Usage, error) {
	if s.startupErr != nil {
		return nil, s.startupErr
	}
	u, err := s.convs.Usage(ctx, id)
	// モデルが無い(降ろした)・書式に未対応のモデルなら、数えられないのでメーターを出さない。
	// エラーにすると会話を開くこと(Conversation)まで失敗し、開くたびにエラーのカードが積み重なるため(未対応なことは読み込んだときと送信したときに伝える)
	if errors.Is(err, engine.ErrNoModel) || errors.Is(err, format.ErrUnsupported) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// NewConversation は、プロファイル profile から新しい会話を作る(空なら新しい会話で最初に選ばれるプロファイル)。
// settings が null ならプロファイルの値のコピーで始まる。
// 画面は、新しい会話の画面で最初に送信するときに呼ぶ(空の会話を作らないため)。
func (s *Service) NewConversation(ctx context.Context, profile uuid.UUID, settings *aibow.ConversationSettings) (aibow.Conversation, error) {
	if s.startupErr != nil {
		return aibow.Conversation{}, s.startupErr
	}
	// プロファイルにモデルが無い(初めての起動)と、会話の仕組みは「読み込んでいるモデル」を会話のモデルにする。
	// ここでは、画面が見せている読み込み中のモデルにする。aibow-server がほかの人のためにモデルを入れ替えている間は、
	// 読み込んでいるモデルが無い(前のモデルは解放済み)が、画面には前のモデルを見せているため(2026-10-22)。
	// モデルを降ろしている(0015 の 8-4)なら、最後に使ったモデルにし、送信の前に読み込む(0015 の 29)
	if settings != nil && settings.Model == "" {
		model := s.lastModel()
		if i := s.host.info(); i != nil {
			model = i.ID
		}
		if model != "" {
			cp := *settings
			cp.Model = model
			settings = &cp
		}
	}
	return s.convs.CreateIn(ctx, profile, settings)
}

// lastModel は最後に読み込んだモデル(次の起動で読み込むモデル)。
func (s *Service) lastModel() string {
	s.appMu.Lock()
	defer s.appMu.Unlock()
	return s.app.Workspace.Model
}

// DeleteConversation は会話を消す。返事を生成中の会話は消せない。
func (s *Service) DeleteConversation(ctx context.Context, id uuid.UUID) error {
	if s.startupErr != nil {
		return s.startupErr
	}
	return s.convs.Delete(ctx, id)
}

// RenameConversation は会話のタイトルを変える。空にはできない。
func (s *Service) RenameConversation(ctx context.Context, id uuid.UUID, title string) error {
	if s.startupErr != nil {
		return s.startupErr
	}
	return s.convs.Rename(ctx, id, title)
}

// SetExcluded は、吹き出し1つ分の発言を「モデルに渡さない」にする / 戻す(0011)。msgID は吹き出しの中の発言のどれか。
// 返事なら、ツールの呼び出しと結果も一緒に変わる。生成中の会話は変えられない。次の送信から効く。
func (s *Service) SetExcluded(ctx context.Context, id, msgID uuid.UUID, excluded bool) error {
	if s.startupErr != nil {
		return s.startupErr
	}
	if err := s.convs.SetExcluded(ctx, id, msgID, excluded); err != nil {
		return err
	}
	log.Infof("会話 %s の発言 %s を、モデルに%s", id, msgID, map[bool]string{true: "渡さないにしました", false: "渡すに戻しました"}[excluded])
	return nil
}

// UpdateConversationSettings は会話の設定を変える。次の送信から効く。
func (s *Service) UpdateConversationSettings(ctx context.Context, id uuid.UUID, settings aibow.ConversationSettings) error {
	if s.startupErr != nil {
		return s.startupErr
	}
	return s.convs.UpdateSettings(ctx, id, settings)
}

// ---- 送信 -------------------------------------------------------------------------------

// Send は会話にユーザーの発言を足し、返事を生成する。
// 返事の中身は戻り値ではなく、chat イベントで少しずつ送る。
//
// 終わりの合図も戻り値ではなく done イベントにしている。
// Wails ではメソッドの戻り値とイベントは別の経路で画面に届くので、
// 戻り値が先に着くと、最後のほうの文字がまだ届いていないことがあるため。
//
// 画面で「停止」を押すと、JavaScript 側で Promise の cancel() が呼ばれ、
// Wails がこの ctx をキャンセルする。エンジンはそれを見て生成を止める。
func (s *Service) Send(ctx context.Context, id uuid.UUID, text string) {
	s.run(ctx, id, func(ctx context.Context, emit func(aibow.ChatEvent)) {
		s.convs.Send(ctx, id, text, emit)
	})
}

// Regenerate は最後の返事を作り直す(0011)。途中経過と終わり(done)は Send と同じく chat イベントで送る。
func (s *Service) Regenerate(ctx context.Context, id uuid.UUID) {
	s.run(ctx, id, func(ctx context.Context, emit func(aibow.ChatEvent)) {
		s.convs.Regenerate(ctx, id, emit)
	})
}

// Resend は最後の発言を text に直して送り直す(0011)。途中経過と終わり(done)は Send と同じく chat イベントで送る。
func (s *Service) Resend(ctx context.Context, id uuid.UUID, text string) {
	s.run(ctx, id, func(ctx context.Context, emit func(aibow.ChatEvent)) {
		s.convs.Resend(ctx, id, text, emit)
	})
}

// Compact は「今すぐ要約」。会話の上限を超えていなくても、古い発言を要約する。
// Send と同じく、途中経過(要約の文)と終わり(done)は chat イベントで送る。
func (s *Service) Compact(ctx context.Context, id uuid.UUID) {
	s.run(ctx, id, func(ctx context.Context, emit func(aibow.ChatEvent)) {
		s.convs.Compact(ctx, id, emit)
	})
}

// ConfirmTool は、実行前の確認を待っているツールに答える(カードの「実行する」「拒否」)。
// reqID は confirm イベントの ID。拒否するときの reason は空でもよい(モデルには「理由: (書かれていません)」と渡る)。
// remember は、このツールのこの会話でのこれから先の扱い(空ならこの1回だけ。allow = この会話では今後聞かない、off = 今後許可しない。0010)。
func (s *Service) ConfirmTool(ctx context.Context, id uuid.UUID, reqID string, approved bool, reason string, remember aibow.ToolPolicy) error {
	if s.startupErr != nil {
		return s.startupErr
	}
	return s.convs.Answer(ctx, id, reqID, aibow.Confirmation{Approved: approved, Reason: reason, Remember: remember})
}

// StopSummary は生成中の要約だけを止める(要約中に出る停止ボタン)。
// 送信の途中の要約なら、その回は古い発言を外して(truncate)返事の生成に進む。
// Promise の cancel() だと送信全体が止まってしまうので、別のメソッドにしている。
func (s *Service) StopSummary() {
	if s.startupErr == nil {
		s.convs.StopSummary()
	}
}

// run は Send / Compact の共通部分: 停止とアプリの終了で止める ctx を作り、chat イベントを画面に送りながらログに出す。
func (s *Service) run(ctx context.Context, id uuid.UUID, f func(ctx context.Context, emit func(aibow.ChatEvent))) {
	emit := s.notify.Chat
	if s.startupErr != nil {
		emit(aibow.ChatEvent{ConversationID: id, Type: "error", Error: s.startupErr.Error()})
		emit(aibow.ChatEvent{ConversationID: id, Type: "done"})
		return
	}

	// 画面の停止ボタンか、アプリの終了のどちらかで止める
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(s.quit, cancel)
	defer stop()

	// 順番待ちの席があれば(aibow-server の中)、送信の全体(モデルの読み直し・要約・返事)の間、席を持つ。
	// 生成1回ごとに取り直すと、その間に Cline などがモデルを入れ替えてしまうため
	if !s.takeSeat(ctx, id, emit) {
		return
	}
	if s.seat != nil {
		defer s.seat.Release()
	}

	f(ctx, func(ev aibow.ChatEvent) {
		switch ev.Type {
		case "summarizing":
			log.Infof("要約を作っています…")
		case "compact":
			logCompact(ev.Compact)
		case "error":
			log.Error(ev.Error)
		case "confirm":
			if c := ev.Confirm; c != nil {
				log.Infof("確認を待っています: %s %s(%s)", c.Call.Name, c.Preview.Path, c.Preview.Note)
			}
		case "reply":
			// ツールを使うと1回の送信で何回も生成する。回ごとに出すと、KV キャッシュの再利用数の変化が見える
			if r := ev.Reply; r != nil && r.Stats != nil {
				st := r.Stats
				log.Infof("プロンプト %d トークン(再利用 %d)%.2f 秒 / 生成 %d トークン %.2f 秒(%.1f tok/s)/ 終了理由: %s / ツール呼び出し %d 件",
					st.PromptTokens, st.ReusedTokens, st.PromptSec, st.GenTokens, st.GenSec, tokPerSec(st.GenerateStats), st.StopReason, len(r.ToolCalls))
			}
		case "tool_result":
			logTool(ev.Message)
		}
		emit(ev)
	})
}

// takeSeat は順番待ちの席を取る(席が無ければ何もしない)。空いていなければ「待っています」と知らせてから待つ。
// 待っている間に止められたら(停止ボタン・ブラウザを閉じた)、done を送って false。
func (s *Service) takeSeat(ctx context.Context, id uuid.UUID, emit func(aibow.ChatEvent)) bool {
	if s.seat == nil || s.seat.TryTake() {
		return true
	}
	log.Infof("会話 %s: ほかの生成(Cline など)が終わるのを待っています", id)
	emit(aibow.ChatEvent{ConversationID: id, Type: "notice", Text: "ほかの生成が終わるのを待っています…"})
	if s.seat.Take(ctx) {
		return true
	}
	log.Infof("会話 %s: 順番を待っている間に止められました", id)
	emit(aibow.ChatEvent{ConversationID: id, Type: "done"})
	return false
}

func tokPerSec(st engine.GenerateStats) float64 {
	if st.GenSec == 0 {
		return 0
	}
	return float64(st.GenTokens) / st.GenSec
}

// logTool はツールの実行をログに出す(学習用: 名前・引数・結果の長さ・秒数・エラー)。
// 書式が崩れていた呼び出しは、モデルが書いたままの文字列も出す(どう崩れたかを見るため)。
func logTool(m *aibow.StoredMessage) {
	r := m.ToolResult
	if r == nil {
		return
	}
	var args []string
	for _, a := range r.Call.Args {
		args = append(args, fmt.Sprintf("%s=%q", a.Name, a.Value))
	}
	status := "成功"
	if r.Error {
		status = m.Content
	}
	log.Infof("ツール %s(%s): 結果 %d 文字 / %.3f 秒 / %s", cmp.Or(r.Call.Name, "?"), strings.Join(args, ", "),
		utf8.RuneCountInString(m.Content), r.Sec, status)
	if c := r.Confirm; c != nil {
		if c.Approved {
			log.Infof("  確認: 実行した")
		} else {
			log.Infof("  確認: 拒否した(理由: %q)", c.Reason)
		}
	}
	if r.Call.Raw != "" {
		log.Infof("  モデルが書いた呼び出し: %q", r.Call.Raw)
	}
}

// logCompact は compact したことをログに出す(学習用: 方式・前後のトークン数・かかった秒数)。
func logCompact(c *aibow.CompactInfo) {
	switch c.Method {
	case aibow.CompactTruncate:
		log.Infof("compact(truncate): %d → %d トークン(古い発言 %d 件をモデルに渡しません)",
			c.BeforeTokens, c.AfterTokens, c.Dropped)
	case aibow.CompactSummary:
		if c.Error != "" {
			log.Warnf("compact(summary): %s", c.Error)
			return
		}
		st := c.Compaction.Stats
		log.Infof("compact(summary): %d → %d トークン(発言 %d 件を要約)/ 要約: 読ませた %d トークン(再利用 %d)・生成 %d トークン / %.1f 秒",
			c.BeforeTokens, c.AfterTokens, c.Dropped, st.PromptTokens, st.ReusedTokens, st.GenTokens, c.Sec)
	}
}
