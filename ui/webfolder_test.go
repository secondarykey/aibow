package ui

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/secondarykey/aibow"
	"github.com/secondarykey/aibow/auth"
	"github.com/secondarykey/aibow/format"
	"github.com/secondarykey/aibow/tool"
)

// サーバーの資料のフォルダ(0024 の 12-2)のテスト。管理者が配るプロファイルでフォルダを選ぶと、配られた人の会話で読み書きできる。

type folderWeb struct {
	*testWeb
	dir               string // 資料のフォルダ(server.json の folders の docs)
	alice, bob, carol *uuid.UUID
	profile           aibow.Profile // alice が bob に配った、docs を選んだプロファイル
}

func newFolderWeb(t *testing.T) *folderWeb {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# 仕様の置き場\n合言葉は「みかん」\n"), 0o644)
	w := &folderWeb{testWeb: newTestWeb(t, func(o *Options) { o.Folders = map[string]string{"docs": dir} }), dir: dir}
	now := time.Now()
	add := func(name string, admin bool) *uuid.UUID {
		u := auth.User{ID: uuid.NewV7(), Name: name, Admin: admin, CreatedAt: now, UpdatedAt: now}
		if err := w.st.CreateUser(context.Background(), u); err != nil {
			t.Fatal(err)
		}
		return &u.ID
	}
	w.alice, w.bob, w.carol = add("alice", true), add("bob", false), add("carol", false)

	w.user.Store(w.alice)
	w.admin.Store(true)
	w.mustCall(t, &w.profile, "CreateProfile", "仕様の相談", aibow.ProfileValues{Model: "ollama:a", HistoryTokens: 8192, ReplyTokens: 8192},
		aibow.ProfileRules{Folder: "docs"})
	w.mustCall(t, nil, "SetProfileShare", w.profile.ID, aibow.ShareUsers, []string{"bob"})
	w.admin.Store(false)
	return w
}

func readCall(path string) []format.ToolCall {
	return []format.ToolCall{{Call: tool.Call{Name: "read_file", Args: []tool.Arg{{Name: "path", Value: path}}}}}
}

// 配られた人の会話では、資料のフォルダを読める。ブラウザにはフォルダの名前だけを見せ、パスは見せない。コマンドは出さない
func TestWebFolderRead(t *testing.T) {
	w := newFolderWeb(t)
	var names []string
	w.mustCall(t, &names, "Folders")
	if len(names) != 1 || names[0] != "docs" {
		t.Errorf("Folders: %v", names)
	}

	w.user.Store(w.bob)
	events := w.openEvents(t)
	var c aibow.Conversation
	w.mustCall(t, &c, "NewConversation", w.profile.ID, nil)
	status, body := w.call(t, context.Background(), "Conversation", c.ID)
	if status != http.StatusOK || !strings.Contains(string(body), `"folder":"docs"`) || strings.Contains(string(body), filepath.Base(w.dir)) {
		t.Errorf("会話の詳細(フォルダの名前だけ・パスは出さない): %d %s", status, body)
	}

	w.eng.calls = [][]format.ToolCall{readCall("README.md")}
	w.mustCall(t, nil, "Send", c.ID, "README を読んで")
	until(t, events, "done")
	if r := w.toolResult(t, c.ID); !strings.Contains(r.Content, "合言葉は「みかん」") {
		t.Errorf("資料のフォルダを読めるはず: %q", r.Content)
	}
	names = w.eng.tools[0]
	for _, want := range []string{"list_files", "read_file", "write_file", "edit_file"} {
		if !slices.Contains(names, want) {
			t.Errorf("%s を知らせるはず: %v", want, names)
		}
	}
	if slices.Contains(names, "run_command") {
		t.Errorf("コマンドのツールは出さない: %v", names)
	}
	if p := w.eng.prompts[0]; !strings.Contains(p, `Working directory: "docs"`) || strings.Contains(p, w.dir) || strings.Contains(p, filepath.ToSlash(w.dir)) {
		t.Errorf("モデルに渡す環境の節は名前だけ: %q", p)
	}
	var groups []aibow.ToolGroupState
	w.mustCall(t, &groups, "ToolStates", c.ID, c.Settings)
	for _, tl := range groups[0].Tools {
		if tl.Name == "run_command" {
			t.Error("会話の設定の画面にもコマンドのツールを出さない")
		}
	}
}

