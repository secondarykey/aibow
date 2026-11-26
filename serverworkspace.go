package aibow

// serverworkspace.go: サーバーモードのアプリで、aibow-server のサーバーのワークスペースを使う(0067。0065 の段2)。
//
// ブラウザから使う会話(aibow-server の中)は、配布プロファイルで選んだサーバーのワークスペースのフォルダを、
// aibow-server の中の MCP のサーバーにメモリ内でつないで読み書きする(SetServerWorkspaces・IgnoreWorkDir)。
// サーバーモードのアプリは会話の仕組みを手元で動かすので、ファイルのツールだけを aibow-server の MCP の口に頼む:
//
//	アプリの会話の仕組み ─ ツールの組(toolsFor) ┬ 時刻・計算・URL を読む … 手元(LocalTools)
//	                                          └ ファイルのツール・update_plan … aibow-server(ServerWorkspaceSource。HTTP の MCP)
//
// 決まりはブラウザと同じ(0067 の決定事項 2・3):
//   - どのワークスペースを使うかは会話のプロファイルで決まる(Profile.GrantedWorkspace。会話ごとに選ばない)
//   - その会話では手元のワークスペース・worktree・run_command・Git のツール・${workdir} の MCP のサーバーを使わない(混ぜない)
//   - モデルにはサーバーのパスも手元のパスも見せない(フォルダの名前と印と説明だけ)
//
// 会話に保存した手元のワークスペースは消さない。使えなくなったら(配布をやめた・ワークスペースが消えた)、
// 手元のワークスペースに戻すのでなく、ファイルのツールの無い会話になる(決定事項 12。勝手に手元のフォルダを使わない)。
// aibow-server が古く口が無いときだけ、今までどおり手元のワークスペースを使う(0064 の知らせが出る。決定事項 16)。

import (
	"context"

	"github.com/secondarykey/aibow/tool"
)

// ServerWorkspaceSource は、サーバーモードのアプリが aibow-server のサーバーのワークスペースを使う係(本物は ui が api/client と mcp/client で組む)。
type ServerWorkspaceSource interface {
	// Workspace は、ワークスペース name のフォルダ(名前・メイン・書ける・説明。パスは空)。
	// supported が false なら aibow-server が古く、アプリからは使えない。ok が false なら、この人は使えない(配られていない・消えた・一覧を引けない)
	Workspace(name string) (folders []tool.Folder, ok, supported bool)
	// Tools は、ワークスペース name のファイルのツール(aibow-server の MCP の口越し)。つなげなければ空
	Tools(name string) []tool.Tool
	// Refresh は、一覧を引き直し、つなげなかったワークスペースをまたつなぐようにする(送信のたび)
	Refresh(ctx context.Context)
}

// SetServerWorkspaceSource は、サーバーモードのアプリで、サーバーのワークスペースを使う係を決める(起動のときに1回)。
func (cs *Conversations) SetServerWorkspaceSource(s ServerWorkspaceSource) {
	cs.serverSrc = s
}

// appServerWorkspace は、サーバーモードのアプリで、会話の設定 set がサーバーのワークスペースを使うなら、その名前と true。
// 会話のプロファイルで配られていて、aibow-server がアプリから使える新しい版のとき(使えるかどうか ok は見ない。使えなくても手元に戻さないため)。
func (cs *Conversations) appServerWorkspace(set ConversationSettings) (string, bool) {
	if cs.serverSrc == nil || cs.noWorkDir {
		return "", false
	}
	name := set.profile.GrantedWorkspace()
	if name == "" {
		return "", false
	}
	_, _, supported := cs.serverSrc.Workspace(name)
	return name, supported
}

// onServer は、会話の設定 set のファイルのツールが、サーバーのワークスペース(aibow-server の PC のフォルダ)のものか。
// ブラウザから使う会話はいつも(フォルダが無ければファイルのツールが無いだけ)、サーバーモードのアプリは配られた会話だけ。
// そのときは、手元のワークスペースと worktree を使わず、モデルにパスを見せない。
func (cs *Conversations) onServer(set ConversationSettings) bool {
	if cs.noWorkDir {
		return true
	}
	_, ok := cs.appServerWorkspace(set)
	return ok
}

// appServerFolders は、サーバーモードのアプリのサーバーのワークスペースのフォルダ(パスは空)。使えなければ空。
func (cs *Conversations) appServerFolders(name string) []tool.Folder {
	folders, ok, _ := cs.serverSrc.Workspace(name)
	if !ok {
		return nil
	}
	return folders
}

// appServerTools は、サーバーモードのアプリのサーバーのワークスペースの会話で使う、手元で動くツール(時刻・計算・URL を読む)と、
// aibow-server の MCP の口越しのファイルのツール。使えなければ手元のものだけ(手元のフォルダのツールは出さない)。
func (cs *Conversations) appServerTools(name string) []tool.Tool {
	var tools []tool.Tool
	if cs.local != nil {
		tools = cs.local.Tools(nil)
	} else {
		tools = cs.tools
	}
	if len(cs.appServerFolders(name)) == 0 {
		return tools
	}
	out := append([]tool.Tool(nil), tools...)
	for _, t := range cs.serverSrc.Tools(name) {
		if tool.Find(out, t.Def().Name) == nil {
			out = append(out, t)
		}
	}
	return out
}

// refreshServerWorkspace は、送信の前に、サーバーのワークスペースの一覧を引き直す(配布をやめた・フォルダを変えたら、この送信から効く)。
func (cs *Conversations) refreshServerWorkspace(ctx context.Context, set ConversationSettings) {
	if cs.serverSrc == nil || cs.noWorkDir || set.profile.GrantedWorkspace() == "" {
		return
	}
	cs.serverSrc.Refresh(ctx)
}
