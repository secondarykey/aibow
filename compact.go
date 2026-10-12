package aibow

// compact.go: 長くなった会話を、会話の上限に収める仕組み(compact)。
//
// モデルは何も覚えていないので、送信のたびに会話を全部並べ直して渡している(conversation.go)。
// 会話が長くなると、いつか作業領域に入りきらなくなる。「記憶」はアプリが管理しているので、
// 長い会話では、アプリが何かを諦めて(捨てるか、縮めて)渡すしかない。方式は会話ごとに選べる:
//
//	none     : 何もしない。上限を超えたら生成する前に止める
//	truncate : 古い発言から順に、モデルに渡すのをやめる。外した内容はモデルからは完全に消える
//	summary  : 古い部分をモデル自身に要約させ、「要約 + 最近の発言」を渡す。
//	           内容は残るが、要約を作る分の待ち時間とトークンを使い、要約の質に左右される
//
// どの方式でも DB の発言は消さない。画面には全部残し、モデルに渡す履歴だけを変える。
//
// 収まっているかは、実際にモデルに渡す文字列(チャットテンプレートを当てたもの)のトークン数で判断する。
// トークン数は保存しない。モデルが変わると同じ文章でもトークン数が変わるので、毎回、今のモデルで数え直す。

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"uuid"
)

// Usage は、会話がモデルに渡す文字列で、会話の上限をどれだけ使っているか(画面のメーター)。
type Usage struct {
	Tokens int `json:"tokens"` // モデルに渡す文字列のトークン数
	Limit  int `json:"limit"`  // 会話の上限
	// FirstID はモデルに渡す最初の発言。これより前の発言はモデルに渡していない(画面で薄く表示する)。
	// 発言が無ければ null。
	FirstID *uuid.UUID `json:"first_id"`
}

// CompactInfo は compact したことの知らせ(chat イベントの compact)。ログと画面に使う。
type CompactInfo struct {
	Method       string      `json:"method"`               // truncate / summary
	BeforeTokens int         `json:"before_tokens"`        // compact する前の、モデルに渡す文字列のトークン数
	AfterTokens  int         `json:"after_tokens"`         // compact したあとの、モデルに渡す文字列のトークン数
	Dropped      int         `json:"dropped"`              // モデルに渡さなかった(truncate)/ 要約に入れた(summary)発言の数
	Sec          float64     `json:"sec"`                  // かかった秒数
	Compaction   *Compaction `json:"compaction,omitempty"` // summary: 保存した要約
	Error        string      `json:"error,omitempty"`      // summary: 要約を作れなかった理由(保存していない)
}

// passing は「モデルに渡す内容」。送信のときと、メーターを出すときに組み立てる。
type passing struct {
	summary  *Compaction // 使う要約。無ければ nil
	first    int         // history のこの位置から後ろを渡す
	messages []Message   // システムプロンプト(+ 要約)+ 発言(history[first:])
	tokens   int         // messages にチャットテンプレートを当てた文字列のトークン数
}

// usage は passing をメーターの形にする。
func (p passing) usage(history []StoredMessage, limit int) Usage {
	u := Usage{Tokens: p.tokens, Limit: limit}
	if p.first < len(history) {
		id := history[p.first].ID
		u.FirstID = &id
	}
	return u
}

// count は、発言の列にチャットテンプレートを当てた文字列(= 実際にモデルに渡す文字列)のトークン数を数える。
func (cs *Conversations) count(messages []Message, think bool) (int, error) {
	return cs.gen.CountTokens(buildPrompt(messages, think))
}

// passFrom は、要約(nil なら無し)と history[first:] を渡すときの内容を組み立てて数える。
func (cs *Conversations) passFrom(set ConversationSettings, summary *Compaction, history []StoredMessage, first int) (passing, error) {
	text := ""
	if summary != nil {
		text = summary.Summary
	}
	msgs := promptMessages(set.System, text, history[first:])
	n, err := cs.count(msgs, set.Think)
	return passing{summary: summary, first: first, messages: msgs, tokens: n}, err
}

