package aibow

// profile.go: 設定のプロファイル(0014)。
//
// プロファイルは「新しい会話の既定」に名前を付けたもの。会話を作るときに、選んだプロファイルの値を写す
// (写しなので、プロファイルをあとで変えても今ある会話は変わらない。0009 の約束のまま)。
// 会話は、どのプロファイルから作ったかを覚える(Conversation.ProfileID)。ステップ 7 の 3 で、プロファイルの「決まり」
// (変えられない項目・使えないツール)を、会話からそのつど見に行くのに使う。
//
// 学べること:設定を「値」(写す)と「決まり」(そのつど見に行く)に分けると、あとから変えたときの効き方が変わる。
// 値はプロファイルを変えても今ある会話に効かず、決まりは今ある会話にもすぐ効く。
//
// プロファイルには、この PC に依存しないものだけを入れる(ステップ 11 で、ほかの人にそのまま配れるようにするため)。
// 作業フォルダ(PC のパス)・GPU の層数・MCP のサーバーの起動のしかた(settings.json)・ツールごとの確認の扱い(tools.json)は入れない。

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"uuid"
)

// DefaultProfileName は、今までの「新しい会話の既定」から引き継いで作るプロファイルの名前。
const DefaultProfileName = "既定"

// ErrProfileNotFound は、指定したプロファイルが保存先に無いときのエラー。
var ErrProfileNotFound = errors.New("プロファイルが見つかりません(削除された可能性があります)")

// ErrProfileInUse は、使っている会話があるプロファイルを消そうとしたときのエラー。
// 会話はプロファイルの決まりを引くので、使っている会話があるうちは消さず「しまう」(Archived)にする(0014 の 11)。
var ErrProfileInUse = errors.New("このプロファイルから作った会話があるので消せません。しまうことはできます")

