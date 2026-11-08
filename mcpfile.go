package aibow

// mcpfile.go: MCP のサーバーの置き場所 mcp.json と、手で settings.json に書いた分の扱い(0034)。
//
// MCP のサーバーは mcp.json で管理する(画面から足す・変える・消す。aibow が書く。tools.json・connection.json と同じ考え方)。
// settings.json に手で書いた mcpServers は、起動したときに mcp.json へ移し、settings.json からは消す(MoveFromSettings。0034 の決定事項 13)。
// 置き場所が2つあると、画面から変えられないサーバーや、同じ名前で使われないサーバーができて分かりにくいため。
//
// 形はどちらも {"mcpServers": {...}}(Claude Desktop などと同じ)なので、MCP サーバーの説明にある JSON をそのまま貼れ、写し合える。
// 移せなかったとき(mcp.json が壊れている・書けない)だけ、settings.json の分を今までどおり別に持つ(画面からは変えない)。
// そのとき同じ名前が両方にあれば settings.json のほうを使う(動いていたものを変えない)。
//
// このファイルの係は、アプリに縛らない(パスを受け取る)。aibow-server(server.json)の分も、あとで同じ部品で作る見込み(0034 の決定事項 4)。

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/secondarykey/aibow/config"
	"github.com/secondarykey/aibow/log"
	"github.com/secondarykey/aibow/mcp/client"
	"github.com/secondarykey/aibow/tool"
)

// MCPFile は、画面から足した MCP のサーバーを置くファイル(mcp.json)。
// 読めなかった・壊れていたときは、中身を使わず、書きもしない(壊れたファイルを上書きして消さない。tools.json と同じ。0034 の決定事項 16)。
type MCPFile struct {
	path    string
	mu      sync.Mutex
	servers map[string]client.ServerConfig
	broken  error // 読めなかった理由(nil なら使える)
}

// mcpFileData は mcp.json の中身。settings.json と同じ形にしている(そのまま写し合えるように)。
type mcpFileData struct {
	MCPServers map[string]client.ServerConfig `json:"mcpServers"`
}

