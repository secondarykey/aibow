package aibow

// conversation.go: 会話の仕組み。履歴の管理と、生成の呼び出し(ツールを使うループは agent.go)。
//
// モデルは何も覚えていない(ステートレス)。毎回「これまでの会話を全部」1本の文字列にして読ませている。
// 「記憶」に見えるものは、アプリがここで発言を保存し、次の送信のたびに並べ直しているだけ。
// 会話が長くなって会話の上限を超えたら、古い部分を捨てるか要約して収める(compact.go)。
// Claude などのチャットアプリも同じ構造になっている。
//
// 画面(_cmd/aibow)はこの仕組みを呼ぶだけで、履歴を持たない。
// Wails に依存しないので、ステップ6の API からも同じものを使える。

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"uuid"

	"github.com/secondarykey/aibow/engine"
	"github.com/secondarykey/aibow/format"
	"github.com/secondarykey/aibow/log"
	"github.com/secondarykey/aibow/tool"
)

// Generator は返事を生成するもの。本物は engine.Engine。テストでは偽物に差し替える。
//
// 会話の仕組みは、モデルに渡す内容を「構造」(format.Prompt)のまま渡し、結果も構造(format.Output)で受け取る。
// 文字列への書式の当て方・出力の振り分け方(モデルの系統ごとに違う)は Generator の側が持つ(format パッケージ)。
// この interface は会話の仕組みが「何を使うか」を表すので、使う側(ここ)に置く。
type Generator interface {
	Generate(ctx context.Context, p format.Prompt, opt engine.GenerateOptions,
		onPrompt func(prompt string), onThinking, onContent func(text string)) (format.Output, engine.GenerateStats, error)
	// CountTokens は、p に書式を当てた文字列のトークン数を数える。会話の上限に収まっているかの判断に使う(compact.go)
	CountTokens(p format.Prompt) (int, error)
	// EnsureContextSize は、作業領域を n トークンの大きさにする(同じ大きさなら何もしない)。作り直したら true。
	// 会話ごとに会話の上限・返事の上限が違うので、送信の前に呼ぶ(ensureWorkspace)
	EnsureContextSize(n int) (bool, error)
	Info() *engine.ModelInfo
}

// ChatEvent は送信の途中経過。Send がコールバックで1つずつ渡す。
//
//	user     : ユーザーの発言を保存した(Message に保存した発言。画面は ID を表示に付けて、薄い表示に使う)
//	prompt   : モデルに渡した文字列(学習用に画面に表示する)
//	thinking : 思考の断片
//	content  : 答えの断片
//	reply    : 返事を1件保存した(Reply)。ツールを使うと1回の送信で何件も保存する(呼び出しは Reply.ToolCalls)
//	tool_start  : ツールの実行を始めた(ToolCall)
//	tool_result : ツールの結果を保存した(Message。role tool の発言)
//	summarizing : 要約を作り始めた(compact の summary。返事の前か「今すぐ要約」)
//	summary  : 要約の断片
//	compact  : 会話の上限に収めるために compact した(方式・前後のトークン数・保存した要約など)。
//	           要約に失敗したときも Compact.Error に理由を入れて送る
//	usage    : メーターの値(モデルに渡す文字列のトークン数 / 会話の上限)。生成の前と、返事を保存したあとに送る
//	confirm  : ツールを実行する前に、ユーザーの確認を待っている(Confirm)。答えは Conversations.Confirm で返す
//	error    : エラー。Caution が立っていれば、赤いエラーではなく注意として出す(理由と対処が書いてある断り。例: 管理者が使えないようにしたモデル)
//	notice   : エラーではないお知らせ(Text。画面はトーストに出す)。例: 今すぐ要約で、要約するものが無かった
//	model_loading : 送信の前に、会話のモデルを読み込み始めた(Text にお知らせの文。0014 の 3)
//	model    : 会話のモデルを読み込んだ(Text にお知らせの文。画面は読み込んでいるモデルの表示を取り直す)
//	done     : 終わり。必ず最後に1回だけ送る。保存した最後の返事があれば Reply に入る
type ChatEvent struct {
	ConversationID uuid.UUID        `json:"conversation_id"` // どの会話の送信か。画面は表示中の会話のものだけを出す
	Type           string           `json:"type"`
	Text           string           `json:"text,omitempty"`
	Format         string           `json:"format,omitempty"` // prompt のとき、その文字列を組み立てた書式の名前(Qwen3.5・Gemma 4)
	Error          string           `json:"error,omitempty"`
	Caution        bool             `json:"caution,omitempty"` // error を注意として出すか(isCaution)
	Reply          *StoredMessage   `json:"reply,omitempty"`
	Message        *StoredMessage   `json:"message,omitempty"`
	Usage          *Usage           `json:"usage,omitempty"`
	Compact        *CompactInfo     `json:"compact,omitempty"`
	ToolCall       *format.ToolCall `json:"tool_call,omitempty"`
	Confirm        *ConfirmRequest  `json:"confirm,omitempty"`
}

// ConfirmRequest は、ツールを実行する前にユーザーに確かめる内容(confirm イベント)。
type ConfirmRequest struct {
	ID      string          `json:"id"`      // 答えるときに渡す(古い確認への答えを取り違えないため)
	Call    format.ToolCall `json:"call"`    // どの呼び出しか
	Preview tool.Preview    `json:"preview"` // 何をするか(パス・上書きか・差分)
}

// ErrGenerating は、別の送信の生成中に送信されたときのエラー。
var ErrGenerating = errors.New("別の会話で返事を生成中です。終わるまで待つか、その会話で停止してください")

