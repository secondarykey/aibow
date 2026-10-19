package aibow

// toolprefs.go: ツールの扱い(使わない / 確認する / 確認しない)と、その PC の既定(tools.json)。
//
// ツールの扱いは2つの層で決める:
//
//	会話ごとの上書き … 会話の設定(DB の conversations.settings の tool_policies)。会話を消せば一緒に消える
//	PC の既定         … tools.json(手元のファイル。aibow が書く)。何も決めていない会話での扱い
//
// 会話の上書き → PC の既定(グループごとの「使わない」→ ツールごと → グループごと)→ 決まりの既定、の順に見て、
// 最初に見つかったものを使う(Conversations.toolPolicy)。グループは、組み込み(aibow の中で動くツール)と、MCP のサーバーごと。
//
// PC の既定を会話の DB に入れないのは、ツールは手元で動くことが多く、「聞かずに動かしてよいか」はその PC を使う人が決めることだから。
// 会話をサーバーに置く形(TODO.md のクライアント/サーバー構成の B)にしても、これは手元に残る。
// 手で書く settings.json とも分けている(画面で変えたものを aibow が書くので、手で書いたファイルの形や並びを崩さないため)。
//
// ツールの名前は、組み込みのツールはそのまま(write_file)、MCP のツールはサーバーの名前が前に付く(Blender__render)ので重ならない。

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// ToolPolicy はツールの扱い。真偽値の組み合わせではなく値で持つ(あとで動きを足すときは値を1つ足す。0010)。
// 決めていないときは空(次の層を見る)。
type ToolPolicy string

const (
	ToolOff   ToolPolicy = "off"   // 使わない: モデルに見せない(説明もプロンプトに入れないので、会話の上限の節約にもなる)
	ToolAsk   ToolPolicy = "ask"   // 確認する: 実行する前にユーザーに聞く(MCP のツールの決まりの既定)
	ToolAllow ToolPolicy = "allow" // 確認しない: 聞かずに実行する(設定の画面・会話の設定の画面で選ぶ。0010)
	// ToolOn は会話の上書きだけで使う値: 使う。PC の既定の「使わない」は「既定では使わない」の意味で、禁止ではないので、
	// 会話ごとに使うことにできる(2026-10-15、ユーザーと決めた)。確認するかは変えない(PC の既定・決まりの既定のまま)
	ToolOn ToolPolicy = "on"
)

func (p ToolPolicy) valid() bool {
	return p == ToolOff || p == ToolAsk || p == ToolAllow
}

// validInConversation は、会話の上書きに置ける値か(on は会話だけ)。
func (p ToolPolicy) validInConversation() bool {
	return p.valid() || p == ToolOn
}

// KnownTool は、サーバーを起動したときに見えたツール1つ(画面の設定に一覧を出すため。ツールの扱いには使わない)。
type KnownTool struct {
	Name        string `json:"name"`        // モデルに見せる名前(Blender__get_objects_summary)
	Description string `json:"description"` // 説明の1行目
}

