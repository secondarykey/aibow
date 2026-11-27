package aibow

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/secondarykey/aibow/config"
	"github.com/secondarykey/aibow/tool"
)

// newServerWorkspaceConversations は、aibow-server のブラウザの会話の形(会話の作業フォルダを使わない)で、サーバーのワークスペースを2つ持つ会話の仕組みを作る(0065)。
//   - 社内規程: 規程(メイン・書ける)と議事録(読むだけ・説明つき)
//   - 手引き: フォルダ1つ(メイン・書ける)
func newServerWorkspaceConversations(t *testing.T) (cs *Conversations, rules, minutes, guide string) {
	t.Helper()
	cs, _, _ = newToolConversation(t, &fakeGen{content: "はい"})
	cs.IgnoreWorkDir()
	rules, minutes, guide = t.TempDir(), t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(minutes, "2024-04.md"), []byte("# 4月\n"), 0o644)
	cs.SetServerWorkspaces([]config.ServerWorkspace{
		{Name: "社内規程", Folders: []config.WorkspaceFolder{
			{Path: rules, Name: "規程", Main: true, Writable: true},
			{Path: minutes, Name: "議事録", Description: "2024 年からの議事録"},
		}},
		{Name: "手引き", Folders: []config.WorkspaceFolder{{Path: guide, Name: "guide", Main: true, Writable: true}}},
	})
	return cs, rules, minutes, guide
}

// settingsWith は、ワークスペース name を選んだ配布プロファイルの会話の設定。
func settingsWith(p *Profile) ConversationSettings {
	set := testDefaults
	set.profile = p
	return set
}

// ブラウザの会話のファイルのツールは、サーバーのワークスペースのフォルダ全部を扱う(0065 の決定事項 2・12)。メインのフォルダが作業フォルダ
func TestServerWorkspaceToolFolders(t *testing.T) {
	cs, rules, minutes, _ := newServerWorkspaceConversations(t)
	set := settingsWith(&Profile{Share: ShareAll, Rules: ProfileRules{Workspace: "社内規程"}})
	folders := cs.toolFolders(set)
	if len(folders) != 2 || folders[0].Name != "規程" || folders[0].Path != rules || !folders[0].Main || !folders[0].Writable ||
		folders[1].Name != "議事録" || folders[1].Path != minutes || folders[1].Main || folders[1].Writable || folders[1].Description == "" {
		t.Errorf("ツールのフォルダ: %+v", folders)
	}
	if d := cs.workDir(set); d != rules {
		t.Errorf("作業フォルダはメインのフォルダのはず: %s", d)
	}
	if n := cs.ServerWorkspaceName(set); n != "社内規程" {
		t.Errorf("画面に出す名前: %s", n)
	}
	// 管理の画面でフォルダを変えると、次に引いたときから効く
	cs.SetServerWorkspaces([]config.ServerWorkspace{{Name: "社内規程", Folders: []config.WorkspaceFolder{{Path: minutes, Name: "議事録", Main: true}}}})
	if folders := cs.toolFolders(set); len(folders) != 1 || folders[0].Path != minutes {
		t.Errorf("差し替えたあと: %+v", folders)
	}
}

// 使えるのは配っていて、まだ配られているときだけ。写したプロファイル(配っていない)や、無いワークスペースではフォルダのツールを出さない
func TestGrantedWorkspace(t *testing.T) {
	cs, _, _, _ := newServerWorkspaceConversations(t)
	for _, c := range []struct {
		name string
		p    *Profile
		want string
	}{
		{"配っている", &Profile{Share: ShareUsers, Rules: ProfileRules{Workspace: "手引き"}}, "手引き"},
		{"配っていない", &Profile{Rules: ProfileRules{Workspace: "手引き"}}, ""},
		{"配るのをやめた", &Profile{Share: ShareAll, Withdrawn: true, Rules: ProfileRules{Workspace: "手引き"}}, ""},
		{"プロファイルが無い", nil, ""},
	} {
		if got := c.p.GrantedWorkspace(); got != c.want {
			t.Errorf("%s: %q", c.name, got)
		}
		if got := cs.toolFolders(settingsWith(c.p)); (len(got) > 0) != (c.want != "") {
			t.Errorf("%s: ツールのフォルダ %+v", c.name, got)
		}
	}
	if got := cs.toolFolders(settingsWith(&Profile{Share: ShareAll, Rules: ProfileRules{Workspace: "無い"}})); got != nil {
		t.Errorf("無いワークスペース: %+v", got)
	}
}

// モデルへの環境の説明にサーバーのパスを出さない。フォルダが1つでも複数でも(0065 の決定事項 13・完了の条件 2)。run_command も出さない
func TestServerWorkspaceEnvironment(t *testing.T) {
	cs, rules, minutes, guide := newServerWorkspaceConversations(t)
	cs.SetLocalTools(filesOnly{}) // ブラウザの会話と同じく、コマンドと Git のツールを出さない(ui の noCommand にあたる)

	multi := cs.environment(settingsWith(&Profile{Share: ShareAll, Rules: ProfileRules{Workspace: "社内規程"}}))
	for _, want := range []string{"- 規程 (main, writable): on the server\n", "- 議事録 (read-only): on the server — 2024 年からの議事録\n"} {
		if !strings.Contains(multi, want) {
			t.Errorf("複数のフォルダの説明に %q が無い:\n%s", want, multi)
		}
	}
	single := cs.environment(settingsWith(&Profile{Share: ShareAll, Rules: ProfileRules{Workspace: "手引き"}}))
	if !strings.Contains(single, `Working directory: "guide" (a shared folder on the server)`) || !strings.Contains(single, `"guide" refers to the working directory itself.`) {
		t.Errorf("フォルダ1つの説明:\n%s", single)
	}
	for _, env := range []string{multi, single} {
		for _, p := range []string{rules, minutes, guide, filepath.ToSlash(rules)} {
			if strings.Contains(env, p) {
				t.Errorf("サーバーのパス %s が出ている:\n%s", p, env)
			}
		}
		if strings.Contains(env, "run_command") || strings.Contains(env, "git") {
			t.Errorf("コマンドと git のことは書かない:\n%s", env)
		}
	}
	if names := namesOf(cs.toolsFor(settingsWith(&Profile{Share: ShareAll, Rules: ProfileRules{Workspace: "社内規程"}}))); slices.Contains(names, "run_command") || !slices.Contains(names, "read_file") {
		t.Errorf("ツール: %v", names)
	}
}

// filesOnly は、ファイルのツールだけを渡す係(テスト用)。
type filesOnly struct{}

func (filesOnly) Tools(folders []tool.Folder) []tool.Tool { return tool.FilesIn(folders) }

func namesOf(tools []tool.Tool) []string {
	var names []string
	for _, t := range tools {
		names = append(names, t.Def().Name)
	}
	return names
}