// LoadMCPFile は mcp.json を読む。ファイルが無ければ空(まだ画面から足していない)。
// 読めない・壊れている・書き方が違う(command も url も無い、など)ときはエラーを返すが、MCPFile も返す(中身は空で、書き換えない)。
func LoadMCPFile(path string) (*MCPFile, error) {
	f := &MCPFile{path: path, servers: map[string]client.ServerConfig{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	var d mcpFileData
	if err == nil {
		if err = json.Unmarshal(data, &d); err != nil {
			err = fmt.Errorf("%s: %w", config.JSONErrorLine(data, err), err)
		}
	}
	if err == nil {
		err = config.CheckMCPServers(path, d.MCPServers)
	}
	if err != nil {
		f.broken = fmt.Errorf("画面から足した MCP のサーバーのファイル %s を読めません(このファイルのサーバーは使わず、書き換えません。直すか消してから、アプリを起動し直してください): %w", path, err)
		return f, f.broken
	}
	if d.MCPServers != nil {
		f.servers = d.MCPServers
	}
	return f, nil
}

// Path は mcp.json の場所。
func (f *MCPFile) Path() string { return f.path }

// Servers は mcp.json のサーバー(写し)。
func (f *MCPFile) Servers() map[string]client.ServerConfig {
	f.mu.Lock()
	defer f.mu.Unlock()
	return maps.Clone(f.servers)
}

// Err は読めなかった理由(使えるなら nil)。
func (f *MCPFile) Err() error { return f.broken }

// save は servers を mcp.json に書く。書きかけのファイルが残らないよう、一時ファイルに書いてから名前を変える(tools.json と同じ)。
func (f *MCPFile) save(servers map[string]client.ServerConfig) error {
	if f.broken != nil {
		return errors.New("画面から足した MCP のサーバーのファイルを読めなかったので、書き換えません(直すか消してから、アプリを起動し直してください)")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.path != "" { // テストなど、ファイルを使わないときは書かない
		b, err := json.MarshalIndent(mcpFileData{MCPServers: servers}, "", "  ")
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
			return fmt.Errorf("MCP のサーバーのファイルの置き場所を作れません: %w", err)
		}
		tmp := f.path + ".tmp"
		if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
			return fmt.Errorf("MCP のサーバーのファイルを書けません: %w", err)
		}
		if err := os.Rename(tmp, f.path); err != nil {
			os.Remove(tmp)
			return fmt.Errorf("MCP のサーバーのファイルを書けません: %w", err)
		}
	}
	f.servers = maps.Clone(servers)
	return nil
}

// MoveFromSettings は、settings.json(settingsPath)に手で書いた MCP のサーバー hand を mcp.json に移し、settings.json から mcpServers を消す
// (0034 の決定事項 13。2026-11-03、ユーザーが決めた)。移したあとは、画面から足したものと同じく変える・消すができる。
//
// mcp.json に同じ名前があれば settings.json の設定にする(今まで動いていたのは settings.json のほうなので、動きを変えない)。
// 先に mcp.json に書き、そのあとで settings.json を書き直す。途中で失敗しても、両方に同じ設定が残るだけで、次の起動でもう一度移す。
//
// left は移せずに残った settings.json の分(今までどおり別に持つ)、notices は画面のお知らせに出す文。
// mcp.json が読めないときは移さない(理由は MCPConfig.Notices が伝える。壊れたファイルを上書きしない)。
func (f *MCPFile) MoveFromSettings(settingsPath string, hand map[string]client.ServerConfig) (left map[string]client.ServerConfig, notices []string) {
	if len(hand) == 0 || f.broken != nil {
		return hand, nil
	}
	next := f.Servers()
	var replaced []string
	for name, cfg := range hand {
		if old, ok := next[name]; ok && !old.Equal(cfg) {
			replaced = append(replaced, name)
		}
		next[name] = cfg
	}
	slices.Sort(replaced)
	names := strings.Join(slices.Sorted(maps.Keys(hand)), ", ")

	if err := config.CheckMCPServers(f.path, next); err != nil {
		return hand, f.moveFailed(names, err)
	}
	if err := f.save(next); err != nil {
		return hand, f.moveFailed(names, err)
	}
	if err := config.RemoveSettingsKey(settingsPath, "mcpServers"); err != nil {
		// mcp.json には書けた。settings.json にも残るので、settings.json の分として持ち続ける(画面で変えても、次の起動で戻ってしまうため)
		n := fmt.Sprintf("MCP のサーバー %s を mcp.json(%s)に写しましたが、settings.json から mcpServers を消せませんでした(settings.json のものとして使います。mcpServers を手で消して、アプリを起動し直してください): %v",
			names, f.path, err)
		log.Warn(n)
		return hand, []string{n}
	}
	n := fmt.Sprintf("settings.json の MCP のサーバー %s を mcp.json(%s)に移しました。これからは設定の画面の「MCP」で変える・消すができます", names, f.path)
	if len(replaced) > 0 {
		n += fmt.Sprintf("(%s は mcp.json にもあったので、使っていた settings.json の設定にしました)", strings.Join(replaced, ", "))
	}
	log.Notice(n)
	return nil, []string{n}
}

// moveFailed は、settings.json の分を mcp.json に移せなかったときのお知らせの文(ログにも出す)。
func (f *MCPFile) moveFailed(names string, err error) []string {
	n := fmt.Sprintf("settings.json の MCP のサーバー %s を mcp.json(%s)に移せませんでした(settings.json のものとして使います。画面からは変えられません): %v", names, f.path, err)
	log.Warn(n)
	return []string{n}
}

// MergeMCPServers は、settings.json(hand)と mcp.json(screen)の MCP のサーバーを合わせる(settings.json の分を移せなかったとき)。
// 同じ名前は settings.json のほうを使い、その名前を dup に返す(起動したときにお知らせで伝える。0034 の決定事項 13)。
func MergeMCPServers(hand, screen map[string]client.ServerConfig) (merged map[string]client.ServerConfig, dup []string) {
	merged = maps.Clone(screen)
	if merged == nil {
		merged = map[string]client.ServerConfig{}
	}
	for name, cfg := range hand {
		if _, ok := merged[name]; ok {
			dup = append(dup, name)
		}
		merged[name] = cfg
	}
	slices.Sort(dup)
	return merged, dup
}

// MCPManager は、動いている MCP のサーバーの係のうち、設定を差し替える・試す口(本物は mcp/client の Manager)。
type MCPManager interface {
	// Update は設定を差し替え、変えた・消したので止めたサーバーの名前を返す
	Update(configs map[string]client.ServerConfig) (stopped []string)
	// TryTools は、保存していない設定で一時的に起動して、ツールの一覧を受け取る
	TryTools(ctx context.Context, name string, cfg client.ServerConfig) ([]tool.Tool, error)
}

// MCPConfig は、MCP のサーバーの設定の全体(settings.json の分 + mcp.json の分)を持ち、画面で変えたものを動いている係に流す。
//
// 画面から変えられるのは mcp.json の分だけ。保存したらすぐ効く(起動し直さない。0034 の決定事項 6)。
type MCPConfig struct {
	handPath string
	hand     map[string]client.ServerConfig // settings.json の分(起動したときに読んだもの。画面からは変えない)
	file     *MCPFile
	prefs    *ToolPrefs

	mu  sync.Mutex
	mgr MCPManager
	// tried は、試しにつないだときの設定と、受け取ったツールの一覧(サーバーの名前ごとに最後の1回)。
	// 同じ設定で保存したら、その一覧を tools.json の known に覚える(会話で使う前から、ツールごとの扱いを決められるように)
	tried map[string]triedServer
}

type triedServer struct {
	cfg   client.ServerConfig
	tools []KnownTool
}

// NewMCPConfig は MCPConfig を作る。hand は settings.json の mcpServers、handPath はその場所(画面に出す)。
// 動いている係は、Servers で Manager を作ってから SetManager で渡す。
func NewMCPConfig(handPath string, hand map[string]client.ServerConfig, file *MCPFile, prefs *ToolPrefs) *MCPConfig {
	return &MCPConfig{handPath: handPath, hand: maps.Clone(hand), file: file, prefs: prefs, tried: map[string]triedServer{}}
}

// SetManager は、設定を流す先(動いている係)を渡す。
func (c *MCPConfig) SetManager(m MCPManager) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.mgr = m
}

