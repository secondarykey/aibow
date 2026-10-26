package aibow

// rules.go: プロファイルの決まり(固定・使えない。0014 の 5〜8)。
//
// 決まりは、会話が覚えているプロファイル(Conversation.ProfileID)を、そのつど見に行く(値は写し、決まりは参照)。
// 管理者があとからツールを禁止したら、今ある会話にもすぐ効いてほしいため。
//
//   - 固定:項目ごとの印。固定した項目は、会話の値ではなくプロファイルの値を使う(あとから固定して、会話の値と違うときも)。
//     会話に保存してある値は書き換えず、使うとき(送信・画面に出すとき)だけプロファイルの値にする(withProfile)。
//     固定を外すと、会話の元の値に戻る
//   - 使えない:ツールのグループ(組み込み・MCP のサーバー)ごとと、ツールごと。会話の上書き(tool_policies の on)でも使えるようにはできない
//
// 扱いを決めるのはここ(と、ツールの扱いの policyOf)だけ。画面は Go が返した結果を出すだけにし、
// 保存のときにも Go が断る(画面で押せなくするだけでは、ほかの入口から変えられてしまうため。checkRules)。

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"uuid"
)

// 固定できる項目(ProfileRules.Fixed に入れる名前)。プロファイルの値(ProfileValues)の項目
const (
	FixModel           = "model"
	FixSystem          = "system"
	FixTemperature     = "temperature"
	FixThink           = "think"
	FixPresencePenalty = "presence_penalty"
	FixCompact         = "compact"
	FixHistoryTokens   = "history_tokens"
	FixReplyTokens     = "reply_tokens"
	FixToolGroups      = "tool_groups" // 使うツールのグループ(組み込みを使うか・使う MCP のサーバー)
)

// ProfileRules は、プロファイルの決まり(会話からそのつど見に行くもの)。
type ProfileRules struct {
	// Fixed は固定した項目(Fix… の名前)。そのプロファイルの会話では変えられず、プロファイルの値を使う
	Fixed []string `json:"fixed,omitempty"`
	// Off は使えないもの。PC の既定(tools.json)や会話の上書きより先に見る
	Off ProfileOff `json:"off,omitzero"`
}

// ProfileOff は、プロファイルで使えないツール。
type ProfileOff struct {
	Builtin bool     `json:"builtin,omitempty"` // 組み込みのツール(aibow の中で動くツール)全部
	Servers []string `json:"servers,omitempty"` // MCP のサーバー(settings.json の名前)のツール全部
	Tools   []string `json:"tools,omitempty"`   // ツール(モデルに見せる名前。会話の上書き tool_policies と同じ)
}

// fixItem は固定できる項目1つ。会話の設定のどこにあたるかを持つ。
type fixItem struct {
	name  string
	label string                                                    // エラーに出す名前
	same  func(a, b ConversationSettings) bool                      // 同じ値か
	copy  func(dst *ConversationSettings, src ConversationSettings) // src の値を dst に写す
}

