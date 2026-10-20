package aibow

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
	"uuid"
)

// startProfiles は、アプリの起動と同じ順に、保存先 st から設定を読んでプロファイルを準備する(model は最後に使ったモデル)。
func startProfiles(t *testing.T, st Store, model string, remote RemoteTools, prefs *ToolPrefs) (*Conversations, []string) {
	t.Helper()
	app, _, err := LoadAppSettings(st, AppSettings{Defaults: testDefaults})
	if err != nil {
		t.Fatal(err)
	}
	app.Workspace.Model = model
	cs := NewConversations(st, &fakeGen{}, app)
	if remote != nil {
		cs.SetRemoteTools(remote)
	}
	cs.SetToolPrefs(prefs)
	notes, err := cs.PrepareProfiles()
	if err != nil {
		t.Fatal(err)
	}
	return cs, notes
}

// プロファイルより前の DB を引き継ぐ:今の「新しい会話の既定」・最後に使ったモデル・最初から使うツールのグループから「既定」を作り、
// 今ある会話はそれに紐づける。2回目の起動では何もしない(0014 の 10)
func TestPrepareProfilesMigratesDefaults(t *testing.T) {
	st := newMemStore()
	// 前の版で保存した既定と、プロファイルを持たない会話
	legacy := testDefaults
	legacy.System, legacy.HistoryTokens = "前の版の既定", 6000
	legacy.Model = "" // プロファイルより前の既定にはモデルが無い
	SaveAppSetting(st, SettingDefaults, legacy)
	old := Conversation{ID: uuid.NewV7(), Title: "前の会話", Settings: legacy, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	st.CreateConversation(old)
	// 設定の画面の「ツール」で、組み込みのツールを最初から使わないにしていた
	prefs := &ToolPrefs{data: toolsFile{NotDefault: NotDefault{Builtin: true}}}

	cs, notes := startProfiles(t, st, "ollama:qwen3.5:4b", &fakeRemote{}, prefs)
	if len(notes) != 2 {
		t.Errorf("引き継ぎと紐づけを知らせる: %q", notes)
	}
	list, _ := st.ListProfiles()
	if len(list) != 1 {
		t.Fatalf("「既定」を1つ作る: %+v", list)
	}
	p := list[0]
	want := valuesOf(legacy.asDefaults())
	want.Model = "ollama:qwen3.5:4b"
	want.BuiltinOff, want.MCPServers = true, []string{"fake"}
	if p.Name != DefaultProfileName || !reflect.DeepEqual(p.Values, want) {
		t.Errorf("値:\n%+v\n期待:\n%+v", p.Values, want)
	}
	if cs.DefaultProfile().ID != p.ID {
		t.Error("新しい会話で最初に選ばれるのは「既定」")
	}
	if got, _, _ := cs.Get(old.ID); got.ProfileID != p.ID || !sameSettings(got.Settings, legacy) {
		t.Errorf("今ある会話は「既定」に紐づけ、中身は変えない: %+v", got)
	}
	if c := mustCreate(t, cs); c.ProfileID != p.ID || c.Settings.System != "前の版の既定" || !c.Settings.BuiltinOff {
		t.Errorf("新しい会話は「既定」から作る: %+v", c)
	}

	// 既定を変えると、プロファイルに保存する(前の置き場 conversation_defaults には書かない)
	d := cs.Defaults()
	d.System = "変えた既定"
	if err := cs.SetDefaults(d); err != nil {
		t.Fatal(err)
	}
	var stale ConversationSettings
	loadAppSetting(st, SettingDefaults, &stale)
	if stale.System != "前の版の既定" {
		t.Errorf("前の置き場には書かない: %q", stale.System)
	}

	// 2回目の起動:作り直さず、変えた既定を読む
	again, notes := startProfiles(t, st, "ollama:qwen3.5:9b", &fakeRemote{}, &ToolPrefs{})
	if list, _ := st.ListProfiles(); len(notes) != 0 || len(list) != 1 {
		t.Errorf("2回目は何もしない: %q %+v", notes, list)
	}
	if d := again.Defaults(); d.System != "変えた既定" || !d.BuiltinOff || !slices.Equal(d.MCPServers, []string{"fake"}) {
		t.Errorf("保存したプロファイルを読む: %+v", d)
	}
	if again.DefaultProfile().Values.Model != "ollama:qwen3.5:4b" {
		t.Error("プロファイルのモデルは引き継いだときのまま(最後に使ったモデルは写さない)")
	}
	app, _, _ := LoadAppSettings(st, AppSettings{Defaults: testDefaults})
	if app.Defaults.System != "変えた既定" {
		t.Errorf("アプリの設定の既定もプロファイルから読む(作業領域の最初の大きさに使う): %+v", app.Defaults)
	}
}

// 初めての起動(DB が空):「既定」の値は環境変数の初期値。足したばかりの MCP のサーバーは最初から使わない
func TestPrepareProfilesFirstRun(t *testing.T) {
	st := newMemStore()
	prefs := &ToolPrefs{data: toolsFile{NotDefault: NotDefault{Servers: []string{"fake"}}}}
	cs, notes := startProfiles(t, st, "", &fakeRemote{}, prefs)
	if len(notes) != 1 {
		t.Errorf("引き継ぎだけを知らせる(紐づける会話は無い): %q", notes)
	}
	d := cs.Defaults()
	if d.System != testDefaults.System || d.HistoryTokens != testDefaults.HistoryTokens || len(d.MCPServers) != 0 || d.BuiltinOff {
		t.Errorf("初期値から作る: %+v", d)
	}
}

// 新しい会話で最初に選ばれる印のプロファイルが無い・しまってあるときは、しまっていない最初のプロファイルに付け直す
func TestPrepareProfilesRepairsMark(t *testing.T) {
	st := newMemStore()
	startProfiles(t, st, "", nil, nil)
	list, _ := st.ListProfiles()
	first := list[0]
	first.Archived = true
	st.UpdateProfile(first)
	other := Profile{ID: uuid.NewV7(), Name: "コマンドなし", Values: first.Values}
	st.CreateProfile(other)

	cs, notes := startProfiles(t, st, "", nil, nil)
	if cs.DefaultProfile().ID != other.ID || len(notes) != 1 {
		t.Errorf("しまっていないプロファイルに付け直す: %+v %q", cs.DefaultProfile(), notes)
	}
	var id string
	if loadAppSetting(st, SettingDefaultProfile, &id); id != other.ID.String() {
		t.Errorf("付け直した印を保存する: %q", id)
	}
}

// プロファイルを準備する前(ライブラリとして使う場合)は、既定を保存できない。会話はプロファイルを持たずに作れる
func TestDefaultsBeforePrepare(t *testing.T) {
	cs := NewConversations(newMemStore(), &fakeGen{}, AppSettings{Defaults: testDefaults})
	if err := cs.SetDefaults(testDefaults); err == nil {
		t.Error("準備する前は保存できない")
	}
	if c := mustCreate(t, cs); c.ProfileID != (uuid.UUID{}) || c.Settings.System != testDefaults.System {
		t.Errorf("プロファイルを持たずに作る: %+v", c)
	}
}

// プロファイルを作る・名前と値と決まりを変える・しまう・消す(使っている会話が無いとき)・最初に選ぶものを変える(0014 の 11・完了の条件)
func TestManageProfiles(t *testing.T) {
	cs, _ := newTestConversations(t, &fakeGen{})
	def := cs.DefaultProfile()
	p, err := cs.CreateProfile(" コマンドなし ", def.Values, ProfileRules{Off: ProfileOff{Tools: []string{"run_command"}}})
	if err != nil || p.Name != "コマンドなし" {
		t.Fatalf("作る: %+v %v", p, err)
	}
	if _, err := cs.CreateProfile("", def.Values, ProfileRules{}); err == nil {
		t.Error("名前が空のプロファイルは作れない")
	}
	if _, err := cs.CreateProfile("x", def.Values, ProfileRules{Fixed: []string{"unknown"}}); err == nil {
		t.Error("知らない項目は固定できない")
	}
	if _, err := cs.CreateProfile(DefaultProfileName, def.Values, ProfileRules{}); err == nil {
		t.Error("ほかのプロファイルと同じ名前では作れない")
	}
	same := p
	same.Name = DefaultProfileName
	if err := cs.UpdateProfile(same); err == nil {
		t.Error("ほかのプロファイルと同じ名前には変えられない")
	}
	if err := cs.UpdateProfile(p); err != nil {
		t.Errorf("自分と同じ名前のまま保存できる: %v", err)
	}

	// 選んだプロファイルから会話を作ると、その値が写り、元のプロファイルを覚える
	p.Values.System = "コマンドなしの会話"
	p.Name = "コマンドなし(9B)"
	if err := cs.UpdateProfile(p); err != nil {
		t.Fatal(err)
	}
	c, err := cs.CreateIn(p.ID, nil)
	if err != nil || c.ProfileID != p.ID || c.Settings.System != "コマンドなしの会話" {
		t.Fatalf("選んだプロファイルから作る: %+v %v", c, err)
	}
	list, _ := cs.Profiles()
	if len(list) != 2 || !list[0].Default || list[1].Uses != 1 || list[1].Profile.Name != "コマンドなし(9B)" {
		t.Errorf("一覧(使っている会話の数・最初に選ぶもの): %+v", list)
	}

	// 使っている会話があれば消せない。しまうことはできる。しまったプロファイルからは新しい会話を作れない
	if err := cs.DeleteProfile(p.ID); err == nil {
		t.Error("使っている会話があれば消せない")
	}
	if err := cs.SetProfileArchived(p.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.CreateIn(p.ID, nil); err == nil {
		t.Error("しまったプロファイルからは作れない")
	}
	if got, _, _ := cs.Get(c.ID); got.Settings.profile == nil || got.Settings.profile.Rules.Off.Tools[0] != "run_command" {
		t.Error("しまっても、決まりは会話から引ける")
	}
	if err := cs.SetDefaultProfile(p.ID); err == nil {
		t.Error("しまったプロファイルは最初に選ぶものにできない")
	}

	// 最初に選ぶものはしまえない・消せない
	if err := cs.SetProfileArchived(def.ID, true); err == nil {
		t.Error("最初に選ぶプロファイルはしまえない")
	}
	if err := cs.DeleteProfile(def.ID); err == nil {
		t.Error("最初に選ぶプロファイルは消せない")
	}
	cs.SetProfileArchived(p.ID, false)
	if err := cs.SetDefaultProfile(p.ID); err != nil || cs.Defaults().System != "コマンドなしの会話" {
		t.Errorf("最初に選ぶものを変えると、新しい会話の既定も変わる: %v", err)
	}

	// 会話を消せば、本当に消せる
	cs.SetDefaultProfile(def.ID)
	cs.Delete(c.ID)
	if err := cs.DeleteProfile(p.ID); err != nil {
		t.Errorf("使っている会話が無ければ消せる: %v", err)
	}
}

// 会話の設定の画面の「プロファイルに保存」(上書き)と「新しいプロファイルとして保存」
func TestSaveConversationToProfile(t *testing.T) {
	cs, _ := newTestConversations(t, &fakeGen{})
	cs.SetRemoteTools(&fakeRemote{})
	c := mustCreate(t, cs)
	c.Settings.System, c.Settings.WorkDir, c.Settings.MCPServers = "会話で直した", t.TempDir(), []string{"fake"}
	if err := cs.UpdateSettings(c.ID, c.Settings); err != nil {
		t.Fatal(err)
	}
	if err := cs.SaveToProfile(c.ID, cs.DefaultProfile().ID); err != nil {
		t.Fatal(err)
	}
	if d := cs.Defaults(); d.System != "会話で直した" || !slices.Equal(d.MCPServers, []string{"fake"}) || d.WorkDir != "" {
		t.Errorf("値と使うグループを写し、作業フォルダは写さない: %+v", d)
	}
	p, err := cs.SaveAsProfile(c.ID, "直した版")
	if err != nil || p.Values.System != "会話で直した" || len(p.Rules.Fixed) != 0 {
		t.Errorf("新しいプロファイルとして保存: %+v %v", p, err)
	}
	if got, _, _ := cs.Get(c.ID); got.ProfileID == p.ID {
		t.Error("会話は元のプロファイルから作ったもののまま")
	}
}

// ツールごとの「最初は使わない」はプロファイルの値。会話を作るときに上書き(off)として写し、会話で選べば使える。
// この PC の既定(tools.json)の「既定で使わない」は、全部のプロファイルと今ある会話に移して tools.json から消す(動きは変えない)
func TestToolsOffMovesToProfiles(t *testing.T) {
	st := newMemStore()
	startProfiles(t, st, "", nil, nil)
	old := Conversation{ID: uuid.NewV7(), Settings: testDefaults, ProfileID: mustDefaultID(t, st), CreatedAt: time.Now(), UpdatedAt: time.Now()}
	old.Settings.ToolPolicies = map[string]ToolPolicy{"calculate": ToolOn} // この会話では使うにしていた
	st.CreateConversation(old)
	other := Conversation{ID: uuid.NewV7(), Settings: testDefaults, ProfileID: old.ProfileID, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	st.CreateConversation(other)

	path := filepath.Join(t.TempDir(), "tools.json")
	os.WriteFile(path, []byte(`{"tools": {"calculate": "off", "get_current_time": "allow"}, "seen": []}`), 0o644)
	prefs, err := LoadToolPrefs(path)
	if err != nil {
		t.Fatal(err)
	}
	cs, notes := startProfiles(t, st, "", nil, prefs)
	if len(notes) != 1 {
		t.Errorf("移したことを知らせる: %q", notes)
	}
	if v := cs.DefaultProfile().Values.ToolsOff; !slices.Equal(v, []string{"calculate"}) {
		t.Errorf("プロファイルの「最初は使わない」に移す: %v", v)
	}
	if got, _, _ := cs.Get(old.ID); got.Settings.ToolPolicies["calculate"] != ToolOn {
		t.Errorf("会話の上書きはそのまま: %v", got.Settings.ToolPolicies)
	}
	if got, _, _ := cs.Get(other.ID); got.Settings.ToolPolicies["calculate"] != ToolOff {
		t.Errorf("上書きの無い会話は、今までどおり使わない: %v", got.Settings.ToolPolicies)
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), `"off"`) || !strings.Contains(string(b), `"allow"`) {
		t.Errorf("tools.json から「使わない」だけ消す: %s", b)
	}

	// 新しい会話には上書き(off)として写り、会話で使うにもできる
	c := mustCreate(t, cs)
	if c.Settings.ToolPolicies["calculate"] != ToolOff {
		t.Errorf("新しい会話に写す: %v", c.Settings.ToolPolicies)
	}
	c.Settings.ToolPolicies = nil
	if err := cs.UpdateSettings(c.ID, c.Settings); err != nil {
		t.Errorf("会話で選べば使える: %v", err)
	}
	// 2回目の起動では何もしない
	if _, notes := startProfiles(t, st, "", nil, prefs); len(notes) != 0 {
		t.Errorf("2回目は何もしない: %q", notes)
	}
}

func mustDefaultID(t *testing.T, st Store) uuid.UUID {
	t.Helper()
	p, ok, err := storedDefaultProfile(st)
	if err != nil || !ok {
		t.Fatal(err)
	}
	return p.ID
}