// 書くのは差分の確認のあと。書いたらファイルが変わる
func TestWebFolderWrite(t *testing.T) {
	w := newFolderWeb(t)
	w.user.Store(w.bob)
	events := w.openEvents(t)
	var c aibow.Conversation
	w.mustCall(t, &c, "NewConversation", w.profile.ID, nil)
	w.eng.calls = [][]format.ToolCall{{{Call: tool.Call{Name: "write_file", Args: []tool.Arg{{Name: "path", Value: "next.md"}, {Name: "content", Value: "# 次のステップ\n"}}}}}}
	done := w.sendAsync(t, context.Background(), c.ID, "次のステップを書いて")
	got := until(t, events, "confirm")
	req := got[len(got)-1].Confirm
	if req == nil || req.Preview.Path != "next.md" || !strings.Contains(req.Preview.Diff, "+# 次のステップ") {
		t.Fatalf("書く前の確認のカード: %+v", req)
	}
	if _, err := os.Stat(filepath.Join(w.dir, "next.md")); err == nil {
		t.Fatal("答える前に書いている")
	}
	w.mustCall(t, nil, "ConfirmTool", c.ID, req.ID, true, "", "")
	until(t, events, "done")
	<-done
	if b, err := os.ReadFile(filepath.Join(w.dir, "next.md")); err != nil || string(b) != "# 次のステップ\n" {
		t.Errorf("書いた中身: %q, %v", b, err)
	}
}

// 配られていない人・写したプロファイル・配るのをやめたあとは、フォルダを使えない。フォルダを選べるのは管理者だけ
func TestWebFolderAccess(t *testing.T) {
	w := newFolderWeb(t)

	// 管理者でない人は、自分のプロファイルでもフォルダを選べない
	w.user.Store(w.carol)
	if status, body := w.call(t, context.Background(), "CreateProfile", "まね", aibow.ProfileValues{HistoryTokens: 8192, ReplyTokens: 8192},
		aibow.ProfileRules{Folder: "docs"}); status == http.StatusOK || !strings.Contains(string(body), "管理者だけ") {
		t.Errorf("管理者でない人がフォルダを選べた: %d %s", status, body)
	}
	// 管理者でも、無いフォルダは選べない
	w.user.Store(w.alice)
	w.admin.Store(true)
	p := w.profile
	p.Rules.Folder = "nothing"
	if status, body := w.call(t, context.Background(), "UpdateProfile", p); status == http.StatusOK || !strings.Contains(string(body), "ありません") {
		t.Errorf("無いフォルダを選べた: %d %s", status, body)
	}
	w.admin.Store(false)

	// 写したプロファイルにはフォルダを写さない
	w.user.Store(w.bob)
	var cp aibow.Profile
	w.mustCall(t, &cp, "CopyProfile", w.profile.ID)
	if cp.Rules.Folder != "" {
		t.Errorf("写しにフォルダが残っている: %+v", cp.Rules)
	}
	var c aibow.Conversation
	w.mustCall(t, &c, "NewConversation", w.profile.ID, nil)

	// 配るのをやめたら、今ある会話でもフォルダを使えない(決まりなので、すぐ効く)
	w.user.Store(w.alice)
	w.admin.Store(true)
	w.mustCall(t, nil, "SetProfileShare", w.profile.ID, aibow.ShareNone, []string{})
	w.admin.Store(false)
	w.user.Store(w.bob)
	var d ConversationDetail
	w.mustCall(t, &d, "Conversation", c.ID)
	if d.Folder != "" {
		t.Errorf("配るのをやめたのにフォルダが見えている: %q", d.Folder)
	}
	events := w.openEvents(t)
	w.eng.calls = [][]format.ToolCall{readCall("README.md")}
	w.mustCall(t, nil, "Send", c.ID, "README を読んで")
	until(t, events, "done")
	if r := w.toolResult(t, c.ID); !r.ToolResult.Error || strings.Contains(r.Content, "みかん") {
		t.Errorf("配るのをやめたあとに読めた: %q", r.Content)
	}
	if last := w.eng.tools[len(w.eng.tools)-1]; slices.Contains(last, "read_file") {
		t.Errorf("フォルダのツールを知らせている: %v", last)
	}
}