// Profile は、名前の付いた「新しい会話の既定」1つ。
type Profile struct {
	ID     uuid.UUID     `json:"id"` // UUIDv7
	Name   string        `json:"name"`
	Values ProfileValues `json:"values"` // 値:会話を作るときに写すもの
	Rules  ProfileRules  `json:"rules"`  // 決まり:会話からそのつど見に行くもの(rules.go)
	// Archived は「しまった」プロファイル。新しい会話では選べないが、決まりは残る(会話から引ける)。
	// 消すと、使っていた会話の決まりが黙って外れてしまうので、使っている会話があるうちは消さずにしまう(0014 の 11)
	Archived  bool      `json:"archived"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ProfileValues は、プロファイルの値(会話を作るときに写すもの)。
// 会話の設定(ConversationSettings)のうち、この PC に依存しないものと、モデル。
type ProfileValues struct {
	// Model はモデルの ID(models の目録の ID)。会話を作るときに、会話のモデルとして写す。
	// プロファイルの値を保存したとき(「この設定を新しい会話の既定にする」など)だけ変わる。入力欄の下でモデルを選んでも変わらない
	// (選ぶのは「この会話のモデル」。0014 の 3)
	Model           string  `json:"model"`
	System          string  `json:"system"`
	Temperature     float64 `json:"temperature"`
	Think           bool    `json:"think"`
	PresencePenalty float64 `json:"presence_penalty"`
	Compact         string  `json:"compact"`
	HistoryTokens   int     `json:"history_tokens"`
	ReplyTokens     int     `json:"reply_tokens"`
	// 使うツールのグループ。新しい会話で最初から使う組み込みのツールと MCP のサーバー(settings.json の名前)。
	// この PC の settings.json に無いサーバーが書かれていても無視する(Conversations.Defaults)。
	// settings.json に足したばかりのサーバーはどのプロファイルにも書かれていないので、新しい会話では使わない(0009 の 7 と同じ結果)
	BuiltinOff bool     `json:"builtin_off,omitempty"`
	MCPServers []string `json:"mcp_servers"`
	// ToolsOff は、新しい会話で最初は使わないツール(モデルに見せる名前)。会話で選べば使える(使えなくするのは決まりの Off.Tools)。
	// 会話を作るときに、会話の上書き(tool_policies の off)として写す。前は この PC の既定(tools.json の tools の off)だった(0014 の 4 の見直し)
	ToolsOff []string `json:"tools_off,omitempty"`
}

// valuesOf は、会話の設定から、プロファイルの値を作る。作業フォルダ・会話だけのツールの扱いは入れない。
func valuesOf(s ConversationSettings) ProfileValues {
	s = s.normalized()
	return ProfileValues{
		Model:  s.Model,
		System: s.System, Temperature: s.Temperature, Think: s.Think, PresencePenalty: s.PresencePenalty, Compact: s.Compact,
		HistoryTokens: s.HistoryTokens, ReplyTokens: s.ReplyTokens,
		BuiltinOff: s.BuiltinOff, MCPServers: slices.Clone(s.MCPServers),
		ToolsOff: toolsOff(s.ToolPolicies),
	}
}

// toolsOff は、会話の上書きのうち「使わない」(off)にしたツールの名前(名前の順)。
func toolsOff(policies map[string]ToolPolicy) []string {
	var out []string
	for name, p := range policies {
		if p == ToolOff {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

// settings は、プロファイルの値を会話の設定の形にする(会話を作るときの写しの元)。
func (v ProfileValues) settings() ConversationSettings {
	return ConversationSettings{
		Model:  v.Model,
		System: v.System, Temperature: v.Temperature, Think: v.Think, PresencePenalty: v.PresencePenalty, Compact: v.Compact,
		HistoryTokens: v.HistoryTokens, ReplyTokens: v.ReplyTokens,
		BuiltinOff: v.BuiltinOff, MCPServers: slices.Clone(v.MCPServers),
		ToolPolicies: offPolicies(v.ToolsOff),
	}.normalized()
}

// offPolicies は、最初は使わないツールを、会話の上書き(off)の形にする。無ければ nil。
func offPolicies(names []string) map[string]ToolPolicy {
	if len(names) == 0 {
		return nil
	}
	m := make(map[string]ToolPolicy, len(names))
	for _, n := range names {
		m[n] = ToolOff
	}
	return m
}

// PrepareProfiles は、プロファイルを使える状態にする。起動したときに1回、SetRemoteTools・SetToolPrefs のあとに呼ぶ。
// お知らせ(ログに出す)を返す。
//
//   - プロファイルが1つも無ければ(この機能より前の DB か、初めての起動)、今の「新しい会話の既定」から「既定」を作る(引き継ぎ。0014 の 10)。
//     値は保存してあった既定(無ければ環境変数の初期値)・最後に使ったモデル・設定の画面の「ツール」で最初から使うにしていたグループ。決まりは無し
//   - 「新しい会話で最初に選ばれる」プロファイルを読む。新しい会話はここから値を写す
//   - どのプロファイルにも紐づいていない会話(この機能より前に作った会話)を、そのプロファイルに紐づける。
//     中身は実際に既定の写しなので、紐づけても動きは変わらない
func (cs *Conversations) PrepareProfiles() ([]string, error) {
	var notes []string
	list, err := cs.store.ListProfiles()
	if err != nil {
		return nil, fmt.Errorf("プロファイルを読めません: %w", err)
	}
	if len(list) == 0 {
		p, err := cs.migrateDefaults()
		if err != nil {
			return nil, err
		}
		list = []Profile{p}
		notes = append(notes, fmt.Sprintf("新しい会話の既定を、プロファイル「%s」に引き継ぎました", p.Name))
	}

	p, note, err := markedProfile(cs.store, list)
	if err != nil {
		return nil, err
	}
	if note != "" {
		notes = append(notes, note)
	}
	cs.mu.Lock()
	cs.profile = p
	cs.mu.Unlock()

	n, err := cs.linkConversations(p)
	if err != nil {
		return notes, err
	}
	if n > 0 {
		notes = append(notes, fmt.Sprintf("プロファイルを持たない %d 個の会話を、プロファイル「%s」から作ったものにしました", n, p.Name))
	}
	moved, err := cs.moveToolsOff()
	if moved != "" {
		notes = append(notes, moved)
	}
	return notes, err
}

// moveToolsOff は、この PC の既定(tools.json)の「既定で使わない」ツールを、プロファイルの値(最初は使わないツール)に移す(起動したときに1回)。
// ツールの一覧をプロファイルの中の1つにまとめたため(2026-10-20、ユーザーと決めた)。動きは変えない:
//   - PC の既定は全部の会話に効いていたので、全部のプロファイルの「最初は使わない」に足す
//   - 今ある会話は、上書きが無ければ「使わない」(off)の上書きを足す(PC の既定から外したあとも、今までどおり使わない)
//   - 移したら tools.json から消す(確認する・確認しないは PC の既定のまま残る)
func (cs *Conversations) moveToolsOff() (string, error) {
	names := cs.prefs.offTools()
	if len(names) == 0 {
		return "", nil
	}
	list, err := cs.store.ListProfiles()
	if err != nil {
		return "", err
	}
	for _, p := range list {
		p.Values.ToolsOff = slices.Compact(slices.Sorted(slices.Values(append(slices.Clone(p.Values.ToolsOff), names...))))
		if err := cs.saveProfile(p); err != nil {
			return "", err
		}
	}
	convs, err := cs.store.ListConversations()
	if err != nil {
		return "", err
	}
	for _, c := range convs {
		changed := false
		for _, n := range names {
			if _, ok := c.Settings.ToolPolicies[n]; ok {
				continue
			}
			if c.Settings.ToolPolicies == nil {
				c.Settings.ToolPolicies = map[string]ToolPolicy{}
			}
			c.Settings.ToolPolicies[n] = ToolOff
			changed = true
		}
		if !changed {
			continue
		}
		if err := cs.store.UpdateConversation(c); err != nil {
			return "", fmt.Errorf("会話 %s に「使わない」のツールを写せません: %w", c.ID, err)
		}
	}
	if err := cs.prefs.clearOff(names); err != nil {
		return "", err
	}
	return fmt.Sprintf("この PC の既定で使わないにしていたツール %d 個(%s)を、プロファイルの「最初は使わない」に移しました",
		len(names), strings.Join(names, ", ")), nil
}

// migrateDefaults は、今の「新しい会話の既定」からプロファイル「既定」を作って保存し、「新しい会話で最初に選ばれる」の印を付ける。
// 使うツールのグループは、tools.json の「新しい会話で最初から選ばない」(notDefault)から引き継ぐ。
// これより後は、グループのチェックもプロファイルに書く(Conversations.SetDefaultGroup)。
func (cs *Conversations) migrateDefaults() (Profile, error) {
	cs.mu.Lock()
	v := cs.profile.Values
	cs.mu.Unlock()
	v.BuiltinOff = !cs.prefs.groupDefault("")
	v.MCPServers = []string{}
	if cs.remote != nil {
		for _, name := range cs.remote.Servers() {
			if cs.prefs.groupDefault(name) {
				v.MCPServers = append(v.MCPServers, name)
			}
		}
	}
	now := time.Now()
	p := Profile{ID: uuid.NewV7(), Name: DefaultProfileName, Values: v, CreatedAt: now, UpdatedAt: now}
	if err := cs.store.CreateProfile(p); err != nil {
		return Profile{}, fmt.Errorf("プロファイル「%s」を作れません: %w", p.Name, err)
	}
	if err := SaveAppSetting(cs.store, SettingDefaultProfile, p.ID.String()); err != nil {
		return Profile{}, err
	}
	return p, nil
}

// markedProfile は、「新しい会話で最初に選ばれる」の印が付いたプロファイルを list から探す。
// 印が無い・印のプロファイルが無いかしまってある(DB を手で直したなど)ときは、しまっていない最初のプロファイルに印を付け直す。
// プロファイルは必ず1つ以上あり、印の付いたものはしまえない(0014 の 10・11)ので、ふつうは付け直さない。
func markedProfile(s Store, list []Profile) (Profile, string, error) {
	i, err := markIndex(s, list)
	if err != nil {
		return Profile{}, "", err
	}
	if i >= 0 && !list[i].Archived {
		return list[i], "", nil
	}
	i = slices.IndexFunc(list, func(p Profile) bool { return !p.Archived })
	if i < 0 {
		return Profile{}, "", errors.New("しまっていないプロファイルがありません。新しい会話を作れません")
	}
	if err := SaveAppSetting(s, SettingDefaultProfile, list[i].ID.String()); err != nil {
		return Profile{}, "", err
	}
	return list[i], fmt.Sprintf("新しい会話で最初に選ばれるプロファイルが見つからないので、「%s」にしました", list[i].Name), nil
}

// markIndex は、「新しい会話で最初に選ばれる」の印(default_profile)が付いたプロファイルの list での位置。無ければ -1。
func markIndex(s Store, list []Profile) (int, error) {
	var id string
	if _, err := loadAppSetting(s, SettingDefaultProfile, &id); err != nil {
		return -1, err
	}
	return slices.IndexFunc(list, func(p Profile) bool { return p.ID.String() == id }), nil
}

// linkConversations は、どのプロファイルにも紐づいていない会話を p に紐づける。紐づけた会話の数を返す。
func (cs *Conversations) linkConversations(p Profile) (int, error) {
	list, err := cs.store.ListConversations()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, c := range list {
		if c.ProfileID != (uuid.UUID{}) {
			continue
		}
		c.ProfileID = p.ID
		if err := cs.store.UpdateConversation(c); err != nil {
			return n, fmt.Errorf("会話 %s をプロファイル「%s」に紐づけられません: %w", c.ID, p.Name, err)
		}
		n++
	}
	return n, nil
}

// DefaultProfile は、新しい会話で最初に選ばれるプロファイル。PrepareProfiles の前は保存していない仮のもの(ID が空)。
func (cs *Conversations) DefaultProfile() Profile {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	p := cs.profile
	p.Values.MCPServers = slices.Clone(p.Values.MCPServers)
	return p
}

// updateProfile は、新しい会話で最初に選ばれるプロファイルの値を change で変えて保存する。
// 保存できたときだけ、覚えているプロファイルを置き換える(保存に失敗したのに画面だけ変わって見えないように)。
func (cs *Conversations) updateProfile(change func(*ProfileValues)) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	p := cs.profile
	if p.ID == (uuid.UUID{}) {
		return errors.New("プロファイルの準備ができていません(PrepareProfiles を先に呼んでください)")
	}
	p.Values.MCPServers = slices.Clone(p.Values.MCPServers)
	change(&p.Values)
	p.UpdatedAt = time.Now()
	if err := cs.store.UpdateProfile(p); err != nil {
		return fmt.Errorf("プロファイル「%s」を保存できません: %w", p.Name, err)
	}
	cs.profile = p
	return nil
}

// SetDefaultGroup は、ツールのグループ(組み込みなら空、MCP ならサーバーの名前)を新しい会話で最初から使うかを変える。
// 設定の画面の「ツール」のグループのチェック。新しい会話で最初に選ばれるプロファイルの「使うツールのグループ」に書く。
func (cs *Conversations) SetDefaultGroup(group string, on bool) error {
	return cs.updateProfile(func(v *ProfileValues) {
		if group == "" {
			v.BuiltinOff = !on
			return
		}
		v.MCPServers = slices.DeleteFunc(v.MCPServers, func(n string) bool { return n == group })
		if on {
			v.MCPServers = append(v.MCPServers, group)
			slices.Sort(v.MCPServers)
		}
	})
}

// ---- プロファイルの管理(設定の画面。0014 の 11・12) ----------------------------------------

// ProfileInfo は、設定の画面に出すプロファイル1つ。
type ProfileInfo struct {
	Profile Profile `json:"profile"`
	Uses    int     `json:"uses"`    // このプロファイルから作った会話の数(0 なら本当に消せる)
	Default bool    `json:"default"` // 新しい会話で最初に選ばれる(しまえない・消せない)
}

// Profiles は、プロファイルを作った順に返す(しまったものも含む)。
func (cs *Conversations) Profiles() ([]ProfileInfo, error) {
	list, err := cs.store.ListProfiles()
	if err != nil {
		return nil, err
	}
	convs, err := cs.store.ListConversations()
	if err != nil {
		return nil, err
	}
	uses := map[uuid.UUID]int{}
	for _, c := range convs {
		uses[c.ProfileID]++
	}
	def := cs.DefaultProfile().ID
	out := make([]ProfileInfo, 0, len(list))
	for _, p := range list {
		out = append(out, ProfileInfo{Profile: p, Uses: uses[p.ID], Default: p.ID == def})
	}
	return out, nil
}

// validate は、保存する前にプロファイルを確かめる。
func (p Profile) validate() error {
	if strings.TrimSpace(p.Name) == "" {
		return errors.New("プロファイルの名前を空にはできません")
	}
	if p.Values.HistoryTokens <= 0 || p.Values.ReplyTokens <= 0 {
		return fmt.Errorf("会話の上限と返事の上限は 1 以上にしてください(%d / %d)", p.Values.HistoryTokens, p.Values.ReplyTokens)
	}
	for _, f := range p.Rules.Fixed {
		if !slices.ContainsFunc(fixItems, func(it fixItem) bool { return it.name == f }) {
			return fmt.Errorf("固定する項目 %q は知らない名前です", f)
		}
	}
	return nil
}

// CreateProfile は、名前・値・決まりから新しいプロファイルを作る。
// 会話の設定の画面の「新しいプロファイルとして保存」は、会話の設定から作った値を渡す(ProfileValuesOf)。
func (cs *Conversations) CreateProfile(name string, values ProfileValues, rules ProfileRules) (Profile, error) {
	now := time.Now()
	p := Profile{ID: uuid.NewV7(), Name: strings.TrimSpace(name), Values: values, Rules: rules, CreatedAt: now, UpdatedAt: now}
	p.Values = valuesOf(p.Values.settings()) // compact の空などをそろえる
	if err := p.validate(); err != nil {
		return Profile{}, err
	}
	if err := cs.store.CreateProfile(p); err != nil {
		return Profile{}, fmt.Errorf("プロファイル「%s」を作れません: %w", p.Name, err)
	}
	return p, nil
}

// ProfileValuesOf は、会話の設定 s をプロファイルの値にする(作業フォルダ・会話だけのツールの扱いは入れない)。
func ProfileValuesOf(s ConversationSettings) ProfileValues {
	return valuesOf(s)
}

// UpdateProfile は、プロファイルの名前・値・決まりを書き換える(しまったかは SetProfileArchived で変える)。
// 値を変えても、今ある会話の値は変わらない(写しなので)。決まりは今ある会話にもすぐ効く(そのつど見に行くので)。
func (cs *Conversations) UpdateProfile(p Profile) error {
	old, err := cs.store.Profile(p.ID)
	if err != nil {
		return err
	}
	p.Name = strings.TrimSpace(p.Name)
	p.Values = valuesOf(p.Values.settings())
	if err := p.validate(); err != nil {
		return err
	}
	p.Archived, p.CreatedAt, p.UpdatedAt = old.Archived, old.CreatedAt, time.Now()
	return cs.saveProfile(p)
}

// saveProfile はプロファイルを保存し、新しい会話で最初に選ばれるものなら覚えているものも置き換える。
func (cs *Conversations) saveProfile(p Profile) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if err := cs.store.UpdateProfile(p); err != nil {
		return fmt.Errorf("プロファイル「%s」を保存できません: %w", p.Name, err)
	}
	if cs.profile.ID == p.ID {
		cs.profile = p
	}
	return nil
}

// SetProfileArchived は、プロファイルをしまう / 戻す。しまったプロファイルは新しい会話では選べないが、決まりは残る(0014 の 11)。
// 新しい会話で最初に選ばれるプロファイルはしまえない(新しい会話を作れなくなるため)。
func (cs *Conversations) SetProfileArchived(id uuid.UUID, archived bool) error {
	if archived && id == cs.DefaultProfile().ID {
		return errors.New("新しい会話で最初に選ばれるプロファイルはしまえません。先にほかのプロファイルを選ばれるものにしてください")
	}
	p, err := cs.store.Profile(id)
	if err != nil {
		return err
	}
	p.Archived, p.UpdatedAt = archived, time.Now()
	return cs.saveProfile(p)
}

// DeleteProfile は、プロファイルを本当に消す。使っている会話があれば消せない(しまうことはできる。ErrProfileInUse)。
// 新しい会話で最初に選ばれるプロファイルも消せない(プロファイルは必ず1つ以上あるように)。
func (cs *Conversations) DeleteProfile(id uuid.UUID) error {
	if id == cs.DefaultProfile().ID {
		return errors.New("新しい会話で最初に選ばれるプロファイルは消せません。先にほかのプロファイルを選ばれるものにしてください")
	}
	return cs.store.DeleteProfile(id)
}

// SetDefaultProfile は、新しい会話で最初に選ばれるプロファイルを変える。しまったプロファイルは選べない。
func (cs *Conversations) SetDefaultProfile(id uuid.UUID) error {
	p, err := cs.store.Profile(id)
	if err != nil {
		return err
	}
	if p.Archived {
		return fmt.Errorf("プロファイル「%s」はしまってあるので、新しい会話で最初に選ばれるものにはできません", p.Name)
	}
	if err := SaveAppSetting(cs.store, SettingDefaultProfile, p.ID.String()); err != nil {
		return err
	}
	cs.mu.Lock()
	cs.profile = p
	cs.mu.Unlock()
	return nil
}

// usableProfile は、新しい会話を作るときのプロファイル。id が空なら新しい会話で最初に選ばれるもの。しまったものは選べない。
func (cs *Conversations) usableProfile(id uuid.UUID) (Profile, error) {
	if id == (uuid.UUID{}) {
		return cs.DefaultProfile(), nil
	}
	p, err := cs.store.Profile(id)
	if err != nil {
		return Profile{}, err
	}
	if p.Archived {
		return Profile{}, fmt.Errorf("プロファイル「%s」はしまってあるので、新しい会話では選べません", p.Name)
	}
	return p, nil
}

// DefaultsOf は、プロファイル id から新しい会話を作るときの設定(新しい会話の画面でプロファイルを選んだときの写し)。
func (cs *Conversations) DefaultsOf(id uuid.UUID) (ConversationSettings, error) {
	p, err := cs.usableProfile(id)
	if err != nil {
		return ConversationSettings{}, err
	}
	return cs.defaultsFor(p), nil
}

// SaveToProfile は、会話 conv の今の設定(使うときの設定)の値で、プロファイル profileID の値を上書きする(決まりは変えない)。
// 会話の設定の画面の「プロファイルに保存」。使うツールのグループも写す(グループはプロファイルの値なので)。モデルも写す。
func (cs *Conversations) SaveToProfile(conv, profileID uuid.UUID) error {
	c, _, err := cs.load(conv)
	if err != nil {
		return err
	}
	p, err := cs.store.Profile(profileID)
	if err != nil {
		return err
	}
	p.Values = valuesOf(c.Settings)
	return cs.UpdateProfile(p)
}

// SaveAsProfile は、会話 conv の今の設定の値で、新しいプロファイル name を作る(決まりは無し)。
// 会話は元のプロファイルから作ったもののまま(どのプロファイルから作ったかは変えない)。
func (cs *Conversations) SaveAsProfile(conv uuid.UUID, name string) (Profile, error) {
	c, _, err := cs.load(conv)
	if err != nil {
		return Profile{}, err
	}
	return cs.CreateProfile(name, valuesOf(c.Settings), ProfileRules{})
}