// Servers は、今の設定の全体(settings.json と mcp.json を合わせたもの)。Manager を作るのに使う。
func (c *MCPConfig) Servers() map[string]client.ServerConfig {
	merged, _ := MergeMCPServers(c.hand, c.file.Servers())
	return merged
}

// Notices は、起動したときに画面のお知らせで伝えること(mcp.json が読めない・settings.json と同じ名前がある)。
func (c *MCPConfig) Notices() []string {
	var out []string
	if err := c.file.Err(); err != nil {
		out = append(out, err.Error())
	}
	if _, dup := MergeMCPServers(c.hand, c.file.Servers()); len(dup) > 0 {
		out = append(out, fmt.Sprintf("MCP のサーバー %s は、settings.json と画面から足した分(%s)の両方にあります。settings.json のほうを使います",
			strings.Join(dup, ", "), c.file.Path()))
	}
	return out
}

// MCP のサーバーをどこに書いたか(MCPServerEntry.Source)。
const (
	MCPFromSettings = "settings" // settings.json(手で書いた。画面からは変えない)
	MCPFromScreen   = "screen"   // mcp.json(画面から足した)
)

// MCPServerEntry は、設定の画面の「MCP」の一覧の1つ。
type MCPServerEntry struct {
	Name string `json:"name"`
	// Config はつなぎ方。env と headers の値は伏せる(キーだけを出し、値は空。0034 の決定事項 10)
	Config client.ServerConfig `json:"config"`
	Source string              `json:"source"` // settings か screen
	// Shadowed は、mcp.json のサーバーのうち、settings.json に同じ名前があって使われていないもの(消すことだけできる)
	Shadowed bool `json:"shadowed,omitempty"`
}

// MCPServerList は、設定の画面の「MCP」に出すもの。
type MCPServerList struct {
	Servers      []MCPServerEntry `json:"servers"`
	Path         string           `json:"path"`          // mcp.json の場所
	SettingsPath string           `json:"settings_path"` // settings.json の場所
	Error        string           `json:"error"`         // mcp.json を読めなかった理由(あれば画面から保存できない)
}

