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
	"github.com/secondarykey/aibow/config"
	"github.com/secondarykey/aibow/format"
	"github.com/secondarykey/aibow/tool"
)

// サーバーのワークスペース(0065。以前の資料のフォルダ。0024 の 12-2)のテスト。管理者が配るプロファイルでワークスペースを選ぶと、配られた人の会話で読み書きできる。

type folderWeb struct {
	*testWeb
	dir               string // サーバーのワークスペース docs のフォルダ(1つ。前の形の folders の docs を移したものと同じ形)
	alice, bob, carol *uuid.UUID
	profile           aibow.Profile // alice が bob に配った、docs を選んだプロファイル
}

func newFolderWeb(t *testing.T, opts ...func(*Options)) *folderWeb {
	t.Helper()
	// フォルダの名前は、ほかの値(時刻・ID)にたまたま出てこないものにする。パスを画面に見せていないかを、名前が返事に無いことで確かめるため
	// (t.TempDir の名前は「001」などで、時刻の「39.001909」にたまたま入ってテストが落ちることがあった)
	dir := filepath.Join(t.TempDir(), "aibow-shared-docs")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# 仕様の置き場\n合言葉は「みかん」\n"), 0o644)
	opts = append([]func(*Options){func(o *Options) {
		o.ServerWorkspaces = []config.ServerWorkspace{{Name: "docs", Folders: []config.WorkspaceFolder{{Path: dir, Name: "docs", Main: true, Writable: true}}}}
	}}, opts...)
	w := &folderWeb{testWeb: newTestWeb(t, opts...), dir: dir}
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
	w.mustCall(t, &w.profile, "ShareNew", "仕様の相談", "ollama:a", aibow.ShareUsers, []string{"bob"}, []string{})
	w.profile.Values.HistoryTokens, w.profile.Values.ReplyTokens = 8192, 8192
	w.profile.Rules.Workspace = "docs"
	w.mustCall(t, nil, "UpdateProfile", w.profile)
	w.admin.Store(false)
	return w
}

func readCall(path string) []format.ToolCall {
	return []format.ToolCall{{Call: tool.Call{Name: "read_file", Args: []tool.Arg{{Name: "path", Value: path}}}}}
}

// 配られた人の会話では、サーバーのワークスペースを読める。ブラウザにはワークスペースとフォルダの名前だけを見せ、パスは見せない。コマンドは出さない
func TestWebFolderRead(t *testing.T) {
	w := newFolderWeb(t)
	var list []ServerWorkspaceInfo
	status, body := w.call(t, context.Background(), "ServerWorkspaces")
	if status != http.StatusOK || strings.Contains(string(body), filepath.Base(w.dir)) {
		t.Errorf("ServerWorkspaces(パスは出さない): %d %s", status, body)
	}
	w.mustCall(t, &list, "ServerWorkspaces")
	if len(list) != 1 || list[0].Name != "docs" || len(list[0].Folders) != 1 || !list[0].Folders[0].Main {
		t.Errorf("ServerWorkspaces: %+v", list)
	}

	w.user.Store(w.bob)
	events := w.openEvents(t)
	var c aibow.Conversation
	w.mustCall(t, &c, "NewConversation", w.profile.ID, nil)
	status, body = w.call(t, context.Background(), "Conversation", c.ID)
	if status != http.StatusOK || !strings.Contains(string(body), `"workspace":"docs"`) || strings.Contains(string(body), filepath.Base(w.dir)) {
		t.Errorf("会話の詳細(フォルダの名前だけ・パスは出さない): %d %s", status, body)
	}

	w.eng.calls = [][]format.ToolCall{readCall("README.md")}
	w.mustCall(t, nil, "Send", c.ID, "README を読んで", nil)
	until(t, events, "done")
	if r := w.toolResult(t, c.ID); !strings.Contains(r.Content, "合言葉は「みかん」") {
		t.Errorf("資料のフォルダを読めるはず: %q", r.Content)
	}
	names := w.eng.tools[0]
	for _, want := range []string{"list_files", "read_file", "search_files", "write_file", "edit_file"} {
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
	w.mustCall(t, nil, "ConfirmTool", c.ID, req.ID, true, "", "", false)
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
		aibow.ProfileRules{Workspace: "docs"}); status == http.StatusOK || !strings.Contains(string(body), "管理者だけ") {
		t.Errorf("管理者でない人がフォルダを選べた: %d %s", status, body)
	}
	// 管理者でも、無いフォルダは選べない
	w.user.Store(w.alice)
	w.admin.Store(true)
	p := w.profile
	p.Rules.Workspace = "nothing"
	if status, body := w.call(t, context.Background(), "UpdateProfile", p); status == http.StatusOK || !strings.Contains(string(body), "ありません") {
		t.Errorf("無いフォルダを選べた: %d %s", status, body)
	}
	w.admin.Store(false)

	// 写したプロファイルにはフォルダを写さない
	w.user.Store(w.bob)
	var cp aibow.Profile
	w.mustCall(t, &cp, "CopyProfile", w.profile.ID)
	if cp.Rules.Workspace != "" {
		t.Errorf("写しにフォルダが残っている: %+v", cp.Rules)
	}
	var c aibow.Conversation
	w.mustCall(t, &c, "NewConversation", w.profile.ID, nil)

	// 配る相手から外れたら、今ある会話でもフォルダを使えない(決まりなので、すぐ効く)
	w.user.Store(w.alice)
	w.admin.Store(true)
	w.mustCall(t, nil, "SetProfileShare", w.profile.ID, aibow.ShareUsers, []string{"carol"}, []string{})
	w.admin.Store(false)
	w.user.Store(w.bob)
	var d ConversationDetail
	w.mustCall(t, &d, "Conversation", c.ID)
	if d.Workspace != "" {
		t.Errorf("配るのをやめたのにワークスペースが見えている: %q", d.Workspace)
	}
	events := w.openEvents(t)
	w.eng.calls = [][]format.ToolCall{readCall("README.md")}
	w.mustCall(t, nil, "Send", c.ID, "README を読んで", nil)
	until(t, events, "done")
	if r := w.toolResult(t, c.ID); !r.ToolResult.Error || strings.Contains(r.Content, "みかん") {
		t.Errorf("配るのをやめたあとに読めた: %q", r.Content)
	}
	if last := w.eng.tools[len(w.eng.tools)-1]; slices.Contains(last, "read_file") {
		t.Errorf("フォルダのツールを知らせている: %v", last)
	}
}