// toolsFile は tools.json の中身。
type toolsFile struct {
	// Builtin は組み込みのツール(aibow の中で動くツール)全部の既定
	Builtin ToolPolicy `json:"builtin,omitempty"`
	// LegacyLocal は、前の名前(「ローカル」と呼んでいたころ)の Builtin。読むときに Builtin に移し、書くときは書かない
	LegacyLocal ToolPolicy `json:"local,omitempty"`
	// Servers は MCP のサーバーごとの既定(そのサーバーの全部のツール)。キーは settings.json のサーバーの名前
	Servers map[string]ToolPolicy `json:"servers,omitempty"`
	// Tools はツールごとの扱い。キーはモデルに見せる名前(Blender__get_objects_summary)。サーバーごとの既定より優先する
	Tools map[string]ToolPolicy `json:"tools,omitempty"`
	// Known は、サーバーを最後に起動したときに見えたツールの一覧。起動のたびに置き換える
	Known map[string][]KnownTool `json:"known,omitempty"`
	// NotDefault は、新しい会話で最初から選ばないグループ(プロファイルより前の、設定の画面の「ツール」のグループのチェック)。
	// 今はグループのチェックをプロファイルの「使うツールのグループ」に書くので(0014)、プロファイル「既定」への引き継ぎのためだけに読む。
	// 足したばかりのサーバーをここに入れるのは、引き継ぐ前の起動で足したサーバーを「既定」に入れないため(NoteServers)
	NotDefault NotDefault `json:"notDefault,omitzero"`
	// Seen は、aibow がすでに見た MCP のサーバー(settings.json の名前)。ここに無いサーバーは、足したばかりのもの。
	// 足したばかりのサーバーは、新しい会話で最初から選ばない(NotDefault に入れる)。足しただけで全部の新しい会話のプロンプトに
	// ツールの説明が入り、会話の上限を食わないように(2026-10-15、ユーザーの指示。NoteServers)。
	// settings.json から消しても残す(戻したときに、前の扱いのまま使えるように)。
	// omitempty にしないのは、「空」と「この仕組みより前のファイル(キーが無い)」を見分けるため
	Seen []string `json:"seen"`
}

// NotDefault は、新しい会話で最初から選ばないグループ。
type NotDefault struct {
	Builtin bool     `json:"builtin,omitempty"` // 組み込みのツール
	Servers []string `json:"servers,omitempty"` // MCP のサーバー(settings.json の名前)
}

// ToolPrefs は、その PC でのツールの扱いの既定(tools.json)。nil でも使える(何も決めていない扱い)。
type ToolPrefs struct {
	path     string
	mu       sync.Mutex
	data     toolsFile
	readOnly bool // 読めなかったファイルを上書きして消さないよう、書かない
	// beforeSeen は、Seen の仕組みより前に書かれたファイル(seen のキーが無い)を読んだか。
	// そのときは、今あるサーバーを全部「見た」ことにする(それまでの扱いを変えない。NoteServers)
	beforeSeen bool
}

// LoadToolPrefs は tools.json を読む。ファイルが無ければ空(何も決めていない)。
// 読めない・壊れているときはエラーを返すが、ToolPrefs も返す(そのときは何も決めていない扱いで、ファイルは書き換えない)。
func LoadToolPrefs(path string) (*ToolPrefs, error) {
	p := &ToolPrefs{path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return p, nil
	}
	if err == nil {
		err = json.Unmarshal(data, &p.data)
		p.beforeSeen = p.data.Seen == nil
	}
	if p.data.Builtin == "" { // 前の名前のキー(local)で書いてあれば移す
		p.data.Builtin = p.data.LegacyLocal
	}
	p.data.LegacyLocal = ""
	p.data.moveGroupOff()
	if err == nil {
		err = p.data.validate()
	}
	if err != nil {
		p.data, p.readOnly = toolsFile{}, true
		return p, fmt.Errorf("ツールの扱いのファイル %s を読めません(ツールの扱いは決まりの既定で動き、ファイルは書き換えません): %w", path, err)
	}
	return p, nil
}

