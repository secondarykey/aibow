package aibow

import (
	"context"
	"sync"
	"testing"
	"uuid"

	"github.com/secondarykey/aibow/engine"
	"github.com/secondarykey/aibow/format"
)

// gateGen は、生成を始めたことを知らせてから、release が閉じられるまで終えない Generator(同時の生成を確かめる)。
// fakeGen は呼ばれた記録をロックなしで足すので、2つの生成が同時に走るテストでは Generate だけを差し替える。
type gateGen struct {
	*fakeGen
	started chan struct{} // 生成を始めるたびに1つ送る
	release chan struct{}
}

func (g *gateGen) Generate(ctx context.Context, p format.Prompt, opt engine.GenerateOptions,
	onPrompt func(string), onThinking, onContent func(string)) (format.Output, engine.GenerateStats, error) {
	g.started <- struct{}{}
	<-g.release
	return format.Output{Content: "ok"}, engine.GenerateStats{GenTokens: 1, StopReason: "eos"}, nil
}

// 「生成は同時に1つ」は人ごと。別の人どうしは同時に生成でき、同じ人の2つ目は断る(0023 の決定事項 25)
func TestGenerationIsPerUser(t *testing.T) {
	g := &gateGen{fakeGen: &fakeGen{}, started: make(chan struct{}, 2), release: make(chan struct{})}
	st := newMemStore()
	cs := NewConversations(st, g, AppSettings{Defaults: testDefaults})
	alice, bob := WithUser(context.Background(), uuid.NewV7()), WithUser(context.Background(), uuid.NewV7())
	create := func(ctx context.Context) Conversation {
		c, err := cs.Create(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	a, a2, b := create(alice), create(alice), create(bob)

	var wg sync.WaitGroup
	errs := map[uuid.UUID]string{} // 会話 → 送信のエラー
	var mu sync.Mutex
	sendAs := func(ctx context.Context, c Conversation) {
		defer wg.Done()
		cs.Send(ctx, c.ID, "質問", nil, func(ev ChatEvent) {
			if ev.Type == "error" {
				mu.Lock()
				errs[c.ID] = ev.Error
				mu.Unlock()
			}
		})
	}
	wg.Add(2)
	go sendAs(alice, a)
	go sendAs(bob, b)
	<-g.started
	<-g.started // 2人とも生成に入った(全体で1つなら、2人目は断られてここに来ない)

	if id := cs.Generating(alice); id == nil || *id != a.ID {
		t.Errorf("alice の生成中の会話: %v", id)
	}
	if id := cs.Generating(bob); id == nil || *id != b.ID {
		t.Errorf("bob の生成中の会話: %v", id)
	}
	// 同じ人の2つ目は、今までどおり断る
	wg.Add(1)
	sendAs(alice, a2)
	if errs[a2.ID] != ErrGenerating.Error() {
		t.Errorf("同じ人の2つ目の送信が断られていません: %q", errs[a2.ID])
	}
	// 生成中の会話は消せない。ほかの人の生成中は、自分の会話に関係しない
	if err := cs.Delete(alice, a.ID); err == nil {
		t.Error("生成中の会話を削除できてしまいました")
	}
	if err := cs.Delete(alice, a2.ID); err != nil {
		t.Errorf("生成していない会話を削除できません: %v", err)
	}

	close(g.release)
	wg.Wait()
	if errs[a.ID] != "" || errs[b.ID] != "" {
		t.Errorf("同時の送信がエラーになりました: %v", errs)
	}
	if cs.Generating(alice) != nil || cs.Generating(bob) != nil {
		t.Error("生成が終わったのに生成中のままです")
	}
}

// 待っている確認は、その送信をしている人だけが見えて、答えられる
func TestConfirmIsPerUser(t *testing.T) {
	g := &fakeGen{replies: []fakeReply{
		{toolCalls: []format.ToolCall{toolCall("edit_file", "path", "main.go", "old_string", "package main", "new_string", "package app")}},
		{content: "直しました"},
	}}
	cs, c, dir := newWorkDirConversation(t, g)
	other := WithUser(context.Background(), uuid.NewV7())
	evs := sendAnswering(cs, ctx, c, "パッケージ名を app にして", func(r *ConfirmRequest) bool {
		if got, _ := cs.Pending(other, c.ID); got != nil {
			t.Errorf("ほかの人に確認が見えています: %+v", got)
		}
		if err := cs.Confirm(other, c.ID, r.ID, true, ""); err == nil {
			t.Error("ほかの人が確認に答えられてしまいました")
		}
		if fileText(t, dir, "main.go") != "package main\n" {
			t.Error("ほかの人の答えで書き換わっています")
		}
		if got, _ := cs.Pending(ctx, c.ID); got == nil || got.ID != r.ID {
			t.Errorf("自分の確認が取り直せません: %+v", got)
		}
		if err := cs.Confirm(ctx, c.ID, r.ID, true, ""); err != nil {
			t.Errorf("自分の確認に答えられません: %v", err)
		}
		return true
	})
	if errs := errorsOf(evs); len(errs) != 0 {
		t.Errorf("エラー: %v", errs)
	}
	if fileText(t, dir, "main.go") != "package app\n" {
		t.Errorf("書き換わっていません: %q", fileText(t, dir, "main.go"))
	}
}