// plan は、今の履歴で次にモデルに渡す内容を決める(メーターと送信で同じものを使う)。
// truncate は上限に収めた内容、summary は「最新の要約 + まだ要約に入っていない発言」(上限を超えていることもある)。
func (cs *Conversations) plan(conv Conversation, history []StoredMessage) (passing, error) {
	set := conv.Settings
	switch set.Compact {
	case CompactTruncate:
		p, _, err := cs.truncate(set, nil, history, 0, cs.currentLimits().history)
		return p, err
	case CompactSummary:
		latest, from, err := cs.latestSummary(conv.ID, history)
		if err != nil {
			return passing{}, err
		}
		return cs.passFrom(set, latest, history, from)
	default:
		return cs.passFrom(set, nil, history, 0)
	}
}

// Usage は、会話がモデルに渡す文字列で、会話の上限をどれだけ使っているかを返す。
// モデルが読み込まれていないと数えられない(ErrNoModel)。
func (cs *Conversations) Usage(id uuid.UUID) (Usage, error) {
	conv, history, err := cs.Get(id)
	if err != nil {
		return Usage{}, err
	}
	return cs.usage(conv, history)
}

func (cs *Conversations) usage(conv Conversation, history []StoredMessage) (Usage, error) {
	p, err := cs.plan(conv, history)
	if err != nil {
		return Usage{}, err
	}
	return p.usage(history, cs.currentLimits().history), nil
}

// Compactions は会話の要約を古い順に返す(画面の区切りに使う)。
func (cs *Conversations) Compactions(id uuid.UUID) ([]Compaction, error) {
	return cs.store.Compactions(id)
}

// ---- 生成する前の確認 -------------------------------------------------------------------

// checkFits は、新しい発言を足した history を会話の上限に収められるかを、発言を保存する前に確かめる。
// 収められないなら、発言を保存せず、生成もせずに止める(返事の無い質問を残さないため)。
//
//	none               : 全部をそのまま渡すので、全部が上限以下でないといけない
//	truncate / summary : 最新の発言は必ず渡すので、それだけで上限を超えていたら収めようがない
func (cs *Conversations) checkFits(set ConversationSettings, history []StoredMessage) error {
	limit := cs.currentLimits().history
	if set.Compact == CompactNone {
		p, err := cs.passFrom(set, nil, history, 0)
		if err != nil {
			return err
		}
		if p.tokens > limit {
			return fmt.Errorf("会話の上限を超えました(%d / %d トークン)。この会話の設定で compact を有効にするか、"+
				"アプリ全体の設定で会話の上限を増やしてください", p.tokens, limit)
		}
		return nil
	}
	p, err := cs.passFrom(set, nil, history, len(history)-1)
	if err != nil {
		return err
	}
	if p.tokens > limit {
		return fmt.Errorf("この発言だけで会話の上限を超えています(システムプロンプト込みで %d / %d トークン)。"+
			"発言を短くするか、アプリ全体の設定で会話の上限を増やしてください", p.tokens, limit)
	}
	return nil
}

// prepare は、送信のときにモデルに渡す内容を決める。会話の上限を超えていたら、会話の設定の方式で収める。
// 収めたときは compact イベントを送る。ctx が止められたら(アプリの終了など)ctx のエラーを返す。
func (cs *Conversations) prepare(ctx context.Context, conv Conversation, history []StoredMessage, emit func(ChatEvent)) (passing, error) {
	limit := cs.currentLimits().history
	p, err := cs.passFrom(conv.Settings, nil, history, 0)
	if conv.Settings.Compact == CompactSummary {
		p, err = cs.plan(conv, history)
	}
	if err != nil || p.tokens <= limit {
		return p, err
	}

	switch conv.Settings.Compact {
	case CompactTruncate:
		return cs.truncateNow(conv, nil, history, 0, p.tokens, emit)
	case CompactSummary:
		// 返事を生成する前に、古い部分を要約する
		if _, err := cs.summarize(ctx, conv, history, emit); err != nil {
			if ctx.Err() != nil {
				return passing{}, ctx.Err() // 送信全体が止められた
			}
			// 要約できなかった回は、保存せず、その回だけ truncate で収めて返事を生成する
			emit(ChatEvent{ConversationID: conv.ID, Type: "compact", Compact: &CompactInfo{
				Method: CompactSummary, BeforeTokens: p.tokens,
				Error: fmt.Sprintf("要約を作れませんでした(%v)。この回は、古い発言を外して(truncate)返事を生成します", err),
			}})
		}
		// 成功しても失敗しても、今ある最新の要約(何回かに分けた途中までの分も含む)で数え直す。
		// それでも超えていれば(要約が長かった、失敗した)、要約に入っていない発言を truncate で外す
		p, err = cs.plan(conv, history)
		if err != nil || p.tokens <= limit {
			return p, err
		}
		return cs.truncateNow(conv, p.summary, history, p.first, p.tokens, emit)
	}
	return p, nil // none は checkFits で確かめ済み
}

