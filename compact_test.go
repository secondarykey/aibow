package aibow

import (
	"context"
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
