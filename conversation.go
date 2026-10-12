package aibow

// conversation.go: 会話の仕組み。履歴の管理と、生成の呼び出し。
//
// モデルは何も覚えていない(ステートレス)。毎回「これまでの会話を全部」1本の文字列にして読ませている。
// 「記憶」に見えるものは、アプリがここで発言を保存し、次の送信のたびに並べ直しているだけ。
// Claude などのチャットアプリも同じ構造になっている。
//
// 画面(_cmd/aibow)はこの仕組みを呼ぶだけで、履歴を持たない。
// Wails に依存しないので、ステップ6の API からも同じものを使える。

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"uuid"
)

// Generator は返事を生成するもの。本物は Engine。テストでは偽物に差し替える。
type Generator interface {
	Generate(ctx context.Context, messages []Message, opt GenerateOptions,
		onPrompt func(prompt string), onThinking, onContent func(text string)) (GenerateStats, error)
	// CountTokens は文字列のトークン数を数える。会話の上限に収まっているかの判断に使う(compact.go)
	CountTokens(text string) (int, error)
	Info() *ModelInfo
}

// ChatEvent は送信の途中経過。Send がコールバックで1つずつ渡す。
//
//	prompt   : モデルに渡した文字列(学習用に画面に表示する)
//	thinking : 思考の断片
//	content  : 答えの断片
//	summarizing : 要約を作り始めた(compact の summary。返事の前か「今すぐ要約」)
//	summary  : 要約の断片
//	compact  : 会話の上限に収めるために compact した(方式・前後のトークン数・保存した要約など)。
//	           要約に失敗したときも Compact.Error に理由を入れて送る
//	usage    : メーターの値(モデルに渡す文字列のトークン数 / 会話の上限)。生成の前と、返事を保存したあとに送る
//	error    : エラー
//	done     : 終わり。必ず最後に1回だけ送る。保存した返事があれば Reply に入る
type ChatEvent struct {
	ConversationID uuid.UUID      `json:"conversation_id"` // どの会話の送信か。画面は表示中の会話のものだけを出す
	Type           string         `json:"type"`
	Text           string         `json:"text,omitempty"`
	Error          string         `json:"error,omitempty"`
	Reply          *StoredMessage `json:"reply,omitempty"`
	Usage          *Usage         `json:"usage,omitempty"`
	Compact        *CompactInfo   `json:"compact,omitempty"`
}

// ErrGenerating は、別の送信の生成中に送信されたときのエラー。
var ErrGenerating = errors.New("別の会話で返事を生成中です。終わるまで待つか、その会話で停止してください")

// Conversations は会話の一覧と送信を扱う。
type Conversations struct {
	store Store
	gen   Generator

	// generating は生成中の会話の ID。生成していなければ nil。
	// エンジン(作業領域)が1つしかないので、生成は同時に1つだけ。
	generating atomic.Pointer[uuid.UUID]

	mu       sync.Mutex
	defaults ConversationSettings // 新しい会話の既定の設定
	limits   limits               // 会話の上限と返事の上限(アプリ全体の設定)

	// stopSummary は生成中の要約だけを止める関数(StopSummary)。要約していなければ nil
	summaryMu   sync.Mutex
	stopSummary context.CancelFunc
}

// limits は会話の上限と返事の上限。WorkspaceSettings のうち、会話の仕組みが使う2つ。
type limits struct {
	history int // 会話の上限: モデルに渡す文字列のトークン数の上限
	reply   int // 返事の上限: 1回の返事で生成するトークン数の上限
}

// NewConversations は会話の仕組みを作る。app からは新しい会話の既定の設定と、会話の上限・返事の上限を使う。
func NewConversations(store Store, gen Generator, app AppSettings) *Conversations {
	return &Conversations{
		store: store, gen: gen, defaults: app.Defaults.normalized(),
		limits: limits{history: app.Workspace.HistoryTokens, reply: app.Workspace.ReplyTokens},
	}
}

// SetLimits は会話の上限と返事の上限を変える。次の送信から効く。
// エンジンの作業領域の作り直し(合計の大きさ)は、呼ぶ側(_cmd/aibow)が先に行う。
func (cs *Conversations) SetLimits(history, reply int) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.limits = limits{history: history, reply: reply}
}

func (cs *Conversations) currentLimits() limits {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.limits
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
	c.Settings = s.normalized()
	return cs.store.UpdateConversation(c)
}

// Defaults は新しい会話の既定の設定を返す。
func (cs *Conversations) Defaults() ConversationSettings {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.defaults
}

