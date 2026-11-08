// Package ui は、画面(frontend)から呼ばれる Go のメソッド(画面向けの処理)。
//
// デスクトップのアプリ(_cmd/aibow)は、Service を ChatService に埋め込んで Wails に登録する。
// Wails では、登録した構造体の「大文字で始まるメソッド」を、画面の JavaScript から関数のように呼べる(中身は Wails が仲立ちする RPC)。
// 埋め込んだ型のメソッドも ChatService のメソッドとして見える。
//
//	TypeScript: ChatService.Send(id, text, files)  ← wails3 generate bindings が frontend/bindings に作る関数
//	        ↓  Wails が引数を JSON にして Go に渡す
//	Go:         (*ChatService).Send(ctx, id, text, files) = (*ui.Service).Send
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
	"context"
	"maps"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/secondarykey/aibow"
	"github.com/secondarykey/aibow/config"
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
	// Chat は chat イベント(生成の途中経過)を送る。ctx は送信の呼び出しのもので、誰の会話かが入っている
	// (ブラウザでは、その人の画面にだけ送る。0017 の 7)
	Chat(ctx context.Context, ev aibow.ChatEvent)
	// ModelLoaded は models イベント(起動時のモデルの読み込みが終わった)を送る。モデルは全員で分け合うので、全員に送る
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
	Prefs *config.ToolPrefs
	// MCPConfig は、画面から MCP のサーバーを足す・変える・消す係(mcp.json。0034)。デスクトップのアプリだけで、ブラウザでは nil
	MCPConfig *config.MCPConfig
	// Workspaces は、その PC のワークスペース(workspaces.json。0035)。デスクトップのアプリだけで、ブラウザでは nil
	Workspaces *config.Workspaces
	// Version はアプリのバージョン(画面の左下に出す)
	Version string
	// Notices は起動時に画面のトーストで伝えること。StartupErr は起動に失敗した理由(画面に表示する)
	Notices    []string
	StartupErr error
	// Notify は画面に知らせる係
	Notify Notifier

	// Seat は生成の順番待ちの席。nil なら待たない(デスクトップのアプリ。モデルを使うのはこのアプリだけか、待つのは aibow-server の側)
	Seat Seat
	// Browser は、aibow-server の中のブラウザの会話か(0024)。ツールはサーバーの PC で動くので、次のように守る:
	//   - 会話に保存した作業フォルダを使わない(サーバーの PC のフォルダに触れさせない。決定事項 17)
	//   - 確認を待つのは confirmLimit まで(席を持ったまま待つので、ほかの人と Cline をずっと待たせない。決定事項 16)
	//   - 利用者は会話の設定で「確認しない」を足せない(足せるのは確認のカードから。決定事項 3・18)
	//   - 作業フォルダの代わりに、配るプロファイルで選んだ資料のフォルダ(Folders)を使う。コマンドのツールは出さない(12-2。決定事項 5)
	Browser bool
	// Folders は aibow-server の資料のフォルダ(server.json の folders。名前 → 絶対パス。Browser のときだけ使う。0024 の 12-2)
	Folders map[string]string
}

// noCommand は、組み込みのツールからコマンドのツール(run_command)を外す係(aibow.LocalTools)。
// ブラウザの会話の資料のフォルダでは、読む・書くだけを出す(コマンドは隔離が要るので 12-3。0024 の決定事項 5)。
// モデルに見せないので、呼ばれても「ツールはありません」になる。
type noCommand struct{ aibow.LocalTools }

func (n noCommand) Tools(folders []tool.Folder) []tool.Tool {
	// 写してから外す(Local はつないだサーバーのツールの一覧を覚えていて、同じスライスを返すため)
	return slices.DeleteFunc(slices.Clone(n.LocalTools.Tools(folders)), func(t tool.Tool) bool { return t.Def().Name == "run_command" })
}

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
	prefs *config.ToolPrefs
	// mcpConfig は、画面から MCP のサーバーを変える係(デスクトップのアプリだけ。ブラウザでは nil。mcpedit.go)
	mcpConfig *config.MCPConfig
	// workspaces は、その PC のワークスペース(デスクトップのアプリだけ。ブラウザでは nil。workspaces.go)
	workspaces *config.Workspaces

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

	// browser は、aibow-server の中のブラウザの会話か(Options.Browser)。folders はその資料のフォルダの名前(名前の順)
	browser bool
	folders []string
	// waits は、確認を待っている送信を打ち切るタイマー(確認の ID → タイマー。ブラウザだけ)。答えたら止める
	waitMu sync.Mutex
	waits  map[string]*time.Timer

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
		quit: quit, cancelQuit: cancel, mcp: o.MCP, prefs: o.Prefs, mcpConfig: o.MCPConfig, workspaces: o.Workspaces, version: o.Version,
		browser: o.Browser, waits: map[string]*time.Timer{}, folders: slices.Sorted(maps.Keys(o.Folders)),
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
		if o.Local != nil { // nil の *Local を interface に入れると nil に見えなくなる(MCP と同じ)
			s.convs.SetLocalTools(o.Local)
		}
		if o.Browser {
			// 会話に保存した作業フォルダは使わず、配るプロファイルで選んだ資料のフォルダだけを使う(無ければ組み込みのツールは時刻と計算だけ)
			s.convs.IgnoreWorkDir()
			s.convs.SetFolders(o.Folders)
			if o.Local != nil {
				s.convs.SetLocalTools(noCommand{o.Local})
			}
		}
		if !o.Browser {
			s.convs.SetWorkspaces(o.Workspaces)
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
		// ワークスペースより前の会話の作業フォルダを、この PC にあるものだけワークスペースに移す(0035 の段1)
		s.migrateWorkDirs(ctx)
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
