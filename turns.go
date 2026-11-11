package aibow

// turns.go: 進行中の送信(返事の生成・今すぐ要約)を、利用者ごとに1つ持つ(0023 の決定事項 25)。
//
// 以前は Conversations の全体で「生成中の会話」を1つだけ持っていた。デスクトップのアプリは使う人が1人なので、それで足りる。
// ところが aibow-server は、ブラウザの全員で1つの Conversations を使う。全体で1つだと、シーケンスを2本にしても(-parallel 2)、
// 2人目の送信は席を取れたのに「別の会話で返事を生成中です」と断られてしまう。
//
// そこで、「同時に1つ」を人ごとにする。
//
//	同じ人の中では … 今までどおり、生成は同時に1つ(画面が、生成中の会話を1つだけ覚える作りなので)
//	人と人の間では … ここでは止めない。同時に何人まで生成できるかは、順番待ちの席(api.Seat。数はシーケンスの数)が決める
//
// 進行中の送信が持つもの(待っている確認・要約を止める関数)も、送信ごとに1つなので、同じ場所に置く。
// 誰の送信かは ctx の利用者(UserOf)で決める。画面から送られてきた値では決めないので、ほかの人の確認に答えたり、
// ほかの人の要約を止めたりはできない(自分の進行中の送信しか引けない)。

import (
	"context"
	"uuid"
)

// activeTurn は進行中の送信1つ。中身は Conversations.turnMu を持って読み書きする。
type activeTurn struct {
	conv uuid.UUID // 生成中の会話
	// stopSummary は生成中の要約だけを止める関数(StopSummary)。要約していなければ nil
	stopSummary context.CancelFunc
	// pending は答えを待っている確認。待っていなければ nil。1つの送信の中ではツールを順に実行するので、確認も同時に1つだけ
	pending *pendingConfirm
}

// turnUser は、進行中の送信を引くときの利用者。
// ctx に利用者が無いとき(デスクトップのアプリ。Wails から来る呼び出しには印が無い・ライブラリとして使うとき)は LocalUser にする
// (保存先の store.SQLite.OneUser と同じ扱い。使うのがその PC の1人なので、全体で1つになる)。
func turnUser(ctx context.Context) uuid.UUID {
	if id, ok := UserOf(ctx); ok {
		return id
	}
	return LocalUser
}

// beginTurn は、ctx の利用者の進行中の送信を会話 id で始め、終えるときに呼ぶ関数を返す。
// その人がもう生成中なら ErrGenerating(ほかの人が生成中かは見ない)。
func (cs *Conversations) beginTurn(ctx context.Context, id uuid.UUID) (end func(), err error) {
	user := turnUser(ctx)
	cs.turnMu.Lock()
	defer cs.turnMu.Unlock()
	if _, busy := cs.turns[user]; busy {
		return nil, ErrGenerating
	}
	if cs.turns == nil {
		cs.turns = map[uuid.UUID]*activeTurn{}
	}
	t := &activeTurn{conv: id}
	cs.turns[user] = t
	return func() {
		cs.turnMu.Lock()
		defer cs.turnMu.Unlock()
		if cs.turns[user] == t {
			delete(cs.turns, user)
		}
	}, nil
}

// turnIn は、ctx の利用者が会話 id で進めている送信(無ければ nil)。turnMu を持って呼ぶ。
func (cs *Conversations) turnIn(ctx context.Context, id uuid.UUID) *activeTurn {
	if t := cs.turns[turnUser(ctx)]; t != nil && t.conv == id {
		return t
	}
	return nil
}

// generatingIn は、ctx の利用者が会話 id の返事を生成中か(生成中の会話は、消す・履歴を変えるなどができない)。
// 会話を変えられるのは持ち主だけで、その会話で生成できるのも持ち主だけなので、自分の進行中の送信だけを見れば足りる。
func (cs *Conversations) generatingIn(ctx context.Context, id uuid.UUID) bool {
	cs.turnMu.Lock()
	defer cs.turnMu.Unlock()
	return cs.turnIn(ctx, id) != nil
}

// Generating は、ctx の利用者が生成中の会話の ID を返す。生成していなければ nil。
func (cs *Conversations) Generating(ctx context.Context) *uuid.UUID {
	cs.turnMu.Lock()
	defer cs.turnMu.Unlock()
	if t := cs.turns[turnUser(ctx)]; t != nil {
		id := t.conv
		return &id
	}
	return nil
}
