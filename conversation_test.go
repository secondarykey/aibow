package aibow

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeGen はテスト用の Generator。決めておいた思考と答えを返し、渡された内容を記録する。
type fakeGen struct {
	thinking, content, stopReason string
	block                         chan struct{} // nil でなければ、閉じられるまで生成を終えない

	gotMessages []Message
	gotOpt      GenerateOptions
}

func (g *fakeGen) Info() *ModelInfo { return &ModelInfo{ID: "file:fake", Name: "fake-model"} }

func (g *fakeGen) Generate(ctx context.Context, messages []Message, opt GenerateOptions,
	onPrompt func(string), onThinking, onContent func(string)) (GenerateStats, error) {
	g.gotMessages, g.gotOpt = messages, opt
	onPrompt(buildPrompt(messages, opt.Think))
	if g.block != nil {
		<-g.block
	}
	if g.thinking != "" {
		onThinking(g.thinking)
	}
	if g.content != "" {
		onContent(g.content)
	}
	reason := g.stopReason
	if reason == "" {
		reason = "eos"
	}
	return GenerateStats{GenTokens: 10, StopReason: reason}, nil
}

var testDefaults = ConversationSettings{System: "あなたはテスト用です。", MaxTokens: 100, Temperature: 0.7, Think: true, PresencePenalty: 1.5}

func newTestConversations(t *testing.T, g *fakeGen) (*Conversations, *memStore) {
	t.Helper()
	st := newMemStore()
	return NewConversations(st, g, testDefaults), st
}

// send は Send を呼び、渡されたイベントを返す。
func send(t *testing.T, cs *Conversations, c Conversation, text string) []ChatEvent {
	t.Helper()
	var evs []ChatEvent
	cs.Send(context.Background(), c.ID, text, func(ev ChatEvent) { evs = append(evs, ev) })
	if len(evs) == 0 || evs[len(evs)-1].Type != "done" {
		t.Fatalf("最後のイベントが done ではありません: %+v", evs)
	}
	for _, ev := range evs {
		if ev.ConversationID != c.ID {
			t.Errorf("イベントの会話 ID が違います: %v", ev.ConversationID)
		}
		if ev.Type == "error" {
			t.Fatalf("エラー: %s", ev.Error)
		}
	}
	return evs
}

