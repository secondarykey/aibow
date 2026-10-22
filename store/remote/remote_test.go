package remote

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"
	"time"
	"uuid"

	"github.com/secondarykey/aibow"
	apiclient "github.com/secondarykey/aibow/api/client"
	"github.com/secondarykey/aibow/engine"
	"github.com/secondarykey/aibow/store"
)

// 本物の保存先(SQLite)を保存の口に載せ、Client から使う。user は口を呼んだ利用者(aibow-server では API キーの持ち主)
func newRemote(t *testing.T) (*Client, *store.SQLite, *uuid.UUID) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	user := aibow.LocalUser
	srv := httptest.NewServer(Handler(st, func(*http.Request) (uuid.UUID, error) { return user, nil }))
	t.Cleanup(srv.Close)
	return New(apiclient.New(srv.URL)), st, &user
}

// 書いたものが、そのままの形で読める(会話 → JSON → 会話 の往復で変わらない)
func TestRoundTrip(t *testing.T) {
	c, _, _ := newRemote(t)
	ctx := context.Background() // 利用者は送らない(サーバーが決める)
	now := time.Now().Truncate(time.Microsecond)
	conv := aibow.Conversation{ID: uuid.NewV7(), Title: "t", CreatedAt: now, UpdatedAt: now,
		Settings: aibow.ConversationSettings{Model: "ollama:qwen3.5:4b", System: "sys <b>&", Temperature: 0.7, Compact: aibow.CompactSummary,
			HistoryTokens: 8192, ReplyTokens: 4096, MCPServers: []string{"fs"}, ToolPolicies: map[string]aibow.ToolPolicy{"run_command": aibow.ToolOff}}}
	if err := c.CreateConversation(ctx, conv); err != nil {
		t.Fatal(err)
	}
	msg := aibow.StoredMessage{ID: uuid.NewV7(), ConversationID: conv.ID, Role: "assistant", Content: "答え", Thinking: "思考",
		Stats: &aibow.ReplyStats{GenerateStats: engine.GenerateStats{GenTokens: 3}, FirstSec: 0.5}, CreatedAt: now}
	if err := c.AddMessage(ctx, msg); err != nil {
		t.Fatal(err)
	}
	got, ms, err := c.Conversation(ctx, conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Settings, conv.Settings) || got.Title != "t" || !got.CreatedAt.Equal(now) {
		t.Errorf("会話:\n got  %+v\n want %+v", got, conv)
	}
	if len(ms) != 1 || ms[0].Content != "答え" || *ms[0].Stats != *msg.Stats {
		t.Errorf("発言: %+v", ms)
	}
	if v, ok, err := c.AppSetting(ctx, "last_model"); ok || err != nil {
		t.Errorf("まだ無い設定: %q %v %v", v, ok, err)
	}
}

// サーバーのエラーは、手元と同じエラーに戻る(会話の仕組みが errors.Is で見分けられる)
func TestErrors(t *testing.T) {
	c, _, _ := newRemote(t)
	ctx := context.Background()
	if _, _, err := c.Conversation(ctx, uuid.NewV7()); !errors.Is(err, aibow.ErrNotFound) {
		t.Errorf("無い会話: %v", err)
	}
	if _, err := c.Profile(ctx, uuid.NewV7()); !errors.Is(err, aibow.ErrProfileNotFound) {
		t.Errorf("無いプロファイル: %v", err)
	}
	now := time.Now()
	conv := aibow.Conversation{ID: uuid.NewV7(), CreatedAt: now, UpdatedAt: now}
	c.CreateConversation(ctx, conv)
	if err := c.LockConversation(ctx, conv.ID, "browser", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := c.LockConversation(ctx, conv.ID, "app", now.Add(time.Minute)); !errors.Is(err, aibow.ErrBusy) {
		t.Errorf("ほかの画面で生成中: %v", err)
	}
}

// 呼んだ人のものだけが見える。アプリ全体の設定は、その人の設定として置かれる(サーバー全体の設定は変えない)
func TestUsersAndAppSettings(t *testing.T) {
	c, st, user := newRemote(t)
	ctx := context.Background()
	now := time.Now()
	c.CreateConversation(ctx, aibow.Conversation{ID: uuid.NewV7(), CreatedAt: now, UpdatedAt: now})
	if err := c.SetAppSetting(ctx, aibow.SettingLastModel, `"ollama:qwen3.5:9b"`); err != nil {
		t.Fatal(err)
	}
	local := aibow.WithUser(ctx, aibow.LocalUser)
	if _, ok, _ := st.AppSetting(local, aibow.SettingLastModel); ok {
		t.Error("サーバー全体の設定には書かない")
	}
	if v, _, _ := st.UserSetting(local, "app."+aibow.SettingLastModel); v != `"ollama:qwen3.5:9b"` {
		t.Errorf("その人の設定に置く: %q", v)
	}

	*user = uuid.NewV7() // ほかの人の API キーで呼ぶ
	if list, err := c.ListConversations(ctx); err != nil || len(list) != 0 {
		t.Errorf("ほかの人の会話は見えない: %+v %v", list, err)
	}
	if _, ok, _ := c.AppSetting(ctx, aibow.SettingLastModel); ok {
		t.Error("ほかの人の「最後に使ったモデル」は見えない")
	}
}

// 会話の仕組みを Client の上で動かす(アプリが aibow-server につないだときと同じ)。プロファイルの準備から会話を作るまで
func TestConversationsOverHTTP(t *testing.T) {
	c, _, _ := newRemote(t)
	ctx := context.Background()
	app := aibow.AppSettings{Workspace: aibow.WorkspaceSettings{Model: "ollama:qwen3.5:4b"},
		Defaults: aibow.ConversationSettings{System: "sys", Compact: aibow.CompactSummary, HistoryTokens: 8192, ReplyTokens: 4096}}
	cs := aibow.NewConversations(c, nil, app)
	if _, err := cs.PrepareProfiles(ctx); err != nil {
		t.Fatal(err)
	}
	conv, err := cs.Create(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := cs.Rename(ctx, conv.ID, "HTTP 越し"); err != nil {
		t.Fatal(err)
	}
	list, err := cs.List(ctx)
	if err != nil || len(list) != 1 || list[0].Title != "HTTP 越し" || list[0].ProfileID == (uuid.UUID{}) {
		t.Errorf("一覧: %+v %v", list, err)
	}
}