// 同じファイルを2人が書くとき:確認を待っている間にほかの人が書き換えたら、見せた差分と違うので書き戻さない(0024 の決定事項 5)
func TestWebFolderWriteConflict(t *testing.T) {
	w := newFolderWeb(t)
	w.user.Store(w.bob)
	events := w.openEvents(t)
	var c aibow.Conversation
	w.mustCall(t, &c, "NewConversation", w.profile.ID, nil)
	w.eng.calls = [][]format.ToolCall{{{Call: tool.Call{Name: "write_file", Args: []tool.Arg{{Name: "path", Value: "README.md"}, {Name: "content", Value: "# bob の版\n"}}}}}}
	done := w.sendAsync(t, context.Background(), c.ID, "README を書き直して")
	got := until(t, events, "confirm")

	// bob が確認のカードを見ている間に、ほかの人(carol の会話や、管理者の git pull)が書き換えた
	readme := filepath.Join(w.dir, "README.md")
	os.WriteFile(readme, []byte("# carol の版\n"), 0o644)
	w.mustCall(t, nil, "ConfirmTool", c.ID, got[len(got)-1].Confirm.ID, true, "", "", false)
	until(t, events, "done")
	<-done
	if b, _ := os.ReadFile(readme); string(b) != "# carol の版\n" {
		t.Errorf("後から書いた人の中身を消した: %q", b)
	}
	if r := w.toolResult(t, c.ID); !r.ToolResult.Error || !strings.Contains(r.Content, "確認のあとでファイルが変わった") {
		t.Errorf("結果: %q", r.Content)
	}
}

