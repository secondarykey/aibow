package aibow

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/secondarykey/aibow/tool"
)

// setRules は、新しい会話で最初に選ばれるプロファイル(テストでは「既定」)の値と決まりを変えて保存する。
// 管理者があとから決まりを変えた、の代わり。決まりは会話からそのつど見に行くので、今ある会話にもすぐ効く。
func setRules(t *testing.T, cs *Conversations, st *memStore, change func(*Profile)) {
	t.Helper()
	p := cs.DefaultProfile()
	change(&p)
	if err := st.UpdateProfile(p); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.PrepareProfiles(); err != nil { // 覚えている既定のプロファイルも読み直す
		t.Fatal(err)
	}
}

// 固定した項目は、会話の値ではなくプロファイルの値を使う(あとから固定しても)。会話に保存してある値は書き換えず、固定を外すと戻る(0014 の 7)
func TestFixedItemsUseProfileValues(t *testing.T) {
	g := &fakeGen{content: "ok"}
	cs, st := newTestConversations(t, g)
	c := mustCreate(t, cs)
	c.Settings.System, c.Settings.Temperature = "会話のシステムプロンプト", 0.2
	if err := cs.UpdateSettings(c.ID, c.Settings); err != nil {
		t.Fatal(err)
	}

	setRules(t, cs, st, func(p *Profile) {
		p.Values.System = "プロファイルのシステムプロンプト"
		p.Rules.Fixed = []string{FixSystem}
	})
	got, _, _ := cs.Get(c.ID)
	if got.Settings.System != "プロファイルのシステムプロンプト" || got.Settings.Temperature != 0.2 {
		t.Errorf("固定した項目だけプロファイルの値: %+v", got.Settings)
	}
	send(t, cs, c, "x")
	if !strings.Contains(g.gotMessages[0].Content, "プロファイルのシステムプロンプト") {
		t.Errorf("送信にもプロファイルの値を使う: %+v", g.gotMessages[0])
	}
	if st.convs[c.ID].Settings.System != "会話のシステムプロンプト" {
		t.Errorf("会話に保存してある値は書き換えない: %q", st.convs[c.ID].Settings.System)
	}

	// 固定していない項目は変えられる。固定した項目は保存してある値のまま残る
	got.Settings.Temperature = 0.9
	if err := cs.UpdateSettings(c.ID, got.Settings); err != nil {
		t.Fatalf("固定していない項目は変えられる: %v", err)
	}
	if s := st.convs[c.ID].Settings; s.Temperature != 0.9 || s.System != "会話のシステムプロンプト" {
		t.Errorf("固定した項目は保存してある値のまま: %+v", s)
	}
	// 固定した項目を変えようとしたら、保存せずに断る
	got.Settings.System = "書き換えたい"
	if err := cs.UpdateSettings(c.ID, got.Settings); err == nil || !strings.Contains(err.Error(), "固定") {
		t.Errorf("固定した項目は変えられない: %v", err)
	}
	if st.convs[c.ID].Settings.Temperature != 0.9 {
		t.Error("断ったときは何も保存しない")
	}

	// 固定を外すと、会話の元の値に戻る
	setRules(t, cs, st, func(p *Profile) { p.Rules.Fixed = nil })
	if got, _, _ := cs.Get(c.ID); got.Settings.System != "会話のシステムプロンプト" {
		t.Errorf("固定を外すと会話の値に戻る: %q", got.Settings.System)
	}
}

// モデル・会話の上限を固定すると、送信の前にプロファイルのモデルを読み込み、作業領域もプロファイルの大きさにする
func TestFixedModelAndLimits(t *testing.T) {
	g := &fakeGen{content: "ok"}
	cs, st := newTestConversations(t, g)
	l := &fakeLoader{g: g}
	cs.SetModelLoader(l)
	c := mustCreate(t, cs)
	setRules(t, cs, st, func(p *Profile) {
		p.Values.Model, p.Values.HistoryTokens = "ollama:qwen3.5:9b", 5000
		p.Rules.Fixed = []string{FixModel, FixHistoryTokens}
	})
	send(t, cs, c, "x")
	if !slices.Equal(l.loaded, []string{"ollama:qwen3.5:9b"}) || g.nCtx != 5000+testDefaults.ReplyTokens {
		t.Errorf("固定したモデルと上限で送信する: %v %d", l.loaded, g.nCtx)
	}
}

// 新しい会話の画面で、固定した項目を変えた設定では作れない
func TestCreateRefusesFixedChange(t *testing.T) {
	cs, st := newTestConversations(t, &fakeGen{})
	setRules(t, cs, st, func(p *Profile) { p.Rules.Fixed = []string{FixThink} })
	s := cs.Defaults()
	s.System = "変えてよい"
	if _, err := cs.Create(&s); err != nil {
		t.Fatalf("固定していない項目は変えて作れる: %v", err)
	}
	s.Think = !s.Think
	if _, err := cs.Create(&s); err == nil {
		t.Error("固定した項目を変えた設定では作れない")
	}
}