// Conversations は会話の一覧と送信を扱う。
type Conversations struct {
	store Store
	gen   Generator
	tools []tool.Tool // 「ツールを使う」が ON の会話で、モデルが使えるツール(作業フォルダのツールは会話ごとに足す。toolsFor)
	// local は aibow の中で動くツール(組み込みと作業フォルダのツール)を渡す係。nil なら tool パッケージのツールを直接呼ぶ。
	// アプリは、aibow の中の自作の MCP サーバー越しに呼ぶもの(mcp/client の Local)を渡す(SetLocalTools。0007 の 5-4)
	local LocalTools

	// remote は MCP のサーバーのツール(nil なら使わない)。設定ファイルにサーバーが書いてあるときだけ、アプリが渡す(SetRemoteTools)
	remote RemoteTools
	// prefs はツールの扱いの PC の既定(tools.json)。nil なら何も決めていない扱い(SetToolPrefs)
	prefs *ToolPrefs
	// models は、会話のモデルを読み込む係(SetModelLoader)。nil なら読み直さず、読み込んでいるモデルで生成する
	models ModelLoader

	// generating は生成中の会話の ID。生成していなければ nil。
	// エンジン(作業領域)が1つしかないので、生成は同時に1つだけ。
	generating atomic.Pointer[uuid.UUID]

	// initial は、NewConversations に渡した既定から作った仮のプロファイル(保存していないので ID が空。作ったあとは変えない)。
	// プロファイルがまだ無いとき(PrepareProfiles の前)の新しい会話の既定と、プロファイル「既定」を作るときの値に使う。
	// 新しい会話で最初に選ばれるプロファイルは人ごとなので、ここには覚えず、そのつど保存先から読む(DefaultProfile。0017)
	initial Profile
	// mu は、プロファイルを読んでから書くまで(updateProfile・saveProfile)を順番にする
	mu sync.Mutex

	// stopSummary は生成中の要約だけを止める関数(StopSummary)。要約していなければ nil
	summaryMu   sync.Mutex
	stopSummary context.CancelFunc

	// pending は答えを待っている確認。待っていなければ nil。生成は同時に1つなので、確認も同時に1つだけ
	confirmMu sync.Mutex
	pending   *pendingConfirm

	// noWorkDir は、会話に保存した作業フォルダを使わないか(IgnoreWorkDir)
	noWorkDir bool
	// folders は aibow-server の資料のフォルダ(名前 → 絶対パス。SetFolders)
	folders map[string]string
}

// pendingConfirm は答えを待っている確認1つ。
type pendingConfirm struct {
	conv   uuid.UUID
	id     string
	tool   string // 確認しているツールの名前(これから先の扱いを会話の上書きに書くとき使う)
	req    ConfirmRequest
	answer chan Confirmation
}

// IgnoreWorkDir は、会話に保存した作業フォルダを使わないようにする(aibow-server の中のブラウザの会話。0024 の決定事項 17)。
//
// 作業フォルダはアプリの PC のパスで、その PC の人が画面のダイアログで選ぶもの。ブラウザの会話でツールを使うとき、
// 保存した値をそのまま使うと、ブラウザの人が画面の口から書いたパス(サーバー越しのアプリで作った会話なら、その PC のパス)で、
// サーバーの PC のファイルのツールとコマンドが出てしまう。なので、使うときの設定(load・List)では空にし、作る会話には書かせない。
// 設定を変えるときは保存してある値を残す(サーバー越しのアプリから開いたときは、今までどおりその PC のフォルダで使える)。
func (cs *Conversations) IgnoreWorkDir() {
	cs.noWorkDir = true
}

// limits は会話の上限と返事の上限。WorkspaceSettings のうち、会話の仕組みが使う2つ。
type limits struct {
	history int // 会話の上限: モデルに渡す文字列のトークン数の上限
	reply   int // 返事の上限: 1回の返事で生成するトークン数の上限
}

// hasRemedy は、err が理由と対処をもう書いてあるエラーか(aibow-server につながらない・サーバーの作業領域に入らない、など。0015)。
// そのときは、会話の仕組みは自分の対処(「モデルを選び直して」「GPU の層を減らして」)を書き足さない
// (手元のエンジンのための対処で、サーバー越しのときは当てはまらず、食い違うため)。
// エラーの側が HasRemedy() bool を持てば当てはまる。生成の相手(engine・api/client)を import せずに見分けるための約束。
func hasRemedy(err error) bool {
	var r interface{ HasRemedy() bool }
	return errors.As(err, &r) && r.HasRemedy()
}

// isCaution は、err を赤いエラーではなく注意として見せるか(0018 の決定事項 13。管理者が使えないようにしたモデル)。
// エラーの側が Caution() bool を持てば当てはまる(hasRemedy と同じく、models や api/client を見ずに見分けるための約束)。
func isCaution(err error) bool {
	var c interface{ Caution() bool }
	return errors.As(err, &c) && c.Caution()
}

// ModelLoader は、会話のモデルを読み込む係(0014 の 3)。アプリが渡す(SetModelLoader)。
// モデルの目録(どの ID がどのファイルか)と、モデルごとの GPU の層数(この PC のもの)はアプリが持つので、会話の仕組みは ID だけを渡す。
type ModelLoader interface {
	LoadModel(id string) (*engine.ModelInfo, error)
}

// SetModelLoader は、会話のモデルを読み込む係を渡す。渡さなければ、読み込んでいるモデルで生成する(ライブラリとして使うとき・テスト)。
func (cs *Conversations) SetModelLoader(m ModelLoader) {
	cs.models = m
}

// NewConversations は会話の仕組みを作る。app からは新しい会話の既定の設定と最後に使ったモデルを使う
// (プロファイルがまだ無いときに、プロファイル「既定」の値にする。PrepareProfiles)。
func NewConversations(store Store, gen Generator, app AppSettings) *Conversations {
	d := app.Defaults.asDefaults()
	d.Model = cmp.Or(d.Model, app.Workspace.Model) // プロファイルより前の既定にはモデルが無い
	initial := Profile{Name: DefaultProfileName, Values: valuesOf(d)}
	return &Conversations{store: store, gen: gen, tools: tool.Builtin(), initial: initial}
}

