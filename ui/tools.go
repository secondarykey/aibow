package ui

// tools.go: 画面から呼ぶ、MCP のサーバーとツールの扱いのメソッド(会話で選ぶもの・扱いの既定・サーバーのワークスペース)。MCP のサーバーを足す・変える・消すのは mcpedit.go。

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"uuid"

	"github.com/secondarykey/aibow"
	"github.com/secondarykey/aibow/config"
	"github.com/secondarykey/aibow/log"
	"github.com/secondarykey/aibow/mcp/client"
	"github.com/secondarykey/aibow/tool"
)

// ---- MCP のサーバーとツールの扱い ------------------------------------------------------

// MCPServers は、会話ごとに使うものを選ぶ MCP のサーバーの一覧(settings.json に書いたもの全部)。書いてなければ空。
// 設定の画面の「ツール」のチェックは「新しい会話で最初から選ぶか」だけなので、チェックを外したものもここに出す。
// ブラウザでは aibow-server の server.json に書いたもの。${workdir} を使うサーバーは、サーバーのワークスペースが無ければ出さない
// (サーバーのワークスペースのある会話でだけ起動する。0024 の決定事項 8・24)。
func (s *Service) MCPServers() []client.ServerInfo {
	if s.mcp == nil {
		return []client.ServerInfo{}
	}
	l := s.mcp.Infos()
	if s.browser && !s.hasServerWorkspaces() {
		l = slices.DeleteFunc(l, func(i client.ServerInfo) bool { return i.UsesWorkDir })
	}
	return l
}

// AllMCPServers は settings.json に書いた MCP のサーバー全部(設定の画面の「ツール」に出す)。
func (s *Service) AllMCPServers() []client.ServerInfo {
	if s.mcp == nil {
		return []client.ServerInfo{}
	}
	return s.mcp.Infos()
}

// BuiltinTools は組み込みのツール(aibow の中で動くツール)の一覧(設定の画面の「ツール」に出す)。
// ブラウザでは、コマンドのツールを出さない。サーバーのワークスペースが無ければ、作業フォルダを使わないツール(時刻・計算・URL を読む)だけ(0024 の決定事項 5・17)。
func (s *Service) BuiltinTools() []config.KnownTool {
	l := aibow.BuiltinTools()
	if s.browser {
		hasFolders := s.hasServerWorkspaces()
		l = slices.DeleteFunc(l, func(k config.KnownTool) bool {
			return browserHides(k.Name) || !hasFolders && tool.Find(tool.Folderless(), k.Name) == nil
		})
	}
	return l
}

// ToolDefaults は、会話の上書きが無いときのツールの扱い(PC の既定と決まりの既定。設定の画面の「ツール」に出す)。
// この PC の既定なので、プロファイルの決まりは当てない。ブラウザでは aibow-server の管理者の既定(誰でも見られる。変えるのは管理者だけ)。
func (s *Service) ToolDefaults() ([]aibow.ToolGroupState, error) {
	if s.startupErr != nil {
		return nil, s.startupErr
	}
	return s.forBrowser(s.convs.ToolStates(aibow.ConversationSettings{})), nil
}

// SetGroupPolicy は、グループ(空なら組み込み、そうでなければ MCP のサーバー)の全部のツールの確認の扱いの PC の既定を変える
// (確認する ask / 確認しない allow / 決めていない 空。tools.json に書く)。
// ブラウザでは aibow-server の管理者だけが変えられ(server-tools.json に書く)、使わない(off。禁止)も決められる(0024 の決定事項 21・22)。
// 画面の「保存」は SetToolPolicies(1回の保存でまとめて書き、ログも1行。0053)。これは1つだけ変える形。
func (s *Service) SetGroupPolicy(ctx context.Context, group string, policy config.ToolPolicy) error {
	return s.SetToolPolicies(ctx, []ToolPolicyChange{{Group: true, Name: group, Policy: policy}})
}

// ToolPolicyChange は、ツールの扱いの既定の変更1つ(画面の「保存」でまとめて送る。0053 の決定事項 7)。
// Group ならグループ(Name が空なら組み込み、そうでなければ MCP のサーバーの名前)、そうでなければツール(モデルに見せる名前)。
// Policy が空なら、決めていない状態に戻す。
type ToolPolicyChange struct {
	Group  bool              `json:"group"`
	Name   string            `json:"name"`
	Policy config.ToolPolicy `json:"policy"`
}