// ---- 捨てる(truncate)-----------------------------------------------------------------

// turnStarts は、history[from:] の中で「組」(ユーザーの発言 + その返事)が始まる位置の一覧。
// 外す・要約するのは組ごとにする。返事だけを残すと、モデルに渡す履歴が返事から始まり、
// 「何への返事か」が分からない文章になってしまうため。
func turnStarts(history []StoredMessage, from int) []int {
	var out []int
	for i := from; i < len(history); i++ {
		if history[i].Role == "user" {
			out = append(out, i)
		}
	}
	return out
}

// firstFitting は、starts の中で fits が true になる一番前の位置(starts の添字)を返す。無ければ -1。
//
// 後ろから始めるほど渡す内容は短くなる(true になりやすい)ので、二分探索で探せる。
// 前から1組ずつ外して数え直すと、長い会話ではトークン化を何十回もすることになる。
func firstFitting(starts []int, fits func(start int) (bool, error)) (int, error) {
	found := -1
	lo, hi := 0, len(starts)-1
	for lo <= hi {
		mid := (lo + hi) / 2
		ok, err := fits(starts[mid])
		if err != nil {
			return -1, err
		}
		if ok {
			found, hi = mid, mid-1
		} else {
			lo = mid + 1
		}
	}
	return found, nil
}

// truncate は、古い組から順にモデルに渡すのをやめて(history[from:] の中で)、会話の上限に収める。
// 要約(summary)があれば、それは残したまま外す(要約に失敗した回の代わり)。
// DB の発言は消さない。保存もしない(毎回、その時点の上限とモデルで計算し直すので、上限やモデルが変わっても合う)。
//
// 最新の組は必ず残す。最新の組だけでも収まらないときは fits=false で、最新の組だけを渡す内容を返す。
func (cs *Conversations) truncate(set ConversationSettings, summary *Compaction, history []StoredMessage, from, limit int) (p passing, fits bool, err error) {
	starts := turnStarts(history, from)
	if len(starts) == 0 {
		p, err = cs.passFrom(set, summary, history, from)
		return p, p.tokens <= limit, err
	}
	var best passing
	i, err := firstFitting(starts, func(start int) (bool, error) {
		q, err := cs.passFrom(set, summary, history, start)
		if q.tokens <= limit {
			best = q // 二分探索で最後に収まった位置 = 収まる一番前の位置
		}
		return q.tokens <= limit, err
	})
	if err != nil {
		return passing{}, false, err
	}
	if i < 0 {
		p, err = cs.passFrom(set, summary, history, starts[len(starts)-1])
		return p, false, err
	}
	return best, true, nil
}

// truncateNow は truncate で収め、外した発言があれば compact イベントで知らせる。
// before は収める前のトークン数(ログ用)。
func (cs *Conversations) truncateNow(conv Conversation, summary *Compaction, history []StoredMessage, from, before int, emit func(ChatEvent)) (passing, error) {
	limit := cs.currentLimits().history
	p, fits, err := cs.truncate(conv.Settings, summary, history, from, limit)
	if err != nil {
		return passing{}, err
	}
	if !fits {
		return passing{}, fmt.Errorf("最新の発言だけで会話の上限を超えています(%d / %d トークン)。"+
			"発言を短くするか、アプリ全体の設定で会話の上限を増やしてください", p.tokens, limit)
	}
	if p.first > from {
		emit(ChatEvent{ConversationID: conv.ID, Type: "compact", Compact: &CompactInfo{
			Method: CompactTruncate, BeforeTokens: before, AfterTokens: p.tokens, Dropped: p.first - from,
		}})
	}
	return p, nil
}

