package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
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

// ctx は、テストで保存先に渡す ctx(ログインなしの形の利用者のもの)。
var ctx = aibow.WithUser(context.Background(), aibow.LocalUser)

var settings = aibow.ConversationSettings{System: "sys", Temperature: 0.7, Think: true, PresencePenalty: 1.5, Compact: aibow.CompactSummary}

func newConv(t *testing.T, s *SQLite, at time.Time) aibow.Conversation {
	t.Helper()
	c := aibow.Conversation{ID: uuid.NewV7(), Title: "", Settings: settings, CreatedAt: at, UpdatedAt: at}
	if err := s.CreateConversation(ctx, c); err != nil {
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
		if err := s.AddMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	c.Title = "やあ"
	c.UpdatedAt = base.Add(time.Minute)
	if err := s.UpdateConversation(ctx, c); err != nil {
		t.Fatal(err)
	}
	s.Close()

	// 開き直して(= アプリの再起動)、中身が残っているか
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	list, err := s.ListConversations(ctx)
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

	_, ms, err := s.Conversation(ctx, c.ID)
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
	if err := s.SetMessagesExcluded(ctx, []uuid.UUID{reply.ID}, true); err != nil {
		t.Fatal(err)
	}
	if _, again, _ := s.Conversation(ctx, c.ID); again[0].Excluded || !again[1].Excluded {
		t.Errorf("印を付けた発言だけ: %v %v", again[0].Excluded, again[1].Excluded)
	}
	defer func() {
		// 発言を消す(作り直す・送り直す。0011)。ほかのテストの確かめが済んでから
		if err := s.DeleteMessages(ctx, []uuid.UUID{reply.ID}); err != nil {
			t.Fatal(err)
		}
		if _, left, _ := s.Conversation(ctx, c.ID); len(left) != 1 || left[0].ID != user.ID {
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
	if err := s.AddMessage(ctx, aibow.StoredMessage{ID: uuid.NewV7(), ConversationID: c.ID, Role: "user", Content: "x", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteConversation(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Conversation(ctx, c.ID); !errors.Is(err, aibow.ErrNotFound) {
		t.Errorf("削除した会話が残っています: %v", err)
	}
	var n int
	s.db.QueryRow("SELECT COUNT(*) FROM messages").Scan(&n)
	if n != 0 {
		t.Errorf("会話を消しても発言が %d 件残っています(foreign_keys が効いていない)", n)
	}
	if err := s.UpdateConversation(ctx, c); !errors.Is(err, aibow.ErrNotFound) {
		t.Errorf("無い会話の更新は ErrNotFound のはずです: %v", err)
	}
}

func TestAppSettings(t *testing.T) {
	s, _ := openTemp(t)
	if _, ok, err := s.AppSetting(ctx, "history_tokens"); ok || err != nil {
		t.Fatalf("まだ無いはずです: ok=%v err=%v", ok, err)
	}
	for _, v := range []string{"4096", "8192"} { // 2回目は上書き
		if err := s.SetAppSetting(ctx, "history_tokens", v); err != nil {
			t.Fatal(err)
		}
	}
	if v, ok, err := s.AppSetting(ctx, "history_tokens"); !ok || err != nil || v != "8192" {
		t.Errorf("値: %q ok=%v err=%v", v, ok, err)
	}

	// aibow.LoadAppSettings と組み合わせて使えること
	aibow.SaveAppSetting(ctx, s, aibow.SettingDefaults, settings)
	app, _, err := aibow.LoadAppSettings(ctx, s, aibow.AppSettings{
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
	if err := s.TouchConversation(ctx, c.ID, base.Add(time.Minute), "自動"); err != nil {
		t.Fatal(err)
	}
	// タイトルがあれば変えない(日時だけ進む)
	if err := s.TouchConversation(ctx, c.ID, base.Add(2*time.Minute), "別の自動"); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.Conversation(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "自動" || !got.UpdatedAt.Equal(base.Add(2*time.Minute).Truncate(time.Microsecond)) {
		t.Errorf("タイトル %q / 更新日時 %v", got.Title, got.UpdatedAt)
	}
	if err := s.TouchConversation(ctx, uuid.NewV7(), base, "x"); !errors.Is(err, aibow.ErrNotFound) {
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
	c, _, err := s.Conversation(ctx, old)
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
	if err := s.AddCompaction(ctx, comp); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, _ = Open(path)
	defer s.Close() // 開き直した方も閉じる(閉じないと Windows では一時フォルダを消せずにテストが失敗する)
	list, err := s.Compactions(ctx, old)
	if err != nil || len(list) != 1 {
		t.Fatalf("要約: %+v %v", list, err)
	}
	got := list[0]
	if got.UptoMessageID != msg || got.Summary != "- 要約" || got.Model != "qwen3.5:4b" || *got.Stats != *comp.Stats {
		t.Errorf("読み戻した要約: %+v", got)
	}

	// 会話を消すと要約も消える(ON DELETE CASCADE)
	if err := s.DeleteConversation(ctx, old); err != nil {
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
		if err := s.AddMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, ms, err := s.Conversation(ctx, c.ID)
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
	_, ms, err := s.Conversation(ctx, conv)
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
	if err := s.CreateProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	c := aibow.Conversation{ID: uuid.NewV7(), Settings: settings, ProfileID: p.ID, CreatedAt: now, UpdatedAt: now}
	if err := s.CreateConversation(ctx, c); err != nil {
		t.Fatal(err)
	}
	free := newConv(t, s, now) // プロファイルを持たない会話(NULL)
	s.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.Profile(ctx, p.ID)
	if err != nil || got.Name != "既定" || !reflect.DeepEqual(got.Values, p.Values) || !reflect.DeepEqual(got.Rules, p.Rules) || got.Archived {
		t.Fatalf("読めない: %+v %v", got, err)
	}
	if c2, _, _ := s.Conversation(ctx, c.ID); c2.ProfileID != p.ID {
		t.Errorf("会話のプロファイル: %v", c2.ProfileID)
	}
	if f, _, _ := s.Conversation(ctx, free.ID); f.ProfileID != (uuid.UUID{}) {
		t.Errorf("プロファイルを持たない会話は空: %v", f.ProfileID)
	}

	// 名前・値・しまったかを書き換える
	got.Name, got.Values.Think, got.Archived, got.UpdatedAt = "コマンドなし", true, true, now.Add(time.Minute)
	if err := s.UpdateProfile(ctx, got); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.ListProfiles(ctx); len(list) != 1 || list[0].Name != "コマンドなし" || !list[0].Values.Think || !list[0].Archived {
		t.Errorf("書き換えた: %+v", list)
	}
	if err := s.UpdateProfile(ctx, aibow.Profile{ID: uuid.NewV7()}); !errors.Is(err, aibow.ErrProfileNotFound) {
		t.Errorf("無いプロファイルの書き換え: %v", err)
	}

	// 使っている会話があれば消せない。会話を消せば消せる
	if err := s.DeleteProfile(ctx, p.ID); !errors.Is(err, aibow.ErrProfileInUse) {
		t.Fatalf("使っている会話があれば消せない: %v", err)
	}
	if err := s.DeleteConversation(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteProfile(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Profile(ctx, p.ID); !errors.Is(err, aibow.ErrProfileNotFound) {
		t.Errorf("消えていない: %v", err)
	}
	// 外部キー:無いプロファイルから作った会話は保存できない
	bad := aibow.Conversation{ID: uuid.NewV7(), Settings: settings, ProfileID: uuid.NewV7(), CreatedAt: now, UpdatedAt: now}
	if err := s.CreateConversation(ctx, bad); err == nil {
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
	c, _, err := s.Conversation(ctx, old)
	if err != nil || c.Title != "古い会話" || c.ProfileID != (uuid.UUID{}) {
		t.Fatalf("今ある会話: %+v %v", c, err)
	}
	if list, err := s.ListProfiles(ctx); err != nil || len(list) != 0 {
		t.Errorf("プロファイルはまだ無い: %+v %v", list, err)
	}
}

// 持ち主(0017):ほかの人の会話・発言・要約・プロファイル・人ごとの設定は、無いものとして扱う。アプリ全体の設定は分けない
func TestOwnersAreSeparate(t *testing.T) {
	s, _ := openTemp(t)
	other := aibow.WithUser(context.Background(), uuid.NewV7())
	now := time.Now()

	// 自分(ctx)の会話とプロファイル
	mine := newConv(t, s, now)
	msg := aibow.StoredMessage{ID: uuid.NewV7(), ConversationID: mine.ID, Role: "user", Content: "私の", CreatedAt: now}
	if err := s.AddMessage(ctx, msg); err != nil {
		t.Fatal(err)
	}
	p := aibow.Profile{ID: uuid.NewV7(), Name: "既定", CreatedAt: now, UpdatedAt: now}
	if err := s.CreateProfile(ctx, p); err != nil {
		t.Fatal(err)
	}

	// ほかの人からは見えない
	if list, err := s.ListConversations(other); err != nil || len(list) != 0 {
		t.Errorf("ほかの人の一覧に出ている: %+v %v", list, err)
	}
	if _, _, err := s.Conversation(other, mine.ID); !errors.Is(err, aibow.ErrNotFound) {
		t.Errorf("ほかの人の会話を開けてしまう: %v", err)
	}
	if list, err := s.ListProfiles(other); err != nil || len(list) != 0 {
		t.Errorf("ほかの人のプロファイルが見える: %+v %v", list, err)
	}
	if _, err := s.Profile(other, p.ID); !errors.Is(err, aibow.ErrProfileNotFound) {
		t.Errorf("ほかの人のプロファイルを読めてしまう: %v", err)
	}
	if list, err := s.Compactions(other, mine.ID); err != nil || len(list) != 0 {
		t.Errorf("ほかの人の要約が見える: %+v %v", list, err)
	}

	// ほかの人からは書き換えられない(無い会話と同じエラーか、何も起きない)
	changed := mine
	changed.Title = "書き換えた"
	if err := s.UpdateConversation(other, changed); !errors.Is(err, aibow.ErrNotFound) {
		t.Errorf("ほかの人の会話を書き換えられる: %v", err)
	}
	if err := s.TouchConversation(other, mine.ID, now, "自動"); !errors.Is(err, aibow.ErrNotFound) {
		t.Errorf("ほかの人の会話の日時を進められる: %v", err)
	}
	if err := s.AddMessage(other, aibow.StoredMessage{ID: uuid.NewV7(), ConversationID: mine.ID, Role: "user", CreatedAt: now}); !errors.Is(err, aibow.ErrNotFound) {
		t.Errorf("ほかの人の会話に発言を足せる: %v", err)
	}
	if err := s.AddCompaction(other, aibow.Compaction{ID: uuid.NewV7(), ConversationID: mine.ID, UptoMessageID: msg.ID, CreatedAt: now}); !errors.Is(err, aibow.ErrNotFound) {
		t.Errorf("ほかの人の会話に要約を足せる: %v", err)
	}
	if err := s.SetMessagesExcluded(other, []uuid.UUID{msg.ID}, true); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteMessages(other, []uuid.UUID{msg.ID}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteConversation(other, mine.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateProfile(other, p); !errors.Is(err, aibow.ErrProfileNotFound) {
		t.Errorf("ほかの人のプロファイルを書き換えられる: %v", err)
	}
	if err := s.DeleteProfile(other, p.ID); !errors.Is(err, aibow.ErrProfileNotFound) {
		t.Errorf("ほかの人のプロファイルを消せる: %v", err)
	}
	c, ms, err := s.Conversation(ctx, mine.ID)
	if err != nil || c.Title != "" || len(ms) != 1 || ms[0].Excluded {
		t.Errorf("ほかの人の操作で自分の会話が変わった: %+v %+v %v", c, ms, err)
	}
	if _, err := s.Profile(ctx, p.ID); err != nil {
		t.Errorf("ほかの人の操作で自分のプロファイルが消えた: %v", err)
	}

	// 人ごとの設定は分かれ、アプリ全体の設定は同じものを見る
	if err := s.SetUserSetting(ctx, aibow.SettingTheme, `"dark"`); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.UserSetting(other, aibow.SettingTheme); ok || err != nil {
		t.Errorf("ほかの人の表示の設定が見える: ok=%v %v", ok, err)
	}
	if err := s.SetUserSetting(other, aibow.SettingTheme, `"light"`); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := s.UserSetting(ctx, aibow.SettingTheme); v != `"dark"` {
		t.Errorf("ほかの人が変えると自分の設定も変わる: %s", v)
	}
	if err := s.SetAppSetting(ctx, aibow.SettingLastModel, `"m"`); err != nil {
		t.Fatal(err)
	}
	if v, ok, _ := s.AppSetting(other, aibow.SettingLastModel); !ok || v != `"m"` {
		t.Errorf("アプリ全体の設定は人によらない: %q %v", v, ok)
	}
}

// 利用者が ctx に無い呼び出しは断る。OneUser にした DB(デスクトップのアプリ)は、ログインなしの形の利用者のものとして扱う
func TestNoUser(t *testing.T) {
	s, _ := openTemp(t)
	bare := context.Background()
	if _, err := s.ListConversations(bare); !errors.Is(err, aibow.ErrNoUser) {
		t.Errorf("利用者が無いのに読めてしまう: %v", err)
	}
	if err := s.SetUserSetting(bare, aibow.SettingTheme, `"dark"`); !errors.Is(err, aibow.ErrNoUser) {
		t.Errorf("利用者が無いのに書けてしまう: %v", err)
	}
	if err := s.SetAppSetting(bare, aibow.SettingLastModel, `"m"`); err != nil {
		t.Errorf("アプリ全体の設定は利用者が無くても読み書きできる: %v", err)
	}

	c := newConv(t, s, time.Now()) // ログインなしの形の利用者の会話
	s.OneUser()
	list, err := s.ListConversations(bare)
	if err != nil || len(list) != 1 || list[0].ID != c.ID {
		t.Errorf("OneUser なら、ログインなしの形の利用者の会話: %+v %v", list, err)
	}
}

// 版 5 の DB(持ち主より前)を開くと、今ある会話・プロファイル・表示の設定は、ログインなしの形の利用者のものになる
func TestMigrationFromVersion5(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v5.db")
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	all := ""
	for _, m := range migrations[:5] {
		all += m
	}
	if _, err := db.Exec(all + "PRAGMA user_version = 5;"); err != nil {
		t.Fatal(err)
	}
	conv, prof := uuid.NewV7(), uuid.NewV7()
	now := formatTime(time.Now())
	if _, err := db.Exec(`INSERT INTO profiles (id, name, settings, rules, created_at, updated_at) VALUES (?, '既定', '{}', '{}', ?, ?)`, prof.String(), now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO conversations (id, title, settings, profile_id, created_at, updated_at) VALUES (?, '古い会話', '{}', ?, ?, ?)`,
		conv.String(), prof.String(), now, now); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{"default_profile": `"` + prof.String() + `"`, "theme": `"dark"`, "pin_question": "false", "gpu_layers": "20"} {
		if _, err := db.Exec(`INSERT INTO app_settings VALUES (?, ?)`, k, v); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if c, _, err := s.Conversation(ctx, conv); err != nil || c.Title != "古い会話" || c.ProfileID != prof {
		t.Errorf("今ある会話はログインなしの形の利用者のもの: %+v %v", c, err)
	}
	if list, err := s.ListProfiles(ctx); err != nil || len(list) != 1 || list[0].ID != prof {
		t.Errorf("今あるプロファイルはログインなしの形の利用者のもの: %+v %v", list, err)
	}
	other := aibow.WithUser(context.Background(), uuid.NewV7())
	if list, _ := s.ListConversations(other); len(list) != 0 {
		t.Errorf("ほかの人には見えない: %+v", list)
	}
	for k, want := range map[string]string{"default_profile": `"` + prof.String() + `"`, "theme": `"dark"`, "pin_question": "false"} {
		if v, ok, err := s.UserSetting(ctx, k); !ok || err != nil || v != want {
			t.Errorf("%s は人ごとの設定に移る: %q %v %v", k, v, ok, err)
		}
		if _, ok, _ := s.AppSetting(ctx, k); ok {
			t.Errorf("%s はアプリ全体の設定から消える", k)
		}
	}
	if v, ok, _ := s.AppSetting(ctx, "gpu_layers"); !ok || v != "20" {
		t.Errorf("GPU の層数はアプリ全体の設定のまま: %q %v", v, ok)
	}
}

// 「生成中」の印(0017 の 10-3):ほかの印が期限の内なら断り、期限が過ぎるか外せば付けられる。ほかの人の会話には付けられない
// 以前の形のモデルの ID(file:<パス>)で保存した会話とプロファイルは、読むときに今の形(file:<ファイル名>)に読み替える。DB は書き換えない(0018 の決定事項 15)
func TestLegacyModelIDs(t *testing.T) {
	s, _ := openTemp(t)
	old := `file:D:` + string(rune(92)) + `models` + string(rune(92)) + `a.gguf`
	c := newConv(t, s, time.Now())
	c.Settings.Model = old
	if err := s.UpdateConversation(ctx, c); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.Conversation(ctx, c.ID)
	if err != nil || got.Settings.Model != "file:a.gguf" {
		t.Errorf("会話のモデル: %q %v", got.Settings.Model, err)
	}
	var raw string
	s.db.QueryRow("SELECT settings FROM conversations WHERE id = ?", c.ID.String()).Scan(&raw)
	if !strings.Contains(raw, "models") {
		t.Errorf("DB を書き換えている: %s", raw)
	}
}

func TestConversationLocks(t *testing.T) {
	s, _ := openTemp(t)
	c := newConv(t, s, time.Now())
	soon := time.Now().Add(time.Minute)
	if err := s.LockConversation(ctx, c.ID, "app", soon); err != nil {
		t.Fatal(err)
	}
	if err := s.LockConversation(ctx, c.ID, "app", soon.Add(time.Minute)); err != nil {
		t.Errorf("自分の印は延ばせる: %v", err)
	}
	if err := s.LockConversation(ctx, c.ID, "browser", soon); !errors.Is(err, aibow.ErrBusy) {
		t.Errorf("ほかの印があれば断る: %v", err)
	}
	s.UnlockConversation(ctx, c.ID, "browser") // ほかの印は外さない
	if err := s.LockConversation(ctx, c.ID, "browser", soon); !errors.Is(err, aibow.ErrBusy) {
		t.Errorf("ほかの holder では外れない: %v", err)
	}
	s.UnlockConversation(ctx, c.ID, "app")
	if err := s.LockConversation(ctx, c.ID, "browser", time.Now().Add(-time.Second)); err != nil {
		t.Errorf("外したら付けられる: %v", err)
	}
	if err := s.LockConversation(ctx, c.ID, "app", soon); err != nil {
		t.Errorf("期限の過ぎた印は無いものとして扱う: %v", err)
	}
	other := aibow.WithUser(context.Background(), uuid.NewV7())
	if err := s.LockConversation(other, c.ID, "x", soon); !errors.Is(err, aibow.ErrNotFound) {
		t.Errorf("ほかの人の会話: %v", err)
	}
}
