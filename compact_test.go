package aibow

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// promptLen は、発言の列を実際にモデルに渡す文字列にしたときのトークン数(fakeGen の数え方)。
func promptLen(msgs []Message, think bool) int {
	return utf8.RuneCountInString(testFormat.BuildPrompt(Prompt{Messages: msgs, Think: think}))
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
	// 保存したユーザーの発言は user イベントで届く(画面は ID を表示に付けて、薄い表示に使う)
	if evs[0].Type != "user" || evs[0].Message == nil || evs[0].Message.ID != ms[0].ID {
		t.Errorf("user イベント: %+v", evs[0])
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
func (noModelGen) CountTokens(Prompt) (int, error) { return 0, ErrNoModel }
func (noModelGen) Generate(context.Context, Prompt, GenerateOptions, func(string), func(string), func(string)) (Output, error) {
	return Output{}, ErrNoModel
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
	fit := promptLen(promptMessages(c.Settings.System, "", append(ms[4:], next)), true)
	cs.SetLimits(fit+30, 100) // +30: 返事1件分(50文字以上)は入らない
	g.content = "ok"
	evs := send(t, cs, c, "質問4")

	want := append([]Message{{Role: "system", Content: c.Settings.System}}, promptMessages("", "", append(ms[4:], next))...)
	if !sameMessages(g.gotMessages, want) {
		t.Fatalf("モデルに渡した発言: %+v\n期待: %+v", g.gotMessages, want)
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
	only := promptLen(promptMessages(c.Settings.System, "", []StoredMessage{{Role: "user", Content: long}}), true)
	cs.SetLimits(only, 100)
	g.content = "ok"
	send(t, cs, c, long)
	if len(g.gotMessages) != 2 || g.gotMessages[1].Content != long {
		t.Errorf("最新の発言だけが渡るはずです: %+v", g.gotMessages)
	}

	// 最新の発言だけで上限を超えるなら、保存も生成もせずにエラー
	calls := len(g.calls)
	evs := sendRaw(cs, c, long+"!")
	if errs := errorsOf(evs); len(errs) != 1 || !strings.Contains(errs[0], "今の発言(とツールの結果)だけで会話の上限を超えています") {
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

// summaryReply は要約のテストの返事の上限。作業領域(会話の上限 + 返事の上限)に、
// 要約の指示と要約の上限(summaryMaxTokens)が入る大きさにする
const summaryReply = summaryMaxTokens + 2000

// overLimit は、今の会話がちょうど入り、新しい発言を足すと超える会話の上限にする。
func overLimit(t *testing.T, cs *Conversations, c Conversation) Usage {
	t.Helper()
	u, err := cs.Usage(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	cs.SetLimits(u.Tokens, summaryReply)
	return u
}

// systemOf は、モデルに渡した発言の system を返す。
func systemOf(msgs []Message) string {
	if len(msgs) > 0 && msgs[0].Role == "system" {
		return msgs[0].Content
	}
	return ""
}

func TestSummaryRunsBeforeTheReply(t *testing.T) {
	g := &fakeGen{}
	cs, _ := newTestConversations(t, g)
	c := longTalk(t, cs, g, CompactSummary, 6)
	before := overLimit(t, cs, c)

	g.replies = []fakeReply{{content: "- 要約1"}, {content: "返事"}}
	evs := send(t, cs, c, "次の質問")
	if len(g.calls) < 2 {
		t.Fatal("生成が足りません")
	}

	// 要約の生成: 思考オフ・temperature 低め・上限 1024。前回の要約は無く、古い発言が文章で渡る
	sum := g.calls[len(g.calls)-2]
	if sum.opt != (GenerateOptions{Temperature: summaryTemperature, MaxTokens: summaryMaxTokens}) || sum.think {
		t.Errorf("要約の生成の設定: %+v think=%v", sum.opt, sum.think)
	}
	if systemOf(sum.messages) != summaryInstruction || !strings.Contains(sum.messages[1].Content, "## 前回までの要約\n(なし)") ||
		!strings.Contains(sum.messages[1].Content, "ユーザー: 質問0\nアシスタント: 返返") {
		t.Errorf("要約に渡した内容: %+v", sum.messages)
	}
	if strings.Contains(sum.messages[1].Content, "次の質問") {
		t.Errorf("最新の発言は要約に入れないはずです")
	}

	// 保存された要約と、返事の生成に渡した内容
	list, _ := cs.Compactions(c.ID)
	if len(list) != 1 || list[0].Summary != "- 要約1" || list[0].Model != "fake-model" || list[0].Stats == nil {
		t.Fatalf("保存された要約: %+v", list)
	}
	_, ms, _ := cs.Get(c.ID)
	keep := 0
	for i, m := range ms {
		if m.ID == list[0].UptoMessageID {
			keep = i + 1
		}
	}
	if keep == 0 || ms[keep].Role != "user" {
		t.Fatalf("要約の区切りが組の切れ目ではありません: upto=%v", list[0].UptoMessageID)
	}
	reply := g.calls[len(g.calls)-1]
	wantSystem := c.Settings.System + "\n\n" + summaryHeading + "\n- 要約1"
	if systemOf(reply.messages) != wantSystem || len(reply.messages) != 1+len(ms)-keep-1 {
		t.Errorf("返事に渡した内容: %+v", reply.messages)
	}
	// 残した最近の発言は、会話の上限の半分以下
	if kept := promptLen(promptMessages(c.Settings.System, "", ms[keep:len(ms)-1]), true); kept > before.Tokens/2 {
		t.Errorf("残した発言が上限の半分を超えています: %d > %d", kept, before.Tokens/2)
	}

	// イベント: 要約中 → 要約の文 → compact(保存した要約つき)→ 返事。メーターは要約で下がる
	var types []string
	for _, ev := range evs {
		types = append(types, ev.Type)
	}
	if got := strings.Join(types, ","); !strings.HasPrefix(got, "user,summarizing,summary,compact,usage,prompt,content,reply,usage") {
		t.Errorf("イベントの順番: %s", got)
	}
	cp := compacts(evs)[0]
	if cp.Compaction == nil || cp.Error != "" || cp.AfterTokens >= cp.BeforeTokens || cp.Dropped != keep {
		t.Errorf("compact イベント: %+v", cp)
	}
	us := usages(evs)
	if us[0].Tokens > us[0].Limit || us[0].FirstID == nil || *us[0].FirstID != ms[keep].ID {
		t.Errorf("要約のあとのメーター: %+v", us[0])
	}
	// 開き直したときも、最新の要約 + 要約に入っていない発言で数える
	u, _ := cs.Usage(c.ID)
	if *u.FirstID != ms[keep].ID {
		t.Errorf("Usage の最初の発言: %v", u.FirstID)
	}
}

func TestSummaryStacks(t *testing.T) {
	g := &fakeGen{}
	cs, _ := newTestConversations(t, g)
	c := longTalk(t, cs, g, CompactSummary, 6)
	overLimit(t, cs, c)
	g.replies = []fakeReply{{content: "- 要約1"}, {content: strings.Repeat("返", 50)}}
	send(t, cs, c, "質問6")

	// もう一度、上限を超えるまで話す
	for i := 7; ; i++ {
		g.replies = []fakeReply{{content: "- 要約2"}, {content: strings.Repeat("返", 50)}}
		calls := len(g.calls)
		send(t, cs, c, fmt.Sprintf("質問%d", i))
		if len(g.calls)-calls == 2 {
			break // 要約 + 返事
		}
		g.replies = nil
		if i > 30 {
			t.Fatal("2回目の要約が起きません")
		}
	}

	sum := g.calls[len(g.calls)-2]
	if !strings.Contains(sum.messages[1].Content, "## 前回までの要約\n- 要約1\n") {
		t.Errorf("2回目の要約に前回の要約が入っていません: %s", sum.messages[1].Content)
	}
	if strings.Contains(sum.messages[1].Content, "ユーザー: 質問0\n") {
		t.Errorf("前回までに要約した発言を、また要約しています")
	}
	reply := g.calls[len(g.calls)-1]
	if s := systemOf(reply.messages); !strings.HasSuffix(s, "\n- 要約2") || strings.Contains(s, "要約1") {
		t.Errorf("返事に渡すのは最新の要約だけ: %q", s)
	}
	if list, _ := cs.Compactions(c.ID); len(list) != 2 || list[0].UptoMessageID.Compare(list[1].UptoMessageID) >= 0 {
		t.Errorf("要約は古いものも残り、新しいほど後ろまで要約している: %+v", list)
	}
}

func TestSummaryFailureFallsBackToTruncate(t *testing.T) {
	for _, tc := range []struct {
		name    string
		summary fakeReply
		stop    bool // 要約中に StopSummary を押す
		want    string
	}{
		{"エラー", fakeReply{err: errors.New("decode に失敗")}, false, "decode に失敗"},
		{"空", fakeReply{thinking: "考えただけ"}, false, "要約が空でした"},
		{"途中で切れた", fakeReply{content: "- 途中", stopReason: "length"}, false, "途中で切れました"},
		{"停止", fakeReply{content: "- 途中", waitCancel: true}, true, "要約を停止しました"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := &fakeGen{}
			cs, _ := newTestConversations(t, g)
			c := longTalk(t, cs, g, CompactSummary, 6)
			before := overLimit(t, cs, c)

			g.replies = []fakeReply{tc.summary, {content: "返事"}}
			var evs []ChatEvent
			cs.Send(context.Background(), c.ID, "次の質問", func(ev ChatEvent) {
				evs = append(evs, ev)
				if tc.stop && ev.Type == "summarizing" {
					cs.StopSummary() // 要約だけが止まり、返事は生成される
				}
			})

			if list, _ := cs.Compactions(c.ID); len(list) != 0 {
				t.Errorf("失敗した要約が保存されました: %+v", list)
			}
			cps := compacts(evs)
			if len(cps) != 2 || cps[0].Method != CompactSummary || !strings.Contains(cps[0].Error, tc.want) ||
				!strings.Contains(cps[0].Error, "truncate") || cps[1].Method != CompactTruncate {
				t.Fatalf("compact イベント: %+v", cps)
			}
			done := evs[len(evs)-1]
			if done.Reply == nil || done.Reply.Content != "返事" || len(errorsOf(evs)) != 0 {
				t.Fatalf("返事が生成されていません: %+v %v", done.Reply, errorsOf(evs))
			}
			reply := g.calls[len(g.calls)-1]
			if strings.Contains(systemOf(reply.messages), summaryHeading) || promptLen(reply.messages, true) > before.Tokens {
				t.Errorf("truncate で収めた内容ではありません: %+v", reply.messages)
			}
		})
	}
}

func TestCompactNow(t *testing.T) {
	g := &fakeGen{}
	cs, _ := newTestConversations(t, g)
	c := longTalk(t, cs, g, CompactSummary, 6)
	u := overLimit(t, cs, c)
	cs.SetLimits(u.Tokens+10, summaryReply) // 上限を超えていなくても(半分より長ければ)要約できる

	g.replies = []fakeReply{{content: "- 今すぐの要約"}}
	var evs []ChatEvent
	cs.Compact(context.Background(), c.ID, func(ev ChatEvent) { evs = append(evs, ev) })
	if errs := errorsOf(evs); len(errs) != 0 || evs[len(evs)-1].Type != "done" {
		t.Fatalf("エラー: %v", errs)
	}
	list, _ := cs.Compactions(c.ID)
	if len(list) != 1 || list[0].Summary != "- 今すぐの要約" {
		t.Fatalf("要約: %+v", list)
	}
	after := usages(evs)
	if len(after) != 1 || after[0].Tokens >= u.Tokens {
		t.Errorf("要約のあとのメーターが下がっていません: %+v → %+v", u, after)
	}
	if _, ms, _ := cs.Get(c.ID); len(ms) != 12 {
		t.Errorf("今すぐ要約で発言が増減しました: %d", len(ms))
	}

	// 最近の発言が上限の半分に収まっていれば、要約できる古い発言は無い
	short := longTalk(t, cs, g, CompactSummary, 1)
	evs = nil
	cs.Compact(context.Background(), short.ID, func(ev ChatEvent) { evs = append(evs, ev) })
	// 失敗ではないので、エラーではなくお知らせ
	if errs, notes := errorsOf(evs), noticesOf(evs); len(errs) != 0 || len(notes) != 1 || notes[0] != errNothingToSummarize.Error() {
		t.Errorf("要約できる発言が無いとき: エラー %v / お知らせ %v", errs, notes)
	}
}

func TestSummaryInSeveralPartsWhenTooLong(t *testing.T) {
	g := &fakeGen{}
	cs, _ := newTestConversations(t, g)
	c := longTalk(t, cs, g, CompactSummary, 10)
	u, _ := cs.Usage(c.ID)
	// 会話の上限をとても小さくし、要約したい部分が作業領域(上限 + 返事の上限 − 要約の上限)に一度に入らないようにする
	cs.SetLimits(u.Tokens/3, summaryMaxTokens+u.Tokens/6)

	g.replies = []fakeReply{{content: "- 1"}, {content: "- 2"}, {content: "- 3"}, {content: "- 4"}, {content: "- 5"}}
	var evs []ChatEvent
	cs.Compact(context.Background(), c.ID, func(ev ChatEvent) { evs = append(evs, ev) })
	if errs := errorsOf(evs); len(errs) != 0 {
		t.Fatalf("エラー: %v", errs)
	}
	list, _ := cs.Compactions(c.ID)
	if len(list) < 2 {
		t.Fatalf("何回かに分けて要約するはずです: %d 回", len(list))
	}
	// 2回目以降は、直前の要約を「前回までの要約」として受け取っている
	for i := 1; i < len(list); i++ {
		in := g.calls[len(g.calls)-len(list)+i].messages[1].Content
		if !strings.Contains(in, "## 前回までの要約\n"+list[i-1].Summary+"\n") {
			t.Errorf("%d 回目に前回の要約が入っていません: %s", i+1, in)
		}
	}
}