// fixItems は固定できる項目の一覧(画面の並びと同じ)。
var fixItems = []fixItem{
	{FixModel, "モデル",
		func(a, b ConversationSettings) bool { return a.Model == b.Model },
		func(d *ConversationSettings, s ConversationSettings) { d.Model = s.Model }},
	{FixSystem, "システムプロンプト",
		func(a, b ConversationSettings) bool { return a.System == b.System },
		func(d *ConversationSettings, s ConversationSettings) { d.System = s.System }},
	{FixCompact, "会話が長くなったら(compact)",
		func(a, b ConversationSettings) bool { return a.Compact == b.Compact },
		func(d *ConversationSettings, s ConversationSettings) { d.Compact = s.Compact }},
	{FixTemperature, "temperature",
		func(a, b ConversationSettings) bool { return a.Temperature == b.Temperature },
		func(d *ConversationSettings, s ConversationSettings) { d.Temperature = s.Temperature }},
	{FixPresencePenalty, "presence penalty",
		func(a, b ConversationSettings) bool { return a.PresencePenalty == b.PresencePenalty },
		func(d *ConversationSettings, s ConversationSettings) { d.PresencePenalty = s.PresencePenalty }},
	{FixThink, "思考する",
		func(a, b ConversationSettings) bool { return a.Think == b.Think },
		func(d *ConversationSettings, s ConversationSettings) { d.Think = s.Think }},
	{FixHistoryTokens, "会話の上限",
		func(a, b ConversationSettings) bool { return a.HistoryTokens == b.HistoryTokens },
		func(d *ConversationSettings, s ConversationSettings) { d.HistoryTokens = s.HistoryTokens }},
	{FixReplyTokens, "返事の上限",
		func(a, b ConversationSettings) bool { return a.ReplyTokens == b.ReplyTokens },
		func(d *ConversationSettings, s ConversationSettings) { d.ReplyTokens = s.ReplyTokens }},
	{FixToolGroups, "使うツールのグループ",
		func(a, b ConversationSettings) bool {
			return a.BuiltinOff == b.BuiltinOff && sameSet(a.MCPServers, b.MCPServers)
		},
		func(d *ConversationSettings, s ConversationSettings) {
			d.BuiltinOff, d.MCPServers = s.BuiltinOff, slices.Clone(s.MCPServers)
		}},
}

// sameSet は、並びによらず同じ名前の集まりか。
func sameSet(a, b []string) bool {
	a, b = slices.Sorted(slices.Values(a)), slices.Sorted(slices.Values(b))
	return slices.Equal(a, b)
}

func (r ProfileRules) fixed(item string) bool { return slices.Contains(r.Fixed, item) }

// offGroup は、グループ(組み込みなら空、MCP ならサーバーの名前)ごと使えないか。
func (r ProfileRules) offGroup(group string) bool {
	if group == "" {
		return r.Off.Builtin
	}
	return slices.Contains(r.Off.Servers, group)
}

// offTool は、ツールが使えないか(グループごと使えないか、ツールを名指しで使えない)。
func (r ProfileRules) offTool(group, name string) bool {
	return r.offGroup(group) || slices.Contains(r.Off.Tools, name)
}

// withProfile は、会話の設定 s に、プロファイル p の決まりを当てる(使うとき・画面に出すときの設定)。
// 固定した項目はプロファイルの値にし、決まりを s に持たせる(ツールの扱い policyOf が見る)。s の元の値(保存してある値)は変えない。
func withProfile(s ConversationSettings, p *Profile) ConversationSettings {
	s.profile = p
	if p == nil {
		return s
	}
	pv := p.Values.settings()
	for _, it := range fixItems {
		if p.Rules.fixed(it.name) {
			it.copy(&s, pv)
		}
	}
	return s
}

// errFixed は、固定した項目を変えようとしたときのエラー。
func errFixed(p *Profile, label string) error {
	return fmt.Errorf("「%s」はプロファイル「%s」で固定されているので、この会話では変えられません", label, p.Name)
}

// errOff は、使えないツールを使えるようにしようとしたときのエラー。
func errOff(p *Profile, what string) error {
	return fmt.Errorf("%s はプロファイル「%s」で使えないので、この会話でも使えません", what, p.Name)
}

// checkRules は、会話の設定を before(今の設定。決まりを当てたもの)から after に変えてよいかを、プロファイル p の決まりで確かめる。
// 変えてはいけないものを変えていたら、理由のエラーを返す(画面で押せなくしていても、ここで断る)。
//
//   - 固定した項目は、プロファイルの値のままでなければならない
//   - 使えないツールの扱いの上書き(tool_policies)は、新しく足したり変えたりできない(消すのはよい)
//   - 使えないグループを、新しく使うことにはできない
//
// group は、ツールの名前からグループ(組み込みなら空、MCP ならサーバーの名前)を引く(分からなければ ok=false)。
func checkRules(p *Profile, before, after ConversationSettings, group func(name string) (string, bool)) error {
	if p == nil {
		return nil
	}
	pv := p.Values.settings()
	for _, it := range fixItems {
		if p.Rules.fixed(it.name) && !it.same(after, pv) {
			return errFixed(p, it.label)
		}
	}
	for name, pol := range after.ToolPolicies {
		if pol == before.ToolPolicies[name] {
			continue
		}
		g, ok := group(name)
		if slices.Contains(p.Rules.Off.Tools, name) || ok && p.Rules.offGroup(g) {
			return errOff(p, "ツール "+name)
		}
	}
	if !after.BuiltinOff && before.BuiltinOff && p.Rules.Off.Builtin {
		return errOff(p, "組み込みのツール")
	}
	for _, s := range after.MCPServers {
		if !slices.Contains(before.MCPServers, s) && p.Rules.offGroup(s) {
			return errOff(p, "MCP のサーバー "+s)
		}
	}
	return nil
}

