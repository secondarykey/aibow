package aibow

// compact.go: 長くなった会話を、会話の上限に収める仕組み(compact)。
//
// モデルは何も覚えていないので、送信のたびに会話を全部並べ直して渡している(conversation.go)。
// 会話が長くなると、いつか作業領域に入りきらなくなる。「記憶」はアプリが管理しているので、
// 長い会話では、アプリが何かを諦めて(捨てるか、縮めて)渡すしかない。
//
// 収まっているかは、実際にモデルに渡す文字列(チャットテンプレートを当てたもの)のトークン数で判断する。
// トークン数は保存しない。モデルが変わると同じ文章でもトークン数が変わるので、毎回、今のモデルで数え直す。

import (
	"fmt"
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
	Method       string  `json:"method"`        // truncate / summary
	BeforeTokens int     `json:"before_tokens"` // compact する前の、モデルに渡す文字列のトークン数
	AfterTokens  int     `json:"after_tokens"`  // compact したあとの、モデルに渡す文字列のトークン数
	Dropped      int     `json:"dropped"`       // モデルに渡さなかった(truncate)/ 要約に入れた(summary)発言の数
	Sec          float64 `json:"sec"`           // かかった秒数
}

// passing は「モデルに渡す内容」。送信のときと、メーターを出すときに組み立てる。
type passing struct {
	first    int       // history のこの位置から後ろを渡す
	messages []Message // システムプロンプト + 発言(history[first:])
	tokens   int       // messages にチャットテンプレートを当てた文字列のトークン数
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

// passFrom は history[first:] をそのまま渡すときの内容を組み立てて数える。
func (cs *Conversations) passFrom(set ConversationSettings, history []StoredMessage, first int) (passing, error) {
	msgs := promptMessages(set.System, history[first:])
	n, err := cs.count(msgs, set.Think)
	return passing{first: first, messages: msgs, tokens: n}, err
}

// plan は、今の履歴で次にモデルに渡す内容を決める(メーターと送信で同じものを使う)。
func (cs *Conversations) plan(set ConversationSettings, history []StoredMessage) (passing, error) {
	switch set.Compact {
	case CompactTruncate:
		p, _, err := cs.truncate(set, history, 0, cs.currentLimits().history)
		return p, err
	default:
		return cs.passFrom(set, history, 0)
	}
}

// ---- 生成する前の確認 -------------------------------------------------------------------

// checkFits は、新しい発言を足した history を会話の上限に収められるかを、発言を保存する前に確かめる。
// 収められないなら、発言を保存せず、生成もせずに止める(返事の無い質問を残さないため)。
//
//	none     : 全部をそのまま渡すので、全部が上限以下でないといけない
//	truncate / summary : 最新の発言は必ず渡すので、それだけで上限を超えていたら収めようがない
func (cs *Conversations) checkFits(set ConversationSettings, history []StoredMessage) error {
	limit := cs.currentLimits().history
	if set.Compact == CompactNone {
		p, err := cs.passFrom(set, history, 0)
		if err != nil {
			return err
		}
		if p.tokens > limit {
			return fmt.Errorf("会話の上限を超えました(%d / %d トークン)。この会話の設定で compact を有効にするか、"+
				"アプリ全体の設定で会話の上限を増やしてください", p.tokens, limit)
		}
		return nil
	}
	p, err := cs.passFrom(set, history, len(history)-1)
	if err != nil {
		return err
	}
	if p.tokens > limit {
		return fmt.Errorf("この発言だけで会話の上限を超えています(システムプロンプト込みで %d / %d トークン)。"+
			"発言を短くするか、アプリ全体の設定で会話の上限を増やしてください", p.tokens, limit)
	}
	return nil
}

// ---- 捨てる(truncate)-----------------------------------------------------------------

// turnStarts は、history[from:] の中で「組」(ユーザーの発言 + その返事)が始まる位置の一覧。
// 外すのは組ごとにする。返事だけを残すと、モデルに渡す履歴が返事から始まり、
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

// truncate は、古い組から順にモデルに渡すのをやめて(history[from:] の中で)、会話の上限に収める。
// DB の発言は消さない。保存もしない(毎回、その時点の上限とモデルで計算し直すので、上限やモデルが変わっても合う)。
//
// 最新の組は必ず残す。最新の組だけでも収まらないときは fits=false で、最新の組だけを渡す内容を返す。
func (cs *Conversations) truncate(set ConversationSettings, history []StoredMessage, from, limit int) (p passing, fits bool, err error) {
	starts := turnStarts(history, from)
	if len(starts) == 0 {
		p, err = cs.passFrom(set, history, from)
		return p, p.tokens <= limit, err
	}

	// 後ろから始めるほどトークン数は減るので、「収まる一番前の位置」を二分探索で探す。
	// 前から1組ずつ外して数え直すと、長い会話ではトークン化を何十回もすることになる。
	var best *passing
	lo, hi := 0, len(starts)-1
	for lo <= hi {
		mid := (lo + hi) / 2
		q, err := cs.passFrom(set, history, starts[mid])
		if err != nil {
			return passing{}, false, err
		}
		if q.tokens <= limit {
			best, hi = &q, mid-1
		} else {
			lo = mid + 1
		}
	}
	if best == nil {
		p, err = cs.passFrom(set, history, starts[len(starts)-1])
		return p, false, err
	}
	return *best, true, nil
}

// prepare は、送信のときにモデルに渡す内容を決める。会話の上限を超えていたら、会話の設定の方式で収める。
// 収めたときは compact イベントを送る。
func (cs *Conversations) prepare(conv Conversation, history []StoredMessage, emit func(ChatEvent)) (passing, error) {
	set := conv.Settings
	limit := cs.currentLimits().history
	all, err := cs.passFrom(set, history, 0)
	if err != nil || all.tokens <= limit || set.Compact == CompactNone {
		return all, err // none は checkFits で確かめ済み
	}
	return cs.truncateNow(conv, history, 0, all.tokens, emit)
}

// truncateNow は truncate で収め、外した発言があれば compact イベントで知らせる。
// before は収める前のトークン数(ログ用)。
func (cs *Conversations) truncateNow(conv Conversation, history []StoredMessage, from, before int, emit func(ChatEvent)) (passing, error) {
	limit := cs.currentLimits().history
	p, fits, err := cs.truncate(conv.Settings, history, from, limit)
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

// Usage は、会話がモデルに渡す文字列で、会話の上限をどれだけ使っているかを返す。
// モデルが読み込まれていないと数えられない(ErrNoModel)。
func (cs *Conversations) Usage(id uuid.UUID) (Usage, error) {
	conv, history, err := cs.Get(id)
	if err != nil {
		return Usage{}, err
	}
	return cs.usage(conv.Settings, history)
}

func (cs *Conversations) usage(set ConversationSettings, history []StoredMessage) (Usage, error) {
	p, err := cs.plan(set, history)
	if err != nil {
		return Usage{}, err
	}
	return p.usage(history, cs.currentLimits().history), nil
}
