package aibow

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/secondarykey/aibow/engine"
	"github.com/secondarykey/aibow/format"
	"github.com/secondarykey/aibow/tool"
)

// fakeGen はテスト用の Generator。決めておいた思考と答えを返し、渡された内容を記録する。
// トークン数は1文字を1トークンとして数える(本物のトークン化はモデルが無いとできないため)。
type fakeGen struct {
	nCtx                          int // EnsureContextSize で決めた作業領域の大きさ
	resized                       int // 作業領域を作り直した回数
	thinking, content, stopReason string
	block                         chan struct{} // nil でなければ、閉じられるまで生成を終えない
	model                         string        // 読み込んでいるモデルの ID(空なら file:fake。fakeLoader が書き換える)

	// replies があれば、thinking / content / stopReason の代わりに、呼ばれた順に1つずつ返す
	// (要約 → 返事、のように1回の送信で何度も生成するときに使う)
	replies []fakeReply

	gotMessages []format.Message
	gotOpt      engine.GenerateOptions
	gotThink    bool
	calls       []fakeCall // 呼ばれたすべての生成
}

type fakeReply struct {
	thinking, content, stopReason string
	toolCalls                     []format.ToolCall // モデルが書いたツール呼び出し(書式から読み取った形)
	err                           error
	waitCancel                    bool // ctx がキャンセルされるまで待ち、停止された扱いで終わる
}

type fakeCall struct {
	messages []format.Message
	tools    []tool.Def
	opt      engine.GenerateOptions
	think    bool
	prompt   string // 書式を当てた文字列
}

// fakeGen の書式は本物と同じ Qwen3.5(トークン数を数えるとき、プロンプトを画面に渡すときに使う)
var testFormat = format.For("qwen35")

func (g *fakeGen) Info() *engine.ModelInfo {
	return &engine.ModelInfo{ID: cmp.Or(g.model, "file:fake"), Name: "fake-model", Arch: "qwen35", Format: testFormat.Name()}
}

// EnsureContextSize は、作業領域の大きさを覚えるだけ(大きさが変わったら作り直したことにする)。
// 最初の1回は、起動時に作った作業領域の代わり(本物も起動時に既定の大きさで作る)なので、作り直しに数えない
func (g *fakeGen) EnsureContextSize(n int) (bool, error) {
	if g.nCtx == 0 {
		g.nCtx = n
	}
	if g.nCtx == n {
		return false, nil
	}
	g.nCtx = n
	g.resized++
	return true, nil
}

func (g *fakeGen) CountTokens(p format.Prompt) (int, error) {
	return utf8.RuneCountInString(testFormat.BuildPrompt(p)), nil
}

func (g *fakeGen) Generate(ctx context.Context, p format.Prompt, opt engine.GenerateOptions,
	onPrompt func(string), onThinking, onContent func(string)) (format.Output, engine.GenerateStats, error) {
	g.gotMessages, g.gotOpt, g.gotThink = p.Messages, opt, p.Think
	g.calls = append(g.calls, fakeCall{p.Messages, p.Tools, opt, p.Think, testFormat.BuildPrompt(p)})
	onPrompt(testFormat.BuildPrompt(p))
	if g.block != nil {
		<-g.block
	}
	r := fakeReply{thinking: g.thinking, content: g.content, stopReason: g.stopReason}
	if len(g.replies) > 0 {
		r, g.replies = g.replies[0], g.replies[1:]
	}
	if r.err != nil {
		return format.Output{}, engine.GenerateStats{}, r.err
	}
	if r.waitCancel {
		<-ctx.Done()
		r.stopReason = "cancel"
	}
	if r.thinking != "" {
		onThinking(r.thinking)
	}
	if r.content != "" {
		onContent(r.content)
	}
	reason := r.stopReason
	if reason == "" {
		reason = "eos"
	}
	return format.Output{Thinking: r.thinking, Content: r.content, ToolCalls: r.toolCalls}, engine.GenerateStats{GenTokens: 10, StopReason: reason}, nil
}

// sameMessages は、モデルに渡した発言が want と同じかを返す。
func sameMessages(got, want []format.Message) bool { return reflect.DeepEqual(got, want) }

// testDefaults は新しい会話の既定(モデルは fakeGen が読み込んでいることにしているもの)。会話の上限と返事の上限は、fakeGen が1文字を1トークンと数えるので、文字数で考える
var testDefaults = ConversationSettings{Model: "file:fake", System: "あなたはテスト用です。", Temperature: 0.7, Think: true, PresencePenalty: 1.5, Compact: CompactSummary,
	HistoryTokens: 100000, ReplyTokens: 100}