// NoteServers は、settings.json の MCP のサーバー(servers)のうち、まだ見ていないもの(足したばかりのもの)を
// 「新しい会話で最初から選ばない」にして、見たことを覚える。起動したときに1回呼ぶ(settings.json は起動したときにだけ読むため)。
// 「最初から選ばない」にしたサーバーの名前を返す。
//
// 「ツール」のグループのチェック(NotDefault)は、外したものだけを覚えているので、そのままでは足したサーバーが「最初から使う」になる。
// 使うかどうかは、足した人が設定の画面の「ツール」でチェックして決める。
// Seen の仕組みより前のファイルなら、今あるサーバーは全部見たことにする(それまで最初から使っていたものを、急に外さない)。
func (p *ToolPrefs) NoteServers(servers []string) ([]string, error) {
	if p == nil {
		return nil, nil
	}
	p.mu.Lock()
	beforeSeen := p.beforeSeen
	var added []string
	if !beforeSeen {
		for _, name := range servers {
			if !slices.Contains(p.data.Seen, name) {
				added = append(added, name)
			}
		}
	}
	if !beforeSeen && len(added) == 0 {
		// 足したサーバーが無ければ書かない(MCP を使っていないのに tools.json を作らないように)。
		// 次に書くときに seen のキーが入るよう、空の一覧にだけしておく
		if p.data.Seen == nil {
			p.data.Seen = []string{}
		}
		p.mu.Unlock()
		return nil, nil
	}
	p.mu.Unlock()
	if p.readOnly {
		return nil, nil
	}
	err := p.update(func(f *toolsFile) {
		if beforeSeen {
			f.Seen = append(f.Seen, servers...)
		}
		for _, name := range added {
			f.Seen = append(f.Seen, name)
			if !slices.Contains(f.NotDefault.Servers, name) {
				f.NotDefault.Servers = append(f.NotDefault.Servers, name)
			}
		}
		if f.Seen == nil {
			f.Seen = []string{} // 「この仕組みで書いたファイル」の印(seen のキーを書く)
		}
		slices.Sort(f.Seen)
		f.Seen = slices.Compact(f.Seen)
		slices.Sort(f.NotDefault.Servers)
	}, "")
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.beforeSeen = false
	p.mu.Unlock()
	return added, nil
}

// moveGroupOff は、グループごとの「使わない」を「新しい会話で最初から選ばない」に読み替える。
//
// 最初は、設定の画面の「ツール」でグループのチェックを外すと「どの会話でも使えない」(グループの off)にしていた。
// 会話で選んでいたものまで消えて分かりにくかったので、「新しい会話で最初から選ばない」(NotDefault)に変えた。
// 前の画面で書いた off はこちらの意味で書いたものなので、読むときに移す。グループの扱い(servers / builtin)には、確認する・確認しないだけが残る。
func (f *toolsFile) moveGroupOff() {
	if f.Builtin == ToolOff {
		f.Builtin, f.NotDefault.Builtin = "", true
	}
	for name, p := range f.Servers {
		if p == ToolOff {
			delete(f.Servers, name)
			if !slices.Contains(f.NotDefault.Servers, name) {
				f.NotDefault.Servers = append(f.NotDefault.Servers, name)
			}
		}
	}
	slices.Sort(f.NotDefault.Servers)
}

func (f toolsFile) validate() error {
	if f.Builtin != "" && !f.Builtin.valid() {
		return fmt.Errorf("builtin の扱い %q は知らない値です(off / ask / allow)", f.Builtin)
	}
	for _, m := range []map[string]ToolPolicy{f.Servers, f.Tools} {
		for k, v := range m {
			if !v.valid() {
				return fmt.Errorf("%s の扱い %q は知らない値です(off / ask / allow)", k, v)
			}
		}
	}
	return nil
}

// policy は PC の既定でのツールの扱い。決めていなければ ok が false。
// group は、組み込みのツールなら空、MCP のツールならサーバーの名前。
//
// グループごと「使わない」なら、ツールごとの扱いにかかわらず使わない(画面でグループを使わないにすると、ツールの一覧ごと畳むため)。
// そうでなければ、ツールごと → グループごと、の順に見る。
func (p *ToolPrefs) policy(group, name string) (ToolPolicy, bool) {
	g, gok := p.groupPolicy(group)
	if gok && g == ToolOff {
		return ToolOff, true
	}
	if p == nil {
		return "", false
	}
	p.mu.Lock()
	v, ok := p.data.Tools[name]
	p.mu.Unlock()
	if ok {
		return v, true
	}
	return g, gok
}

