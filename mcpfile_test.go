package aibow

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/secondarykey/aibow/mcp/client"
	"github.com/secondarykey/aibow/tool"
)

// fakeMCP は、プロセスを起動しない MCPManager(差し替えた設定と、試した設定を覚える)。
type fakeMCP struct {
	configs map[string]client.ServerConfig
	stop    []string // 次の Update で止めたことにする名前
	tried   []client.ServerConfig
}

func (f *fakeMCP) Update(configs map[string]client.ServerConfig) []string {
	f.configs = configs
	s := f.stop
	f.stop = nil
	return s
}

func (f *fakeMCP) TryTools(_ context.Context, name string, cfg client.ServerConfig) ([]tool.Tool, error) {
	f.tried = append(f.tried, cfg)
	return tool.Builtin()[:1], nil
}

// mcp.json が無ければ空。壊れていたらエラーで、中身を使わず、書き換えない(0034 の決定事項 16)
func TestLoadMCPFile(t *testing.T) {
	dir := t.TempDir()
	f, err := LoadMCPFile(filepath.Join(dir, "none.json"))
	if err != nil || len(f.Servers()) != 0 || f.Err() != nil {
		t.Fatalf("無ければ空: %v %v", f.Servers(), err)
	}

	broken := filepath.Join(dir, "broken.json")
	os.WriteFile(broken, []byte("{\n  \"mcpServers\": {,\n}"), 0o644)
	f, err = LoadMCPFile(broken)
	if err == nil || !strings.Contains(err.Error(), "2 行目") || f.Err() == nil || len(f.Servers()) != 0 {
		t.Fatalf("壊れていたら何行目かの分かるエラー: %v", err)
	}
	c := NewMCPConfig("", nil, f, nil)
	if _, err := c.Add([]MCPServerInput{{Name: "a", Config: client.ServerConfig{Command: "a"}}}); err == nil {
		t.Error("壊れたファイルには書かない")
	}
	if b, _ := os.ReadFile(broken); string(b) != "{\n  \"mcpServers\": {,\n}" {
		t.Errorf("壊れたファイルを上書きしない: %s", b)
	}
	if l := c.List(); l.Error == "" {
		t.Error("画面に読めない理由を出す")
	}

	invalid := filepath.Join(dir, "invalid.json")
	os.WriteFile(invalid, []byte(`{"mcpServers": {"x": {}}}`), 0o644)
	if f, err := LoadMCPFile(invalid); err == nil || f.Err() == nil {
		t.Error("command も url も無いサーバーは、読めないのと同じ扱い")
	}
}

// 同じ名前は settings.json のほうを使い、起動したときにお知らせで伝える(0034 の決定事項 13)
func TestMergeMCPServers(t *testing.T) {
	hand := map[string]client.ServerConfig{"a": {Command: "hand-a"}, "b": {Command: "hand-b"}}
	screen := map[string]client.ServerConfig{"a": {Command: "screen-a"}, "c": {Command: "screen-c"}}
	merged, dup := MergeMCPServers(hand, screen)
	if len(merged) != 3 || merged["a"].Command != "hand-a" || merged["c"].Command != "screen-c" || !slices.Equal(dup, []string{"a"}) {
		t.Errorf("%v %v", merged, dup)
	}
	if screen["a"].Command != "screen-a" {
		t.Error("渡した map を変えない")
	}

	path := filepath.Join(t.TempDir(), "mcp.json")
	os.WriteFile(path, []byte(`{"mcpServers": {"a": {"command": "screen-a"}}}`), 0o644)
	f, _ := LoadMCPFile(path)
	c := NewMCPConfig("settings.json", hand, f, nil)
	if n := c.Notices(); len(n) != 1 || !strings.Contains(n[0], "settings.json のほうを使います") {
		t.Errorf("お知らせ: %v", n)
	}
	l := c.List()
	if len(l.Servers) != 3 || l.Servers[0].Source != MCPFromSettings || !l.Servers[1].Shadowed || l.Servers[1].Source != MCPFromScreen {
		t.Errorf("一覧は名前の順で、同じ名前は settings.json を先に、mcp.json のほうに印: %+v", l.Servers)
	}
}