// フォルダを2つ(書ける1つ・読むだけ1つ)持つサーバーのワークスペースを配ると、配られた人の会話で、両方のフォルダを folder で読め、
// 書けるほうだけに書ける。確認のカードにフォルダの名前が出る。モデルに渡す環境の説明にサーバーのパスは出ない(0065 の完了の条件 1・2)。
// フォルダがリポジトリでも、Git とコマンドのツールは出さない(決定事項 5)
func TestWebServerWorkspaceFolders(t *testing.T) {
	minutes := filepath.Join(t.TempDir(), "aibow-shared-minutes")
	os.Mkdir(minutes, 0o755)
	os.Mkdir(filepath.Join(minutes, ".git"), 0o755) // リポジトリに見える(git_status などを出す条件)
	os.WriteFile(filepath.Join(minutes, "2024-04.md"), []byte("# 4月\n議題は「ぶどう」\n"), 0o644)
	w := newFolderWeb(t, func(o *Options) {
		docs := o.ServerWorkspaces[0].Folders[0]
		o.ServerWorkspaces[0].Folders = []config.WorkspaceFolder{
			{Path: docs.Path, Name: "規程", Main: true, Writable: true},
			{Path: minutes, Name: "議事録", Description: "2024 年からの議事録"},
		}
	})
	// ツールを4回使うので、会話の上限を広げておく(テストの数え方では、環境の説明と作業の決まりだけで 8192 のほとんどを使う)
	w.user.Store(w.alice)
	w.admin.Store(true)
	w.profile.Values.HistoryTokens = 24576
	w.mustCall(t, nil, "UpdateProfile", w.profile)
	w.admin.Store(false)
	w.user.Store(w.bob)
	events := w.openEvents(t)
	var c aibow.Conversation
	w.mustCall(t, &c, "NewConversation", w.profile.ID, nil)
	call := func(name string, kv ...string) []format.ToolCall {
		var args []tool.Arg
		for i := 0; i+1 < len(kv); i += 2 {
			args = append(args, tool.Arg{Name: kv[i], Value: kv[i+1]})
		}
		return []format.ToolCall{{Call: tool.Call{Name: name, Args: args}}}
	}
	w.eng.calls = [][]format.ToolCall{
		call("read_file", "folder", "規程", "path", "README.md"),
		call("read_file", "folder", "議事録", "path", "2024-04.md"),
		call("write_file", "folder", "議事録", "path", "2024-04.md", "content", "消した"),
		call("write_file", "folder", "規程", "path", "next.md", "content", "# 次\n"),
	}
	done := w.sendAsync(t, context.Background(), c.ID, "規程と議事録を読んで、まとめを書いて")
	got := until(t, events, "confirm")
	req := got[len(got)-1].Confirm
	if req == nil || req.Preview.Folder != "規程" || req.Preview.Path != "next.md" || strings.Contains(req.Preview.Diff, w.dir) {
		t.Fatalf("確認のカード(フォルダの名前が出る・パスは出さない): %+v", req)
	}
	w.mustCall(t, nil, "ConfirmTool", c.ID, req.ID, true, "", "", false)
	until(t, events, "done")
	<-done

	var d ConversationDetail
	w.mustCall(t, &d, "Conversation", c.ID)
	var results []aibow.StoredMessage
	for _, m := range d.Messages {
		if m.Role == "tool" {
			results = append(results, m)
		}
	}
	if len(results) != 4 || !strings.Contains(results[0].Content, "みかん") || !strings.Contains(results[1].Content, "ぶどう") ||
		!results[2].ToolResult.Error || !strings.Contains(results[2].Content, "読むだけのフォルダ") || results[3].ToolResult.Error {
		t.Errorf("ツールの結果: %+v", results)
	}
	if b, _ := os.ReadFile(filepath.Join(minutes, "2024-04.md")); !strings.Contains(string(b), "ぶどう") {
		t.Errorf("読むだけのフォルダが書き換わった: %q", b)
	}
	if b, err := os.ReadFile(filepath.Join(w.dir, "next.md")); err != nil || string(b) != "# 次\n" {
		t.Errorf("書けるフォルダに書いた中身: %q %v", b, err)
	}

	p := w.eng.prompts[0]
	for _, want := range []string{"- 規程 (main, writable): on the server\n", "- 議事録 (read-only): on the server — 2024 年からの議事録\n"} {
		if !strings.Contains(p, want) {
			t.Errorf("環境の説明に %q が無い: %q", want, p)
		}
	}
	for _, path := range []string{w.dir, minutes, filepath.ToSlash(w.dir), filepath.ToSlash(minutes)} {
		if strings.Contains(p, path) {
			t.Errorf("モデルに渡す環境の説明にサーバーのパスが出ている: %q", p)
		}
	}
	for _, name := range []string{"run_command", "git_status", "git_diff"} {
		if slices.Contains(w.eng.tools[0], name) {
			t.Errorf("%s は出さない: %v", name, w.eng.tools[0])
		}
	}
	var builtin []config.KnownTool
	w.mustCall(t, &builtin, "BuiltinTools")
	if slices.ContainsFunc(builtin, func(k config.KnownTool) bool { return k.Name == "git_status" || k.Name == "run_command" }) {
		t.Errorf("設定の画面にも Git とコマンドのツールを出さない: %+v", builtin)
	}
}
