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
//	error    : エラー
//	notice   : エラーではないお知らせ(Text。画面はトーストに出す)。例: 今すぐ要約で、要約するものが無かった
//	done     : 終わり。必ず最後に1回だけ送る。保存した最後の返事があれば Reply に入る
type ChatEvent struct {
	ConversationID uuid.UUID        `json:"conversation_id"` // どの会話の送信か。画面は表示中の会話のものだけを出す
	Type           string           `json:"type"`
	Text           string           `json:"text,omitempty"`
	Error          string           `json:"error,omitempty"`
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

	// generating は生成中の会話の ID。生成していなければ nil。
	// エンジン(作業領域)が1つしかないので、生成は同時に1つだけ。
	generating atomic.Pointer[uuid.UUID]

	mu       sync.Mutex
	defaults ConversationSettings // 新しい会話の既定の設定

	// stopSummary は生成中の要約だけを止める関数(StopSummary)。要約していなければ nil
	summaryMu   sync.Mutex
	stopSummary context.CancelFunc

	// pending は答えを待っている確認。待っていなければ nil。生成は同時に1つなので、確認も同時に1つだけ
	confirmMu sync.Mutex
	pending   *pendingConfirm
}

// pendingConfirm は答えを待っている確認1つ。
type pendingConfirm struct {
	conv   uuid.UUID
	id     string
	tool   string // 確認しているツールの名前(これから先の扱いを会話の上書きに書くとき使う)
	answer chan Confirmation
}

// limits は会話の上限と返事の上限。WorkspaceSettings のうち、会話の仕組みが使う2つ。
type limits struct {
	history int // 会話の上限: モデルに渡す文字列のトークン数の上限
	reply   int // 返事の上限: 1回の返事で生成するトークン数の上限
}

// NewConversations は会話の仕組みを作る。app からは新しい会話の既定の設定を使う。
func NewConversations(store Store, gen Generator, app AppSettings) *Conversations {
	return &Conversations{store: store, gen: gen, tools: tool.Builtin(), defaults: app.Defaults.asDefaults()}
}

// limitsOf は会話の上限と返事の上限。会話の設定の値を使う。
// 持っていない(0 の)ものは、新しい会話の既定の値を使う(この機能より前の会話は起動時に埋めるので、ふつうは来ない。FillMissingLimits)。
func (cs *Conversations) limitsOf(set ConversationSettings) limits {
	cs.mu.Lock()
	d := cs.defaults
	cs.mu.Unlock()
	return limits{history: cmp.Or(set.HistoryTokens, d.HistoryTokens), reply: cmp.Or(set.ReplyTokens, d.ReplyTokens)}
}