// confirmPolicy は、会話で「使う」(ToolOn)にしたツールの、PC の既定での確認の扱い。決めていなければ ok が false。
// 「使わない」は会話の「使う」で打ち消されるので飛ばし、ツールごと → グループごと、の順に見る。
func (p *ToolPrefs) confirmPolicy(group, name string) (ToolPolicy, bool) {
	if p == nil {
		return "", false
	}
	p.mu.Lock()
	v, ok := p.data.Tools[name]
	p.mu.Unlock()
	if ok && v != ToolOff {
		return v, true
	}
	if g, ok := p.groupPolicy(group); ok && g != ToolOff {
		return g, true
	}
	return "", false
}

// groupPolicy はグループ(組み込みなら空、MCP ならサーバーの名前)全部の扱い。決めていなければ ok が false。
func (p *ToolPrefs) groupPolicy(group string) (ToolPolicy, bool) {
	if p == nil {
		return "", false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if group == "" {
		return p.data.Builtin, p.data.Builtin != ""
	}
	v, ok := p.data.Servers[group]
	return v, ok
}

// SetToolPolicy はツールの扱いの PC の既定を変えて、tools.json に書く。policy が空なら決めていない状態に戻す。
func (p *ToolPrefs) SetToolPolicy(name string, policy ToolPolicy) error {
	return p.update(func(f *toolsFile) { f.Tools = setPolicy(f.Tools, name, policy) }, policy)
}

// SetBuiltinPolicy は組み込みのツール全部の扱いの PC の既定を変えて、tools.json に書く。policy が空なら決めていない状態に戻す。
func (p *ToolPrefs) SetBuiltinPolicy(policy ToolPolicy) error {
	return p.update(func(f *toolsFile) { f.Builtin = policy }, policy)
}

// SetGroupPolicy は、グループ(組み込みなら空、MCP ならサーバーの名前)の全部のツールの確認の扱いの PC の既定を変えて、tools.json に書く。
// 確認する(ask)・確認しない(allow)・決めていない(空)だけ(グループごとの「使わない」は「新しい会話で最初から選ばない」= プロファイルの使うツールのグループ。Conversations.SetDefaultGroup)。
func (p *ToolPrefs) SetGroupPolicy(group string, policy ToolPolicy) error {
	if policy != "" && policy != ToolAsk && policy != ToolAllow {
		return fmt.Errorf("グループの扱いは確認する(ask)か確認しない(allow)です: %q", policy)
	}
	if group == "" {
		return p.SetBuiltinPolicy(policy)
	}
	return p.SetServerPolicy(group, policy)
}

// SetServerPolicy はサーバーの全部のツールの扱いの PC の既定を変えて、tools.json に書く。policy が空なら決めていない状態に戻す。
func (p *ToolPrefs) SetServerPolicy(server string, policy ToolPolicy) error {
	return p.update(func(f *toolsFile) { f.Servers = setPolicy(f.Servers, server, policy) }, policy)
}

func setPolicy(m map[string]ToolPolicy, key string, policy ToolPolicy) map[string]ToolPolicy {
	if policy == "" {
		delete(m, key)
		return m
	}
	if m == nil {
		m = map[string]ToolPolicy{}
	}
	m[key] = policy
	return m
}

// groupDefault は、グループを新しい会話で最初から選ぶか(決めていなければ選ぶ)。
// プロファイルより前の値で、プロファイル「既定」への引き継ぎ(Conversations.migrateDefaults)のためだけに読む。
func (p *ToolPrefs) groupDefault(group string) bool {
	if p == nil {
		return true
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if group == "" {
		return !p.data.NotDefault.Builtin
	}
	return !slices.Contains(p.data.NotDefault.Servers, group)
}

// setKnown は、サーバーを起動したときに見えたツールの一覧を覚える(前の一覧と同じなら書かない)。
func (p *ToolPrefs) setKnown(server string, tools []KnownTool) error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	same := slices.Equal(p.data.Known[server], tools)
	p.mu.Unlock()
	if same {
		return nil
	}
	return p.update(func(f *toolsFile) {
		if f.Known == nil {
			f.Known = map[string][]KnownTool{}
		}
		f.Known[server] = tools
	}, "")
}

// update は中身を変えて、ファイルに書く。書けなかったら、変える前に戻す。
func (p *ToolPrefs) update(change func(*toolsFile), policy ToolPolicy) error {
	if p == nil {
		return errors.New("ツールの扱いのファイルを使っていません")
	}
	if policy != "" && !policy.valid() {
		return fmt.Errorf("ツールの扱い %q は知らない値です(off / ask / allow)", policy)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.readOnly {
		return fmt.Errorf("ツールの扱いのファイル %s を読めなかったので、書き換えません(直すか消してから、アプリを起動し直してください)", p.path)
	}
	before := p.data.clone()
	change(&p.data)
	if err := p.save(); err != nil {
		p.data = before
		return err
	}
	return nil
}

func (f toolsFile) clone() toolsFile {
	c := toolsFile{Builtin: f.Builtin, Servers: maps.Clone(f.Servers), Tools: maps.Clone(f.Tools), Known: map[string][]KnownTool{},
		NotDefault: NotDefault{Builtin: f.NotDefault.Builtin, Servers: slices.Clone(f.NotDefault.Servers)}, Seen: slices.Clone(f.Seen)}
	for k, v := range f.Known {
		c.Known[k] = slices.Clone(v)
	}
	return c
}

// save は tools.json に書く。書きかけのファイルが残らないよう、一時ファイルに書いてから名前を変える。
func (p *ToolPrefs) save() error {
	if p.path == "" {
		return nil // テストなど、ファイルを使わないとき
	}
	b, err := json.MarshalIndent(p.data, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p.path), 0o755); err != nil {
		return fmt.Errorf("ツールの扱いのファイルの置き場所を作れません: %w", err)
	}
	tmp := p.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return fmt.Errorf("ツールの扱いのファイルを書けません: %w", err)
	}
	if err := os.Rename(tmp, p.path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("ツールの扱いのファイルを書けません: %w", err)
	}
	return nil
}