// 使えないツールはモデルに見せず、会話の上書き(on)でも使えるようにできない。あとから使えないにしても効く(0014 の 5)
func TestOffToolsAreHidden(t *testing.T) {
	g := &fakeGen{content: "ok"}
	cs, st, c := newToolConversation(t, g)
	c.Settings.ToolPolicies = map[string]ToolPolicy{"calculate": ToolOn}
	if err := cs.UpdateSettings(c.ID, c.Settings); err != nil {
		t.Fatal(err)
	}
	setRules(t, cs, st, func(p *Profile) { p.Rules.Off.Tools = []string{"calculate"} })

	send(t, cs, c, "x")
	if names := promptToolNames(g.calls[0]); slices.Contains(names, "calculate") || !slices.Contains(names, "get_current_time") {
		t.Errorf("使えないツールは会話で使うにしていてもモデルに見せない: %v", names)
	}
	got, _, _ := cs.Get(c.ID)
	states, _ := cs.ConversationToolStates(c.ID, got.Settings)
	if i := slices.IndexFunc(states[0].Tools, func(s ToolState) bool { return s.Name == "calculate" }); i < 0 ||
		states[0].Tools[i].Policy != ToolOff || states[0].Tools[i].From != PolicyFromProfile {
		t.Errorf("画面には「プロファイルで使えない」と返す: %+v", states[0].Tools)
	}

	// 使えないツールの上書きは、新しく書けない(消すのはよい)。確認のカードからも書けない
	got.Settings.ToolPolicies = map[string]ToolPolicy{"calculate": ToolAllow}
	if err := cs.UpdateSettings(c.ID, got.Settings); err == nil {
		t.Error("使えないツールの上書きは変えられない")
	}
	if err := cs.SetToolPolicy(c.ID, "calculate", ToolAllow); err == nil {
		t.Error("確認のカードからも書けない")
	}
	got.Settings.ToolPolicies = nil
	if err := cs.UpdateSettings(c.ID, got.Settings); err != nil {
		t.Errorf("上書きを消すのはよい: %v", err)
	}
	// ほかの項目を変えるだけなら、残っている上書きがあっても保存できる
	setRules(t, cs, st, func(p *Profile) { p.Rules.Off.Tools = []string{"get_current_time"} })
	got, _, _ = cs.Get(c.ID)
	got.Settings.ToolPolicies = map[string]ToolPolicy{"calculate": ToolOff}
	cs.UpdateSettings(c.ID, got.Settings)
	setRules(t, cs, st, func(p *Profile) { p.Rules.Off.Tools = []string{"calculate"} })
	got, _, _ = cs.Get(c.ID)
	got.Settings.System = "別の文"
	if err := cs.UpdateSettings(c.ID, got.Settings); err != nil {
		t.Errorf("上書きを変えていなければ保存できる: %v", err)
	}
}

// グループごと使えない:組み込みのツールを全部外し、MCP のサーバーは選んであっても起動しない。新しく選ぶことはできない
func TestOffGroups(t *testing.T) {
	g := &fakeGen{content: "ok"}
	cs, st, c := newToolConversation(t, g)
	r := &fakeRemote{tools: []tool.Tool{&remoteTool{name: "fake__write"}}}
	cs.SetRemoteTools(r)
	useFake(t, cs, &c)
	setRules(t, cs, st, func(p *Profile) {
		p.Rules.Off.Builtin = true
		p.Rules.Off.Servers = []string{"fake"}
	})
	sendAnswering(cs, context.Background(), c, "x", func(*ConfirmRequest) bool { return false })
	if names := promptToolNames(g.calls[0]); len(names) != 0 || r.started {
		t.Errorf("使えないグループは見せず、起動もしない: %v %v", names, r.started)
	}
	if d := cs.Defaults(); !d.BuiltinOff || len(d.MCPServers) != 0 {
		t.Errorf("新しい会話では使えないグループを選ばない: %+v", d)
	}

	// 新しく選ぶことはできない
	b := mustCreate(t, cs)
	b.Settings.MCPServers = []string{"fake"}
	if err := cs.UpdateSettings(b.ID, b.Settings); err == nil {
		t.Error("使えないサーバーは新しく選べない")
	}
	b.Settings.MCPServers, b.Settings.BuiltinOff = nil, false
	if err := cs.UpdateSettings(b.ID, b.Settings); err == nil {
		t.Error("使えない組み込みのツールは新しく使うにできない")
	}
}