// ---- 要約(summary)--------------------------------------------------------------------

// 要約を作るときの生成の設定。要約は「考える」より「まとめる」作業なので、思考はオフにし、
// temperature を低めにして、事実から外れた言い換えをしにくくする。
const (
	summaryMaxTokens   = 1024 // 要約1回で生成するトークン数の上限
	summaryTemperature = 0.3
	summaryMaxRunes    = 800 // 指示で伝える要約の文字数。上限トークン数に収まるよう、少なめに伝える
)

// summaryHeading は、モデルに渡すときに要約の前に付ける見出し(システムプロンプトの後ろに付ける)。
const summaryHeading = "# これまでの会話の要約(古い部分は省略されています)"

// summaryInstruction は要約を作るときのシステムプロンプト(_docs/features/0002-compact.md で決めた文面)。
var summaryInstruction = fmt.Sprintf(`あなたは会話の要約係です。「前回までの要約」と「その続きの会話」を、1つの要約にまとめ直してください。
この要約は、このあと会話を続けるときに、これまでの会話の代わりとしてアシスタントに渡されます。

必ず残すこと:
- 出てきた人名・物の名前・数値
- 決まったこと・合意したこと
- ユーザーの好みや前提・条件(「〜は使わない」「〜で答えてほしい」など)
- まだ答えが出ていない問い・やりかけの作業

書き方:
- 日本語の箇条書きで、%d字以内
- 挨拶や言い回しは省き、事実だけを書く
- 会話に無いことを付け足さない
- 要約だけを出力する(「以下が要約です」などの前置きは書かない)`, summaryMaxRunes)

// errNothingToSummarize は、要約できる古い発言が無いときのエラー。
var errNothingToSummarize = errors.New("要約できる古い発言がありません(最近の発言が、会話の上限の半分に収まっています)")

// latestSummary は会話の最新の要約と、まだ要約に入っていない最初の発言の位置を返す。要約が無ければ nil, 0。
func (cs *Conversations) latestSummary(id uuid.UUID, history []StoredMessage) (*Compaction, int, error) {
	list, err := cs.store.Compactions(id)
	if err != nil || len(list) == 0 {
		return nil, 0, err
	}
	latest := list[len(list)-1]
	// UUIDv7 は作った順に並ぶので、upto より後ろの ID の発言が、まだ要約に入っていない
	from := len(history)
	for i, m := range history {
		if m.ID.Compare(latest.UptoMessageID) > 0 {
			from = i
			break
		}
	}
	return &latest, from, nil
}

// summaryMessages は要約を作らせるときに渡す発言。前回の要約と、その続きの会話を1つの user の発言にする。
// 会話は ChatML の発言として並べず、「ユーザー: …」の文章にする。発言として並べると、
// モデルは要約ではなく、会話の続き(最後の質問への返事)を書こうとしてしまうため。
func summaryMessages(prev string, part []StoredMessage) []Message {
	var b strings.Builder
	b.WriteString("## 前回までの要約\n")
	if prev == "" {
		b.WriteString("(なし)")
	} else {
		b.WriteString(prev)
	}
	b.WriteString("\n\n## その続きの会話\n")
	for _, m := range part {
		who := "ユーザー"
		if m.Role == "assistant" {
			who = "アシスタント"
		}
		fmt.Fprintf(&b, "%s: %s\n", who, m.Content)
	}
	return []Message{{Role: "system", Content: summaryInstruction}, {Role: "user", Content: b.String()}}
}