// SetDefaults は新しい会話の既定の設定を変えて保存する。今ある会話には効かない。
func (cs *Conversations) SetDefaults(s ConversationSettings) error {
	s = s.normalized()
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
	var reply *StoredMessage
	defer func() { emit(ChatEvent{ConversationID: id, Type: "done", Reply: reply}) }()
	fail := func(err error) { emit(ChatEvent{ConversationID: id, Type: "error", Error: err.Error()}) }

	if !cs.generating.CompareAndSwap(nil, &id) {
		fail(ErrGenerating)
		return
	}
	defer cs.generating.Store(nil)

	conv, history, err := cs.Get(id)
	if err != nil {
		fail(err)
		return
	}

	// 1. 会話の上限に収められるかを確かめてから、ユーザーの発言を保存する。
	// タイトルがまだ無ければ(最初の発言なら)、その先頭をタイトルにする
	user := StoredMessage{ID: uuid.NewV7(), ConversationID: id, Role: "user", Content: text, CreatedAt: time.Now()}
	history = append(history, user)
	if err := cs.checkFits(conv.Settings, history); err != nil {
		fail(err)
		return
	}
	if err := cs.save(user, titleFrom(text)); err != nil {
		fail(err)
		return
	}

	// 2. モデルに渡す内容を決める。会話の上限を超えていたら compact で収める(compact.go)。
	// モデルに渡すのは、システムプロンプトと各発言の「答え」だけ
	p, err := cs.prepare(ctx, conv, history, emit)
	if err != nil {
		if ctx.Err() == nil { // 止められた(アプリの終了など)ときは、エラーを出さずに終わる
			fail(err)
		}
		return
	}
	cs.emitUsage(emit, id, p.usage(history, cs.currentLimits().history))

	// 3. 生成する
	r, err := cs.generate(ctx, conv, p.messages, emit)
	if err != nil {
		fail(err)
		return
	}
	if r == nil {
		return // 何も生成されなかった(すぐに停止した、など)
	}

	// 4. 返事を保存する
	if err := cs.save(*r, ""); err != nil {
		fail(err)
		return
	}
	reply = r

	// 5. 返事が増えたあとのメーター。次の送信では、この返事もモデルに渡る
	if u, err := cs.usage(conv, append(history, *r)); err == nil {
		cs.emitUsage(emit, id, u)
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
func (cs *Conversations) generate(ctx context.Context, conv Conversation, messages []Message, emit func(ChatEvent)) (*StoredMessage, error) {
	set := conv.Settings
	opt := GenerateOptions{
		Think:           set.Think,
		Temperature:     float32(set.Temperature),
		MaxTokens:       cs.currentLimits().reply,
		PresencePenalty: float32(set.PresencePenalty),
	}
	model := ""
	if info := cs.gen.Info(); info != nil {
		model = info.Name // どのモデルの返事か、発言ごとに記録する
	}

	// 思考と答えを受け取りながら、時間を測る(画面の統計行と「思考の過程(N秒)」に使う)
	var thinking, content strings.Builder
	start := time.Now()
	var firstAt, thinkStart, thinkEnd time.Time
	onText := func(kind string, b *strings.Builder) func(string) {
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
			b.WriteString(t)
			emit(ChatEvent{ConversationID: conv.ID, Type: kind, Text: t})
		}
	}

	st, err := cs.gen.Generate(ctx, messages, opt,
		func(p string) { emit(ChatEvent{ConversationID: conv.ID, Type: "prompt", Text: p}) },
		onText("thinking", &thinking), onText("content", &content),
	)
	if err != nil {
		return nil, err
	}
	if thinking.Len() == 0 && content.Len() == 0 {
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
		Content: content.String(), Thinking: thinking.String(),
		Model: model, Stats: stats, CreatedAt: now,
	}
	if m.Content == "" {
		// 考えている途中で終わり、答えが出なかった場合。
		// 何も残さないと、モデルからは「質問に返事をしていない」ように見えるので、
		// 何が起きたかを返事として残す(次の送信でもモデルに渡る)。
		m.Content = noAnswerNote(st.StopReason)
		stats.NoAnswer = true
	}
	return m, nil
}

// promptMessages は保存した発言を、モデルに渡す形にする。
// 過去の返事は「答え」だけを入れ、思考は入れない(Qwen3.5 の公式テンプレートと同じ)。
// システムプロンプトは発言としては保存せず、会話の設定から毎回先頭に付ける。
//
// summary はこれまでの会話の要約(compact.go)。システムプロンプトの後ろに見出しを付けて足す。
// システムプロンプトが空でも、要約があれば system を作る。
func promptMessages(system, summary string, history []StoredMessage) []Message {
	var out []Message
	s := strings.TrimSpace(system)
	if summary != "" {
		if s != "" {
			s += "\n\n"
		}
		s += summaryHeading + "\n" + summary
	}
	if s != "" {
		out = append(out, Message{Role: "system", Content: s})
	}
	for _, m := range history {
		out = append(out, Message{Role: m.Role, Content: m.Content})
	}
	return out
}

// noAnswerNote は、答えが出ないまま終わったときに答えの代わりに残す文。
func noAnswerNote(stopReason string) string {
	switch stopReason {
	case "length":
		return "(考えている途中で上限トークン数に達したため、答えを出せませんでした)"
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