// List は、設定の画面の「MCP」に出す一覧(名前の順。同じ名前は settings.json のものを先に)。
func (c *MCPConfig) List() MCPServerList {
	l := MCPServerList{Servers: []MCPServerEntry{}, Path: c.file.Path(), SettingsPath: c.handPath}
	if err := c.file.Err(); err != nil {
		l.Error = err.Error()
	}
	for name, cfg := range c.hand {
		l.Servers = append(l.Servers, MCPServerEntry{Name: name, Config: masked(cfg), Source: MCPFromSettings})
	}
	for name, cfg := range c.file.Servers() {
		_, dup := c.hand[name]
		l.Servers = append(l.Servers, MCPServerEntry{Name: name, Config: masked(cfg), Source: MCPFromScreen, Shadowed: dup})
	}
	slices.SortFunc(l.Servers, func(a, b MCPServerEntry) int {
		return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(b.Source, a.Source)) // settings を先に
	})
	return l
}

// masked は、env と headers の値を伏せた写し(画面に秘密の値を返さない。API キーなどが入るため)。
func masked(cfg client.ServerConfig) client.ServerConfig {
	hide := func(m map[string]string) map[string]string {
		if m == nil {
			return nil
		}
		out := make(map[string]string, len(m))
		for k := range m {
			out[k] = ""
		}
		return out
	}
	cfg.Args = slices.Clone(cfg.Args)
	cfg.Env, cfg.Headers = hide(cfg.Env), hide(cfg.Headers)
	return cfg
}

// unmask は、画面から来た設定の env と headers の空の値を、前の設定 old の値で埋める(空のまま保存したら前の値のまま。0034 の決定事項 10)。
// 画面には値を返さないので、変えないときは空で送られてくる。前に無かったキーなら空のまま。
func unmask(cfg, old client.ServerConfig) client.ServerConfig {
	fill := func(m, prev map[string]string) map[string]string {
		if m == nil {
			return nil
		}
		out := maps.Clone(m)
		for k, v := range out {
			if p, ok := prev[k]; ok && v == "" {
				out[k] = p
			}
		}
		return out
	}
	cfg.Env, cfg.Headers = fill(cfg.Env, old.Env), fill(cfg.Headers, old.Headers)
	return cfg
}

// MCPServerInput は、画面から足す・変えるサーバー1つ。
type MCPServerInput struct {
	Name   string              `json:"name"`
	Config client.ServerConfig `json:"config"`
}