// summarize は、まだ要約に入っていない古い発言を、前回の要約と一緒に要約し直して保存する(要約を積み重ねる)。
// 最近の発言は、会話の上限のおよそ半分までそのまま残す(最新の組は必ず残す)。
//
// 要約したい部分が作業領域に入りきらないときは、古い方から何回かに分けて要約し、そのたびに保存する。
// 要約の生成中は、画面の停止ボタン(StopSummary)で要約だけを止められる。
func (cs *Conversations) summarize(ctx context.Context, conv Conversation, history []StoredMessage, emit func(ChatEvent)) (*Compaction, error) {
	set := conv.Settings
	lim := cs.currentLimits()
	latest, from, err := cs.latestSummary(conv.ID, history)
	if err != nil {
		return nil, err
	}

	// 1. どこから後ろを残すか: 会話の上限の半分に収まる、一番前の組から
	keep, err := cs.keepFrom(set, history, from, lim.history/2)
	if err != nil {
		return nil, err
	}
	if keep <= from {
		return nil, errNothingToSummarize
	}

	// 2. history[from:keep] を要約する。作業領域(会話の上限 + 返事の上限)に入る分ずつ
	ctxSize := lim.history + lim.reply
	for from < keep {
		end, err := cs.summaryEnd(history, latest, from, keep, ctxSize-summaryMaxTokens)
		if err != nil {
			return nil, err
		}
		c, err := cs.summarizePart(ctx, conv, history, latest, from, end, emit)
		if err != nil {
			return nil, err
		}
		latest, from = c, end
	}
	return latest, nil
}

// keepFrom は、最近の発言をそのまま残す位置を返す。history[from:] の組の始まりのうち、
// そこから後ろが budget トークンに収まる一番前の位置。どれも収まらなければ最新の組の始まり。
func (cs *Conversations) keepFrom(set ConversationSettings, history []StoredMessage, from, budget int) (int, error) {
	starts := turnStarts(history, from)
	if len(starts) == 0 {
		return from, nil
	}
	i, err := firstFitting(starts, func(start int) (bool, error) {
		p, err := cs.passFrom(set, nil, history, start)
		return p.tokens <= budget, err
	})
	if err != nil {
		return 0, err
	}
	if i < 0 {
		i = len(starts) - 1
	}
	return starts[i], nil
}

// summaryEnd は、history[from:end] を前回の要約と一緒に要約させるとき、要約の指示が budget に収まる一番大きい end を返す。
// end は組の切れ目(組の始まりか keep)にする。1組も収まらなければエラー。
func (cs *Conversations) summaryEnd(history []StoredMessage, prev *Compaction, from, keep, budget int) (int, error) {
	prevText := ""
	if prev != nil {
		prevText = prev.Summary
	}
	fits := func(end int) (bool, error) {
		n, err := cs.count(summaryMessages(prevText, history[from:end]), false)
		return n <= budget, err
	}
	// 候補は from より後ろの組の始まりと keep。長くなるほど収まりにくいので、後ろから二分探索する
	ends := append(turnStarts(history[:keep], from+1), keep)
	lo, hi, best := 0, len(ends)-1, -1
	for lo <= hi {
		mid := (lo + hi) / 2
		ok, err := fits(ends[mid])
		if err != nil {
			return 0, err
		}
		if ok {
			best, lo = mid, mid+1
		} else {
			hi = mid - 1
		}
	}
	if best < 0 {
		return 0, errors.New("要約する発言が長すぎて、作業領域に入りません。アプリ全体の設定で会話の上限か返事の上限を増やしてください")
	}
	return ends[best], nil
}

