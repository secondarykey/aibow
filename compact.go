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
	return cs.passFrom(set, history, 0)
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