// limitsOf は会話の上限と返事の上限。会話の設定の値を使う。
// 会話は作るときに上限を持ち(CreateIn)、この機能より前の会話は起動時に埋める(FillMissingLimits)ので、ふつうは持っている。
// 持っていない(0 の)ものは、起動したときの新しい会話の既定の値を使う。
func (cs *Conversations) limitsOf(set ConversationSettings) limits {
	return limitsWith(set, cs.initial.Values)
}

// limitsWith は、会話の上限・返事の上限。持っていない(0 の)ものは d の値。
func limitsWith(set ConversationSettings, d ProfileValues) limits {
	return limits{history: cmp.Or(set.HistoryTokens, d.HistoryTokens), reply: cmp.Or(set.ReplyTokens, d.ReplyTokens)}
}

// withLimits は、会話の上限・返事の上限を持っていない設定に、新しい会話の既定(ctx の利用者の、最初に選ばれるプロファイル)の値を入れる。
func (cs *Conversations) withLimits(ctx context.Context, set ConversationSettings) (ConversationSettings, error) {
	if set.HistoryTokens > 0 && set.ReplyTokens > 0 {
		return set, nil
	}
	d, err := cs.DefaultProfile(ctx)
	if err != nil {
		return set, err
	}
	lim := limitsWith(set, d.Values)
	set.HistoryTokens, set.ReplyTokens = lim.history, lim.reply
	return set, nil
}

// ids は発言の ID の並び。
func ids(ms []StoredMessage) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.ID)
	}
	return out
}

// forModel は、モデルに渡す履歴(ユーザーが選んで「モデルに渡さない」にした発言を外したもの。0011)。
// 送信・メーター・今すぐ要約で、履歴を読んだところで通す。外したあとの並びで組(ユーザーの発言から次の発言の前まで)を数え直すので、
// 「質問 → 壊れた図 → 直して → 直った図」から真ん中の2つを外すと、「質問」の組に「直った図」が続く。
// 外すのは途中なので、そこから後ろは KV キャッシュが再利用できない(プロンプトの途中が変わるため)。
func forModel(history []StoredMessage) []StoredMessage {
	if !slices.ContainsFunc(history, func(m StoredMessage) bool { return m.Excluded }) {
		return history
	}
	return slices.DeleteFunc(slices.Clone(history), func(m StoredMessage) bool { return m.Excluded })
}

// SetExcluded は、吹き出し1つ分の発言を「モデルに渡さない」にする / 戻す(0011)。msg は吹き出しの中の発言のどれか。
// ユーザーの発言ならその1件、返事(assistant・tool)なら、前後のユーザーの発言の間にある発言を全部(ツールの呼び出しと結果を一緒に)。
// 生成中の会話は変えられない(途中で履歴が変わらないように)。
func (cs *Conversations) SetExcluded(ctx context.Context, id, msg uuid.UUID, excluded bool) error {
	if g := cs.generating.Load(); g != nil && *g == id {
		return errors.New("返事を生成中の会話では、モデルに渡すかを変えられません。終わってから変えてください")
	}
	_, history, err := cs.store.Conversation(ctx, id)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(history, func(m StoredMessage) bool { return m.ID == msg })
	if i < 0 {
		return errors.New("発言が見つかりません(削除された可能性があります)")
	}
	from, to := i, i+1
	if history[i].Role != "user" {
		for from > 0 && history[from-1].Role != "user" {
			from--
		}
		for to < len(history) && history[to].Role != "user" {
			to++
		}
	}
	return cs.store.SetMessagesExcluded(ctx, ids(history[from:to]), excluded)
}

// ensureWorkspace は、エンジンのモデルと作業領域をこの会話のものにする(モデルは会話のモデル、作業領域は会話の上限 + 返事の上限)。
// モデルも作業領域も1つだけなので、違う会話に送信するときに読み直す・作り直す(会話を開いただけではしない)。
// 作り直すと KV キャッシュは空になる(同じシステムプロンプト・ツールの説明の部分も読み直しになる)。
func (cs *Conversations) ensureWorkspace(conv Conversation, emit func(ChatEvent)) error {
	if err := cs.ensureModel(conv, emit); err != nil {
		return err
	}
	lim := cs.limitsOf(conv.Settings)
	n := lim.history + lim.reply
	changed, err := cs.gen.EnsureContextSize(n)
	if err != nil && hasRemedy(err) {
		return fmt.Errorf("この会話の作業領域(会話の上限 %d + 返事の上限 %d = %d トークン)を確保できません: %w", lim.history, lim.reply, n, err)
	}
	if err != nil {
		return fmt.Errorf("この会話の作業領域(会話の上限 %d + 返事の上限 %d = %d トークン)を確保できません。"+
			"会話の設定で上限を下げるか、GPU に載せる層の数を減らしてください: %w", lim.history, lim.reply, n, err)
	}
	if changed {
		log.Infof("作業領域を %d トークン(会話の上限 %d + 返事の上限 %d)に作り直しました", n, lim.history, lim.reply)
		emit(ChatEvent{ConversationID: conv.ID, Type: "notice",
			Text: fmt.Sprintf("この会話の大きさに合わせて、作業領域を %d トークンに作り直しました", n)})
	}
	return nil
}

// ensureModel は、読み込んでいるモデルがこの会話のモデルと違えば、会話のモデルを読み込む(0014 の 3)。
// モデルの違う会話に切り替えた最初の送信は、読み直しの分(数秒〜十数秒)待つ。読み直したらお知らせ(トースト)とログで知らせる。
// 会話がモデルを持たない(この機能より前の会話)・読み込む係が無いときは、読み込んでいるモデルのまま。
func (cs *Conversations) ensureModel(conv Conversation, emit func(ChatEvent)) error {
	want := conv.Settings.Model
	if want == "" || cs.models == nil {
		return nil
	}
	if info := cs.gen.Info(); info != nil && info.ID == want {
		return nil
	}
	emit(ChatEvent{ConversationID: conv.ID, Type: "model_loading", Text: fmt.Sprintf("この会話のモデル %s を読み込み中…", want)})
	info, err := cs.models.LoadModel(want)
	if err != nil {
		if hasRemedy(err) {
			return fmt.Errorf("この会話のモデル(%s)を読み込めません: %w", want, err)
		}
		return fmt.Errorf("この会話のモデル(%s)を読み込めません。入力欄の下でモデルを選び直してください: %w", want, err)
	}
	log.Infof("会話 %s のモデル %s を読み込みました(%.1f 秒)", conv.ID, info.Name, info.LoadSec)
	emit(ChatEvent{ConversationID: conv.ID, Type: "model",
		Text: fmt.Sprintf("この会話のモデル %s を読み込みました(%.1f 秒)", info.Name, info.LoadSec)})
	return nil
}

