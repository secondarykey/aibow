package main

// service.go: 画面(frontend)から呼ばれる Go のメソッド。
//
// Wails では、Service として登録した構造体の「大文字で始まるメソッド」を、
// 画面の JavaScript から関数のように呼べる(中身は Wails が仲立ちする RPC)。
//
//	TypeScript: ChatService.Send(id, text)  ← wails3 generate bindings が frontend/bindings に作る関数
//	        ↓  Wails が引数を JSON にして Go に渡す
//	Go:         (*ChatService).Send(ctx, id, text)
//
// メソッドのコメントと構造体の json タグは、バインディングの TypeScript の型とコメントにもなる。
//
// 会話の履歴や設定を持つのは aibow パッケージの Conversations で、ChatService は画面との中継をするだけ。

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
	"github.com/secondarykey/aibow/log"
	"github.com/secondarykey/aibow/mcp/client"

	"github.com/wailsapp/wails/v3/pkg/application"
)

type ChatService struct {
	engine  *aibow.Engine        // 起動に失敗したときは nil
	store   aibow.Store          // 起動に失敗したときは nil
	convs   *aibow.Conversations // 起動に失敗したときは nil
	catalog aibow.ModelCatalog

	// startupErr は起動に失敗した理由(lib が無いなど)。画面に表示する
	startupErr error

	// mcp は settings.json の MCP のサーバー(書いてなければ nil)。prefs はツールの扱いの PC の既定(tools.json)
	mcp   *client.Manager
	prefs *aibow.ToolPrefs

	// version はアプリのバージョン(_cmd/aibow/version。画面の左下に出す)
	version string

	// app はアプリ全体の設定(作業領域に関わる設定)。画面で変えると DB にも保存する
	appMu sync.Mutex
	app   aibow.AppSettings

	// notices は起動時に画面のトーストで伝えること(使われなくなった環境変数など)
	notices []string

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

// mcp は MCP のサーバー(settings.json にサーバーが無ければ nil)、prefs はツールの扱いの PC の既定(tools.json)。
func NewChatService(engine *aibow.Engine, st aibow.Store, app aibow.AppSettings, catalog aibow.ModelCatalog,
	initial *aibow.ModelEntry, mcp *client.Manager, prefs *aibow.ToolPrefs, version string, notices []string, startupErr error) *ChatService {
	quit, cancel := context.WithCancel(context.Background())
	s := &ChatService{
		engine: engine, catalog: catalog, app: app, initial: initial, notices: notices, startupErr: startupErr,
		quit: quit, cancelQuit: cancel, mcp: mcp, prefs: prefs, version: version,
	}
	if startupErr == nil {
		s.store = st
		s.convs = aibow.NewConversations(st, engine, app)
		if mcp != nil { // nil の *Manager を interface に入れると nil に見えなくなるので、確かめてから渡す
			s.convs.SetRemoteTools(mcp)
		}
		s.convs.SetToolPrefs(prefs)
	}
	return s
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
	s.loadModel(m)
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

// ModelsResult は Models の戻り値。
type ModelsResult struct {
	Models  []aibow.ModelEntry `json:"models"`
	Current *aibow.ModelInfo   `json:"current"`           // 未読み込みなら null
	Loading string             `json:"loading,omitempty"` // 起動時に読み込み中のモデルの名前
	GPU     bool               `json:"gpu"`
	Error   string             `json:"error,omitempty"` // 起動に失敗した理由。あれば何もできない
	Notices []string           `json:"notices"`         // 起動時に伝えること(使われなくなった環境変数など)
}

// Models は使えるモデルの一覧と、今読み込んでいるモデルを返す。
func (s *ChatService) Models() ModelsResult {
	if s.startupErr != nil {
		return ModelsResult{Models: []aibow.ModelEntry{}, Error: s.startupErr.Error(), Notices: s.notices}
	}
	r := ModelsResult{
		Models:  s.catalog.List(),
		Current: s.engine.Info(),
		GPU:     s.engine.HasGPU,
		Notices: s.notices,
	}
	if name := s.loading.Load(); name != nil {
		r.Loading = *name
	}
	return r
}

// SwitchModel はモデルを切り替える。読み込み終わるまで返らない。
// 失敗したときは元のモデルに戻っている(画面は Models を呼び直して確認する)。
// Go で error を返すと、JavaScript 側では Promise が reject される。
//
// モデルはアプリ全体で1つ(VRAM に1つしか載らない)。会話を切り替えてもモデルは変わらない。
func (s *ChatService) SwitchModel(id string) (*aibow.ModelInfo, error) {
	if s.startupErr != nil {
		return nil, s.startupErr
	}
	m, err := s.catalog.Find(id)
	if err != nil {
		return nil, err
	}
	return s.loadModel(m)
}

// loadModel はモデルを読み込み、成功したら「最後に使ったモデル」として保存する(次の起動で読み込む)。
func (s *ChatService) loadModel(m aibow.ModelEntry) (*aibow.ModelInfo, error) {
	log.Infof("モデルを読み込み中: %s (%s)", m.Name, m.Path)
	i, err := s.engine.LoadModel(m)
	if err != nil {
		log.Error(err.Error())
		return nil, err
	}
	logModel("読み込み完了", i)
	if i.Format == "" {
		log.Warnf("このモデルの書式(種類: %s)には未対応です。送信するとエラーになります", i.Arch)
	}
	s.saveAppSetting(aibow.SettingLastModel, m.ID, func(a *aibow.AppSettings) { a.Workspace.Model = m.ID })
	return i, nil
}

func logModel(what string, i *aibow.ModelInfo) {
	log.Noticef("%s (%.1f 秒): %s / %.2fB パラメータ / %.2f GB / %d 層 / GPU に載せる層: %d / 作業領域: %d トークン / 書式: %s",
		what, i.LoadSec, i.Desc, i.ParamsB, i.SizeGB, i.Layers, i.GPULayers, i.NCtx, cmp.Or(i.Format, "未対応("+i.Arch+")"))
}

// ---- アプリ全体の設定 -------------------------------------------------------------------

// AppSettings はアプリ全体の設定(会話の上限・返事の上限・GPU の層数・新しい会話の既定の設定)を返す。
func (s *ChatService) AppSettings() (aibow.AppSettings, error) {
	if s.startupErr != nil {
		return aibow.AppSettings{}, s.startupErr
	}
	s.appMu.Lock()
	defer s.appMu.Unlock()
	a := s.app
	a.Defaults = s.convs.Defaults()
	return a, nil
}

// SetLimits は会話の上限と返事の上限を変える。
// 作業領域(コンテキスト)を「会話の上限 + 返事の上限」の大きさで作り直す。モデルは読み直さない。
// モデルが未読み込みなら nil を返す(次に読み込むときから効く)。
func (s *ChatService) SetLimits(history, reply int) (*aibow.ModelInfo, error) {
	if s.startupErr != nil {
		return nil, s.startupErr
	}
	if history <= 0 || reply <= 0 {
		return nil, errors.New("会話の上限と返事の上限は 1 以上にしてください")
	}
	w := aibow.WorkspaceSettings{HistoryTokens: history, ReplyTokens: reply}
	i, err := s.engine.SetContextSize(w.ContextSize())
	if err != nil {
		log.Error(err.Error())
		return nil, err
	}
	s.convs.SetLimits(history, reply)
	log.Noticef("会話の上限 %d + 返事の上限 %d = 作業領域 %d トークンにしました(モデルは読み直していません)",
		history, reply, w.ContextSize())
	s.saveAppSetting(aibow.SettingHistoryTokens, history, func(a *aibow.AppSettings) { a.Workspace.HistoryTokens = history })
	s.saveAppSetting(aibow.SettingReplyTokens, reply, func(a *aibow.AppSettings) { a.Workspace.ReplyTokens = reply })
	return i, nil
}

// SetGPULayers は GPU に載せる層の数を変え、今のモデルを読み込み直す。
// 失敗したときは元の層の数で読み直している。モデルが未読み込みなら nil を返す。
func (s *ChatService) SetGPULayers(n int) (*aibow.ModelInfo, error) {
	if s.startupErr != nil {
		return nil, s.startupErr
	}
	i, err := s.engine.SetGPULayers(n)
	if err != nil {
		log.Error(err.Error())
		return nil, err
	}
	if i != nil {
		logModel("GPU に載せる層を変えて読み直しました", i)
	}
	s.saveAppSetting(aibow.SettingGPULayers, n, func(a *aibow.AppSettings) { a.Workspace.GPULayers = n })
	return i, nil
}

// SetDefaultSettings は新しい会話の既定の設定を変える。今ある会話には効かない。
func (s *ChatService) SetDefaultSettings(settings aibow.ConversationSettings) error {
	if s.startupErr != nil {
		return s.startupErr
	}
	return s.convs.SetDefaults(settings)
}

// saveAppSetting はアプリ全体の設定を1つ保存する。
// 保存に失敗しても、変えた設定は今の起動中は効いているので、ログに出すだけにする。
func (s *ChatService) saveAppSetting(key string, v any, apply func(*aibow.AppSettings)) {
	s.appMu.Lock()
	defer s.appMu.Unlock()
	apply(&s.app)
	if err := aibow.SaveAppSetting(s.store, key, v); err != nil {
		log.Error(err.Error())
	}
}

// Version はアプリのバージョン(画面の左下に出す)。起動に失敗していても返す。
func (s *ChatService) Version() string {
	return s.version
}

// ---- MCP のサーバーとツールの扱い ------------------------------------------------------

// MCPServers は、会話ごとに使うものを選ぶ MCP のサーバーの一覧(settings.json に書いたもの全部)。書いてなければ空。
// 設定の画面の「ツール」のチェックは「新しい会話で最初から選ぶか」だけなので、チェックを外したものもここに出す。
func (s *ChatService) MCPServers() []client.ServerInfo {
	if s.mcp == nil {
		return []client.ServerInfo{}
	}
	return s.mcp.Infos()
}

// AllMCPServers は settings.json に書いた MCP のサーバー全部(設定の画面の「ツール」に出す)。
func (s *ChatService) AllMCPServers() []client.ServerInfo {
	if s.mcp == nil {
		return []client.ServerInfo{}
	}
	return s.mcp.Infos()
}

// BuiltinTools は組み込みのツール(aibow の中で動くツール)の一覧(設定の画面の「ツール」に出す)。
func (s *ChatService) BuiltinTools() []aibow.KnownTool {
	return aibow.BuiltinTools()
}

// ToolStates は、会話の設定 settings でのツールの扱い(会話の設定の画面に出す)。
// 画面がまだ保存していない設定でも見られるよう、会話の ID ではなく設定を受け取る。
func (s *ChatService) ToolStates(settings aibow.ConversationSettings) ([]aibow.ToolGroupState, error) {
	if s.startupErr != nil {
		return nil, s.startupErr
	}
	return s.convs.ToolStates(settings), nil
}

// ToolPrefs は、ツールの扱いの PC の既定(tools.json)と、サーバーを起動したときに見えたツールの一覧。
func (s *ChatService) ToolPrefs() aibow.ToolPrefsView {
	return s.prefs.View()
}

// LoadToolList は、MCP のサーバーを一時的に起動してツールの一覧を受け取り、tools.json に覚える(設定の画面で、一覧が無いときと「読み直す」)。
func (s *ChatService) LoadToolList(ctx context.Context, server string) error {
	if s.startupErr != nil {
		return s.startupErr
	}
	return s.convs.LoadToolList(ctx, server)
}

// SetToolPolicy は、ツールの扱いの PC の既定を変える(tools.json に書く)。policy が空なら決めていない状態に戻す。
// 次の送信から効く(プロンプトに入るツールが変わるので、画面はメーターを数え直す)。
func (s *ChatService) SetToolPolicy(name string, policy aibow.ToolPolicy) error {
	if err := s.prefs.SetToolPolicy(name, policy); err != nil {
		return err
	}
	log.Noticef("ツール %s の扱いの PC の既定: %s", name, policyLabel(policy))
	return nil
}

// SetGroupDefault は、ツールのグループを新しい会話で最初から選ぶかを変える(tools.json に書く)。
// group が空なら組み込みのツール、そうでなければ MCP のサーバー。画面ではグループのチェック。
func (s *ChatService) SetGroupDefault(group string, on bool) error {
	if err := s.prefs.SetGroupDefault(group, on); err != nil {
		return err
	}
	name := group
	if name == "" {
		name = "組み込み"
	}
	log.Noticef("ツールのグループ %s を新しい会話で最初から選ぶ: %v", name, on)
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
	}
	return "決めていない"
}

// ---- 会話 -------------------------------------------------------------------------------

// Conversations は会話の一覧を、更新の新しい順に返す。
func (s *ChatService) Conversations() ([]aibow.Conversation, error) {
	if s.startupErr != nil {
		return nil, s.startupErr
	}
	return s.convs.List()
}

// ConversationDetail は会話1つと、その発言。
type ConversationDetail struct {
	Conversation aibow.Conversation    `json:"conversation"`
	Messages     []aibow.StoredMessage `json:"messages"`
	Compactions  []aibow.Compaction    `json:"compactions"` // 要約(古い順)。画面の区切りに使う
	Generating   bool                  `json:"generating"`  // この会話で返事を生成中か
	Usage        *aibow.Usage          `json:"usage"`       // メーター。モデルが読み込まれていなければ null
}

// Conversation は会話1つと、その発言を古い順に返す。
func (s *ChatService) Conversation(id uuid.UUID) (ConversationDetail, error) {
	if s.startupErr != nil {
		return ConversationDetail{}, s.startupErr
	}
	c, ms, err := s.convs.Get(id)
	if err != nil {
		return ConversationDetail{}, err
	}
	comps, err := s.convs.Compactions(id)
	if err != nil {
		return ConversationDetail{}, err
	}
	g := s.convs.Generating()
	u, err := s.Usage(id)
	if err != nil {
		return ConversationDetail{}, err
	}
	return ConversationDetail{Conversation: c, Messages: ms, Compactions: comps, Generating: g != nil && *g == id, Usage: u}, nil
}

// Usage は、会話がモデルに渡す文字列で、会話の上限をどれだけ使っているか(メーター)を返す。
// トークン数は今のモデルで数えるので、モデルが読み込まれていなければ null を返す。
// 画面は、モデル・上限・会話の設定を変えたあとに呼び直す。
func (s *ChatService) Usage(id uuid.UUID) (*aibow.Usage, error) {
	if s.startupErr != nil {
		return nil, s.startupErr
	}
	u, err := s.convs.Usage(id)
	if errors.Is(err, aibow.ErrNoModel) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// NewConversation は新しい会話を作る。settings が null なら「新しい会話の既定の設定」のコピーで始まる。
// 画面は、新しい会話の画面で最初に送信するときに呼ぶ(空の会話を作らないため)。
func (s *ChatService) NewConversation(settings *aibow.ConversationSettings) (aibow.Conversation, error) {
	if s.startupErr != nil {
		return aibow.Conversation{}, s.startupErr
	}
	return s.convs.Create(settings)
}

// DeleteConversation は会話を消す。返事を生成中の会話は消せない。
func (s *ChatService) DeleteConversation(id uuid.UUID) error {
	if s.startupErr != nil {
		return s.startupErr
	}
	return s.convs.Delete(id)
}

// RenameConversation は会話のタイトルを変える。空にはできない。
func (s *ChatService) RenameConversation(id uuid.UUID, title string) error {
	if s.startupErr != nil {
		return s.startupErr
	}
	return s.convs.Rename(id, title)
}

// UpdateConversationSettings は会話の設定を変える。次の送信から効く。
func (s *ChatService) UpdateConversationSettings(id uuid.UUID, settings aibow.ConversationSettings) error {
	if s.startupErr != nil {
		return s.startupErr
	}
	return s.convs.UpdateSettings(id, settings)
}

// ChooseWorkDir は作業フォルダを選ぶダイアログを出し、選んだフォルダの絶対パスを返す。取り消したら空。
// current が空でなければ、そのフォルダから開く。選んだフォルダを会話に入れるのは画面の側(会話の設定として保存する)。
func (s *ChatService) ChooseWorkDir(current string) (string, error) {
	app := application.Get()
	d := app.Dialog.OpenFile().
		SetTitle("作業フォルダを選ぶ").
		CanChooseFiles(false).
		CanChooseDirectories(true).
		CanCreateDirectories(true)
	if current != "" {
		d.SetDirectory(current)
	}
	if w := app.Window.Current(); w != nil {
		d.AttachToWindow(w) // ウィンドウの前に出す(後ろに隠れて、操作できなくなったように見えるのを防ぐ)
	}
	dir, err := d.PromptForSingleSelection()
	if err != nil {
		return "", fmt.Errorf("フォルダを選ぶダイアログを出せません: %w", err)
	}
	return dir, nil
}

// ---- 送信 -------------------------------------------------------------------------------

// chatEventName は、生成の途中経過を画面に送るイベントの名前。
// 中身は aibow.ChatEvent(main.go の RegisterEvent で型を登録している)。
const chatEventName = "chat"

// Send は会話にユーザーの発言を足し、返事を生成する。
// 返事の中身は戻り値ではなく、chat イベントで少しずつ送る。
//
// 終わりの合図も戻り値ではなく done イベントにしている。
// Wails ではメソッドの戻り値とイベントは別の経路で画面に届くので、
// 戻り値が先に着くと、最後のほうの文字がまだ届いていないことがあるため。
//
// 画面で「停止」を押すと、JavaScript 側で Promise の cancel() が呼ばれ、
// Wails がこの ctx をキャンセルする。エンジンはそれを見て生成を止める。
func (s *ChatService) Send(ctx context.Context, id uuid.UUID, text string) {
	s.run(ctx, id, func(ctx context.Context, emit func(aibow.ChatEvent)) {
		s.convs.Send(ctx, id, text, emit)
	})
}

// Compact は「今すぐ要約」。会話の上限を超えていなくても、古い発言を要約する。
// Send と同じく、途中経過(要約の文)と終わり(done)は chat イベントで送る。
func (s *ChatService) Compact(ctx context.Context, id uuid.UUID) {
	s.run(ctx, id, func(ctx context.Context, emit func(aibow.ChatEvent)) {
		s.convs.Compact(ctx, id, emit)
	})
}

// ConfirmTool は、実行前の確認を待っているツールに答える(カードの「実行する」「拒否」)。
// reqID は confirm イベントの ID。拒否するときの reason は空でもよい(モデルには「理由: (書かれていません)」と渡る)。
func (s *ChatService) ConfirmTool(id uuid.UUID, reqID string, approved bool, reason string) error {
	if s.startupErr != nil {
		return s.startupErr
	}
	return s.convs.Confirm(id, reqID, approved, reason)
}

// StopSummary は生成中の要約だけを止める(要約中に出る停止ボタン)。
// 送信の途中の要約なら、その回は古い発言を外して(truncate)返事の生成に進む。
// Promise の cancel() だと送信全体が止まってしまうので、別のメソッドにしている。
func (s *ChatService) StopSummary() {
	if s.startupErr == nil {
		s.convs.StopSummary()
	}
}

// run は Send / Compact の共通部分: 停止とアプリの終了で止める ctx を作り、chat イベントを画面に送りながらログに出す。
func (s *ChatService) run(ctx context.Context, id uuid.UUID, f func(ctx context.Context, emit func(aibow.ChatEvent))) {
	emit := func(ev aibow.ChatEvent) { application.Get().Event.Emit(chatEventName, ev) }
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

func tokPerSec(st aibow.GenerateStats) float64 {
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