// SetToolPolicies は、ツールの扱いの既定をまとめて変える(管理の画面の「ツール」の「保存」)。ログは1回の保存で1行(0053 の決定事項 21)。
// ブラウザでは全員の会話に効く aibow-server の既定なので Notice で、誰が変えたかも添える。アプリはこの PC の既定。
func (s *Service) SetToolPolicies(ctx context.Context, changes []ToolPolicyChange) error {
	items, err := s.applyToolPolicies(ctx, changes)
	if err != nil || len(items) == 0 {
		return err
	}
	log.Noticef("ツールの扱いの%sを保存しました(%s%s)", s.prefsName(), strings.Join(items, "・"), byWhom(ctx))
	return nil
}

// applyToolPolicies は、ツールの扱いの既定の変更を順に書き、ログに書く項目(「組み込み: 使わない」の形)を返す。
// プロファイルの保存(SaveProfile)からも呼ぶ(この PC の「確認しない」。ログはプロファイルの1行に入れる)。
// 途中で書けなければ、そこで止めてエラーを返す(画面は下書きを残す。書けた分は tools.json に残るので、画面は読み直す)。
func (s *Service) applyToolPolicies(ctx context.Context, changes []ToolPolicyChange) ([]string, error) {
	if len(changes) == 0 {
		return nil, nil
	}
	if err := s.canSetTools(ctx); err != nil {
		return nil, err
	}
	var items []string
	for _, c := range changes {
		var err error
		name := c.Name
		if c.Group {
			err = s.prefs.SetGroupPolicy(c.Name, c.Policy)
			name = cmp.Or(c.Name, "組み込み")
		} else {
			err = s.prefs.SetToolPolicy(c.Name, c.Policy)
		}
		if err != nil {
			return nil, err
		}
		items = append(items, name+": "+policyLabel(c.Policy))
	}
	return items, nil
}

// ToolStates は、会話 id の設定 settings でのツールの扱い(会話の設定の画面に出す)。
// 画面がまだ保存していない設定でも見られるよう、設定を受け取る。会話の ID は、プロファイルの決まり(使えない)を引くために使う
// (空なら新しい会話 = 新しい会話で最初に選ばれるプロファイル。0014)。
func (s *Service) ToolStates(ctx context.Context, id uuid.UUID, settings aibow.ConversationSettings) ([]aibow.ToolGroupState, error) {
	if s.startupErr != nil {
		return nil, s.startupErr
	}
	groups, err := s.convs.ConversationToolStates(ctx, id, settings)
	if err != nil {
		return nil, err
	}
	name, err := s.convs.ConversationServerWorkspace(ctx, id)
	if err != nil {
		return nil, err
	}
	if name != "" {
		groups = s.forServerWorkspace(groups)
	}
	return s.forBrowser(groups), nil
}

// forServerWorkspace は、サーバーモードのアプリのサーバーのワークスペースの会話で、ツールの一覧から使わないものを外す(0067 の決定事項 13)。
// ブラウザと同じく run_command と Git のツールを出さず、${workdir} を使う MCP のサーバーも出さない(この PC のフォルダが無いため)。
func (s *Service) forServerWorkspace(groups []aibow.ToolGroupState) []aibow.ToolGroupState {
	var workDir []string
	if s.mcp != nil {
		for _, i := range s.mcp.Infos() {
			if i.UsesWorkDir {
				workDir = append(workDir, i.Name)
			}
		}
	}
	groups = slices.DeleteFunc(groups, func(g aibow.ToolGroupState) bool { return g.Server != "" && slices.Contains(workDir, g.Server) })
	for i := range groups {
		groups[i].Tools = slices.DeleteFunc(groups[i].Tools, func(t aibow.ToolState) bool { return browserHides(t.Name) })
	}
	return groups
}