// FillMissingModels は、モデルを持っていない会話(会話ごとのモデルにする前に作った会話)に model(最後に使ったモデル)を写して保存する。
// 起動したときに1回呼ぶ。写した会話の数を返す。model が空(まだモデルを使ったことが無い)なら何もしない。
// 写しておかないと、その会話は「読み込んでいるモデル」で生成し続け、ほかの会話でモデルを変えると一緒に変わってしまう。
//
// 埋めるのは ctx の利用者の会話(今は利用者の表が無いので、ログインなしの形の利用者 LocalUser だけ。0017)。
func (cs *Conversations) FillMissingModels(ctx context.Context, model string) (int, error) {
	if model == "" {
		return 0, nil
	}
	list, err := cs.store.ListConversations(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, c := range list {
		if c.Settings.Model != "" {
			continue
		}
		c.Settings.Model = model
		if err := cs.store.UpdateConversation(ctx, c); err != nil {
			return n, fmt.Errorf("会話 %s にモデルを入れられません: %w", c.ID, err)
		}
		n++
	}
	return n, nil
}

// FillMissingLimits は、会話の上限・返事の上限を持っていない会話(会話ごとの設定にする前に作った会話)に、
// 今の新しい会話の既定の値を写して保存する。起動したときに1回呼ぶ。写した会話の数を返す。
// 写しておかないと、既定を変えたときに、その会話の上限まで変わってしまう(既定は今ある会話に効かない、の約束)。
// 埋めるのは ctx の利用者の会話(FillMissingModels と同じ)。
func (cs *Conversations) FillMissingLimits(ctx context.Context) (int, error) {
	list, err := cs.store.ListConversations(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, c := range list {
		if c.Settings.HistoryTokens > 0 && c.Settings.ReplyTokens > 0 {
			continue
		}
		if c.Settings, err = cs.withLimits(ctx, c.Settings.normalized()); err != nil {
			return n, err
		}
		if err := cs.store.UpdateConversation(ctx, c); err != nil {
			return n, fmt.Errorf("会話 %s に会話の上限・返事の上限を入れられません: %w", c.ID, err)
		}
		n++
	}
	return n, nil
}

// List は会話の一覧を、更新の新しい順に返す。設定はプロファイルの決まりを当てたもの(固定した項目はプロファイルの値)。
func (cs *Conversations) List(ctx context.Context) ([]Conversation, error) {
	list, err := cs.store.ListConversations(ctx)
	if err != nil {
		return list, err
	}
	profiles := map[uuid.UUID]*Profile{} // 同じプロファイルを何度も読まない
	for i := range list {
		id := list[i].ProfileID
		p, ok := profiles[id]
		if !ok {
			if p, err = cs.profileFor(ctx, id); err != nil {
				return nil, err
			}
			profiles[id] = p
		}
		list[i].Settings = cs.forUse(withProfile(list[i].Settings.normalized(), p))
	}
	return list, nil
}

// Get は会話1つと、その発言を古い順に返す。
// 設定は、使うときの設定(プロファイルの決まりを当てたもの。固定した項目はプロファイルの値)。送信も画面もこれを使う。
func (cs *Conversations) Get(ctx context.Context, id uuid.UUID) (Conversation, []StoredMessage, error) {
	return cs.load(ctx, id)
}

// Create は新しい会話を作る。settings が nil なら「新しい会話の既定の設定」をコピーする。
// 画面は、新しい会話の画面で既定の写しを変えてから、最初の送信のときに settings を渡して作る。
// どちらにしてもコピーなので、会話の中で設定を変えても既定やほかの会話には効かない。
// 会話は、新しい会話で最初に選ばれるプロファイルから作ったものとして覚える(0014。プロファイルを選んで作るのはステップ 7 の 4)。
func (cs *Conversations) Create(ctx context.Context, settings *ConversationSettings) (Conversation, error) {
	return cs.CreateIn(ctx, uuid.UUID{}, settings)
}

// CreateIn は、プロファイル profileID から新しい会話を作る(空なら新しい会話で最初に選ばれるプロファイル)。
// 新しい会話の画面でプロファイルを選んだとき。しまったプロファイルからは作れない。ほかは Create と同じ。
// CopyShared は、ほかの人の会話(共有された・管理者として読んだ)を写して、自分の会話にする(0018 の決定事項 2・26)。
// 発言・要約・「モデルに渡さない」の印も写す(ID は新しく振り、要約がどの発言までかは写した発言に付け替える)。
// 名前は「<名前>(写し)」。設定は写す(作業フォルダは持ち主の PC のパスなので写さない)。
// プロファイルは、写す人にも見えるなら同じもの(決まりも同じ)、見えなければ写す人の最初に選ばれるもの。
func (cs *Conversations) CopyShared(ctx context.Context, id uuid.UUID) (Conversation, error) {
	src, err := cs.store.ReadConversation(ctx, id)
	if err != nil {
		return Conversation{}, err
	}
	profile := src.Conversation.ProfileID
	if p, err := cs.store.Profile(ctx, profile); err != nil || p.Withdrawn || p.Archived {
		def, err := cs.DefaultProfile(ctx)
		if err != nil {
			return Conversation{}, err
		}
		profile = def.ID
	}
	s := src.Conversation.Settings.normalized()
	s.WorkDir, s.profile = "", nil
	now := time.Now()
	c := Conversation{ID: uuid.NewV7(), Title: src.Conversation.Title + "(写し)", Settings: s, ProfileID: profile, CreatedAt: now, UpdatedAt: now}
	if err := cs.store.CreateConversation(ctx, c); err != nil {
		return Conversation{}, fmt.Errorf("会話を写せません: %w", err)
	}
	// 発言の ID は作った順に並ぶので、古い順に新しい ID を振る(並びが変わらない)
	ids := map[uuid.UUID]uuid.UUID{}
	var excluded []uuid.UUID
	for _, m := range src.Messages {
		old := m.ID
		m.ID, m.ConversationID = uuid.NewV7(), c.ID
		ids[old] = m.ID
		if err := cs.store.AddMessage(ctx, m); err != nil {
			return c, fmt.Errorf("会話の発言を写せません: %w", err)
		}
		if m.Excluded {
			excluded = append(excluded, m.ID)
		}
	}
	if len(excluded) > 0 {
		if err := cs.store.SetMessagesExcluded(ctx, excluded, true); err != nil {
			return c, err
		}
	}
	for _, k := range src.Compactions {
		k.ID, k.ConversationID, k.UptoMessageID = uuid.NewV7(), c.ID, ids[k.UptoMessageID]
		if err := cs.store.AddCompaction(ctx, k); err != nil {
			return c, fmt.Errorf("会話の要約を写せません: %w", err)
		}
	}
	return c, nil
}

func (cs *Conversations) CreateIn(ctx context.Context, profileID uuid.UUID, settings *ConversationSettings) (Conversation, error) {
	p, err := cs.usableProfile(ctx, profileID)
	if err != nil {
		return Conversation{}, err
	}
	s := cs.defaultsFor(p)
	if settings != nil {
		// 新しい会話の画面で変えた設定も、プロファイルの決まりで変えられないものは断る(既定の写しから変えたところを見る)
		if err := checkRules(&p, s, settings.normalized(), cs.groupOfTool); err != nil {
			return Conversation{}, err
		}
		s = settings.normalized()
	}
	s.profile = nil
	if cs.noWorkDir {
		s.WorkDir = ""
	}
	// 会話の上限・返事の上限も会話の値として持つ(画面が渡さなかったら既定の値)
	if s, err = cs.withLimits(ctx, s); err != nil {
		return Conversation{}, err
	}
	if s.Model == "" {
		// プロファイルにモデルが無い(初めての起動でまだモデルを選んでいなかった)なら、読み込んでいるモデルにする
		if info := cs.gen.Info(); info != nil {
			s.Model = info.ID
		}
	}
	if err := s.validate(); err != nil {
		return Conversation{}, err
	}
	now := time.Now()
	c := Conversation{
		ID:        uuid.NewV7(),
		Settings:  s,
		ProfileID: p.ID,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := cs.store.CreateConversation(ctx, c); err != nil {
		return Conversation{}, fmt.Errorf("会話を作れません: %w", err)
	}
	return c, nil
}

// Delete は会話を消す。生成中の会話は消せない(書き込み先が消えてしまうため)。
func (cs *Conversations) Delete(ctx context.Context, id uuid.UUID) error {
	if g := cs.generating.Load(); g != nil && *g == id {
		return errors.New("返事を生成中の会話は削除できません。停止してから削除してください")
	}
	return cs.store.DeleteConversation(ctx, id)
}

// Rename は会話のタイトルを変える。自動で付いたタイトルも、あとから変えられる。
// 最初の送信の前に付けた名前は、自動のタイトルで上書きしない(TouchConversation がタイトルが空のときだけ入れる)。
func (cs *Conversations) Rename(ctx context.Context, id uuid.UUID, title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return errors.New("タイトルを空にはできません")
	}
	c, _, err := cs.store.Conversation(ctx, id)
	if err != nil {
		return err
	}
	c.Title = title
	return cs.store.UpdateConversation(ctx, c) // 更新日時は変えない(名前を変えただけで一覧の並びが変わらないように)
}

// UpdateSettings は会話の設定を変える。次の送信から効く。
// プロファイルの決まりで変えられないもの(固定した項目・使えないツール)を変えていたら、保存せずに理由のエラーを返す(checkRules)。
// 固定した項目は、会話に保存してある値のまま残す(使うときだけプロファイルの値。keepStored)。
func (cs *Conversations) UpdateSettings(ctx context.Context, id uuid.UUID, s ConversationSettings) error {
	c, _, err := cs.store.Conversation(ctx, id)
	if err != nil {
		return err
	}
	if err := s.validate(); err != nil {
		return err
	}
	p, err := cs.profileFor(ctx, c.ProfileID)
	if err != nil {
		return err
	}
	stored := c.Settings.normalized()
	s = s.normalized()
	if err := checkRules(p, withProfile(stored, p), s, cs.groupOfTool); err != nil {
		return err
	}
	s = keepStored(s, stored, p)
	s.profile = nil
	if cs.noWorkDir {
		s.WorkDir = stored.WorkDir // 決めも消しもしない(画面には空で見せているため)
	}
	c.Settings = s
	return cs.store.UpdateConversation(ctx, c)
}

// Defaults は新しい会話の既定の設定(ctx の利用者の、新しい会話で最初に選ばれるプロファイルの値)を返す。
//
// 使う MCP のサーバーは、プロファイルに書いたもののうち、この PC の settings.json にあるものだけ(無いものは無視する。0014 の 1)。
// プロファイルのグループは、設定の画面の「ツール」のグループのチェックで変える(SetDefaultGroup)。
// チェックを外したものも、会話の「この会話のツール」からはいつでも選べる。
// プロファイルで使えないグループは、選ばない(0014 の 5)。
func (cs *Conversations) Defaults(ctx context.Context) (ConversationSettings, error) {
	p, err := cs.DefaultProfile(ctx)
	if err != nil {
		return ConversationSettings{}, err
	}
	return cs.defaultsFor(p), nil
}

// defaultsFor は、プロファイル p から新しい会話を作るときの設定(Defaults と同じ決め方)。
func (cs *Conversations) defaultsFor(p Profile) ConversationSettings {
	d := p.Values.settings()
	d.MCPServers = slices.DeleteFunc(cs.existingServers(d.MCPServers), p.Rules.offGroup)
	d.BuiltinOff = d.BuiltinOff || p.Rules.Off.Builtin
	return d
}

// existingServers は、names のうち settings.json にある MCP のサーバー(settings.json の順)。
func (cs *Conversations) existingServers(names []string) []string {
	out := []string{}
	if cs.remote == nil {
		return out
	}
	for _, name := range cs.remote.Servers() {
		if slices.Contains(names, name) {
			out = append(out, name)
		}
	}
	return out
}

// SetDefaults は新しい会話の既定の設定(新しい会話で最初に選ばれるプロファイルの値)を変えて保存する。今ある会話には効かない。
// 作業フォルダは既定に入れない(asDefaults)。
// 使うツールのグループ(組み込み・MCP のサーバー)とモデルは変えない(グループは設定の画面の「ツール」で決める。SetDefaultGroup)。
func (cs *Conversations) SetDefaults(ctx context.Context, s ConversationSettings) error {
	return cs.updateProfile(ctx, func(v *ProfileValues) {
		next := valuesOf(s.asDefaults())
		next.Model = cmp.Or(next.Model, v.Model) // モデルを持たない設定(この機能より前の会話)なら、今のモデルのまま
		next.BuiltinOff, next.MCPServers, next.ToolsOff = v.BuiltinOff, v.MCPServers, v.ToolsOff
		*v = next
	})
}

// Generating は生成中の会話の ID を返す。生成していなければ nil。
func (cs *Conversations) Generating() *uuid.UUID {
	return cs.generating.Load()
}

// Send はユーザーの発言を保存し、返事を生成して保存する。
// 途中経過は emit に ChatEvent で渡す。最後に必ず done を1回渡す。
// ctx がキャンセルされると(画面の停止ボタン)、そこまでの返事を保存して終わる。
func (cs *Conversations) Send(ctx context.Context, id uuid.UUID, text string, emit func(ChatEvent)) {
	cs.turn(ctx, id, text, keepAll, emit)
}

// Regenerate は、最後の返事を作り直す(0011)。最後のユーザーの発言の後ろ(返事とツールの結果)を消し、
// 今のモデル・設定でもう一度返事を作る。ツールをオンにした・9B に切り替えた、など条件を変えてやり直すときのため。
// 途中経過と終わりは Send と同じく emit で渡す。前の返事でツールが行ったこと(ファイルの書き換えなど)は元に戻らない。
func (cs *Conversations) Regenerate(ctx context.Context, id uuid.UUID, emit func(ChatEvent)) {
	cs.turn(ctx, id, "", dropReply, emit)
}

// Resend は、最後のユーザーの発言を text に直して送り直す(0011)。最後の発言とその返事を消してから送る。
func (cs *Conversations) Resend(ctx context.Context, id uuid.UUID, text string, emit func(ChatEvent)) {
	cs.turn(ctx, id, text, dropLastTurn, emit)
}

// やり直しの種類(turn の drop)。保存された発言のうち、消して送り直す部分を決める
type dropFunc func(history []StoredMessage) (keep []StoredMessage, drop []StoredMessage, err error)

// keepAll はふつうの送信: 何も消さない。
func keepAll(history []StoredMessage) ([]StoredMessage, []StoredMessage, error) {
	return history, nil, nil
}

// dropReply は作り直す: 最後のユーザーの発言は残し、その後ろ(返事とツールの結果)を消す。
func dropReply(history []StoredMessage) ([]StoredMessage, []StoredMessage, error) {
	last := lastUserIndex(history)
	if last < 0 {
		return nil, nil, errors.New("作り直す返事がありません")
	}
	if history[last].Excluded {
		return nil, nil, errors.New("最後の発言を「モデルに渡さない」にしているので、作り直せません。戻してから作り直してください")
	}
	// 残す側は Clip する(あとで append したときに、消す側と同じ配列を書き換えないように)
	return slices.Clip(history[:last+1]), history[last+1:], nil
}

// dropLastTurn は送り直す: 最後のユーザーの発言とその返事を消す。
func dropLastTurn(history []StoredMessage) ([]StoredMessage, []StoredMessage, error) {
	last := lastUserIndex(history)
	if last < 0 {
		return nil, nil, errors.New("送り直す発言がありません")
	}
	return slices.Clip(history[:last]), history[last:], nil
}

// lastUserIndex は最後のユーザーの発言の位置。無ければ -1(compact.go の lastUser は無ければ 0)。
func lastUserIndex(history []StoredMessage) int {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == "user" {
			return i
		}
	}
	return -1
}

// turn は Send / Regenerate / Resend の共通部分。drop で保存された発言から消す部分を決め、text が空でなければ新しい発言として送る。
// 消すのは、会話の上限に収まるかを確かめてから(送れないときに、前のやり取りだけが消えないように)。
func (cs *Conversations) turn(ctx context.Context, id uuid.UUID, text string, drop dropFunc, emit func(ChatEvent)) {
	var reply *StoredMessage
	defer func() { emit(ChatEvent{ConversationID: id, Type: "done", Reply: reply}) }()
	fail := func(err error) {
		emit(ChatEvent{ConversationID: id, Type: "error", Error: err.Error(), Caution: isCaution(err)})
	}

	if !cs.generating.CompareAndSwap(nil, &id) {
		fail(ErrGenerating)
		return
	}
	defer cs.generating.Store(nil)
	// ほかの画面(ブラウザ・ほかの PC のアプリ)が同じ会話を生成中なら断る(lock.go。保存をサーバーに置いたとき)
	unlock, err := cs.lockConversation(ctx, id)
	if err != nil {
		fail(busyError(err))
		return
	}
	defer unlock()

	conv, saved, err := cs.Get(ctx, id)
	if err != nil {
		fail(err)
		return
	}
	saved, dropped, err := drop(saved)
	if err != nil {
		fail(err)
		return
	}
	history := forModel(saved)

	// 0. 作業領域をこの会話の大きさにする(会話の上限 + 返事の上限。要約も返事も、この中で生成する)。
	// この会話で使う MCP のサーバーが動いていなければ、起動する(そのツールも会話の上限に数えるので、確かめる前に)
	if err := cs.ensureWorkspace(conv, emit); err != nil {
		fail(err)
		return
	}
	// 書式に未対応の種類のモデル(gemma4 など)では生成できないので、発言を保存する前に止める(返事の無い質問を残さないため)。
	// 数える(checkFits)ときにも同じエラーになるが、MCP のサーバーを起動する前に、理由がはっきり分かる形で止める
	if info := cs.gen.Info(); info != nil && info.Format == "" {
		fail(format.Unsupported(info.Arch))
		return
	}
	cs.startRemote(ctx, conv, emit)

	// 1. 会話の上限に収められるかを確かめてから、(やり直しなら前の部分を消して)ユーザーの発言を保存する。
	// タイトルがまだ無ければ(最初の発言なら)、その先頭をタイトルにする。作り直す(text が空)なら、最後の発言をそのまま使う
	var user *StoredMessage
	if text != "" {
		user = &StoredMessage{ID: uuid.NewV7(), ConversationID: id, Role: "user", Content: text, CreatedAt: time.Now()}
		history = append(history, *user)
	}
	if err := cs.checkFits(conv.Settings, history); err != nil {
		fail(err)
		return
	}
	if len(dropped) > 0 {
		if err := cs.store.DeleteMessages(ctx, ids(dropped)); err != nil {
			fail(fmt.Errorf("前のやり取りを消せません: %w", err))
			return
		}
		log.Infof("会話 %s のやり直し: 発言 %d 件を消しました", id, len(dropped))
	}
	if user != nil {
		if err := cs.save(ctx, *user, titleFrom(text)); err != nil {
			fail(err)
			return
		}
		emit(ChatEvent{ConversationID: id, Type: "user", Message: user})
	}

	// 2. 返事を生成して保存する。モデルに渡す内容は毎回、会話の上限に収めてから決める(compact.go)。
	// ツールを呼んだら、実行して結果を渡し、ツールを呼ばなくなるまで生成を繰り返す(agent.go)
	reply, err = cs.runTurn(ctx, conv, history, emit)
	if err != nil {
		fail(err)
	}
}

func (cs *Conversations) emitUsage(emit func(ChatEvent), id uuid.UUID, u Usage) {
	emit(ChatEvent{ConversationID: id, Type: "usage", Usage: &u})
}

// save は発言を1件保存し、会話の更新日時(一覧の並び順に使う)を進める。
// タイトルが空なら autoTitle を入れる。
func (cs *Conversations) save(ctx context.Context, m StoredMessage, autoTitle string) error {
	// 停止ボタンで ctx が止まっていても、そこまでの返事は保存する(止めたのは生成で、保存ではない)。
	// 利用者などの値は残したまま、止める合図だけを外す
	ctx = context.WithoutCancel(ctx)
	if err := cs.store.AddMessage(ctx, m); err != nil {
		return fmt.Errorf("発言を保存できません: %w", err)
	}
	if err := cs.store.TouchConversation(ctx, m.ConversationID, m.CreatedAt, autoTitle); err != nil {
		return fmt.Errorf("会話を更新できません: %w", err)
	}
	return nil
}

// generate は返事を生成して、保存する形(StoredMessage)にして返す。
// 何も生成されなかったときは nil を返す。
func (cs *Conversations) generate(ctx context.Context, conv Conversation, messages []format.Message, emit func(ChatEvent)) (*StoredMessage, error) {
	set := conv.Settings
	opt := engine.GenerateOptions{
		Temperature:     float32(set.Temperature),
		MaxTokens:       cs.limitsOf(set).reply,
		PresencePenalty: float32(set.PresencePenalty),
	}
	model := ""
	if info := cs.gen.Info(); info != nil {
		model = info.Name // どのモデルの返事か、発言ごとに記録する
	}

	// 思考と答えを受け取りながら、時間を測る(画面の統計行と「思考の過程(N秒)」に使う)
	start := time.Now()
	var firstAt, thinkStart, thinkEnd time.Time
	onText := func(kind string) func(string) {
		return func(t string) {
			now := time.Now()
			if firstAt.IsZero() {
				firstAt = now
			}
			if kind == "thinking" {
				if thinkStart.IsZero() {
					thinkStart = now
				}
			} else if thinkEnd.IsZero() {
				thinkEnd = now
			}
			emit(ChatEvent{ConversationID: conv.ID, Type: kind, Text: t})
		}
	}

	out, st, err := cs.gen.Generate(ctx, cs.promptFor(set, messages), opt,
		func(p string) {
			ev := ChatEvent{ConversationID: conv.ID, Type: "prompt", Text: p}
			if i := cs.gen.Info(); i != nil {
				ev.Format = i.Format // どの書式で組み立てた文字列か(0026 の決定事項 17)
			}
			emit(ev)
		},
		onText("thinking"), onText("content"),
	)
	if err != nil {
		return nil, err
	}
	if out.Thinking == "" && out.Content == "" && len(out.ToolCalls) == 0 {
		return nil, nil
	}

	now := time.Now()
	stats := &ReplyStats{GenerateStats: st, FirstSec: firstAt.Sub(start).Seconds()}
	if !thinkStart.IsZero() {
		if thinkEnd.IsZero() {
			thinkEnd = now
		}
		stats.ThinkSec = thinkEnd.Sub(thinkStart).Seconds()
	}
	m := &StoredMessage{
		ID: uuid.NewV7(), ConversationID: conv.ID, Role: "assistant",
		Content: out.Content, Thinking: out.Thinking, ToolCalls: out.ToolCalls,
		Model: model, Stats: stats, CreatedAt: now,
	}
	if m.Content == "" && len(m.ToolCalls) == 0 {
		// 考えている途中で終わり、答えが出なかった場合(ツールを呼んで答えが空なのは正常。ループの続きで答える)。
		// 何も残さないと、モデルからは「質問に返事をしていない」ように見えるので、
		// 何が起きたかを返事として残す(次の送信でもモデルに渡る)。
		m.Content = noAnswerNote(st.StopReason)
		stats.NoAnswer = true
	}
	return m, nil
}

// systemPrompt は、会話の設定のシステムプロンプトに、環境の説明(environment)を足したもの。
func (cs *Conversations) systemPrompt(set ConversationSettings) string {
	env := cs.environment(set)
	if env == "" {
		return set.System
	}
	if s := strings.TrimSpace(set.System); s != "" {
		return s + "\n\n" + env
	}
	return env
}

// environment は、作業フォルダを決めた会話で、モデルに知らせる環境の説明。
//
// モデルは自分がどこで動いているかを知らないので、作業フォルダ・OS を書いておく(パスの書き方を間違えにくくする)。
// ツールの指示文に合わせて英語。書式に関係ない情報なので、書式ではなく会話の仕組みの側で組み立てる。
// 会話の中で変わらないので、システムプロンプトの後ろ(要約より前)に置き、KV キャッシュの再利用を崩さない。
//
// 使うツールに合わせて書く:時刻・計算のほかにツールが無ければ書かない(作業フォルダを使うツールが無い)。
// シェルと cd の注意は、run_command を使うときだけ書く(使わないツールのことを書かない)。
func (cs *Conversations) environment(set ConversationSettings) string {
	dir := cs.workDir(set)
	if dir == "" {
		return ""
	}
	tools := cs.toolsFor(set)
	usesFolder := slices.ContainsFunc(tools, func(t tool.Tool) bool {
		n := t.Def().Name
		return n != "get_current_time" && n != "calculate"
	})
	if !usesFolder {
		return ""
	}
	command := tool.Find(tools, "run_command") != nil
	var b strings.Builder
	b.WriteString("# Environment\n")
	// 資料のフォルダ(aibow-server)は、サーバーの PC のパスを見せず、名前だけを書く(パスはツールの中だけで使う。0024 の決定事項 32)
	name := filepath.Base(dir)
	if folder := cs.FolderName(set); folder != "" {
		name = folder
		b.WriteString("Working directory: \"" + folder + "\" (a shared folder on the server)\n")
	} else {
		b.WriteString("Working directory: " + dir + "\n")
	}
	b.WriteString("OS: " + runtime.GOOS + "\n")
	if command {
		b.WriteString("Shell for run_command: " + tool.ShellNote + "\n")
	}
	b.WriteString("Use paths relative to the working directory.\n")
	// ユーザーが「ytscroll をコミットして」のようにフォルダの名前で呼ぶと、4B は ytscroll を作業フォルダの下のフォルダだと思い、
	// 「cd ytscroll && git status」と書いて失敗した(3 回に 1 回)。すでにその中にいることと、その名前は作業フォルダそのものだと書く
	if command {
		b.WriteString("Commands already run inside the working directory, so do not cd into it. ")
	}
	b.WriteString("\"" + name + "\" refers to the working directory itself.")
	return b.String()
}

// promptMessages は保存した発言を、モデルに渡す形にする。
// 返事の思考も渡す。プロンプトに入れるかどうかは書式が決める(Qwen3.5 は、ツールのループの途中の返事だけ入れる)。
// システムプロンプトは発言としては保存せず、会話の設定から毎回先頭に付ける。
//
// summary はこれまでの会話の要約(compact.go)。システムプロンプトの後ろに見出しを付けて足す。
// システムプロンプトが空でも、要約があれば system を作る。
func promptMessages(system, summary string, history []StoredMessage) []format.Message {
	var out []format.Message
	s := strings.TrimSpace(system)
	if summary != "" {
		if s != "" {
			s += "\n\n"
		}
		s += summaryHeading + "\n" + summary
	}
	if s != "" {
		out = append(out, format.Message{Role: "system", Content: s})
	}
	for _, m := range history {
		fm := format.Message{Role: m.Role, Content: m.Content, Thinking: m.Thinking, ToolCalls: m.ToolCalls}
		if m.ToolResult != nil {
			// ツールの結果には、元の呼び出しの名前を付ける(Gemma 4 は結果に名前を書く。0026 の決定事項 5)
			fm.Name = m.ToolResult.Call.Name
		}
		out = append(out, fm)
	}
	return out
}

// promptFor は、会話の設定でモデルに渡す内容(書式を当てる前の構造)を作る。
// 「ツールを使う」が ON ならツールの定義も入れる。
func (cs *Conversations) promptFor(set ConversationSettings, messages []format.Message) format.Prompt {
	return format.Prompt{Messages: messages, Tools: tool.Defs(cs.toolsFor(set)), Think: set.Think}
}

// noAnswerNote は、答えが出ないまま終わったときに答えの代わりに残す文。
func noAnswerNote(stopReason string) string {
	switch stopReason {
	case "length":
		return "(考えている途中で返事の上限に達したため、答えを出せませんでした)"
	case "context":
		return "(考えている途中でコンテキストがいっぱいになったため、答えを出せませんでした)"
	case "cancel":
		return "(考えている途中で停止されたため、答えを出していません)"
	}
	return "(考えている途中で終わったため、答えを出せませんでした)"
}

// titleMaxRunes は自動で付けるタイトルの長さ(文字数)。
const titleMaxRunes = 30

// titleFrom は最初のユーザー発言から会話のタイトルを作る。
// モデルに考えさせると生成が1回増えるので、先頭を切り出すだけにしている。
func titleFrom(text string) string {
	// 改行やタブは1つの空白にまとめる(一覧で1行に収めるため)
	t := strings.Join(strings.FieldsFunc(text, unicode.IsSpace), " ")
	r := []rune(t)
	if len(r) > titleMaxRunes {
		return string(r[:titleMaxRunes]) + "…"
	}
	return t
}
