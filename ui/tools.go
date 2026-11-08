package ui

// tools.go: 画面から呼ぶ、MCP のサーバーとツールの扱いのメソッド(会話で選ぶもの・扱いの既定・資料のフォルダ)。MCP のサーバーを足す・変える・消すのは mcpedit.go。

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"uuid"

	"github.com/secondarykey/aibow"
	"github.com/secondarykey/aibow/log"
	"github.com/secondarykey/aibow/mcp/client"
	"github.com/secondarykey/aibow/tool"
)

// ---- MCP のサーバーとツールの扱い ------------------------------------------------------

// MCPServers は、会話ごとに使うものを選ぶ MCP のサーバーの一覧(settings.json に書いたもの全部)。書いてなければ空。
// 設定の画面の「ツール」のチェックは「新しい会話で最初から選ぶか」だけなので、チェックを外したものもここに出す。
// ブラウザでは aibow-server の server.json に書いたもの。${workdir} を使うサーバーは、資料のフォルダが無ければ出さない
// (資料のフォルダのある会話でだけ起動する。0024 の決定事項 8・24)。
func (s *Service) MCPServers() []client.ServerInfo {
	if s.mcp == nil {
		return []client.ServerInfo{}
	}
	l := s.mcp.Infos()
	if s.browser && len(s.folders) == 0 {
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
// ブラウザでは、コマンドのツールを出さない。資料のフォルダが無ければ、作業フォルダを使わないツール(時刻・計算・URL を読む)だけ(0024 の決定事項 5・17)。
func (s *Service) BuiltinTools() []aibow.KnownTool {
	l := aibow.BuiltinTools()
	if s.browser {
		l = slices.DeleteFunc(l, func(k aibow.KnownTool) bool {
			return k.Name == "run_command" || len(s.folders) == 0 && tool.Find(tool.Folderless(), k.Name) == nil
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
func (s *Service) SetGroupPolicy(ctx context.Context, group string, policy aibow.ToolPolicy) error {
	if err := s.canSetTools(ctx); err != nil {
		return err
	}
	if err := s.prefs.SetGroupPolicy(group, policy); err != nil {
		return err
	}
	log.Noticef("ツールのグループ %s の扱いの%s: %s", cmp.Or(group, "組み込み"), s.prefsName(ctx), policyLabel(policy))
	return nil
}

// ToolStates は、会話 id の設定 settings でのツールの扱い(会話の設定の画面に出す)。
// 画面がまだ保存していない設定でも見られるよう、設定を受け取る。会話の ID は、プロファイルの決まり(使えない)を引くために使う
// (空なら新しい会話 = 新しい会話で最初に選ばれるプロファイル。0014)。
func (s *Service) ToolStates(ctx context.Context, id uuid.UUID, settings aibow.ConversationSettings) ([]aibow.ToolGroupState, error) {
	if s.startupErr != nil {
		return nil, s.startupErr
	}
	groups, err := s.convs.ConversationToolStates(ctx, id, settings)
	return s.forBrowser(groups), err
}

// forBrowser は、ブラウザに出すツールの一覧から、使えないものを外す。
// コマンドのツールは出さない(0024 の決定事項 5)。資料のフォルダが1つも無ければ、作業フォルダのツールは「作業フォルダが要る」とも見せない(決定事項 17)。
// 出さない MCP のサーバー(MCPServers が外したもの)も外す。
func (s *Service) forBrowser(groups []aibow.ToolGroupState) []aibow.ToolGroupState {
	if !s.browser {
		return groups
	}
	servers := s.MCPServers()
	groups = slices.DeleteFunc(groups, func(g aibow.ToolGroupState) bool {
		return g.Server != "" && !slices.ContainsFunc(servers, func(i client.ServerInfo) bool { return i.Name == g.Server })
	})
	for i := range groups {
		groups[i].Tools = slices.DeleteFunc(groups[i].Tools, func(t aibow.ToolState) bool {
			return t.Name == "run_command" || t.NeedsWorkDir && len(s.folders) == 0
		})
	}
	return groups
}

// Folders は aibow-server の資料のフォルダの名前(名前の順。パスは返さない)。配るプロファイルで選ぶ(0024 の 12-2)。
// 名前は誰でも見られる(どのフォルダを使えるかは、配られたプロファイルで決まる)。アプリの中で動かすときは空。
func (s *Service) Folders() []string {
	return slices.Clone(s.folders)
}

// checkFolder は、プロファイルの資料のフォルダを old から folder に変えてよいか(0024 の決定事項 31)。
// 選べるのは aibow-server の管理者(とサーバーの PC の上から開いた画面)だけで、server.json にあるフォルダだけ。変えないなら確かめない。
func (s *Service) checkFolder(ctx context.Context, old, folder string) error {
	if folder == old || folder == "" {
		return nil
	}
	if !s.browser || !managesPC(ctx) {
		return errors.New("資料のフォルダを選べるのは、aibow-server の管理者だけです")
	}
	if !slices.Contains(s.folders, folder) {
		return fmt.Errorf("資料のフォルダ %s はありません(server.json の folders に書いたもの: %s)", folder, strings.Join(s.folders, "・"))
	}
	return nil
}

// ToolPrefs は、ツールの扱いの PC の既定(tools.json)と、サーバーを起動したときに見えたツールの一覧。
// 新しい会話で最初から使うグループはプロファイルの値なので、設定の画面はプロファイルから出す(ここの notDefault は引き継ぐ前の値)。
// ブラウザでは aibow-server の管理者の既定。ファイルの場所(サーバーの PC のパス)は管理者にだけ見せる(0024 の決定事項 25)。
func (s *Service) ToolPrefs(ctx context.Context) aibow.ToolPrefsView {
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
func (s *Service) SetToolPolicy(ctx context.Context, name string, policy aibow.ToolPolicy) error {
	if err := s.canSetTools(ctx); err != nil {
		return err
	}
	if err := s.prefs.SetToolPolicy(name, policy); err != nil {
		return err
	}
	log.Noticef("ツール %s の扱いの%s: %s", name, s.prefsName(ctx), policyLabel(policy))
	return nil
}

// canSetTools は、ツールの扱いの既定を変えてよいか。ブラウザでは管理者(とサーバーの PC の上から開いた画面)だけ。
// 口でも断っている(web.go の adminToolMethods)。ここでも確かめるのは、口の作りを間違えても漏れないように(サーバーの PC の事情と同じ)。
func (s *Service) canSetTools(ctx context.Context) error {
	if s.browser && !managesPC(ctx) {
		return errors.New("ツールの扱いの既定は、aibow-server を動かしている PC からか、管理者だけが変えられます")
	}
	return nil
}

// prefsName は、ログに出す既定の名前(アプリなら PC の既定、ブラウザなら誰が変えた aibow-server の既定か)。
func (s *Service) prefsName(ctx context.Context) string {
	if s.browser {
		return fmt.Sprintf(" aibow-server の既定(%s が変えた)", whoOf(ctx))
	}
	return " PC の既定"
}

func policyLabel(p aibow.ToolPolicy) string {
	switch p {
	case aibow.ToolOff:
		return "使わない"
	case aibow.ToolAsk:
		return "確認する"
	case aibow.ToolAllow:
		return "確認しない"
	case aibow.ToolOn:
		return "使う"
	}
	return "決めていない"
}
