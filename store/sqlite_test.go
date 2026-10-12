package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
	"uuid"

	"aibow"
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

var settings = aibow.ConversationSettings{System: "sys", MaxTokens: 100, Temperature: 0.7, Think: true, PresencePenalty: 1.5}

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
		Stats: &aibow.ReplyStats{GenerateStats: aibow.GenerateStats{GenTokens: 12, StopReason: "eos"}, FirstSec: 0.5, ThinkSec: 1.25},
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
	if list[0].Title != "やあ" || list[0].Settings != settings {
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
	if _, ok, err := s.AppSetting("n_ctx"); ok || err != nil {
		t.Fatalf("まだ無いはずです: ok=%v err=%v", ok, err)
	}
	for _, v := range []string{"4096", "8192"} { // 2回目は上書き
		if err := s.SetAppSetting("n_ctx", v); err != nil {
			t.Fatal(err)
		}
	}
	if v, ok, err := s.AppSetting("n_ctx"); !ok || err != nil || v != "8192" {
		t.Errorf("値: %q ok=%v err=%v", v, ok, err)
	}

	// aibow.LoadAppSettings と組み合わせて使えること
	aibow.SaveAppSetting(s, aibow.SettingDefaults, settings)
	app, err := aibow.LoadAppSettings(s, aibow.AppSettings{GPULayers: 999})
	if err != nil || app.NCtx != 8192 || app.GPULayers != 999 || app.Defaults != settings {
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