// keepStored は、保存する設定 after の固定した項目を、会話に保存してある値 stored に戻す。
// 固定した項目は使うときだけプロファイルの値にするので、保存してある値は書き換えない(固定を外したら元の値に戻るように)。
func keepStored(after, stored ConversationSettings, p *Profile) ConversationSettings {
	if p == nil {
		return after
	}
	for _, it := range fixItems {
		if p.Rules.fixed(it.name) {
			it.copy(&after, stored)
		}
	}
	return after
}

// profileFor は、会話のプロファイルを保存先から読む(決まりはそのつど見に行く)。
// プロファイルを持たない会話(ライブラリとして使うとき)・見つからないプロファイルは nil(決まり無し)。
// プロファイルは消せない(使っている会話があるうちは)ので、見つからないのは DB を手で直したときくらい。
func (cs *Conversations) profileFor(ctx context.Context, id uuid.UUID) (*Profile, error) {
	if id == (uuid.UUID{}) {
		return nil, nil
	}
	p, err := cs.store.Profile(ctx, id)
	if errors.Is(err, ErrProfileNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("会話のプロファイルを読めません: %w", err)
	}
	return &p, nil
}

// ProfileOf は、会話 c のプロファイル(保存先から読み直したもの)。プロファイルを持たない会話なら nil。
func (cs *Conversations) ProfileOf(ctx context.Context, c Conversation) (*Profile, error) {
	return cs.profileFor(ctx, c.ProfileID)
}

// load は、会話1つと発言を保存先から読み、決まりを当てる(使うときの設定)。
func (cs *Conversations) load(ctx context.Context, id uuid.UUID) (Conversation, []StoredMessage, error) {
	c, ms, err := cs.store.Conversation(ctx, id)
	if err != nil {
		return c, ms, err
	}
	p, err := cs.profileFor(ctx, c.ProfileID)
	if err != nil {
		return c, ms, err
	}
	c.Settings = cs.forUse(withProfile(c.Settings.normalized(), p))
	return c, ms, nil
}

// forUse は、使うときの設定の仕上げ。作業フォルダを使わない形(IgnoreWorkDir)なら、作業フォルダを空にする。
func (cs *Conversations) forUse(s ConversationSettings) ConversationSettings {
	if cs.noWorkDir {
		s.WorkDir = ""
	}
	return s
}

// groupOfTool は、ツールの名前からグループ(組み込みなら空、MCP ならサーバーの名前)を引く。
// MCP のツールは、起動したときに見えた一覧(tools.json の known)か、起動済みのツールから探す。見つからなければ ok=false。
func (cs *Conversations) groupOfTool(name string) (string, bool) {
	if slices.ContainsFunc(BuiltinTools(), func(k KnownTool) bool { return k.Name == name }) {
		return "", true
	}
	if cs.remote == nil {
		return "", false
	}
	known := cs.prefs.View().Known
	for _, server := range cs.remote.Servers() {
		if slices.ContainsFunc(known[server], func(k KnownTool) bool { return k.Name == name }) {
			return server, true
		}
		for _, t := range cs.remote.Tools("", []string{server}) {
			if t.Def().Name == name {
				return server, true
			}
		}
	}
	return "", false
}