// summarizePart は、前回の要約 prev と history[from:end] を要約し直して保存する。
// 要約の文は summary イベントで流す。保存したら compact イベントで知らせる。
func (cs *Conversations) summarizePart(ctx context.Context, conv Conversation, history []StoredMessage,
	prev *Compaction, from, end int, emit func(ChatEvent)) (*Compaction, error) {
	set := conv.Settings
	before, err := cs.passFrom(set, prev, history, from)
	if err != nil {
		return nil, err
	}

	start := time.Now()
	prevText := ""
	if prev != nil {
		prevText = prev.Summary
	}
	text, st, err := cs.generateSummary(ctx, conv.ID, summaryMessages(prevText, history[from:end]), emit)
	if err != nil {
		return nil, err
	}

	c := Compaction{
		ID: uuid.NewV7(), ConversationID: conv.ID, UptoMessageID: history[end-1].ID,
		Summary: text, CreatedAt: time.Now(),
	}
	if info := cs.gen.Info(); info != nil {
		c.Model = info.Name
	}
	after, err := cs.passFrom(set, &c, history, end)
	if err != nil {
		return nil, err
	}
	c.Stats = &CompactStats{
		GenerateStats: st, BeforeTokens: before.tokens, AfterTokens: after.tokens,
		Messages: end - from, Sec: time.Since(start).Seconds(),
	}
	if err := cs.store.AddCompaction(c); err != nil {
		return nil, fmt.Errorf("要約を保存できません: %w", err)
	}
	emit(ChatEvent{ConversationID: conv.ID, Type: "compact", Compact: &CompactInfo{
		Method: CompactSummary, BeforeTokens: before.tokens, AfterTokens: after.tokens,
		Dropped: end - from, Sec: c.Stats.Sec, Compaction: &c,
	}})
	return &c, nil
}

// generateSummary はモデルに要約を書かせる。答えが出なかった・途中で切れた・停止されたときはエラーにする
// (中途半端な要約を保存して、以後ずっとモデルに渡し続けないため)。
func (cs *Conversations) generateSummary(ctx context.Context, id uuid.UUID, msgs []Message, emit func(ChatEvent)) (string, GenerateStats, error) {
	// 停止ボタン(StopSummary)で、送信全体ではなく要約だけを止められるようにする
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cs.summaryMu.Lock()
	cs.stopSummary = cancel
	cs.summaryMu.Unlock()
	defer func() {
		cs.summaryMu.Lock()
		cs.stopSummary = nil
		cs.summaryMu.Unlock()
	}()

	emit(ChatEvent{ConversationID: id, Type: "summarizing"})
	var b strings.Builder
	opt := GenerateOptions{Think: false, Temperature: summaryTemperature, MaxTokens: summaryMaxTokens}
	st, err := cs.gen.Generate(sctx, msgs, opt, func(string) {}, func(string) {}, func(t string) {
		b.WriteString(t)
		emit(ChatEvent{ConversationID: id, Type: "summary", Text: t})
	})
	if err != nil {
		return "", st, err
	}
	text := strings.TrimSpace(b.String())
	switch {
	case st.StopReason == "cancel":
		return "", st, errors.New("要約を停止しました")
	case st.StopReason == "length":
		return "", st, fmt.Errorf("要約が上限(%d トークン)で途中で切れました", summaryMaxTokens)
	case st.StopReason == "context":
		return "", st, errors.New("要約の途中で作業領域がいっぱいになりました")
	case text == "":
		return "", st, errors.New("要約が空でした")
	}
	return text, st, nil
}

// StopSummary は生成中の要約だけを止める(画面の要約中の停止ボタン)。
// 送信の途中の要約なら、その回は truncate で収めて返事の生成に進む。
func (cs *Conversations) StopSummary() {
	cs.summaryMu.Lock()
	defer cs.summaryMu.Unlock()
	if cs.stopSummary != nil {
		cs.stopSummary()
	}
}

// Compact は「今すぐ要約」。会話の上限を超えていなくても、古い発言を要約する(Claude Code の /compact と同じ)。
// 要約する範囲は送信のときの自動の要約と同じ(最近の発言を会話の上限の半分まで残す)。
// 途中経過は Send と同じ chat イベントで渡し、最後に必ず done を1回渡す。
func (cs *Conversations) Compact(ctx context.Context, id uuid.UUID, emit func(ChatEvent)) {
	defer emit(ChatEvent{ConversationID: id, Type: "done"})
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
	if _, err := cs.summarize(ctx, conv, history, emit); err != nil {
		if errors.Is(err, errNothingToSummarize) {
			fail(err)
		} else {
			fail(fmt.Errorf("要約を作れませんでした: %w", err))
		}
	}
	if u, err := cs.usage(conv, history); err == nil {
		cs.emitUsage(emit, id, u)
	}
}