func mustCreate(t *testing.T, cs *Conversations) Conversation {
	t.Helper()
	c, err := cs.Create(nil)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSendSavesThinkingButDoesNotPassIt(t *testing.T) {
	g := &fakeGen{thinking: "挨拶だな。", content: "こんにちは!"}
	cs, _ := newTestConversations(t, g)
	c := mustCreate(t, cs)

	evs := send(t, cs, c, "やあ")
	done := evs[len(evs)-1]
	if done.Reply == nil || done.Reply.Content != "こんにちは!" || done.Reply.Thinking != "挨拶だな。" || done.Reply.Model != "fake-model" {
		t.Fatalf("done の返事が違います: %+v", done.Reply)
	}

	// 保存されている: ユーザー発言と、思考つきの返事
	_, ms, err := cs.Get(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 || ms[0].Role != "user" || ms[1].Thinking != "挨拶だな。" {
		t.Fatalf("保存された発言が違います: %+v", ms)
	}

	// 2回目の送信: 過去の思考はモデルに渡らない
	g.thinking, g.content = "", "元気です。"
	send(t, cs, c, "元気?")
	want := []Message{
		{"system", "あなたはテスト用です。"},
		{"user", "やあ"},
		{"assistant", "こんにちは!"},
		{"user", "元気?"},
	}
	if len(g.gotMessages) != len(want) {
		t.Fatalf("モデルに渡した発言の数が違います: %+v", g.gotMessages)
	}
	for i := range want {
		if g.gotMessages[i] != want[i] {
			t.Errorf("%d 件目: %+v(期待: %+v)", i, g.gotMessages[i], want[i])
		}
	}
	if p := buildPrompt(g.gotMessages, true); strings.Contains(p, "挨拶だな") {
		t.Errorf("モデルに渡した文字列に過去の思考が入っています:\n%s", p)
	}
}

func TestNoAnswerNoteIsSavedAndSentNextTime(t *testing.T) {
	g := &fakeGen{thinking: "えーと、えーと", stopReason: "length"}
	cs, _ := newTestConversations(t, g)
	c := mustCreate(t, cs)

	evs := send(t, cs, c, "難しい質問")
	r := evs[len(evs)-1].Reply
	if r == nil || !r.Stats.NoAnswer || r.Content != noAnswerNote("length") || r.Thinking != "えーと、えーと" {
		t.Fatalf("打ち切りの返事が違います: %+v", r)
	}

	g.thinking, g.content, g.stopReason = "", "はい", ""
	send(t, cs, c, "続けて")
	if got := g.gotMessages[2]; got.Role != "assistant" || got.Content != noAnswerNote("length") {
		t.Errorf("次の送信に理由の文が入っていません: %+v", g.gotMessages)
	}
}

func TestNothingGeneratedSavesNoReply(t *testing.T) {
	g := &fakeGen{stopReason: "cancel"}
	cs, _ := newTestConversations(t, g)
	c := mustCreate(t, cs)

	evs := send(t, cs, c, "やあ")
	if evs[len(evs)-1].Reply != nil {
		t.Errorf("何も生成していないのに返事が保存されました")
	}
	_, ms, _ := cs.Get(c.ID)
	if len(ms) != 1 {
		t.Errorf("ユーザー発言だけが残るはずです: %+v", ms)
	}
}

func TestSettingsArePerConversation(t *testing.T) {
	g := &fakeGen{content: "ok"}
	cs, st := newTestConversations(t, g)
	a := mustCreate(t, cs)
	b := mustCreate(t, cs)

	// a の設定だけを変える
	changed := testDefaults
	changed.System, changed.MaxTokens, changed.Temperature, changed.Think, changed.PresencePenalty = "", 42, 0, false, 0
	if err := cs.UpdateSettings(a.ID, changed); err != nil {
		t.Fatal(err)
	}

	send(t, cs, a, "x")
	if g.gotOpt != (GenerateOptions{Think: false, Temperature: 0, MaxTokens: 42, PresencePenalty: 0}) {
		t.Errorf("a の設定が効いていません: %+v", g.gotOpt)
	}
	if g.gotMessages[0].Role != "user" {
		t.Errorf("システムプロンプトが空なら付けないはずです: %+v", g.gotMessages)
	}

	send(t, cs, b, "x")
	if g.gotOpt.MaxTokens != 100 || !g.gotOpt.Think || g.gotMessages[0].Role != "system" {
		t.Errorf("b は既定の設定のままのはずです: %+v %+v", g.gotOpt, g.gotMessages)
	}

	// 既定を変えると、それ以降に作った会話だけに効き、保存もされる
	if err := cs.SetDefaults(changed); err != nil {
		t.Fatal(err)
	}
	if c := mustCreate(t, cs); c.Settings != changed {
		t.Errorf("新しい会話が既定をコピーしていません: %+v", c.Settings)
	}
	// 設定を渡して作ると、既定ではなくその設定で始まる(新しい会話の画面で変えた設定)
	given := testDefaults
	given.System = "画面で変えたシステムプロンプト"
	c, err := cs.Create(&given)
	if err != nil {
		t.Fatal(err)
	}
	if got, _, _ := cs.Get(c.ID); got.Settings != given {
		t.Errorf("渡した設定で作られていません: %+v", got.Settings)
	}
	if _, ms, _ := cs.Get(b.ID); len(ms) == 0 {
		t.Fatal("b の発言がありません")
	}
	if b2, _, _ := cs.Get(b.ID); b2.Settings != testDefaults {
		t.Errorf("既定を変えたら今ある会話まで変わりました: %+v", b2.Settings)
	}
	app, err := LoadAppSettings(st, AppSettings{Defaults: testDefaults})
	if err != nil || app.Defaults != changed {
		t.Errorf("既定が保存されていません: %+v %v", app.Defaults, err)
	}
}

func TestTitleFromFirstMessage(t *testing.T) {
	g := &fakeGen{content: "ok"}
	cs, _ := newTestConversations(t, g)
	c := mustCreate(t, cs)

	long := "Go で\nHTTP サーバーを書きたいのですが、標準ライブラリだけでどこまでできますか?"
	send(t, cs, c, long)
	send(t, cs, c, "2つ目の発言はタイトルにしない")

	got, _, _ := cs.Get(c.ID)
	want := string([]rune("Go で HTTP サーバーを書きたいのですが、標準ライブラリだけでどこまでできますか?")[:titleMaxRunes]) + "…"
	if got.Title != want {
		t.Errorf("タイトル: %q(期待: %q)", got.Title, want)
	}
	if titleFrom("短い") != "短い" {
		t.Errorf("短い発言はそのままのはずです: %q", titleFrom("短い"))
	}
}

func TestOnlyOneGenerationAtATime(t *testing.T) {
	g := &fakeGen{content: "ok", block: make(chan struct{})}
	cs, _ := newTestConversations(t, g)
	a := mustCreate(t, cs)
	b := mustCreate(t, cs)

	started := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		cs.Send(context.Background(), a.ID, "長い質問", func(ev ChatEvent) {
			if ev.Type == "prompt" {
				close(started)
			}
		})
		close(finished)
	}()
	<-started

	if g := cs.Generating(); g == nil || *g != a.ID {
		t.Errorf("生成中の会話が a になっていません: %v", g)
	}
	// 生成中でも別の会話は読める
	if _, _, err := cs.Get(b.ID); err != nil {
		t.Errorf("生成中に別の会話を読めません: %v", err)
	}
	// 送信と、生成中の会話の削除はできない
	var gotErr string
	cs.Send(context.Background(), b.ID, "割り込み", func(ev ChatEvent) {
		if ev.Type == "error" {
			gotErr = ev.Error
		}
	})
	if gotErr != ErrGenerating.Error() {
		t.Errorf("生成中の送信が断られていません: %q", gotErr)
	}
	if err := cs.Delete(a.ID); err == nil {
		t.Errorf("生成中の会話を削除できてしまいました")
	}

	close(g.block)
	<-finished
	if cs.Generating() != nil {
		t.Errorf("生成が終わったのに生成中のままです")
	}
	if err := cs.Delete(a.ID); err != nil {
		t.Errorf("生成が終わった会話を削除できません: %v", err)
	}
	if _, _, err := cs.Get(a.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("削除した会話が残っています: %v", err)
	}
}

func TestLoadAppSettingsPrefersStoredValues(t *testing.T) {
	st := newMemStore()
	initial := AppSettings{NCtx: 16384, GPULayers: 999, Defaults: testDefaults}

	got, err := LoadAppSettings(st, initial)
	if err != nil || got != initial {
		t.Fatalf("保存が空なら初期値のまま: %+v %v", got, err)
	}

	SaveAppSetting(st, SettingNCtx, 4096)
	SaveAppSetting(st, SettingLastModel, "ollama:qwen3.5:4b")
	got, err = LoadAppSettings(st, initial)
	if err != nil {
		t.Fatal(err)
	}
	if got.NCtx != 4096 || got.LastModel != "ollama:qwen3.5:4b" || got.GPULayers != 999 {
		t.Errorf("保存した値が優先されていません: %+v", got)
	}
}

func TestRename(t *testing.T) {
	g := &fakeGen{content: "ok"}
	cs, _ := newTestConversations(t, g)

	// 最初の送信の前に付けた名前は、自動のタイトルで上書きされない
	a := mustCreate(t, cs)
	if err := cs.Rename(a.ID, "  買い物メモ  "); err != nil {
		t.Fatal(err)
	}
	send(t, cs, a, "牛乳を買う")
	if got, _, _ := cs.Get(a.ID); got.Title != "買い物メモ" {
		t.Errorf("付けた名前が上書きされました: %q", got.Title)
	}

	// 自動で付いたタイトルも、あとから変えられる。名前を変えても更新日時(一覧の並び)は変わらない
	b := mustCreate(t, cs)
	send(t, cs, b, "自動のタイトル")
	before, _, _ := cs.Get(b.ID)
	if err := cs.Rename(b.ID, "変えた名前"); err != nil {
		t.Fatal(err)
	}
	after, _, _ := cs.Get(b.ID)
	if after.Title != "変えた名前" || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("名前の変更: %q / 更新日時 %v → %v", after.Title, before.UpdatedAt, after.UpdatedAt)
	}

	if err := cs.Rename(b.ID, "   "); err == nil {
		t.Errorf("空のタイトルにできてしまいました")
	}
}

func TestChangesDuringGenerationAreKept(t *testing.T) {
	g := &fakeGen{content: "ok", block: make(chan struct{})}
	cs, _ := newTestConversations(t, g)
	c := mustCreate(t, cs)

	started := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		cs.Send(context.Background(), c.ID, "質問", func(ev ChatEvent) {
			if ev.Type == "prompt" {
				close(started)
			}
		})
		close(finished)
	}()
	<-started

	// 生成中に画面で名前と設定を変える
	changed := testDefaults
	changed.MaxTokens = 7
	if err := cs.Rename(c.ID, "生成中に変えた名前"); err != nil {
		t.Fatal(err)
	}
	if err := cs.UpdateSettings(c.ID, changed); err != nil {
		t.Fatal(err)
	}
	close(g.block)
	<-finished

	// 返事を保存したときに、生成を始めたときの古い値で書き戻されていないこと
	got, ms, _ := cs.Get(c.ID)
	if got.Title != "生成中に変えた名前" || got.Settings != changed {
		t.Errorf("生成中の変更が消えました: %q %+v", got.Title, got.Settings)
	}
	if len(ms) != 2 {
		t.Errorf("発言の数: %d", len(ms))
	}
}
