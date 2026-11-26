package aibow

import (
	"context"
	"slices"
	"strings"
	"testing"
	"uuid"

	"github.com/secondarykey/aibow/tool"
)

// fakeServerWs は、サーバーモードのアプリの、aibow-server のサーバーのワークスペースの係の代わり(0067)。
type fakeServerWs struct {
	supported bool
	list      map[string][]tool.Folder
	dir       string // ツールを動かすフォルダ(本物は aibow-server の PC のフォルダ。テストでは手元の一時フォルダ)
	refreshed int
}

func (f *fakeServerWs) Workspace(name string) ([]tool.Folder, bool, bool) {
	if !f.supported {
		return nil, false, false
	}
	folders, ok := f.list[name]
	return slices.Clone(folders), ok, true
}

// Tools は、サーバーの MCP の口越しのツールの代わり。見分けられるよう印を付ける
func (f *fakeServerWs) Tools(name string) []tool.Tool {
	var out []tool.Tool
	for _, t := range append(tool.FilesIn([]tool.Folder{{Name: "規程", Path: f.dir, Main: true, Writable: true}}), tool.UpdatePlan()) {
		out = append(out, fromServer{t})
	}
	return out
}

func (f *fakeServerWs) Refresh(context.Context) { f.refreshed++ }

// fromServer は、aibow-server の MCP の口越しのツールの印(テスト用)。
type fromServer struct{ tool.Tool }

