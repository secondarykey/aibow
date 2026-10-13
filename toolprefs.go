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

// ToolPolicy はツールの扱い。
type ToolPolicy string

const (
	ToolOff   ToolPolicy = "off"   // 使わない: モデルに見せない(説明もプロンプトに入れないので、会話の上限の節約にもなる)
	ToolAsk   ToolPolicy = "ask"   // 確認する: 実行する前にユーザーに聞く(MCP のツールの決まりの既定)
	ToolAllow ToolPolicy = "allow" // 確認しない: 聞かずに実行する。今は画面から選べない(tools.json を手で書けば効く)
)

func (p ToolPolicy) valid() bool {
	return p == ToolOff || p == ToolAsk || p == ToolAllow
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
	// NotDefault は、新しい会話で最初から選ばないグループ(設定の画面の「ツール」で、グループのチェックを外したもの)。
	// 使えなくするのではない。会話の「この会話のツール」からは、いつでも選べる
	NotDefault NotDefault `json:"notDefault,omitzero"`
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

// SetGroupDefault は、グループ(組み込みなら空、MCP ならサーバーの名前)を新しい会話で最初から選ぶかを変えて、tools.json に書く。
func (p *ToolPrefs) SetGroupDefault(group string, on bool) error {
	return p.update(func(f *toolsFile) {
		if group == "" {
			f.NotDefault.Builtin = !on
			return
		}
		f.NotDefault.Servers = slices.DeleteFunc(f.NotDefault.Servers, func(n string) bool { return n == group })
		if !on {
			f.NotDefault.Servers = append(f.NotDefault.Servers, group)
			slices.Sort(f.NotDefault.Servers)
		}
	}, "")
}

// groupDefault は、グループを新しい会話で最初から選ぶか(決めていなければ選ぶ)。
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
		NotDefault: NotDefault{Builtin: f.NotDefault.Builtin, Servers: slices.Clone(f.NotDefault.Servers)}}
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