// forBrowser は、ブラウザに出すツールの一覧から、使えないものを外す。
// コマンドのツールは出さない(0024 の決定事項 5)。サーバーのワークスペースが1つも無ければ、作業フォルダのツールは「作業フォルダが要る」とも見せない(決定事項 17)。
// 出さない MCP のサーバー(MCPServers が外したもの)も外す。
func (s *Service) forBrowser(groups []aibow.ToolGroupState) []aibow.ToolGroupState {
	if !s.browser {
		return groups
	}
	servers := s.MCPServers()
	hasFolders := s.hasServerWorkspaces()
	groups = slices.DeleteFunc(groups, func(g aibow.ToolGroupState) bool {
		return g.Server != "" && !slices.ContainsFunc(servers, func(i client.ServerInfo) bool { return i.Name == g.Server })
	})
	for i := range groups {
		groups[i].Tools = slices.DeleteFunc(groups[i].Tools, func(t aibow.ToolState) bool {
			return browserHides(t.Name) || t.NeedsWorkDir && !hasFolders
		})
	}
	return groups
}

// ServerWorkspaceInfo は、サーバーのワークスペース1つの、誰にでも見せてよいところ(名前とフォルダの名前・印。パスは返さない。0065 の決定事項 19)。
type ServerWorkspaceInfo struct {
	Name    string             `json:"name"`
	Folders []ServerFolderInfo `json:"folders"`
}

// ServerFolderInfo は、サーバーのワークスペースのフォルダ1つの名前と印と説明(パスは返さない)。
// 説明は、サーバーモードのアプリがモデルへの環境の説明に書く(0067 の決定事項 11)。
type ServerFolderInfo struct {
	Name        string `json:"name"`
	Main        bool   `json:"main"`
	Writable    bool   `json:"writable"`
	Description string `json:"description,omitempty"`
}

// ServerWorkspaces は aibow-server のサーバーのワークスペース(server.json の並び。パスは返さない)。配るプロファイルで選ぶ(0065)。
// 名前は誰でも見られる(どのワークスペースを使えるかは、配られたプロファイルで決まる)。入力欄の上でマウスを乗せた説明にフォルダを並べるのにも使う。
// アプリモードのときは空。パスも見る管理の画面は ServerSettings(管理者だけ。0061)。
// サーバーモードのアプリでは、aibow-server に引いた、その人が使えるもの(配られたもの。0067 の決定事項 15。古い aibow-server なら空)。
func (s *Service) ServerWorkspaces() []ServerWorkspaceInfo {
	if s.appWs != nil {
		list, _ := s.appWs.infos(context.Background())
		return list
	}
	return ServerWorkspaceInfos(s.serverWorkspaceList())
}

// ServerWorkspacesUsable は、この画面でサーバーのワークスペースを使えるか(0067)。
// ブラウザはいつも。サーバーモードのアプリは aibow-server がアプリから使える新しい版のとき(古ければ、手元のワークスペースのまま 0064 の知らせを出す)。
// アプリモードは使わない(配布プロファイルが無い)。
func (s *Service) ServerWorkspacesUsable() bool {
	if s.browser {
		return true
	}
	if s.appWs == nil {
		return false
	}
	_, supported := s.appWs.infos(context.Background())
	return supported
}

// ServerWorkspaceInfos は、サーバーのワークスペースの一覧から、誰にでも見せてよいところ(名前とフォルダの名前・印・説明)だけを取り出す。
func ServerWorkspaceInfos(list []config.ServerWorkspace) []ServerWorkspaceInfo {
	out := []ServerWorkspaceInfo{}
	for _, w := range list {
		info := ServerWorkspaceInfo{Name: w.Name, Folders: []ServerFolderInfo{}}
		for _, f := range w.Folders {
			info.Folders = append(info.Folders, ServerFolderInfo{Name: f.Name, Main: f.Main, Writable: f.Writable, Description: f.Description})
		}
		out = append(out, info)
	}
	return out
}

// GrantedServerWorkspaces は、ctx の利用者が使えるサーバーのワークスペース(パスも含む。aibow-server の中だけで使う)。
// 使えるのは、その人に配られている(Share があり Withdrawn でない)配布プロファイルのどれかが選んでいるもの(0067 の決定事項 6)。
// 会話ごとには見ない(サーバーモードのアプリのつながりは、ワークスペースごとに1つで会話をまたぐため)。
// アプリは会話のプロファイルで選ばれているときだけ使うので、決まりはブラウザから使う会話と同じになる。
// 呼ぶたびにプロファイルと一覧を読み直す(配布をやめた・管理の画面でフォルダを変えたら、次の呼び出しから効く)。
func (s *Service) GrantedServerWorkspaces(ctx context.Context) ([]config.ServerWorkspace, error) {
	if s.startupErr != nil {
		return nil, s.startupErr
	}
	profiles, err := s.store.ListProfiles(ctx)
	if err != nil {
		return nil, fmt.Errorf("配布プロファイルを読めません: %w", err)
	}
	granted := map[string]bool{}
	for i := range profiles {
		if name := profiles[i].GrantedWorkspace(); name != "" {
			granted[name] = true
		}
	}
	out := []config.ServerWorkspace{}
	for _, w := range s.serverWorkspaceList() {
		if granted[w.Name] {
			out = append(out, w)
		}
	}
	return out, nil
}

