package aibow

// lock.go: ほかの画面と同じ会話を同時に生成しないための「生成中」の印(0017 の 10-3。ユーザーと決めた)。
//
// 1つのアプリの中では、生成は同時に1つ(Conversations.generating)。ところが保存をサーバーに置くと、
// 同じ人がブラウザとアプリ(または2台のアプリ)から、同じ会話に同時に送れてしまう。それぞれのプロセスは相手を知らないので、
// 両方が返事を書き足し、発言が互い違いに並ぶ。そこで、会話を持っている保存先(server.db)に印を付けて、後から来たほうを断る。
//
// 印には期限を付ける。アプリが生成の途中で落ちても印が残り続けないように、期限(lockTTL)で外れるようにし、
// 生成している間は少し前に延ばし続ける(lockRenew ごと)。
//
// 保存先が印を扱えるとき(ConversationLocks を持っているとき)だけ使う。メモリの保存先(テスト)やライブラリとして使うときは印を付けない。

import (
	"context"
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/secondarykey/aibow/log"
)

// ConversationLocks は、会話の「生成中」の印を扱える保存先(store の SQLite と、その HTTP 版)。
type ConversationLocks interface {
	// LockConversation は、会話に holder の印を until まで付ける。同じ holder なら延ばす。
	// ほかの holder の印が期限の内にあれば ErrBusy。会話が無ければ ErrNotFound
	LockConversation(ctx context.Context, id uuid.UUID, holder string, until time.Time) error
	// UnlockConversation は、holder の印を外す(ほかの holder の印は外さない)
	UnlockConversation(ctx context.Context, id uuid.UUID, holder string) error
}

// ErrBusy は、ほかの画面(ブラウザ・ほかの PC のアプリ)で同じ会話の返事を生成中のときのエラー。
var ErrBusy = errors.New("この会話は、ほかの画面(ブラウザかほかの PC のアプリ)で返事を生成中です。終わってから送ってください")

const (
	lockTTL   = 2 * time.Minute  // 印の期限(落ちたアプリの印は、これで外れる)
	lockRenew = 30 * time.Second // 生成している間、印を延ばす間隔
)

// lockConversation は、保存先が印を扱えるなら会話 id に印を付け、外す関数を返す(扱えなければ何もしない関数)。
// 印は送信の全体(モデルの読み直し・要約・返事・ツール)の間付けておき、その間 lockRenew ごとに延ばす。
func (cs *Conversations) lockConversation(ctx context.Context, id uuid.UUID) (unlock func(), err error) {
	locks, ok := cs.store.(ConversationLocks)
	if !ok {
		return func() {}, nil
	}
	holder := uuid.NewV7().String()
	// 止められても印は外したいので、付ける・延ばす・外すは止める合図を外した ctx で行う(利用者は残る)
	lctx := context.WithoutCancel(ctx)
	if err := locks.LockConversation(lctx, id, holder, time.Now().Add(lockTTL)); err != nil {
		return nil, err
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(lockRenew)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				if err := locks.LockConversation(lctx, id, holder, time.Now().Add(lockTTL)); err != nil {
					log.Warnf("会話 %s の「生成中」の印を延ばせません(期限が切れると、ほかの画面から送れるようになります): %v", id, err)
				}
			}
		}
	}()
	return func() {
		close(stop)
		<-done
		if err := locks.UnlockConversation(lctx, id, holder); err != nil {
			log.Warnf("会話 %s の「生成中」の印を外せません(%s で外れます): %v", id, lockTTL, err)
		}
	}, nil
}

// busyError は、印が付けられなかった理由を送信のエラーにする。
func busyError(err error) error {
	if errors.Is(err, ErrBusy) || errors.Is(err, ErrNotFound) {
		return err
	}
	return fmt.Errorf("会話に「生成中」の印を付けられません: %w", err)
}
