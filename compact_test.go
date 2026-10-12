package aibow

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// promptLen は、発言の列を実際にモデルに渡す文字列にしたときのトークン数(fakeGen の数え方)。
func promptLen(msgs []Message, think bool) int {
	return utf8.RuneCountInString(buildPrompt(msgs, think))
}

// usages は送信のイベントから usage だけを取り出す。
func usages(evs []ChatEvent) []Usage {
	var out []Usage
	for _, ev := range evs {
		if ev.Type == "usage" {
			out = append(out, *ev.Usage)
		}
	}
	return out
}

func TestUsageCountsThePromptWithTemplate(t *testing.T) {
	g := &fakeGen{content: "こんにちは!"}
	cs, _ := newTestConversations(t, g)
	c := mustCreate(t, cs)

	evs := send(t, cs, c, "やあ")
	us := usages(evs)
	if len(us) != 2 {
		t.Fatalf("usage は生成の前と返事のあとの2回: %+v", us)
	}
	// 生成の前: 実際にモデルに渡した文字列のトークン数
	if want := promptLen(g.gotMessages, true); us[0].Tokens != want || us[0].Limit != testWorkspace.HistoryTokens {
		t.Errorf("生成の前: %+v(期待: %d)", us[0], want)
	}
	// 返事のあと: 返事も足した、次の送信で渡す分。会話を開いたときの Usage と同じ値
	got, err := cs.Usage(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if us[1].Tokens <= us[0].Tokens || us[1].Tokens != got.Tokens {
		t.Errorf("返事のあと: %+v / Usage: %+v", us[1], got)
	}
	_, ms, _ := cs.Get(c.ID)
	if got.FirstID == nil || *got.FirstID != ms[0].ID {
		t.Errorf("全部渡しているので、最初の発言から: %+v", got)
	}
	// 思考の ON/OFF でプロンプトの末尾が変わるので、トークン数も変わる
	set := c.Settings
	set.Think = false
	cs.UpdateSettings(c.ID, set)
	off, _ := cs.Usage(c.ID)
	if off.Tokens == got.Tokens {
		t.Errorf("思考オフでもトークン数が同じです: %d", off.Tokens)
	}
}

func TestUsageWithoutModel(t *testing.T) {
	cs, _ := newTestConversations(t, &fakeGen{})
	c := mustCreate(t, cs)
	cs.gen = noModelGen{}
	if _, err := cs.Usage(c.ID); err != ErrNoModel {
		t.Errorf("モデルが無ければ数えられない: %v", err)
	}
}

// noModelGen はモデルが読み込まれていない Generator。
type noModelGen struct{}

func (noModelGen) Info() *ModelInfo                { return nil }
func (noModelGen) CountTokens(string) (int, error) { return 0, ErrNoModel }
func (noModelGen) Generate(context.Context, []Message, GenerateOptions, func(string), func(string), func(string)) (GenerateStats, error) {
	return GenerateStats{}, ErrNoModel
}

// longTalk は、組(ユーザーの発言 + 返事)を n 個持つ会話を作る。返事は同じ長さにする。
func longTalk(t *testing.T, cs *Conversations, g *fakeGen, compact string, n int) Conversation {
	t.Helper()
	c := mustCreate(t, cs)
	set := c.Settings
	set.Compact = compact
	if err := cs.UpdateSettings(c.ID, set); err != nil {
		t.Fatal(err)
	}
	c.Settings = set
	g.thinking = ""
	for i := range n {
		g.content = strings.Repeat("返", 50) + fmt.Sprint(i)
		send(t, cs, c, fmt.Sprintf("質問%d", i))
	}
	return c
}

// compacts は送信のイベントから compact だけを取り出す。
func compacts(evs []ChatEvent) []CompactInfo {
	var out []CompactInfo
	for _, ev := range evs {
		if ev.Type == "compact" {
			out = append(out, *ev.Compact)
		}
	}
	return out
}

// errorsOf は送信のイベントからエラーの文だけを取り出す。
func errorsOf(evs []ChatEvent) []string {
	var out []string
	for _, ev := range evs {
		if ev.Type == "error" {
			out = append(out, ev.Error)
		}
	}
	return out
}

// sendRaw は Send を呼び、エラーがあっても止めずにイベントを返す。
func sendRaw(cs *Conversations, c Conversation, text string) []ChatEvent {
	var evs []ChatEvent
	cs.Send(context.Background(), c.ID, text, func(ev ChatEvent) { evs = append(evs, ev) })
	return evs
}

func TestTruncateDropsOldTurnsAsPairs(t *testing.T) {
	g := &fakeGen{}
	cs, _ := newTestConversations(t, g)
	c := longTalk(t, cs, g, CompactTruncate, 4) // 発言 8 件
	_, ms, _ := cs.Get(c.ID)

	// 会話の上限を「最近の2組 + 新しい発言」がちょうど入る大きさにする。
	// 組の途中(返事だけ)を残すと1組より少なく収まるが、組ごとに外すので、外れるのは古い2組まるごと
	next := StoredMessage{Role: "user", Content: "質問4"}
	fit := promptLen(promptMessages(c.Settings.System, append(ms[4:], next)), true)
	cs.SetLimits(fit+30, 100) // +30: 返事1件分(50文字以上)は入らない
	g.content = "ok"
	evs := send(t, cs, c, "質問4")

	want := append([]Message{{"system", c.Settings.System}}, promptMessages("", append(ms[4:], next))...)
	if len(g.gotMessages) != len(want) {
		t.Fatalf("モデルに渡した発言: %+v", g.gotMessages)
	}
	for i := range want {
		if g.gotMessages[i] != want[i] {
			t.Errorf("%d 件目: %+v(期待: %+v)", i, g.gotMessages[i], want[i])
		}
	}
	if g.gotMessages[1].Role != "user" {
		t.Errorf("モデルに渡す履歴が返事から始まっています: %+v", g.gotMessages[1])
	}
	cps := compacts(evs)
	if len(cps) != 1 || cps[0].Method != CompactTruncate || cps[0].Dropped != 4 || cps[0].AfterTokens > fit+30 || cps[0].BeforeTokens <= cps[0].AfterTokens {
		t.Errorf("compact イベント: %+v", cps)
	}
	// 生成の前のメーターは上限以下で、渡した最初の発言を指している
	us := usages(evs)
	if us[0].Tokens > us[0].Limit || us[0].FirstID == nil || *us[0].FirstID != ms[4].ID {
		t.Errorf("メーター: %+v", us[0])
	}
	// DB の発言は消えていない
	if _, after, _ := cs.Get(c.ID); len(after) != 10 {
		t.Errorf("DB の発言が減りました: %d 件", len(after))
	}
}

func TestTruncateKeepsTheLatestMessage(t *testing.T) {
	g := &fakeGen{}
	cs, _ := newTestConversations(t, g)
	c := longTalk(t, cs, g, CompactTruncate, 2)

	// 最新の発言だけを渡すのがちょうど収まる上限: 過去の組は全部外れる
	long := strings.Repeat("長", 200)
	only := promptLen(promptMessages(c.Settings.System, []StoredMessage{{Role: "user", Content: long}}), true)
	cs.SetLimits(only, 100)
	g.content = "ok"
	send(t, cs, c, long)
	if len(g.gotMessages) != 2 || g.gotMessages[1].Content != long {
		t.Errorf("最新の発言だけが渡るはずです: %+v", g.gotMessages)
	}

	// 最新の発言だけで上限を超えるなら、保存も生成もせずにエラー
	calls := len(g.calls)
	evs := sendRaw(cs, c, long+"!")
	if errs := errorsOf(evs); len(errs) != 1 || !strings.Contains(errs[0], "この発言だけで会話の上限を超えています") {
		t.Errorf("エラー: %v", errs)
	}
	if len(g.calls) != calls {
		t.Errorf("生成してしまいました")
	}
	if _, ms, _ := cs.Get(c.ID); len(ms) != 6 {
		t.Errorf("収まらない発言が保存されました: %d 件", len(ms))
	}
}

func TestNoneStopsBeforeGenerating(t *testing.T) {
	g := &fakeGen{}
	cs, _ := newTestConversations(t, g)
	c := longTalk(t, cs, g, CompactNone, 2)
	u, _ := cs.Usage(c.ID)
	cs.SetLimits(u.Tokens, 100) // 今の会話がちょうど入る。新しい発言の分は入らない

	calls := len(g.calls)
	evs := sendRaw(cs, c, "もう1つ")
	if errs := errorsOf(evs); len(errs) != 1 || !strings.Contains(errs[0], "会話の上限を超えました") || !strings.Contains(errs[0], "compact") {
		t.Errorf("エラー: %v", errs)
	}
	if len(g.calls) != calls || len(compacts(evs)) != 0 {
		t.Errorf("生成か compact をしてしまいました")
	}
	if _, ms, _ := cs.Get(c.ID); len(ms) != 4 {
		t.Errorf("発言が保存されました: %d 件", len(ms))
	}
	// メーターは上限を超えた値をそのまま出す(none は何も外さない)
	cs.SetLimits(u.Tokens-1, 100)
	if over, _ := cs.Usage(c.ID); over.Tokens != u.Tokens || *over.FirstID != *u.FirstID {
		t.Errorf("none のメーター: %+v", over)
	}
}