// checkWorkspace は、プロファイルのサーバーのワークスペースを old から name に変えてよいか(0024 の決定事項 31・0065 の決定事項 18)。
// 選べるのは aibow-server の管理者(ログインなしで使うときはいつも。0046)だけで、server.json にあるワークスペースだけ。変えないなら確かめない。
func (s *Service) checkWorkspace(ctx context.Context, old, name string) error {
	if name == old || name == "" {
		return nil
	}
	if !s.browser || !managesPC(ctx) {
		return errors.New("サーバーのワークスペースを選べるのは、aibow-server の管理者だけです")
	}
	var names []string
	for _, w := range s.serverWorkspaceList() {
		if w.Name == name {
			return nil
		}
		names = append(names, w.Name)
	}
	return fmt.Errorf("サーバーのワークスペース %s はありません(管理の画面の「サーバーのワークスペース」のタブにあるもの: %s)", name, strings.Join(names, "・"))
}

// ToolPrefs は、ツールの扱いの PC の既定(tools.json)と、サーバーを起動したときに見えたツールの一覧。
// 新しい会話で最初から使うグループはプロファイルの値なので、設定の画面はプロファイルから出す(ここの notDefault は引き継ぐ前の値)。
// ブラウザでは aibow-server の管理者の既定。ファイルの場所(サーバーの PC のパス)は管理者にだけ見せる(0024 の決定事項 25)。
func (s *Service) ToolPrefs(ctx context.Context) config.ToolPrefsView {
	v := s.prefs.View()
	if s.browser && !managesPC(ctx) {
		v.Path = ""
	}
	return v
}

// LoadToolList は、MCP のサーバーを一時的に起動してツールの一覧を受け取り、tools.json に覚える(設定の画面で、一覧が無いときと「読み直す」)。
// ブラウザでは管理者だけ(サーバーの PC でプロセスを起動するため)。
func (s *Service) LoadToolList(ctx context.Context, server string) error {
	if s.startupErr != nil {
		return s.startupErr
	}
	if err := s.canSetTools(ctx); err != nil {
		return err
	}
	return s.convs.LoadToolList(ctx, server)
}

// SetToolPolicy は、ツールの扱いの PC の既定を変える(tools.json に書く)。policy が空なら決めていない状態に戻す。
// 次の送信から効く(プロンプトに入るツールが変わるので、画面はメーターを数え直す)。
// ブラウザでは aibow-server の管理者だけが変えられる(server-tools.json に書く。使わないは禁止。0024 の決定事項 21・22)。
func (s *Service) SetToolPolicy(ctx context.Context, name string, policy config.ToolPolicy) error {
	return s.SetToolPolicies(ctx, []ToolPolicyChange{{Name: name, Policy: policy}})
}

// canSetTools は、ツールの扱いの既定を変えてよいか。ブラウザでは管理者(ログインなしの形ではいつも。0046)だけ。
// 口でも断っている(web.go の adminToolMethods)。ここでも確かめるのは、口の作りを間違えても漏れないように(サーバーの PC の事情と同じ)。
func (s *Service) canSetTools(ctx context.Context) error {
	if s.browser && !managesPC(ctx) {
		return errors.New("ツールの扱いの既定は、aibow-server を動かしている PC からか、管理者だけが変えられます")
	}
	return nil
}

// prefsName は、ログに出す既定の名前(アプリなら PC の既定、ブラウザなら aibow-server の既定。誰が変えたかは byWhom で添える)。
func (s *Service) prefsName() string {
	if s.browser {
		return " aibow-server の既定"
	}
	return "この PC の既定"
}

func policyLabel(p config.ToolPolicy) string {
	switch p {
	case config.ToolOff:
		return "使わない"
	case config.ToolAsk:
		return "確認する"
	case config.ToolAllow:
		return "確認しない"
	case config.ToolOn:
		return "使う"
	}
	return "決めていない"
}