// withLimits は、会話の上限・返事の上限を持っていない設定に、新しい会話の既定の値を入れる。
func (cs *Conversations) withLimits(set ConversationSettings) ConversationSettings {
	lim := cs.limitsOf(set)
	set.HistoryTokens, set.ReplyTokens = lim.history, lim.reply
	return set
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
func (cs *Conversations) SetExcluded(id, msg uuid.UUID, excluded bool) error {
	if g := cs.generating.Load(); g != nil && *g == id {
		return errors.New("返事を生成中の会話では、モデルに渡すかを変えられません。終わってから変えてください")
	}
	_, history, err := cs.store.Conversation(id)
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
	return cs.store.SetMessagesExcluded(ids(history[from:to]), excluded)
}

// ensureWorkspace は、エンジンの作業領域をこの会話の大きさ(会話の上限 + 返事の上限)にする。
// 作業領域は1つだけなので、大きさの違う会話に送信するときに作り直す(会話を開いただけでは作り直さない)。
// 作り直すと KV キャッシュは空になる(同じシステムプロンプト・ツールの説明の部分も読み直しになる)。
func (cs *Conversations) ensureWorkspace(conv Conversation, emit func(ChatEvent)) error {
	lim := cs.limitsOf(conv.Settings)
	n := lim.history + lim.reply
	changed, err := cs.gen.EnsureContextSize(n)
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

// FillMissingLimits は、会話の上限・返事の上限を持っていない会話(会話ごとの設定にする前に作った会話)に、
// 今の新しい会話の既定の値を写して保存する。起動したときに1回呼ぶ。写した会話の数を返す。
// 写しておかないと、既定を変えたときに、その会話の上限まで変わってしまう(既定は今ある会話に効かない、の約束)。
func (cs *Conversations) FillMissingLimits() (int, error) {
	list, err := cs.store.ListConversations()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, c := range list {
		if c.Settings.HistoryTokens > 0 && c.Settings.ReplyTokens > 0 {
			continue
		}
		c.Settings = cs.withLimits(c.Settings.normalized())
		if err := cs.store.UpdateConversation(c); err != nil {
			return n, fmt.Errorf("会話 %s に会話の上限・返事の上限を入れられません: %w", c.ID, err)
		}
		n++
	}
	return n, nil
}

// List は会話の一覧を、更新の新しい順に返す。
func (cs *Conversations) List() ([]Conversation, error) {
	list, err := cs.store.ListConversations()
	for i := range list {
		list[i].Settings = list[i].Settings.normalized()
	}
	return list, err
}

// Get は会話1つと、その発言を古い順に返す。
func (cs *Conversations) Get(id uuid.UUID) (Conversation, []StoredMessage, error) {
	c, ms, err := cs.store.Conversation(id)
	c.Settings = c.Settings.normalized()
	return c, ms, err
}

// Create は新しい会話を作る。settings が nil なら「新しい会話の既定の設定」をコピーする。
// 画面は、新しい会話の画面で既定の写しを変えてから、最初の送信のときに settings を渡して作る。
// どちらにしてもコピーなので、会話の中で設定を変えても既定やほかの会話には効かない。
func (cs *Conversations) Create(settings *ConversationSettings) (Conversation, error) {
	s := cs.Defaults()
	if settings != nil {
		s = settings.normalized()
	}
	s = cs.withLimits(s) // 会話の上限・返事の上限も会話の値として持つ(画面が渡さなかったら既定の値)
	if err := s.validate(); err != nil {
		return Conversation{}, err
	}
	now := time.Now()
	c := Conversation{
		ID:        uuid.NewV7(),
		Settings:  s,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := cs.store.CreateConversation(c); err != nil {
		return Conversation{}, fmt.Errorf("会話を作れません: %w", err)
	}
	return c, nil
}

// Delete は会話を消す。生成中の会話は消せない(書き込み先が消えてしまうため)。
func (cs *Conversations) Delete(id uuid.UUID) error {
	if g := cs.generating.Load(); g != nil && *g == id {
		return errors.New("返事を生成中の会話は削除できません。停止してから削除してください")
	}
	return cs.store.DeleteConversation(id)
}

// Rename は会話のタイトルを変える。自動で付いたタイトルも、あとから変えられる。
// 最初の送信の前に付けた名前は、自動のタイトルで上書きしない(TouchConversation がタイトルが空のときだけ入れる)。
func (cs *Conversations) Rename(id uuid.UUID, title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return errors.New("タイトルを空にはできません")
	}
	c, _, err := cs.store.Conversation(id)
	if err != nil {
		return err
	}
	c.Title = title
	return cs.store.UpdateConversation(c) // 更新日時は変えない(名前を変えただけで一覧の並びが変わらないように)
}

// UpdateSettings は会話の設定を変える。次の送信から効く。
func (cs *Conversations) UpdateSettings(id uuid.UUID, s ConversationSettings) error {
	c, _, err := cs.store.Conversation(id)
	if err != nil {
		return err
	}
	if err := s.validate(); err != nil {
		return err
	}
	c.Settings = s.normalized()
	return cs.store.UpdateConversation(c)
}

// Defaults は新しい会話の既定の設定を返す。
//
// 使う MCP のサーバーと組み込みのツールを使うかは、保存した既定ではなく、設定の画面の「ツール」のグループのチェックで決める
// (「ツール」のチェックが「新しい会話で最初から使う」の意味。決める場所を1つにするため、保存した既定には入れない)。
// チェックを外したものも、会話の「この会話のツール」からはいつでも選べる。
func (cs *Conversations) Defaults() ConversationSettings {
	cs.mu.Lock()
	d := cs.defaults
	cs.mu.Unlock()
	d.MCPServers = cs.defaultServers()
	d.BuiltinOff = !cs.prefs.groupDefault("")
	return d
}

// defaultServers は、新しい会話で最初から使う MCP のサーバー(settings.json のうち、「ツール」でチェックを外していないもの)。
func (cs *Conversations) defaultServers() []string {
	out := []string{}
	if cs.remote == nil {
		return out
	}
	for _, name := range cs.remote.Servers() {
		if cs.prefs.groupDefault(name) {
			out = append(out, name)
		}
	}
	return out
}

// SetDefaults は新しい会話の既定の設定を変えて保存する。今ある会話には効かない。
// 作業フォルダは既定に入れない(asDefaults)。
func (cs *Conversations) SetDefaults(s ConversationSettings) error {
	s = s.asDefaults()
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if err := SaveAppSetting(cs.store, SettingDefaults, s); err != nil {
		return err
	}
	cs.defaults = s
	return nil
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
	fail := func(err error) { emit(ChatEvent{ConversationID: id, Type: "error", Error: err.Error()}) }

	if !cs.generating.CompareAndSwap(nil, &id) {
		fail(ErrGenerating)
		return
	}
	defer cs.generating.Store(nil)

	conv, saved, err := cs.Get(id)
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
		if err := cs.store.DeleteMessages(ids(dropped)); err != nil {
			fail(fmt.Errorf("前のやり取りを消せません: %w", err))
			return
		}
		log.Infof("会話 %s のやり直し: 発言 %d 件を消しました", id, len(dropped))
	}
	if user != nil {
		if err := cs.save(*user, titleFrom(text)); err != nil {
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
func (cs *Conversations) save(m StoredMessage, autoTitle string) error {
	if err := cs.store.AddMessage(m); err != nil {
		return fmt.Errorf("発言を保存できません: %w", err)
	}
	if err := cs.store.TouchConversation(m.ConversationID, m.CreatedAt, autoTitle); err != nil {
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
		func(p string) { emit(ChatEvent{ConversationID: conv.ID, Type: "prompt", Text: p}) },
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
	if set.WorkDir == "" {
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
	b.WriteString("Working directory: " + set.WorkDir + "\n")
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
	b.WriteString("\"" + filepath.Base(set.WorkDir) + "\" refers to the working directory itself.")
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
		out = append(out, format.Message{Role: m.Role, Content: m.Content, Thinking: m.Thinking, ToolCalls: m.ToolCalls})
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
