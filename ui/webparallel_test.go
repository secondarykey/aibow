package ui

import (
	"context"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/secondarykey/aibow"
	"github.com/secondarykey/aibow/api"
	"github.com/secondarykey/aibow/auth"
)

// 席が2つ(-parallel 2)なら、ブラウザの2人が同時に生成できる(0023 の決定事項 25)。
// 以前は会話の仕組みが「生成中の会話」を全体で1つだけ持っていたので、2人目は席を取れても「別の会話で返事を生成中です」と断られた。
func TestWebTwoUsersGenerateAtOnce(t *testing.T) {
	w := newTestWeb(t, func(o *Options) { o.Seat = api.NewSeats(2) })
	w.eng.wait, w.eng.began = true, make(chan struct{}, 2)
	now := time.Now()
	add := func(name string) *uuid.UUID {
		u := auth.User{ID: uuid.NewV7(), Name: name, CreatedAt: now, UpdatedAt: now}
		if err := w.st.CreateUser(context.Background(), u); err != nil {
			t.Fatal(err)
		}
		uctx := aibow.WithUser(context.Background(), u.ID)
		app, _, _ := aibow.LoadAppSettings(uctx, w.st, InitialAppSettings(InitialValues{GPULayers: 999, PresencePenalty: 1.5, HistoryTokens: 8192, ReplyTokens: 8192}))
		if _, err := aibow.NewConversations(w.st, nil, app).PrepareProfiles(uctx); err != nil {
			t.Fatal(err)
		}
		return &u.ID
	}
	alice, bob := add("alice"), add("bob")

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	var wg sync.WaitGroup
	// 利用者を切り替えてから送り、生成に入るのを待つ(誰の呼び出しかは、リクエストが届いたときの w.user で決まる)
	sendAs := func(user *uuid.UUID) aibow.Conversation {
		t.Helper()
		w.user.Store(user)
		c := w.newConversation(t)
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.call(t, ctx, "Send", c.ID, "長く書いて", nil)
		}()
		select {
		case <-w.eng.began:
		case <-time.After(5 * time.Second):
			t.Fatal("生成が始まりません(ほかの人の生成中に断られた・待たされた)")
		}
		return c
	}
	a := sendAs(alice)
	b := sendAs(bob) // alice が生成している間に、bob も生成に入る

	// 生成中の印は人ごと(自分の会話が生成中に見える)
	var d ConversationDetail
	w.mustCall(t, &d, "Conversation", b.ID)
	if !d.Generating {
		t.Error("bob の会話が生成中になっていません")
	}
	w.user.Store(alice)
	w.mustCall(t, &d, "Conversation", a.ID)
	if !d.Generating {
		t.Error("alice の会話が生成中になっていません")
	}

	stop() // 2人とも停止する
	wg.Wait()
	for _, tc := range []struct {
		user *uuid.UUID
		conv aibow.Conversation
	}{{alice, a}, {bob, b}} {
		w.user.Store(tc.user)
		// 停止のあとの保存を待つ(fetch を止めても、サーバーの側はそこまでの返事を保存してから終わる)
		deadline := time.Now().Add(5 * time.Second)
		for {
			w.mustCall(t, &d, "Conversation", tc.conv.ID)
			if !d.Generating && len(d.Messages) == 2 || time.Now().After(deadline) {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if d.Generating || len(d.Messages) != 2 || d.Messages[1].Content != "途中" {
			t.Errorf("止めたところまでの返事が残るはず: 生成中=%v %+v", d.Generating, d.Messages)
		}
	}
}