// settings.json に手で書いたサーバーは、起動したときに mcp.json へ移し、settings.json からは mcpServers だけを消す(0034 の決定事項 13)
func TestMoveFromSettings(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.json")
	os.WriteFile(settings, []byte(`{
    "serverUrl": "http://127.0.0.1:8089",
    "mcpServers": {"a": {"command": "hand-a"}, "b": {"command": "hand-b", "env": {"TOKEN": "secret"}}},
    "custom": {"x": [1, 2]}
}`), 0o644)
	path := filepath.Join(dir, "mcp.json")
	os.WriteFile(path, []byte(`{"mcpServers": {"a": {"command": "screen-a"}, "c": {"command": "screen-c"}}}`), 0o644)

	s, err := LoadSettingsFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	f, _ := LoadMCPFile(path)
	left, notices := f.MoveFromSettings(settings, s.MCPServers)
	if left != nil || len(notices) != 1 || !strings.Contains(notices[0], "移しました") || !strings.Contains(notices[0], "a は mcp.json にもあった") {
		t.Fatalf("移した: %v %v", left, notices)
	}

	// mcp.json:同じ名前は settings.json の設定(動いていたほう)。env の値もそのまま移る
	f2, err := LoadMCPFile(path)
	got := f2.Servers()
	if err != nil || len(got) != 3 || got["a"].Command != "hand-a" || got["b"].Env["TOKEN"] != "secret" || got["c"].Command != "screen-c" {
		t.Errorf("mcp.json: %v %v", got, err)
	}

	// settings.json:mcpServers だけが消え、ほかの項目は並びも値も残る(aibow の知らない custom も)
	b, _ := os.ReadFile(settings)
	want := "{\n  \"serverUrl\": \"http://127.0.0.1:8089\",\n  \"custom\": {\"x\": [1, 2]}\n}\n"
	var a, w any
	json.Unmarshal(b, &a)
	json.Unmarshal([]byte(want), &w)
	if strings.Contains(string(b), "mcpServers") || strings.Index(string(b), "serverUrl") > strings.Index(string(b), "custom") || !jsonEqual(a, w) {
		t.Errorf("settings.json:\n%s", b)
	}
	if s, err := LoadSettingsFile(settings); err != nil || s.ServerURL != "http://127.0.0.1:8089" || len(s.MCPServers) != 0 {
		t.Errorf("書き直した settings.json を読める: %+v %v", s, err)
	}

	// もう一度起動しても何もしない
	if left, notices := f2.MoveFromSettings(settings, nil); left != nil || notices != nil {
		t.Errorf("移すものが無ければ何もしない: %v %v", left, notices)
	}
}