// setLimits は会話 c の会話の上限と返事の上限を変えて保存する(c も書き換える)。
func setLimits(t *testing.T, cs *Conversations, c *Conversation, history, reply int) {
	t.Helper()
	c.Settings.HistoryTokens, c.Settings.ReplyTokens = history, reply
	if err := cs.UpdateSettings(c.ID, c.Settings); err != nil {
		t.Fatal(err)
	}
}

func newTestConversations(t *testing.T, g *fakeGen) (*Conversations, *memStore) {
	t.Helper()
	st := newMemStore()
	cs := NewConversations(st, g, AppSettings{Defaults: testDefaults})
	// アプリと同じく、プロファイル「既定」を作ってから使う(新しい会話の既定はプロファイルの値)
	if _, err := cs.PrepareProfiles(); err != nil {
		t.Fatal(err)
	}
	// ツールの定義でプロンプトのトークン数が変わらないよう、ツール無しで始める(会話の上限を小さくして確かめるテストのため)。
	// ツールを使うテストは newToolConversation で足す
	cs.tools = nil
	return cs, st
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
	want := []format.Message{
		{Role: "system", Content: "あなたはテスト用です。"},
		{Role: "user", Content: "やあ"},
		{Role: "assistant", Content: "こんにちは!", Thinking: "挨拶だな。"},
		{Role: "user", Content: "元気?"},
	}
	if !sameMessages(g.gotMessages, want) {
		t.Fatalf("モデルに渡した発言が違います:\n%+v\n期待: %+v", g.gotMessages, want)
	}
	if p := testFormat.BuildPrompt(format.Prompt{Messages: g.gotMessages, Think: true}); strings.Contains(p, "挨拶だな") {
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
	changed.System, changed.Temperature, changed.Think, changed.PresencePenalty, changed.Compact = "", 0, false, 0, CompactNone
	if err := cs.UpdateSettings(a.ID, changed); err != nil {
		t.Fatal(err)
	}

	send(t, cs, a, "x")
	// 返事の上限は会話ごとではなく、アプリ全体の設定
	if g.gotOpt != (engine.GenerateOptions{Temperature: 0, MaxTokens: 100, PresencePenalty: 0}) || g.gotThink {
		t.Errorf("a の設定が効いていません: %+v think=%v", g.gotOpt, g.gotThink)
	}
	if g.gotMessages[0].Role != "user" {
		t.Errorf("システムプロンプトが空なら付けないはずです: %+v", g.gotMessages)
	}

	send(t, cs, b, "x")
	if g.gotOpt.Temperature != 0.7 || !g.gotThink || g.gotMessages[0].Role != "system" {
		t.Errorf("b は既定の設定のままのはずです: %+v %+v", g.gotOpt, g.gotMessages)
	}

	// 既定を変えると、それ以降に作った会話だけに効き、保存もされる
	if err := cs.SetDefaults(changed); err != nil {
		t.Fatal(err)
	}
	if c := mustCreate(t, cs); !sameSettings(c.Settings, changed) {
		t.Errorf("新しい会話が既定をコピーしていません: %+v", c.Settings)
	}
	// 設定を渡して作ると、既定ではなくその設定で始まる(新しい会話の画面で変えた設定)
	given := testDefaults
	given.System = "画面で変えたシステムプロンプト"
	c, err := cs.Create(&given)
	if err != nil {
		t.Fatal(err)
	}
	if got, _, _ := cs.Get(c.ID); !sameSettings(got.Settings, given) {
		t.Errorf("渡した設定で作られていません: %+v", got.Settings)
	}
	if _, ms, _ := cs.Get(b.ID); len(ms) == 0 {
		t.Fatal("b の発言がありません")
	}
	if b2, _, _ := cs.Get(b.ID); !sameSettings(b2.Settings, testDefaults) {
		t.Errorf("既定を変えたら今ある会話まで変わりました: %+v", b2.Settings)
	}
	app, _, err := LoadAppSettings(st, AppSettings{Defaults: testDefaults})
	if err != nil || !sameSettings(app.Defaults, changed) {
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
	initial := AppSettings{Workspace: WorkspaceSettings{GPULayers: 999}, Defaults: testDefaults}
	initial.Defaults.HistoryTokens, initial.Defaults.ReplyTokens = 8192, 8192

	got, notes, err := LoadAppSettings(st, initial)
	if err != nil || got.Workspace.GPULayers != 999 || got.Workspace.Model != "" || len(got.Workspace.ModelGPULayers) != 0 ||
		!sameSettings(got.Defaults, initial.Defaults) || len(notes) != 0 {
		t.Fatalf("保存が空なら初期値のまま: %+v %v %v", got, notes, err)
	}

	SaveAppSetting(st, SettingHistoryTokens, 4096)
	SaveAppSetting(st, SettingLastModel, "ollama:qwen3.5:4b")
	SaveAppSetting(st, SettingPinQuestion, false)
	SaveAppSetting(st, SettingTheme, ThemeDark)
	initial.Display.PinQuestion = true
	got, _, err = LoadAppSettings(st, initial)
	if err != nil {
		t.Fatal(err)
	}
	w, d := got.Workspace, got.Defaults
	if got.Display.PinQuestion || got.Display.Theme != ThemeDark {
		t.Errorf("保存した画面の設定(質問を右上に出さない・ダーク)が初期値より優先されていません: %+v", got.Display)
	}
	// 知らない明るさはシステムに合わせる
	SaveAppSetting(st, SettingTheme, "sepia")
	if got, _, _ := LoadAppSettings(st, initial); got.Display.Theme != ThemeSystem {
		t.Errorf("知らない明るさ: %q", got.Display.Theme)
	}
	if (DisplaySettings{Theme: "sepia"}).Validate() == nil {
		t.Error("知らない明るさを受け付けました")
	}
	SaveAppSetting(st, SettingTheme, ThemeDark)
	if d.HistoryTokens != 4096 || d.ReplyTokens != 8192 || w.Model != "ollama:qwen3.5:4b" || w.GPULayers != 999 {
		t.Errorf("保存した値が優先されていません(古い置き場の会話の上限は、新しい会話の既定に入る): %+v", got)
	}
	if d.ContextSize() != 4096+8192 {
		t.Errorf("作業領域は会話の上限 + 返事の上限: %d", d.ContextSize())
	}

	// 新しい会話の既定に会話の上限が入っていれば、古い置き場より優先する
	d.HistoryTokens = 6000
	SaveAppSetting(st, SettingDefaults, d)
	got, _, _ = LoadAppSettings(st, initial)
	if got.Defaults.HistoryTokens != 6000 || got.Defaults.ReplyTokens != 8192 {
		t.Errorf("既定に入っている値を使う: %+v", got.Defaults)
	}
}

// 古い版の DB: コンテキスト長(n_ctx)と、会話ごとの max_tokens が入った既定の設定
func TestLoadAppSettingsMigratesNCtx(t *testing.T) {
	initial := AppSettings{Defaults: testDefaults}
	initial.Defaults.HistoryTokens, initial.Defaults.ReplyTokens = 8192, 8192
	for _, tc := range []struct {
		name                string
		nCtx, maxTokens     int // maxTokens が 0 なら既定の設定を保存しない
		wantHistory, wantRe int
	}{
		{"n_ctx − 返事の上限", 16384, 4096, 12288, 4096},
		{"最低 2048", 8192, 8192, 2048, 8192},
		{"既定の設定が無ければ初期値の返事の上限", 20000, 0, 20000 - 8192, 8192},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newMemStore()
			SaveAppSetting(st, SettingNCtx, tc.nCtx)
			if tc.maxTokens > 0 {
				st.SetAppSetting(SettingDefaults, fmt.Sprintf(`{"system":"古い","max_tokens":%d,"temperature":0.5,"think":true}`, tc.maxTokens))
			}
			got, notes, err := LoadAppSettings(st, initial)
			if err != nil {
				t.Fatal(err)
			}
			if got.Defaults.HistoryTokens != tc.wantHistory || got.Defaults.ReplyTokens != tc.wantRe || len(notes) != 1 {
				t.Errorf("引き継ぎ: %+v %v(期待: %d + %d)", got.Defaults, notes, tc.wantHistory, tc.wantRe)
			}
			// 古い既定の設定は compact が無いので summary になる
			if tc.maxTokens > 0 && (got.Defaults.System != "古い" || got.Defaults.Compact != CompactSummary) {
				t.Errorf("古い既定の設定: %+v", got.Defaults)
			}
			// 引き継いだ値は保存されていて、次の起動では引き継ぎ直さない(返事の上限だけ変えても会話の上限がずれない)
			SaveAppSetting(st, SettingReplyTokens, 1000)
			again, notes, _ := LoadAppSettings(st, initial)
			if again.Defaults.HistoryTokens != tc.wantHistory || again.Defaults.ReplyTokens != 1000 || len(notes) != 0 {
				t.Errorf("2回目の起動: %+v %v", again.Defaults, notes)
			}
		})
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
	changed.Temperature = 0.1
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
	if got.Title != "生成中に変えた名前" || !sameSettings(got.Settings, changed) {
		t.Errorf("生成中の変更が消えました: %q %+v", got.Title, got.Settings)
	}
	if len(ms) != 2 {
		t.Errorf("発言の数: %d", len(ms))
	}
}

// sameSettings は会話の設定を比べる(使う MCP のサーバーの「空」と「無し」は同じとみなす。保存すると空の配列になるため)。
// 会話のプロファイル(保存しない欄)は比べない。
func sameSettings(a, b ConversationSettings) bool {
	a.profile, b.profile = nil, nil
	return reflect.DeepEqual(a.normalized(), b.normalized())
}

// isResizeNotice は、作業領域を作り直したお知らせか。
func isResizeNotice(text string) bool {
	return strings.HasPrefix(text, "この会話の大きさに合わせて、作業領域を")
}

// 会話の上限・返事の上限は会話ごと。送信の前に、作業領域をその会話の大きさにする(大きさが同じなら作り直さない)。
// 返事の上限は、その会話の値で生成を止める
func TestWorkspaceFollowsConversation(t *testing.T) {
	g := &fakeGen{content: "ok"}
	cs, _ := newTestConversations(t, g)
	a := mustCreate(t, cs)
	b := mustCreate(t, cs)
	setLimits(t, cs, &b, 50000, 300)

	send(t, cs, a, "やあ")
	if g.nCtx != 100100 || g.resized != 0 || g.gotOpt.MaxTokens != 100 {
		t.Fatalf("会話A: 作業領域 %d・作り直し %d 回・返事の上限 %d", g.nCtx, g.resized, g.gotOpt.MaxTokens)
	}
	evs := send(t, cs, b, "やあ")
	if g.nCtx != 50300 || g.resized != 1 || g.gotOpt.MaxTokens != 300 {
		t.Errorf("会話B: 作業領域 %d・作り直し %d 回・返事の上限 %d", g.nCtx, g.resized, g.gotOpt.MaxTokens)
	}
	if notes := noticesOf(evs); len(notes) != 1 || !isResizeNotice(notes[0]) {
		t.Errorf("作り直したらお知らせする: %v", notes)
	}
	if evs := send(t, cs, b, "もう1つ"); len(noticesOf(evs)) != 0 || g.resized != 1 {
		t.Errorf("同じ大きさなら作り直さない: %v(%d 回)", noticesOf(evs), g.resized)
	}
	if u, _ := cs.Usage(b.ID); u.Limit != 50000 {
		t.Errorf("メーターの上限は会話の値: %d", u.Limit)
	}
}

// 既定の会話の上限・返事の上限は、会話を作るときに写す。既定を変えても今ある会話は変わらない
func TestDefaultLimitsAreCopied(t *testing.T) {
	g := &fakeGen{content: "ok"}
	cs, _ := newTestConversations(t, g)
	a := mustCreate(t, cs)
	if a.Settings.HistoryTokens != testDefaults.HistoryTokens || a.Settings.ReplyTokens != testDefaults.ReplyTokens {
		t.Fatalf("作るときに写す: %+v", a.Settings)
	}
	d := cs.Defaults()
	d.HistoryTokens, d.ReplyTokens = 3000, 200
	if err := cs.SetDefaults(d); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := cs.Get(a.ID); got.Settings.HistoryTokens != testDefaults.HistoryTokens {
		t.Errorf("既定を変えても今ある会話は変わらない: %+v", got.Settings)
	}
	// 画面が上限を渡さなかったとき(0)も、既定の値を写す
	b, err := cs.Create(&ConversationSettings{System: "x"})
	if err != nil || b.Settings.HistoryTokens != 3000 || b.Settings.ReplyTokens != 200 {
		t.Errorf("新しい会話は今の既定: %+v %v", b.Settings, err)
	}
}

// この機能より前に作った会話(上限を持っていない)には、起動したときに今の既定の値を写す
func TestFillMissingLimits(t *testing.T) {
	g := &fakeGen{content: "ok"}
	cs, st := newTestConversations(t, g)
	old := mustCreate(t, cs)
	old.Settings.HistoryTokens, old.Settings.ReplyTokens = 0, 0
	if err := st.UpdateConversation(old); err != nil {
		t.Fatal(err)
	}
	set := mustCreate(t, cs)
	setLimits(t, cs, &set, 5000, 500)

	n, err := cs.FillMissingLimits()
	if err != nil || n != 1 {
		t.Fatalf("写した会話の数: %d %v", n, err)
	}
	if got, _, _ := cs.Get(old.ID); got.Settings.HistoryTokens != testDefaults.HistoryTokens || got.Settings.ReplyTokens != testDefaults.ReplyTokens {
		t.Errorf("古い会話に既定を写す: %+v", got.Settings)
	}
	if got, _, _ := cs.Get(set.ID); got.Settings.HistoryTokens != 5000 {
		t.Errorf("上限を持っている会話は変えない: %+v", got.Settings)
	}
	if n, _ := cs.FillMissingLimits(); n != 0 {
		t.Errorf("2回目は何もしない: %d", n)
	}
}

// 選んだ吹き出しをモデルに渡さない(0011)。「質問 → 壊れた図 → 直して → 直った図」から真ん中の2つを外すと、
// 次の送信では「質問 → 直った図 → 次の質問」を渡す
func TestExcludeMessages(t *testing.T) {
	g := &fakeGen{replies: []fakeReply{{content: "壊れた図"}, {content: "直った図"}, {content: "次の答え"}}}
	cs, _ := newTestConversations(t, g)
	c := mustCreate(t, cs)
	send(t, cs, c, "図を描いて")
	send(t, cs, c, "直して")
	_, ms, _ := cs.Get(c.ID)
	broken, fix := ms[1], ms[2]
	if err := cs.SetExcluded(c.ID, broken.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := cs.SetExcluded(c.ID, fix.ID, true); err != nil {
		t.Fatal(err)
	}

	send(t, cs, c, "次の質問")
	var got []string
	for _, m := range g.gotMessages[1:] { // 先頭は system
		got = append(got, m.Content)
	}
	if want := []string{"図を描いて", "直った図", "次の質問"}; !slices.Equal(got, want) {
		t.Errorf("モデルに渡した発言: %v(期待: %v)", got, want)
	}
	if _, after, _ := cs.Get(c.ID); len(after) != 6 || !after[1].Excluded || !after[2].Excluded || after[3].Excluded {
		t.Errorf("画面には全部残し、外した発言に印を付ける: %+v", after)
	}

	// 戻せば、また渡す
	cs.SetExcluded(c.ID, fix.ID, false)
	cs.SetExcluded(c.ID, broken.ID, false)
	if u, _ := cs.Usage(c.ID); u.FirstID == nil || *u.FirstID != ms[0].ID {
		t.Errorf("戻したら最初から渡す: %+v", u)
	}

	// 生成中の会話は変えられない
	cs.generating.Store(&c.ID)
	if err := cs.SetExcluded(c.ID, broken.ID, true); err == nil {
		t.Error("生成中は変えられない")
	}
	cs.generating.Store(nil)
}

// 返事は、ツールの呼び出しと結果を一緒に外す(吹き出し1つ分)。ユーザーの発言は外さない
func TestExcludeReplyWithTools(t *testing.T) {
	g := &fakeGen{replies: []fakeReply{
		{toolCalls: []format.ToolCall{toolCall("calculate", "expression", "1+1")}},
		{content: "2 です"},
	}}
	cs, _, c := newToolConversation(t, g)
	send(t, cs, c, "1+1 は?")
	_, ms, _ := cs.Get(c.ID)
	if len(ms) != 4 || ms[2].Role != "tool" {
		t.Fatalf("発言の並び: %+v", ms)
	}
	if err := cs.SetExcluded(c.ID, ms[2].ID, true); err != nil { // 結果の発言を指しても、返事全体を外す
		t.Fatal(err)
	}
	_, ms, _ = cs.Get(c.ID)
	if ms[0].Excluded || !ms[1].Excluded || !ms[2].Excluded || !ms[3].Excluded {
		t.Errorf("返事の発言を全部外す: %v %v %v %v", ms[0].Excluded, ms[1].Excluded, ms[2].Excluded, ms[3].Excluded)
	}
	if got := forModel(ms); len(got) != 1 || got[0].Role != "user" {
		t.Errorf("モデルに渡す履歴: %+v", got)
	}
}

// collect は、Regenerate / Resend の途中経過を集める
func collect(f func(emit func(ChatEvent))) []ChatEvent {
	var evs []ChatEvent
	f(func(ev ChatEvent) { evs = append(evs, ev) })
	return evs
}

func contents(ms []StoredMessage) []string {
	var out []string
	for _, m := range ms {
		out = append(out, m.Content)
	}
	return out
}

// 最後の返事を作り直す: 最後の発言の後ろを消して、もう一度返事を作る(0011)。ツールの呼び出しと結果も消える
func TestRegenerate(t *testing.T) {
	g := &fakeGen{replies: []fakeReply{
		{content: "A1"},
		{toolCalls: []format.ToolCall{toolCall("calculate", "expression", "1+1")}},
		{content: "A2"},
		{content: "A2 作り直し"},
	}}
	cs, _, c := newToolConversation(t, g)
	send(t, cs, c, "Q1")
	send(t, cs, c, "Q2")
	if _, ms, _ := cs.Get(c.ID); len(ms) != 6 {
		t.Fatalf("発言の数: %d", len(ms))
	}

	evs := collect(func(emit func(ChatEvent)) { cs.Regenerate(context.Background(), c.ID, emit) })
	if errs := errorsOf(evs); len(errs) != 0 {
		t.Fatalf("エラー: %v", errs)
	}
	_, ms, _ := cs.Get(c.ID)
	if got, want := contents(ms), []string{"Q1", "A1", "Q2", "A2 作り直し"}; !slices.Equal(got, want) {
		t.Errorf("保存された発言: %v(期待: %v)", got, want)
	}
	var passed []string
	for _, m := range g.gotMessages[1:] {
		passed = append(passed, m.Content)
	}
	if want := []string{"Q1", "A1", "Q2"}; !slices.Equal(passed, want) {
		t.Errorf("モデルに渡した発言: %v(期待: %v)", passed, want)
	}
	for _, ev := range evs {
		if ev.Type == "user" {
			t.Error("作り直しでは発言を保存し直さない")
		}
	}
}

// 最後の発言を直して送り直す: 最後の発言とその返事を消して、直した文で送る(0011)
func TestResend(t *testing.T) {
	g := &fakeGen{replies: []fakeReply{{content: "A1"}, {content: "A2"}, {content: "A2 直した"}}}
	cs, _ := newTestConversations(t, g)
	c := mustCreate(t, cs)
	send(t, cs, c, "Q1")
	send(t, cs, c, "Q2")

	evs := collect(func(emit func(ChatEvent)) { cs.Resend(context.Background(), c.ID, "Q2 直した", emit) })
	if errs := errorsOf(evs); len(errs) != 0 {
		t.Fatalf("エラー: %v", errs)
	}
	_, ms, _ := cs.Get(c.ID)
	if got, want := contents(ms), []string{"Q1", "A1", "Q2 直した", "A2 直した"}; !slices.Equal(got, want) {
		t.Errorf("保存された発言: %v(期待: %v)", got, want)
	}

	// 収まらないときは、消さずに止める(前のやり取りだけが消えないように)
	setLimits(t, cs, &c, 10, 100)
	evs = collect(func(emit func(ChatEvent)) { cs.Resend(context.Background(), c.ID, strings.Repeat("長", 50), emit) })
	if errs := errorsOf(evs); len(errs) != 1 {
		t.Fatalf("収まらなければエラー: %v", errs)
	}
	if _, after, _ := cs.Get(c.ID); !slices.Equal(contents(after), contents(ms)) {
		t.Errorf("送れなかったときは消さない: %v", contents(after))
	}
}

// 作り直せないとき: 発言が無い・最後の発言をモデルに渡さないにしている
func TestRegenerateErrors(t *testing.T) {
	g := &fakeGen{content: "A"}
	cs, _ := newTestConversations(t, g)
	c := mustCreate(t, cs)
	if errs := errorsOf(collect(func(emit func(ChatEvent)) { cs.Regenerate(context.Background(), c.ID, emit) })); len(errs) != 1 {
		t.Errorf("発言が無ければ作り直せない: %v", errs)
	}
	send(t, cs, c, "Q")
	_, ms, _ := cs.Get(c.ID)
	cs.SetExcluded(c.ID, ms[0].ID, true)
	if errs := errorsOf(collect(func(emit func(ChatEvent)) { cs.Regenerate(context.Background(), c.ID, emit) })); len(errs) != 1 {
		t.Errorf("最後の発言を渡さないにしていたら作り直せない: %v", errs)
	}
	if _, after, _ := cs.Get(c.ID); len(after) != 2 {
		t.Errorf("作り直せなかったときは消さない: %d", len(after))
	}
}
