package aibow

import (
	"reflect"
	"slices"
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
	want := valuesOf(legacy.asDefaults(), "ollama:qwen3.5:4b")
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