// ToolPrefsView は、画面の設定に出すための PC の既定の写し。
type ToolPrefsView struct {
	Builtin    ToolPolicy             `json:"builtin"` // 組み込みのツール全部の扱い(空なら決めていない)
	Servers    map[string]ToolPolicy  `json:"servers"`
	Tools      map[string]ToolPolicy  `json:"tools"`
	Known      map[string][]KnownTool `json:"known"`
	NotDefault NotDefault             `json:"notDefault"` // 新しい会話で最初から選ばないグループ
	Path       string                 `json:"path"`       // tools.json の場所(画面に出す)
	Error      string                 `json:"error"`      // 読めなかったときの理由(書き換えない)
}

// View は画面に出すための写しを返す。
func (p *ToolPrefs) View() ToolPrefsView {
	v := ToolPrefsView{Servers: map[string]ToolPolicy{}, Tools: map[string]ToolPolicy{}, Known: map[string][]KnownTool{}}
	if p == nil {
		return v
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	c := p.data.clone()
	v.Builtin = c.Builtin
	v.NotDefault = c.NotDefault
	if v.NotDefault.Servers == nil {
		v.NotDefault.Servers = []string{}
	}
	maps.Copy(v.Servers, c.Servers)
	maps.Copy(v.Tools, c.Tools)
	maps.Copy(v.Known, c.Known)
	v.Path = p.path
	if p.readOnly {
		v.Error = "ファイルを読めなかったので、書き換えません"
	}
	return v
}

// firstLine は説明の1行目(画面の一覧に出す。長い説明を全部出すと読めないため)。
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return s
}