// newAppServerWs は、サーバーモードのアプリの形の会話の仕組みと、手元のワークスペースを選び、サーバーのワークスペース「社内規程」を配ったプロファイルの会話を作る。
func newAppServerWs(t *testing.T) (cs *Conversations, st *memStore, c Conversation, src *fakeServerWs, local string) {
	t.Helper()
	g := &fakeGen{content: "はい"}
	cs, st, c = newToolConversation(t, g)
	local = t.TempDir()
	src = &fakeServerWs{supported: true, dir: t.TempDir(), list: map[string][]tool.Folder{
		"社内規程": {{Name: "規程", Main: true, Writable: true}, {Name: "議事録", Description: "2024 年からの議事録"}},
	}}
	cs.SetServerWorkspaceSource(src)
	c.Settings.Workspace = workspaceFor(t, cs, local) // 会話に保存した手元のワークスペース(残すが使わない)
	c.Settings.Worktree = true
	p := Profile{ID: uuid.NewV7(), Name: "配布", Share: ShareAll, Rules: ProfileRules{Workspace: "社内規程"}}
	if err := st.CreateProfile(ctx, p); err != nil {
		t.Fatal(err)
	}
	c.ProfileID = p.ID
	if err := st.UpdateConversation(ctx, c); err != nil {
		t.Fatal(err)
	}
	c, _, err := cs.load(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	return cs, st, c, src, local
}

// サーバーのワークスペースの会話では、ファイルのツールを aibow-server の口越しに使い、手元のワークスペース・worktree・コマンド・Git を使わない。
// モデルへの環境の説明にパスを出さない(0067 の決定事項 3・11・完了の条件 2)
func TestAppServerWorkspaceTools(t *testing.T) {
	cs, _, c, src, local := newAppServerWs(t)
	set := c.Settings

	if folders := cs.toolFolders(set); len(folders) != 2 || folders[0].Name != "規程" || folders[0].Path != "" || folders[1].Description == "" {
		t.Errorf("ツールのフォルダは aibow-server が知らせたもの(パスは空): %+v", folders)
	}
	if d := cs.workDir(set); d != "" {
		t.Errorf("この PC のフォルダは無い(${workdir} の MCP のサーバーを起動しない): %q", d)
	}
	tools := cs.toolsFor(set)
	names := namesOf(tools)
	for _, want := range []string{"get_current_time", "calculate", "read_file", "write_file", "update_plan"} {
		if !slices.Contains(names, want) {
			t.Errorf("%s が無い: %v", want, names)
		}
	}
	if slices.Contains(names, "run_command") || slices.ContainsFunc(names, func(n string) bool { return strings.HasPrefix(n, "git_") }) {
		t.Errorf("コマンドと Git のツールは出さない: %v", names)
	}
	if _, ok := tool.Find(tools, "read_file").(fromServer); !ok {
		t.Error("ファイルのツールは aibow-server の口越しのもの")
	}
	if cs.worktreeMode(set) {
		t.Error("worktree は使わない")
	}
	if n := cs.ServerWorkspaceName(set); n != "社内規程" {
		t.Errorf("画面に出す名前: %q", n)
	}

	env := cs.environment(set)
	if !strings.Contains(env, "- 規程 (main, writable): on the server\n") || !strings.Contains(env, "- 議事録 (read-only): on the server — 2024 年からの議事録\n") {
		t.Errorf("環境の説明:\n%s", env)
	}
	for _, p := range []string{local, src.dir} {
		if strings.Contains(env, p) {
			t.Errorf("パス %s が出ている:\n%s", p, env)
		}
	}
	if strings.Contains(env, "run_command") {
		t.Errorf("コマンドのことは書かない:\n%s", env)
	}

	// フォルダが1つなら、ブラウザと同じ1行の形
	src.list["社内規程"] = []tool.Folder{{Name: "規程", Main: true, Writable: true}}
	if env := cs.environment(set); !strings.Contains(env, `Working directory: "規程" (a shared folder on the server)`) || strings.Contains(env, local) {
		t.Errorf("フォルダ1つの説明:\n%s", env)
	}
}

// 配布をやめた・ワークスペースが消えたら、手元のワークスペースに戻さず、ファイルのツールの無い会話になる(決定事項 12)。
// aibow-server が古い(口が無い)ときだけ、今までどおり手元のワークスペースを使う(決定事項 16)
func TestAppServerWorkspaceUnavailable(t *testing.T) {
	cs, _, c, src, local := newAppServerWs(t)
	set := c.Settings

	delete(src.list, "社内規程")
	if names := namesOf(cs.toolsFor(set)); !slices.Equal(names, []string{"get_current_time", "calculate"}) {
		t.Errorf("使えなくなったら、手元のファイルのツールも出さない: %v", names)
	}
	if f := cs.toolFolders(set); f != nil || cs.environment(set) != "" || cs.workDir(set) != "" {
		t.Errorf("手元のフォルダを使わない: %+v %q", f, cs.environment(set))
	}

	// 配る相手から外れた(その人から見たプロファイルは「配られていない」)。ほかの配布プロファイルで同じワークスペースが配られていても使わず、手元にも戻さない
	src.list["社内規程"] = []tool.Folder{{Name: "規程", Main: true, Writable: true}}
	withdrawn := set
	p := *set.profile
	p.Withdrawn = true
	withdrawn.profile = &p
	if names := namesOf(cs.toolsFor(withdrawn)); !slices.Equal(names, []string{"get_current_time", "calculate"}) {
		t.Errorf("配る相手から外れたら、ファイルのツールを出さない: %v", names)
	}
	if f := cs.toolFolders(withdrawn); f != nil || cs.workDir(withdrawn) != "" || cs.worktreeMode(withdrawn) {
		t.Errorf("配る相手から外れても、手元のフォルダを使わない: %+v", f)
	}

	src.supported = false
	if d := cs.workDir(set); d != local {
		t.Errorf("古い aibow-server なら、今までどおり手元のワークスペース: %q", d)
	}
	if names := namesOf(cs.toolsFor(set)); !slices.Contains(names, "run_command") {
		t.Errorf("古い aibow-server なら、今までどおり手元のツール: %v", names)
	}
	if n := cs.ServerWorkspaceName(set); n != "" {
		t.Errorf("古い aibow-server なら、画面には出さない(0064 の知らせ): %q", n)
	}
}

// 送信のたびに一覧を引き直し、そのあとで MCP のサーバーを起動する(${workdir} のサーバーには、この PC のフォルダを渡さない)
func TestAppServerWorkspaceSend(t *testing.T) {
	cs, _, c, src, _ := newAppServerWs(t)
	r := &fakeRemote{}
	cs.SetRemoteTools(r)
	c.Settings.MCPServers = []string{"fake"}
	if err := cs.UpdateSettings(ctx, c.ID, c.Settings); err != nil {
		t.Fatal(err)
	}
	send(t, cs, c, "質問")
	if src.refreshed != 1 {
		t.Errorf("送信の前に引き直す: %d", src.refreshed)
	}
	if len(r.workDirs) != 1 || r.workDirs[0] != "" {
		t.Errorf("MCP のサーバーにこの PC のフォルダを渡さない: %q", r.workDirs)
	}
	// 会話に保存した手元のワークスペースは消さない(決定事項 12)
	got, _, err := cs.store.Conversation(ctx, c.ID)
	if err != nil || got.Settings.Workspace == "" {
		t.Errorf("手元のワークスペースは残す: %+v %v", got.Settings, err)
	}
}