// 移せないとき(mcp.json が壊れている・合わせるとツールの名前で重なる)は、settings.json の分として持ち続け、settings.json も変えない
func TestMoveFromSettingsFails(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.json")
	orig := `{"mcpServers": {"a.b": {"command": "hand"}}}`
	os.WriteFile(settings, []byte(orig), 0o644)
	hand := map[string]client.ServerConfig{"a.b": {Command: "hand"}}

	broken := filepath.Join(dir, "broken.json")
	os.WriteFile(broken, []byte("{,"), 0o644)
	f, _ := LoadMCPFile(broken)
	if left, notices := f.MoveFromSettings(settings, hand); len(left) != 1 || notices != nil {
		t.Errorf("mcp.json が壊れていたら移さない(理由は Notices が出す): %v %v", left, notices)
	}

	clash := filepath.Join(dir, "clash.json")
	os.WriteFile(clash, []byte(`{"mcpServers": {"a_b": {"command": "screen"}}}`), 0o644)
	f, _ = LoadMCPFile(clash)
	left, notices := f.MoveFromSettings(settings, hand)
	if len(left) != 1 || len(notices) != 1 || !strings.Contains(notices[0], "移せませんでした") {
		t.Errorf("ツールの名前で重なるなら移さない: %v %v", left, notices)
	}
	if len(f.Servers()) != 1 {
		t.Errorf("mcp.json は変えない: %v", f.Servers())
	}
	if b, _ := os.ReadFile(settings); string(b) != orig {
		t.Errorf("settings.json は変えない: %s", b)
	}
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// 画面から足す・変える・消す。mcp.json に書き、動いている係に流す。settings.json の分は変えない
func TestMCPConfigEdit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	f, _ := LoadMCPFile(path)
	prefs, _ := LoadToolPrefs(filepath.Join(dir, "tools.json"))
	prefs.NoteServers([]string{"hand"}) // 前の起動で settings.json のサーバーを見ている
	hand := map[string]client.ServerConfig{"hand": {Command: "hand-server", Env: map[string]string{"TOKEN": "hand-secret"}}}
	c := NewMCPConfig("settings.json", hand, f, prefs)
	m := &fakeMCP{}
	c.SetManager(m)

	// 足す。足したサーバーは新しい会話で最初から使わない(seen に入る)
	api := client.ServerConfig{URL: "https://example.com/mcp", Headers: map[string]string{"Authorization": "Bearer secret"}}
	notices, err := c.Add([]MCPServerInput{{Name: " api ", Config: api}})
	if err != nil || len(notices) != 1 || !strings.Contains(notices[0], "最初から使いません") {
		t.Fatalf("足す: %v %v", notices, err)
	}
	if got := m.configs; len(got) != 2 || got["api"].URL != api.URL || got["hand"].Command != "hand-server" {
		t.Errorf("動いている係に、settings.json と合わせた全体を流す: %v", got)
	}
	var saved mcpFileData
	b, _ := os.ReadFile(path)
	if json.Unmarshal(b, &saved) != nil || len(saved.MCPServers) != 1 || saved.MCPServers["api"].Headers["Authorization"] != "Bearer secret" {
		t.Errorf("mcp.json には画面から足した分だけを書く: %s", b)
	}
	if !slices.Contains(prefs.data.Seen, "api") || !slices.Contains(prefs.data.NotDefault.Servers, "api") {
		t.Errorf("NoteServers: %+v", prefs.data)
	}

	// 一覧では headers・env の値を伏せる
	l := c.List()
	if len(l.Servers) != 2 || l.Servers[0].Name != "api" || l.Servers[0].Config.Headers["Authorization"] != "" ||
		l.Servers[1].Config.Env["TOKEN"] != "" || !hasKey(l.Servers[1].Config.Env, "TOKEN") {
		t.Errorf("伏せ字: %+v", l.Servers)
	}

	// 変える。空の値は前の値のまま。動いていれば止めたことを伝える
	m.stop = []string{"api"}
	notices, err = c.Update("api", client.ServerConfig{URL: "https://example.com/v2", Headers: map[string]string{"Authorization": "", "X-New": "1"}})
	if err != nil || len(notices) != 1 || !strings.Contains(notices[0], "止めました") {
		t.Fatalf("変える: %v %v", notices, err)
	}
	if h := c.file.Servers()["api"].Headers; h["Authorization"] != "Bearer secret" || h["X-New"] != "1" {
		t.Errorf("空のまま保存したら前の値: %v", h)
	}

	// 断るもの
	for _, tc := range []struct {
		name string
		do   func() error
		want string
	}{
		{"settings.json と同じ名前", func() error {
			_, err := c.Add([]MCPServerInput{{Name: "hand", Config: client.ServerConfig{Command: "x"}}})
			return err
		}, "settings.json にあります"},
		{"もうある名前", func() error { _, err := c.Add([]MCPServerInput{{Name: "api", Config: api}}); return err }, "もうあります"},
		{"名前が空", func() error { _, err := c.Add([]MCPServerInput{{Config: api}}); return err }, "名前"},
		{"settings.json のサーバーを変える", func() error { _, err := c.Update("hand", client.ServerConfig{Command: "x"}); return err }, "画面からは変えられません"},
		{"settings.json のサーバーを消す", func() error { _, err := c.Delete("hand"); return err }, "画面からは変えられません"},
		{"ツールの名前で重なる", func() error {
			_, err := c.Add([]MCPServerInput{{Name: "ap i", Config: client.ServerConfig{Command: "x"}}, {Name: "ap_i", Config: client.ServerConfig{Command: "y"}}})
			return err
		}, "区別できません"},
		{"形が違う", func() error {
			_, err := c.Add([]MCPServerInput{{Name: "bad", Config: client.ServerConfig{URL: "https://x/${workdir}"}}})
			return err
		}, "作業フォルダ"},
	} {
		if err := tc.do(); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
	if len(c.file.Servers()) != 1 {
		t.Errorf("断ったときは何も書かない: %v", c.file.Servers())
	}

	// 消す。tools.json の seen は残す
	if _, err := c.Delete("api"); err != nil {
		t.Fatal(err)
	}
	if len(c.file.Servers()) != 0 || len(m.configs) != 1 || !slices.Contains(prefs.data.Seen, "api") {
		t.Errorf("消す: %v %v %v", c.file.Servers(), m.configs, prefs.data.Seen)
	}
}

// 試しにつなぐ:何も書かない。前の値で埋めて試し、同じ設定で保存したら受け取った一覧を known に覚える
func TestMCPConfigTry(t *testing.T) {
	dir := t.TempDir()
	f, _ := LoadMCPFile(filepath.Join(dir, "mcp.json"))
	prefs, _ := LoadToolPrefs(filepath.Join(dir, "tools.json"))
	hand := map[string]client.ServerConfig{"hand": {Command: "hand-server", Env: map[string]string{"TOKEN": "hand-secret"}}}
	c := NewMCPConfig("", hand, f, prefs)
	m := &fakeMCP{}
	c.SetManager(m)

	tools, err := c.Try(context.Background(), "hand", client.ServerConfig{Command: "hand-server", Env: map[string]string{"TOKEN": ""}}, false)
	if err != nil || len(tools) != 1 || m.tried[0].Env["TOKEN"] != "hand-secret" {
		t.Fatalf("settings.json のサーバーも試せて、伏せた値は前の値で埋める: %v %v %v", tools, err, m.tried)
	}
	if _, err := os.Stat(filepath.Join(dir, "mcp.json")); err == nil {
		t.Error("試しただけでは書かない")
	}

	cfg := client.ServerConfig{Command: "new-server"}
	if _, err := c.Try(context.Background(), "new", cfg, true); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Add([]MCPServerInput{{Name: "new", Config: cfg}}); err != nil {
		t.Fatal(err)
	}
	if k := prefs.View().Known["new"]; len(k) != 1 {
		t.Errorf("試した一覧を覚える: %v", k)
	}
}

// 貼った JSON の3つの形と、{} を付けずに写したもの。同じ名前・形の違うものは理由を付けて返す
func TestParseMCPJSON(t *testing.T) {
	f, _ := LoadMCPFile("")
	c := NewMCPConfig("", map[string]client.ServerConfig{"hand": {Command: "x"}}, f, nil)
	for _, tc := range []struct {
		name, text string
		want       []string // 名前:エラーの有無
	}{
		{"mcpServers", `{"mcpServers": {"filesystem": {"command": "npx", "args": ["-y", "@modelcontextprotocol/server-filesystem", "C:/work"]}, "hand": {"command": "y"}}}`,
			[]string{"filesystem:", "hand:error"}},
		{"名前 → 設定", `{"a": {"url": "https://example.com/mcp", "disabled": false}, "b": {}}`, []string{"a:", "b:error"}},
		{"1つだけ", `{"command": "uvx", "args": ["blender-mcp"]}`, []string{":"}},
		{"{} なし", `"fs": {"command": "npx"},`, []string{"fs:"}},
	} {
		got, err := c.ParseMCPJSON(tc.text)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		var names []string
		for _, p := range got {
			e := ""
			if p.Error != "" {
				e = "error"
			}
			names = append(names, p.Name+":"+e)
		}
		if !slices.Equal(names, tc.want) {
			t.Errorf("%s: %v", tc.name, names)
		}
	}
	got, _ := c.ParseMCPJSON(`{"mcpServers": {"filesystem": {"command": "npx", "args": ["-y", "pkg", "C:/work"], "env": {"K": "v"}}}}`)
	if cfg := got[0].Config; cfg.Command != "npx" || len(cfg.Args) != 3 || cfg.Env["K"] != "v" {
		t.Errorf("中身も読む: %+v", cfg)
	}
	if _, err := c.ParseMCPJSON(`{"mcpServers": `); err == nil {
		t.Error("読めない JSON はエラー")
	}
	if _, err := c.ParseMCPJSON(" "); err == nil {
		t.Error("空はエラー")
	}
}