// Add は、サーバーを mcp.json に足す(フォームの1つか、貼った JSON のいくつか)。足したらすぐ使える(次にそのサーバーを使う送信で起動する)。
// 足したサーバーは、新しい会話で最初から使わない(NoteServers。2026-10-15 の決まり。0034 の決定事項 12)。お知らせの文を返す。
func (c *MCPConfig) Add(servers []MCPServerInput) ([]string, error) {
	if len(servers) == 0 {
		return nil, errors.New("足すサーバーがありません")
	}
	var names []string
	notices, err := c.change(func(next map[string]client.ServerConfig) error {
		for _, s := range servers {
			name := strings.TrimSpace(s.Name)
			if err := c.checkName(name, next); err != nil {
				return err
			}
			next[name] = s.Config
			names = append(names, name)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if _, err := c.prefs.NoteServers(slices.Sorted(maps.Keys(c.Servers()))); err != nil {
		log.Error(err.Error())
	}
	n := fmt.Sprintf("MCP のサーバー %s を足しました。新しい会話では最初から使いません(会話の「ツール」で選ぶと使えます。最初から使うなら、プロファイルの「ツール」で選びます)",
		strings.Join(names, ", "))
	log.Noticef("MCP のサーバー %s を足しました(%s)", strings.Join(names, ", "), c.file.Path())
	return append([]string{n}, notices...), nil
}

// checkName は、画面から足す名前が使えるか(空でない・settings.json にも mcp.json にも無い)。上書きはしない(変えるときはフォームで。0034 の決定事項 7)。
func (c *MCPConfig) checkName(name string, screen map[string]client.ServerConfig) error {
	switch {
	case name == "":
		return errors.New("サーバーの名前を入れてください")
	case hasKey(c.hand, name):
		return fmt.Errorf("MCP のサーバー %s は settings.json にあります(画面からは足せません。別の名前にしてください)", name)
	case hasKey(screen, name):
		return fmt.Errorf("MCP のサーバー %s はもうあります(上書きはしません。変えるときは一覧から選んでください)", name)
	}
	return nil
}

func hasKey[V any](m map[string]V, k string) bool {
	_, ok := m[k]
	return ok
}

// Update は、mcp.json のサーバー name の設定を変える。env と headers の空の値は前の値のまま。
// 動いていれば止める(次に使う送信で、新しい設定で起動する)。名前は変えられない(会話の設定と tools.json のキーのため。0034 の決定事項 11)。
func (c *MCPConfig) Update(name string, cfg client.ServerConfig) ([]string, error) {
	notices, err := c.change(func(next map[string]client.ServerConfig) error {
		old, err := c.editable(name, next)
		if err != nil {
			return err
		}
		next[name] = unmask(cfg, old)
		return nil
	})
	if err != nil {
		return nil, err
	}
	log.Noticef("MCP のサーバー %s の設定を変えました(%s)", name, c.file.Path())
	return notices, nil
}

// Delete は、mcp.json のサーバー name を消す。動いていれば止める。
// tools.json の扱いと seen は残す(戻したときに前の扱いのまま使えるように。settings.json から消したときと同じ。0034 の決定事項 11)。
func (c *MCPConfig) Delete(name string) ([]string, error) {
	notices, err := c.change(func(next map[string]client.ServerConfig) error {
		if _, err := c.editable(name, next); err != nil {
			return err
		}
		delete(next, name)
		return nil
	})
	if err != nil {
		return nil, err
	}
	log.Noticef("MCP のサーバー %s を消しました(%s)", name, c.file.Path())
	return notices, nil
}

// editable は、画面から変えられるサーバーか(mcp.json にある)を確かめ、今の設定を返す。
func (c *MCPConfig) editable(name string, screen map[string]client.ServerConfig) (client.ServerConfig, error) {
	old, ok := screen[name]
	if !ok {
		if hasKey(c.hand, name) {
			return old, fmt.Errorf("MCP のサーバー %s は settings.json に書いたものです(画面からは変えられません。settings.json を直して、アプリを起動し直してください)", name)
		}
		return old, fmt.Errorf("MCP のサーバー %s はありません", name)
	}
	return old, nil
}

// change は、mcp.json の分を edit で変えて、確かめてから書き、動いている係に流す。止めたサーバーがあれば、お知らせの文を返す。
//
// 確かめるのは settings.json の分と合わせた全体(名前がツールの名前で重なる・${workdir} を url に書いた、など。0034 の決定事項 14)。
// エラーなら何も書かない。
func (c *MCPConfig) change(edit func(next map[string]client.ServerConfig) error) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.file.Err(); err != nil {
		return nil, errors.New("画面から足した MCP のサーバーのファイルを読めなかったので、書き換えません(直すか消してから、アプリを起動し直してください)")
	}
	next := c.file.Servers()
	if err := edit(next); err != nil {
		return nil, err
	}
	merged, _ := MergeMCPServers(c.hand, next)
	if err := config.CheckMCPServers(c.file.Path(), merged); err != nil {
		return nil, err
	}
	if err := c.file.save(next); err != nil {
		return nil, err
	}
	// 試しにつないだのと同じ設定で保存したなら、そのとき受け取ったツールの一覧を覚える
	for name, t := range c.tried {
		if cfg, ok := next[name]; ok && cfg.Equal(t.cfg) {
			if err := c.prefs.setKnown(name, t.tools); err != nil {
				log.Warnf("MCP のサーバー %s のツールの一覧を覚えられません: %v", name, err)
			}
		}
	}
	c.tried = map[string]triedServer{}
	if c.mgr == nil {
		return nil, nil
	}
	return stopNotices(c.mgr.Update(merged), merged), nil
}

// stopNotices は、止めたサーバーのお知らせの文。変えたものは次に使う送信で起動し直すが、消したものはもう起動しないので、文を分ける。
func stopNotices(stopped []string, now map[string]client.ServerConfig) []string {
	var changed, removed []string
	for _, name := range stopped {
		if hasKey(now, name) {
			changed = append(changed, name)
		} else {
			removed = append(removed, name)
		}
	}
	var out []string
	if len(changed) > 0 {
		out = append(out, fmt.Sprintf("動いていた MCP のサーバー %s を止めました(次に使う送信で、新しい設定で起動します)", strings.Join(changed, ", ")))
	}
	if len(removed) > 0 {
		out = append(out, fmt.Sprintf("動いていた MCP のサーバー %s を、消したので止めました", strings.Join(removed, ", ")))
	}
	return out
}

// Try は、設定 cfg のサーバーを一時的に起動してツールの一覧を受け取り、止める(画面の「試しにつなぐ」。0034 の決定事項 8)。何も書かない。
// add が false なら、今あるサーバー name を変えるときで、env と headers の空の値は前の値で埋める(settings.json のサーバーも試せる)。
func (c *MCPConfig) Try(ctx context.Context, name string, cfg client.ServerConfig, add bool) ([]KnownTool, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("サーバーの名前を入れてください")
	}
	if !add {
		if old, ok := c.hand[name]; ok {
			cfg = unmask(cfg, old)
		} else if old, ok := c.file.Servers()[name]; ok {
			cfg = unmask(cfg, old)
		}
	}
	c.mu.Lock()
	mgr := c.mgr
	c.mu.Unlock()
	if mgr == nil {
		return nil, errors.New("MCP のサーバーの係がありません")
	}
	tools, err := mgr.TryTools(ctx, name, cfg)
	if err != nil {
		return nil, err
	}
	known := knownTools(tools)
	c.mu.Lock()
	c.tried[name] = triedServer{cfg: cfg, tools: known}
	c.mu.Unlock()
	return known, nil
}

// ParsedMCPServer は、貼った JSON から読み取ったサーバー1つ。
type ParsedMCPServer struct {
	Name   string              `json:"name"` // 名前の無い形({"command": ...} だけ)なら空(画面で打つ)
	Config client.ServerConfig `json:"config"`
	// Error は、足せない理由(形が違う・同じ名前がある)。空なら足せる
	Error string `json:"error,omitempty"`
}

// ParseMCPJSON は、貼った JSON を読み取る(0034 の決定事項 7)。受け付ける形は3つ:
//
//	{"mcpServers": {"名前": {...}}} … MCP サーバーの説明によくある形(Claude Desktop などの設定と同じ)
//	{"名前": {...}}                 … mcpServers の中身だけ
//	{"command": ...} か {"url": ...} … サーバー1つだけ(名前は画面で打つ)
//
// 外側の {} を付けずに写したもの("名前": {...})も、{} を補って読む。
// ほかのツールが足した項目(disabled・autoApprove など)は読まずに捨てる(貼れることを優先する)。
func (c *MCPConfig) ParseMCPJSON(text string) ([]ParsedMCPServer, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("JSON を貼ってください")
	}
	var top map[string]json.RawMessage
	err := json.Unmarshal([]byte(text), &top)
	if err != nil && !strings.HasPrefix(text, "{") {
		if json.Unmarshal([]byte("{"+strings.TrimSuffix(text, ",")+"}"), &top) == nil {
			err = nil
		}
	}
	if err != nil {
		return nil, fmt.Errorf("JSON として読めません(%s): %w", config.JSONErrorLine([]byte(text), err), err)
	}
	servers := map[string]json.RawMessage{}
	switch {
	case top["mcpServers"] != nil:
		if err := json.Unmarshal(top["mcpServers"], &servers); err != nil {
			return nil, fmt.Errorf("mcpServers の形が違います(名前 → 設定 の組で書きます): %w", err)
		}
	case top["command"] != nil || top["url"] != nil:
		b, err := json.Marshal(top) // {} を補って読んだときのために、読んだものから作り直す
		if err != nil {
			return nil, err
		}
		servers[""] = b
	default:
		servers = top
	}
	if len(servers) == 0 {
		return nil, errors.New("サーバーが見つかりません({\"mcpServers\": {\"名前\": {\"command\": …}}} の形で貼ってください)")
	}
	current := c.Servers()
	var out []ParsedMCPServer
	for _, name := range slices.Sorted(maps.Keys(servers)) {
		p := ParsedMCPServer{Name: name}
		switch err := json.Unmarshal(servers[name], &p.Config); {
		case err != nil:
			p.Error = fmt.Sprintf("サーバーの設定の形が違います: %v", err)
		case hasKey(c.hand, name):
			p.Error = "settings.json に同じ名前のサーバーがあります(足すなら別の名前にしてください)"
		case name != "" && hasKey(current, name):
			p.Error = "同じ名前のサーバーがあります(上書きはしません。変えるときは一覧から選んでください)"
		default:
			if err := p.Config.Validate(); err != nil {
				p.Error = err.Error()
			}
		}
		out = append(out, p)
	}
	return out, nil
}
