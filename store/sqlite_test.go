package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"
	"uuid"

	"github.com/secondarykey/aibow"

	"github.com/secondarykey/aibow/engine"
	"github.com/secondarykey/aibow/format"
	"github.com/secondarykey/aibow/tool"
)

func openTemp(t *testing.T) (*SQLite, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sub", "aibow.db") // フォルダが無くても作られること
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

var settings = aibow.ConversationSettings{System: "sys", Temperature: 0.7, Think: true, PresencePenalty: 1.5, Compact: aibow.CompactSummary}

func newConv(t *testing.T, s *SQLite, at time.Time) aibow.Conversation {
	t.Helper()
	c := aibow.Conversation{ID: uuid.NewV7(), Title: "", Settings: settings, CreatedAt: at, UpdatedAt: at}
	if err := s.CreateConversation(c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestMigrationSetsUserVersion(t *testing.T) {
	s, path := openTemp(t)
	var v int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&v); err != nil || v != len(migrations) {
		t.Fatalf("user_version = %d(期待: %d)%v", v, len(migrations), err)
	}
	s.Close()

	// 開き直しても、作り変えはもう行われない(CREATE TABLE が2回目で失敗しない)
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s2.Close()
}

func TestConversationsAndMessagesPersist(t *testing.T) {
	s, path := openTemp(t)
	base := time.Now()
	old := newConv(t, s, base.Add(-time.Hour))
	c := newConv(t, s, base)

	user := aibow.StoredMessage{ID: uuid.NewV7(), ConversationID: c.ID, Role: "user", Content: "やあ", CreatedAt: base}
	reply := aibow.StoredMessage{
		ID: uuid.NewV7(), ConversationID: c.ID, Role: "assistant", Content: "こんにちは", Thinking: "挨拶だ",
		Model: "qwen3.5:4b", CreatedAt: base,
		Stats: &aibow.ReplyStats{GenerateStats: engine.GenerateStats{GenTokens: 12, StopReason: "eos"}, FirstSec: 0.5, ThinkSec: 1.25},
	}
	// 追加の順番をわざと逆にしても、読み出しは ID(作った順)で並ぶ
	for _, m := range []aibow.StoredMessage{reply, user} {
		if err := s.AddMessage(m); err != nil {
			t.Fatal(err)
		}
	}
	c.Title = "やあ"
	c.UpdatedAt = base.Add(time.Minute)
	if err := s.UpdateConversation(c); err != nil {
		t.Fatal(err)
	}
	s.Close()

	// 開き直して(= アプリの再起動)、中身が残っているか
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	list, err := s.ListConversations()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != c.ID || list[1].ID != old.ID {
		t.Fatalf("一覧が更新の新しい順になっていません: %+v", list)
	}
	if list[0].Title != "やあ" || !reflect.DeepEqual(list[0].Settings, settings) {
		t.Errorf("タイトルか設定が違います: %+v", list[0])
	}
	if !list[0].UpdatedAt.Equal(c.UpdatedAt.Truncate(time.Microsecond)) {
		t.Errorf("更新日時: %v(期待: %v)", list[0].UpdatedAt, c.UpdatedAt)
	}

	_, ms, err := s.Conversation(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 || ms[0].ID != user.ID || ms[1].ID != reply.ID {
		t.Fatalf("発言の並びが違います: %+v", ms)
	}
	// モデルに渡さない印(版 4。0011)
	if ms[0].Excluded || ms[1].Excluded {
		t.Errorf("印は最初は付いていない")
	}
	if err := s.SetMessagesExcluded([]uuid.UUID{reply.ID}, true); err != nil {
		t.Fatal(err)
	}
	if _, again, _ := s.Conversation(c.ID); again[0].Excluded || !again[1].Excluded {
		t.Errorf("印を付けた発言だけ: %v %v", again[0].Excluded, again[1].Excluded)
	}
	defer func() {
		// 発言を消す(作り直す・送り直す。0011)。ほかのテストの確かめが済んでから
		if err := s.DeleteMessages([]uuid.UUID{reply.ID}); err != nil {
			t.Fatal(err)
		}
		if _, left, _ := s.Conversation(c.ID); len(left) != 1 || left[0].ID != user.ID {
			t.Errorf("消した発言だけが無くなる: %+v", left)
		}
	}()
	got := ms[1]
	if got.Thinking != "挨拶だ" || got.Model != "qwen3.5:4b" || got.Stats == nil || *got.Stats != *reply.Stats {
		t.Errorf("返事の中身が違います: %+v / %+v", got, got.Stats)
	}
	if ms[0].Stats != nil {
		t.Errorf("ユーザー発言の stats は NULL のはずです: %+v", ms[0].Stats)
	}
}

func TestDeleteCascadesMessages(t *testing.T) {
	s, _ := openTemp(t)
	c := newConv(t, s, time.Now())
	if err := s.AddMessage(aibow.StoredMessage{ID: uuid.NewV7(), ConversationID: c.ID, Role: "user", Content: "x", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteConversation(c.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Conversation(c.ID); !errors.Is(err, aibow.ErrNotFound) {
		t.Errorf("削除した会話が残っています: %v", err)
	}
	var n int
	s.db.QueryRow("SELECT COUNT(*) FROM messages").Scan(&n)
	if n != 0 {
		t.Errorf("会話を消しても発言が %d 件残っています(foreign_keys が効いていない)", n)
	}
	if err := s.UpdateConversation(c); !errors.Is(err, aibow.ErrNotFound) {
		t.Errorf("無い会話の更新は ErrNotFound のはずです: %v", err)
	}
}

func TestAppSettings(t *testing.T) {
	s, _ := openTemp(t)
	if _, ok, err := s.AppSetting("history_tokens"); ok || err != nil {
		t.Fatalf("まだ無いはずです: ok=%v err=%v", ok, err)
	}
	for _, v := range []string{"4096", "8192"} { // 2回目は上書き
		if err := s.SetAppSetting("history_tokens", v); err != nil {
			t.Fatal(err)
		}
	}
	if v, ok, err := s.AppSetting("history_tokens"); !ok || err != nil || v != "8192" {
		t.Errorf("値: %q ok=%v err=%v", v, ok, err)
	}

	// aibow.LoadAppSettings と組み合わせて使えること
	aibow.SaveAppSetting(s, aibow.SettingDefaults, settings)
	app, _, err := aibow.LoadAppSettings(s, aibow.AppSettings{
		Workspace: aibow.WorkspaceSettings{GPULayers: 999},
		Defaults:  aibow.ConversationSettings{ReplyTokens: 100},
	})
	w := app.Workspace
	want := settings
	want.MCPServers = []string{} // 既定として読むときは整えた形(使う MCP のサーバーは空の配列)になる
	// 既定に会話の上限・返事の上限が入っていなければ、古い置き場(history_tokens)か初期値を使う
	want.HistoryTokens, want.ReplyTokens = 8192, 100
	if err != nil || w.GPULayers != 999 || !reflect.DeepEqual(app.Defaults, want) {
		t.Errorf("アプリ設定: %+v %v", app, err)
	}
}

func TestTouchConversation(t *testing.T) {
	s, _ := openTemp(t)
	base := time.Now()
	c := newConv(t, s, base)

	// タイトルが空なら自動のタイトルが入り、更新日時が進む
	if err := s.TouchConversation(c.ID, base.Add(time.Minute), "自動"); err != nil {
		t.Fatal(err)
	}
	// タイトルがあれば変えない(日時だけ進む)
	if err := s.TouchConversation(c.ID, base.Add(2*time.Minute), "別の自動"); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.Conversation(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "自動" || !got.UpdatedAt.Equal(base.Add(2*time.Minute).Truncate(time.Microsecond)) {
		t.Errorf("タイトル %q / 更新日時 %v", got.Title, got.UpdatedAt)
	}
	if err := s.TouchConversation(uuid.NewV7(), base, "x"); !errors.Is(err, aibow.ErrNotFound) {
		t.Errorf("無い会話は ErrNotFound のはずです: %v", err)
	}
}

// 版 1 の DB(compact の機能より前)を開くと、要約の表が足され、今ある会話はそのまま残る
func TestMigrationFromVersion1(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(migrations[0] + "PRAGMA user_version = 1;"); err != nil {
		t.Fatal(err)
	}
	// 古い版の設定: max_tokens があり、compact が無い
	old := uuid.NewV7()
	now := formatTime(time.Now())
	if _, err := db.Exec(`INSERT INTO conversations VALUES (?, '古い会話', '{"system":"sys","max_tokens":4096,"temperature":0.7,"think":true}', ?, ?)`,
		old.String(), now, now); err != nil {
		t.Fatal(err)
	}
	db.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c, _, err := s.Conversation(old)
	if err != nil || c.Title != "古い会話" || c.Settings.System != "sys" {
		t.Fatalf("今ある会話: %+v %v", c, err)
	}

	// 要約を保存して読める。開き直しても残っている
	msg := uuid.NewV7()
	comp := aibow.Compaction{
		ID: uuid.NewV7(), ConversationID: old, UptoMessageID: msg, Summary: "- 要約", Model: "qwen3.5:4b",
		Stats:     &aibow.CompactStats{BeforeTokens: 900, AfterTokens: 300, Messages: 4, Sec: 1.5},
		CreatedAt: time.Now(),
	}
	if err := s.AddCompaction(comp); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, _ = Open(path)
	defer s.Close() // 開き直した方も閉じる(閉じないと Windows では一時フォルダを消せずにテストが失敗する)
	list, err := s.Compactions(old)
	if err != nil || len(list) != 1 {
		t.Fatalf("要約: %+v %v", list, err)
	}
	got := list[0]
	if got.UptoMessageID != msg || got.Summary != "- 要約" || got.Model != "qwen3.5:4b" || *got.Stats != *comp.Stats {
		t.Errorf("読み戻した要約: %+v", got)
	}

	// 会話を消すと要約も消える(ON DELETE CASCADE)
	if err := s.DeleteConversation(old); err != nil {
		t.Fatal(err)
	}
	var n int
	s.db.QueryRow("SELECT count(*) FROM compactions").Scan(&n)
	if n != 0 {
		t.Errorf("会話を消しても要約が %d 件残っています", n)
	}
}

// ツールの呼び出しと結果が、開き直しても同じように読めること
func TestToolMessagesPersist(t *testing.T) {
	s, path := openTemp(t)
	c := newConv(t, s, time.Now())
	call := format.ToolCall{Call: tool.Call{Name: "calculate", Args: []tool.Arg{{Name: "expression", Value: "7/2"}}}}
	broken := format.ToolCall{Raw: "<tool_call>?</tool_call>", Error: "書式が崩れています"}
	reply := aibow.StoredMessage{ID: uuid.NewV7(), ConversationID: c.ID, Role: "assistant", ToolCalls: []format.ToolCall{call, broken}, CreatedAt: time.Now()}
	result := aibow.StoredMessage{ID: uuid.NewV7(), ConversationID: c.ID, Role: "tool", Content: "3.5",
		ToolResult: &aibow.ToolResult{Call: call, Sec: 0.01, Confirm: &aibow.Confirmation{Approved: false, Reason: "消さないで"}}, CreatedAt: time.Now()}
	for _, m := range []aibow.StoredMessage{reply, result} {
		if err := s.AddMessage(m); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, ms, err := s.Conversation(c.ID)
	if err != nil || len(ms) != 2 {
		t.Fatalf("%+v %v", ms, err)
	}
	if !reflect.DeepEqual(ms[0].ToolCalls, reply.ToolCalls) || ms[0].ToolResult != nil {
		t.Errorf("呼び出し: %+v / %+v", ms[0].ToolCalls, ms[0].ToolResult)
	}
	if ms[1].Content != "3.5" || ms[1].ToolResult == nil || !reflect.DeepEqual(*ms[1].ToolResult, *result.ToolResult) || ms[1].ToolCalls != nil {
		t.Errorf("結果: %+v / %+v", ms[1], ms[1].ToolResult)
	}
}

// 版 2 の DB(ツールの列が無い)を開くと列が足され、今までの発言はツールの情報なしで読めること
func TestMigrationFromVersion2(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v2.db")
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(migrations[0] + migrations[1] + "PRAGMA user_version = 2;"); err != nil {
		t.Fatal(err)
	}
	conv, msg := uuid.NewV7(), uuid.NewV7()
	now := formatTime(time.Now())
	if _, err := db.Exec(`INSERT INTO conversations VALUES (?, 't', '{"system":"","think":true,"compact":"summary"}', ?, ?)`, conv.String(), now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO messages VALUES (?, ?, 'assistant', 'こんにちは', '', 'm', NULL, ?)`, msg.String(), conv.String(), now); err != nil {
		t.Fatal(err)
	}
	db.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, ms, err := s.Conversation(conv)
	if err != nil || len(ms) != 1 || ms[0].Content != "こんにちは" || ms[0].ToolCalls != nil || ms[0].ToolResult != nil {
		t.Fatalf("今ある発言: %+v %v", ms, err)
	}
}

// プロファイル(0014)を保存して読める。会話はどのプロファイルから作ったかを覚え、使っている会話があるプロファイルは消せない
func TestProfilesPersist(t *testing.T) {
	s, path := openTemp(t)
	now := time.Now()
	p := aibow.Profile{
		ID: uuid.NewV7(), Name: "既定", CreatedAt: now, UpdatedAt: now,
		Values: aibow.ProfileValues{Model: "ollama:qwen3.5:4b", System: "sys", Temperature: 0.7, Compact: aibow.CompactSummary,
			HistoryTokens: 8192, ReplyTokens: 4096, MCPServers: []string{"filesystem"}},
		Rules: aibow.ProfileRules{Fixed: []string{aibow.FixModel}, Off: aibow.ProfileOff{Servers: []string{"Blender"}, Tools: []string{"run_command"}}},
	}
	if err := s.CreateProfile(p); err != nil {
		t.Fatal(err)
	}
	c := aibow.Conversation{ID: uuid.NewV7(), Settings: settings, ProfileID: p.ID, CreatedAt: now, UpdatedAt: now}
	if err := s.CreateConversation(c); err != nil {
		t.Fatal(err)
	}
	free := newConv(t, s, now) // プロファイルを持たない会話(NULL)
	s.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.Profile(p.ID)
	if err != nil || got.Name != "既定" || !reflect.DeepEqual(got.Values, p.Values) || !reflect.DeepEqual(got.Rules, p.Rules) || got.Archived {
		t.Fatalf("読めない: %+v %v", got, err)
	}
	if c2, _, _ := s.Conversation(c.ID); c2.ProfileID != p.ID {
		t.Errorf("会話のプロファイル: %v", c2.ProfileID)
	}
	if f, _, _ := s.Conversation(free.ID); f.ProfileID != (uuid.UUID{}) {
		t.Errorf("プロファイルを持たない会話は空: %v", f.ProfileID)
	}

	// 名前・値・しまったかを書き換える
	got.Name, got.Values.Think, got.Archived, got.UpdatedAt = "コマンドなし", true, true, now.Add(time.Minute)
	if err := s.UpdateProfile(got); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.ListProfiles(); len(list) != 1 || list[0].Name != "コマンドなし" || !list[0].Values.Think || !list[0].Archived {
		t.Errorf("書き換えた: %+v", list)
	}
	if err := s.UpdateProfile(aibow.Profile{ID: uuid.NewV7()}); !errors.Is(err, aibow.ErrProfileNotFound) {
		t.Errorf("無いプロファイルの書き換え: %v", err)
	}

	// 使っている会話があれば消せない。会話を消せば消せる
	if err := s.DeleteProfile(p.ID); !errors.Is(err, aibow.ErrProfileInUse) {
		t.Fatalf("使っている会話があれば消せない: %v", err)
	}
	if err := s.DeleteConversation(c.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteProfile(p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Profile(p.ID); !errors.Is(err, aibow.ErrProfileNotFound) {
		t.Errorf("消えていない: %v", err)
	}
	// 外部キー:無いプロファイルから作った会話は保存できない
	bad := aibow.Conversation{ID: uuid.NewV7(), Settings: settings, ProfileID: uuid.NewV7(), CreatedAt: now, UpdatedAt: now}
	if err := s.CreateConversation(bad); err == nil {
		t.Error("無いプロファイルを指す会話を保存できてしまう")
	}
}

// 版 4 の DB(プロファイルより前)を開くと、表と列が足され、今ある会話はプロファイルを持たない(NULL)まま読める
func TestMigrationFromVersion4(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(migrations[0] + migrations[1] + migrations[2] + migrations[3] + "PRAGMA user_version = 4;"); err != nil {
		t.Fatal(err)
	}
	old := uuid.NewV7()
	now := formatTime(time.Now())
	if _, err := db.Exec(`INSERT INTO conversations VALUES (?, '古い会話', '{"system":"sys","history_tokens":8192,"reply_tokens":4096}', ?, ?)`,
		old.String(), now, now); err != nil {
		t.Fatal(err)
	}
	db.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c, _, err := s.Conversation(old)
	if err != nil || c.Title != "古い会話" || c.ProfileID != (uuid.UUID{}) {
		t.Fatalf("今ある会話: %+v %v", c, err)
	}
	if list, err := s.ListProfiles(); err != nil || len(list) != 0 {
		t.Errorf("プロファイルはまだ無い: %+v %v", list, err)
	}
}
