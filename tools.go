package aibow

// tools.go: 会話で使うツール。並べる(toolsFor)・使わない / 確認する / 確認しないの決め方(toolPolicy・policyOf)・設定の画面に出す状態(ToolStates)・
// 組み込みのツールと MCP のサーバーのツールの受け取りと起動(LocalTools・RemoteTools)。
//
// エージェントループ(agent.go)は、ここで決めた「使えるツール」と「扱い」を見て実行するだけ。扱いの既定(その PC の tools.json)は toolprefs.go。

import (
	"context"
	"errors"
	"slices"
	"strings"
	"uuid"

	"github.com/secondarykey/aibow/format"
	"github.com/secondarykey/aibow/log"
	"github.com/secondarykey/aibow/tool"
)

// toolsFor は会話で使えるツール。使うかどうかは toolPolicy で決める(設定の画面の「ツール」と、会話の上書き)。
// 組み込みのツールの後ろに、起動済みの MCP のサーバーのツールを並べる。
func (cs *Conversations) toolsFor(set ConversationSettings) []tool.Tool {
	// 「使わない」にしたツールはモデルに見せない(説明もプロンプトに入らない)
	// 組み込みのツールは、この会話で外していなければ(入力欄の上の「この会話のツール」)
	var tools []tool.Tool
	for _, t := range cs.localTools(set) {
		if !set.BuiltinOff && cs.toolPolicy(set, t) != ToolOff {
			tools = append(tools, t)
		}
	}
	if cs.remote == nil {
		return tools
	}
	for _, t := range cs.remote.Tools(cs.workDir(set), cs.usableServers(set)) {
		if cs.toolPolicy(set, t) == ToolOff {
			continue
		}
		// MCP のツールの名前にはサーバーの名前が前に付いている(filesystem__read_file。mcp/client の ToolName)ので、ふつうは重ならない。
		// それでも重なったら出さない(先にあるほう = 組み込みのツールを使う)。同じ名前が2つあると、モデルも aibow も区別できないため。
		// 重なったものは、起動したときにログに出す(logRemoteTools)
		if tool.Find(tools, t.Def().Name) == nil {
			tools = append(tools, t)
		}
	}
	return tools
}

// toolPolicy は、会話でのツールの扱い(使わない / 確認する / 確認しない)を決める。扱いを決めるのはここだけ。
//
//  0. プロファイルで使えない(0014 の 5。会話のプロファイルの決まり)・aibow-server の管理者が禁止した(0024 の決定事項 22)。
//     会話の上書きでも使えるようにはできない
//  1. 会話ごとの上書き(会話の設定の tool_policies)。「使う」(on)は使うかだけを決め、確認するかは 2・3 で決める
//  2. PC の既定(tools.json)。グループ(組み込み / MCP のサーバー)ごと「使わない」なら使わない。
//     そうでなければ、ツールごと → グループごとの順(ToolPrefs.policy)
//  3. 決まりの既定: MCP のツールは確認する(中身が見えないので)。
//     組み込みのツールは、書き換えるツール・コマンド(tool.Confirmable)は確認する、読むだけのツールは確認しない(今までの動き)
//
// の順に見て、最初に決めてあったものを使う。
func (cs *Conversations) toolPolicy(set ConversationSettings, t tool.Tool) ToolPolicy {
	group := "" // 組み込みのツール
	if r, ok := t.(tool.Remote); ok {
		group = r.Server()
	}
	_, confirmable := t.(tool.Confirmable)
	p, _ := cs.policyOf(set, group, t.Def().Name, confirmable)
	return p
}

// どこでツールの扱いが決まったか(会話の設定の画面に出す。ToolState.From)
const (
	PolicyFromProfile      = "profile"      // プロファイルの決まり(使えない。0014)
	PolicyFromConversation = "conversation" // 会話ごとの上書き(tool_policies)
	PolicyFromPC           = "pc"           // PC の既定(tools.json。aibow-server なら管理者の既定 server-tools.json)
	PolicyFromAdmin        = "admin"        // aibow-server の管理者が禁止した(server-tools.json の「使わない」。0024 の決定事項 22)
	PolicyFromDefault      = "default"      // 決まりの既定
)

// policyOf は toolPolicy の中身。ツールの実体が無くても(起動していない MCP のサーバーのツールでも)決められるよう、
// グループ・名前・書き換えるツールか(tool.Confirmable)で受け取り、どこで決まったかも返す。
//
// 返す扱いは off / ask / allow のどれか(on は返さない。使うことにしたうえで、確認するかを返す)。
func (cs *Conversations) policyOf(set ConversationSettings, group, name string, confirmable bool) (ToolPolicy, string) {
	if set.profile != nil && set.profile.Rules.offTool(group, name) {
		return ToolOff, PolicyFromProfile
	}
	if cs.prefs.banned(group, name) {
		return ToolOff, PolicyFromAdmin // aibow-server の管理者が禁止した(会話の上書きでも使えない。0024 の決定事項 22)
	}
	p, ok := set.ToolPolicies[name]
	if ok && p != ToolOn {
		return p, PolicyFromConversation
	}
	if ok {
		// この会話では使う。PC の既定の「使わない」は「既定では使わない」の意味なので打ち消し、確認するかだけを PC の既定から取る
		if c, cok := cs.prefs.confirmPolicy(group, name); cok {
			return c, PolicyFromConversation
		}
		return cs.defaultPolicy(set, group, name, confirmable), PolicyFromConversation
	}
	if p, ok := cs.prefs.policy(group, name); ok {
		return p, PolicyFromPC
	}
	return cs.defaultPolicy(set, group, name, confirmable), PolicyFromDefault
}

// defaultPolicy は、会話の設定 set での決まりの既定。worktree で作業する会話では、ファイルを書き換えるツールを確認しない
// (書き換えは worktree に入り、本体に入れる前に差分の画面でまとめて見られるため。0039)。
// run_command は確認する(コマンドは worktree の外にも書けるため)。会話・PC で「確認する」にしていれば、そちらが先に効く(policyOf)。
func (cs *Conversations) defaultPolicy(set ConversationSettings, group, name string, confirmable bool) ToolPolicy {
	if group == "" && worktreeWrites[name] && cs.worktreeMode(set) {
		return ToolAllow
	}
	return defaultPolicy(group, confirmable)
}

// defaultPolicy は決まりの既定: MCP のツールは確認する。組み込みのツールは、書き換えるツール・コマンドは確認する、読むだけのツールは確認しない。
func defaultPolicy(group string, confirmable bool) ToolPolicy {
	if !confirmable && group == "" {
		return ToolAllow
	}
	return ToolAsk
}

// ToolGroupState は、会話の設定の画面に出すツールのグループ1つ(組み込み / MCP のサーバー1つ)。
type ToolGroupState struct {
	Server string      `json:"server"` // MCP のサーバーの名前。組み込みのツールなら空
	Tools  []ToolState `json:"tools"`  // MCP のサーバーは、最後に起動したときに見えたツール(tools.json の known)。まだ無ければ空
}

// ToolState は、会話でのツール1つの扱い。
type ToolState struct {
	Name         string     `json:"name"`           // モデルに見せる名前
	Description  string     `json:"description"`    // 説明の1行目
	Policy       ToolPolicy `json:"policy"`         // 使わない / 確認する / 確認しない(toolPolicy と同じ決め方)
	From         string     `json:"from"`           // どこで決まったか(PolicyFrom…)
	Default      ToolPolicy `json:"default"`        // 会話の上書きが無いときの扱い(PC の既定か決まりの既定)。画面は、これと同じにするなら上書きを消す
	NeedsWorkDir bool       `json:"needs_work_dir"` // 作業フォルダを決めた会話でだけ使える(組み込みのファイル・コマンドのツール)
}

// ToolStates は、会話の設定 set でのツールの扱いを、グループごとに返す(会話の設定の画面に出す)。
// 会話で使うグループを選んでいるか(builtin_off / mcp_servers)は set を見れば分かるので、ここでは見ない。
// 扱いは toolPolicy と同じ policyOf で決める(扱いを決める場所を1つにするため。画面で同じ判断を書き直さない)。
func (cs *Conversations) ToolStates(set ConversationSettings) []ToolGroupState {
	builtin := ToolGroupState{Tools: []ToolState{}}
	add := func(t tool.Tool, needsWorkDir bool) {
		_, confirmable := t.(tool.Confirmable)
		p, from := cs.policyOf(set, "", t.Def().Name, confirmable)
		// 会話の上書きを外したときの扱い。worktree で作業する会話かで既定が変わるので、それは残す(0039)
		bare := ConversationSettings{profile: set.profile, Workspace: set.Workspace, Worktree: set.Worktree, conv: set.conv}
		def, _ := cs.policyOf(bare, "", t.Def().Name, confirmable)
		builtin.Tools = append(builtin.Tools, ToolState{
			Name: t.Def().Name, Description: firstLine(t.Def().Description), Policy: p, From: from, Default: def, NeedsWorkDir: needsWorkDir,
		})
	}
	for _, t := range tool.Folderless() {
		add(t, false)
	}
	// 作業フォルダのツールは、定義が作業フォルダによらないので仮のフォルダで作る(BuiltinTools と同じ)
	for _, t := range append(tool.Files("."), tool.UpdatePlan(), tool.RunCommand(".")) {
		add(t, true)
	}
	groups := []ToolGroupState{builtin}
	if cs.remote == nil {
		return groups
	}
	known := cs.prefs.View().Known
	for _, server := range cs.remote.Servers() {
		g := ToolGroupState{Server: server, Tools: []ToolState{}}
		for _, k := range known[server] {
			p, from := cs.policyOf(set, server, k.Name, false)
			def, _ := cs.policyOf(ConversationSettings{profile: set.profile}, server, k.Name, false)
			g.Tools = append(g.Tools, ToolState{Name: k.Name, Description: k.Description, Policy: p, From: from, Default: def})
		}
		groups = append(groups, g)
	}
	return groups
}

// ConversationToolStates は、会話 id のプロファイルの決まりを当てて、設定 set でのツールの扱いを返す(ToolStates)。
// id が空なら、新しい会話(新しい会話で最初に選ばれるプロファイル)。画面から来た set は決まりを持たないので、ここで足す。
func (cs *Conversations) ConversationToolStates(ctx context.Context, id uuid.UUID, set ConversationSettings) ([]ToolGroupState, error) {
	var p *Profile
	if id == (uuid.UUID{}) {
		d, err := cs.DefaultProfile(ctx)
		if err != nil {
			return nil, err
		}
		if d.ID != (uuid.UUID{}) {
			p = &d
		}
	} else {
		c, _, err := cs.store.Conversation(ctx, id)
		if err != nil {
			return nil, err
		}
		if p, err = cs.profileFor(ctx, c.ProfileID); err != nil {
			return nil, err
		}
	}
	set.profile = p
	return cs.ToolStates(cs.forUse(set)), nil
}

// usableServers は、会話で選んだ MCP のサーバーのうち、プロファイルで使えないものでも、PC の既定でサーバーごと「使わない」にしたものでもないもの。
// 使わないサーバーは、選んであっても起動しない。
func (cs *Conversations) usableServers(set ConversationSettings) []string {
	var out []string
	for _, name := range set.MCPServers {
		if set.profile != nil && set.profile.Rules.offGroup(name) {
			continue
		}
		if p, ok := cs.prefs.groupPolicy(name); !ok || p != ToolOff {
			out = append(out, name)
		}
	}
	return out
}

// LocalTools は、aibow の中で動くツール(組み込みと作業フォルダのツール)を渡す係(0007 の 5-4)。
//
// 会話の仕組みは、ツールが tool パッケージを直接呼ぶものか、aibow の中の自作の MCP サーバー越しに呼ぶものかを知らない。
// 名前・グループ(組み込み)・扱いの決まりの既定は、どちらでも同じ。
type LocalTools interface {
	// Tools は、作業フォルダ(フォルダの組 folders。ワークスペース。0035)の会話で使う、aibow の中のツール
	// (folders が空なら、作業フォルダを使わないツールだけ)
	Tools(folders []tool.Folder) []tool.Tool
}

// SetLocalTools は、aibow の中で動くツールを渡す係を決める(アプリの起動時に1回)。決めなければ tool パッケージのツールを直接呼ぶ。
func (cs *Conversations) SetLocalTools(l LocalTools) {
	cs.local = l
}

// localTools は、aibow の中で動くツール(組み込みと、作業フォルダのツール)。使うかどうかは見ない(toolsFor が見る)。
// 作業フォルダを決めた会話だけ、そのフォルダの中を扱うツールを足す(作業フォルダは会話ごとなので、ここで作る)。
func (cs *Conversations) localTools(set ConversationSettings) []tool.Tool {
	folders := cs.toolFolders(set)
	if cs.local != nil {
		return cs.local.Tools(folders)
	}
	if len(folders) == 0 {
		return cs.tools
	}
	// 作業の予定(update_plan。0037)もフォルダのある会話だけで出す(mcp/server の ToolsIn と同じ並び)
	tools := append(append(slices.Clip(cs.tools), tool.FilesIn(folders)...), tool.UpdatePlan(), tool.RunCommandIn(folders))
	return append(tools, tool.GitIn(folders)...) // 読むだけの Git のツール(0041)
}

// RemoteTools は、aibow の外のプログラムが実行するツールの置き場(MCP のサーバー。本物は mcp/client の Manager)。
// 会話の仕組みは MCP を知らない。ツールが tool.Tool の形で並んでいれば、エージェントループはそのまま使える。
type RemoteTools interface {
	// Servers は settings.json に書いたサーバーの名前(名前の順)
	Servers() []string
	// Start は、作業フォルダ workDir の会話で使うサーバー(use)のうち、動いていないものを起動する。
	// 起動したサーバーの名前と、起動できなかったサーバーのエラーを返す
	Start(ctx context.Context, workDir string, use []string) (started []string, errs []error)
	// Tools は、会話で使うサーバー(use)の、起動済みのツール(起動はしない。メーターの計算などからも呼ばれるため)
	Tools(workDir string, use []string) []tool.Tool
	// ListTools は、サーバーを一時的に起動してツールの一覧を受け取る(設定の画面で、会話で使う前にツールを選べるように)
	ListTools(ctx context.Context, server string) ([]tool.Tool, error)
}

// LoadToolList は、サーバーを一時的に起動してツールの一覧を受け取り、PC の既定(tools.json)に覚える。
func (cs *Conversations) LoadToolList(ctx context.Context, server string) error {
	if cs.remote == nil {
		return errors.New("MCP のサーバーがありません(settings.json の mcpServers)")
	}
	tools, err := cs.remote.ListTools(ctx, server)
	if err != nil {
		return err
	}
	return cs.prefs.setKnown(server, knownTools(tools))
}

// BuiltinTools は、組み込みのツール(aibow の中で動くツール)の一覧(設定の画面に出す)。
// 作業フォルダのツールも入れる(作業フォルダを決めた会話でだけ使える)。定義は作業フォルダによらないので、仮のフォルダで作る。
func BuiltinTools() []KnownTool {
	tools := append(tool.Folderless(), tool.Files(".")...)
	tools = append(tools, tool.UpdatePlan(), tool.RunCommand("."))
	return knownTools(append(tools, tool.GitTools()...)) // Git のツールはリポジトリの会話でだけ出るが、扱いは前もって決められるように並べる
}

// knownTools は、設定の画面に出すツールの一覧(名前と説明の1行目)。
func knownTools(tools []tool.Tool) []KnownTool {
	known := make([]KnownTool, 0, len(tools))
	for _, t := range tools {
		known = append(known, KnownTool{Name: t.Def().Name, Description: firstLine(t.Def().Description)})
	}
	return known
}

// SetToolPrefs は、ツールの扱いの PC の既定(tools.json)を使えるようにする(アプリの起動時に1回)。
func (cs *Conversations) SetToolPrefs(p *ToolPrefs) {
	cs.prefs = p
}

// SetRemoteTools は MCP のサーバーのツールを使えるようにする(アプリの起動時に1回)。
func (cs *Conversations) SetRemoteTools(r RemoteTools) {
	cs.remote = r
}

// startRemote は、会話で使う MCP のサーバーを、送信の前に起動する。
// 起動できなかったサーバーは、お知らせ(トースト)に出して、そのサーバーのツール無しで続ける。
func (cs *Conversations) startRemote(ctx context.Context, conv Conversation, emit func(ChatEvent)) {
	set := conv.Settings
	if cs.remote == nil {
		return
	}
	started, errs := cs.remote.Start(ctx, cs.workDir(set), cs.usableServers(set))
	for _, err := range errs {
		log.Warn(err.Error())
		emit(ChatEvent{ConversationID: conv.ID, Type: "notice", Text: err.Error() + "(このサーバーのツールは使わずに続けます)"})
	}
	if len(started) > 0 {
		cs.rememberTools(set, started)
		cs.logRemoteTools(set)
	}
}

// rememberTools は、起動したサーバーのツールの一覧を PC の既定(tools.json)に覚える。
// 設定の画面で、サーバーを起動していなくてもツールの一覧を出して、使う / 使わないを決められるようにするため。
func (cs *Conversations) rememberTools(set ConversationSettings, started []string) {
	for _, server := range started {
		if err := cs.prefs.setKnown(server, knownTools(cs.remote.Tools(cs.workDir(set), []string{server}))); err != nil {
			log.Warnf("MCP のサーバー %s のツールの一覧を覚えられません: %v", server, err)
		}
	}
}

// logRemoteTools は、MCP のツールの数と、その定義がプロンプトをどれだけ長くするかをログに出す(学習用)。
// ツールの定義はシステムプロンプトに毎回入るので、会話の上限を使う。既製のサーバーはツールが多く、説明も長い。
func (cs *Conversations) logRemoteTools(set ConversationSettings) {
	local, all := cs.localTools(set), cs.toolsFor(set)
	var skipped []string
	for _, t := range cs.remote.Tools(cs.workDir(set), cs.usableServers(set)) {
		if tool.Find(local, t.Def().Name) != nil {
			skipped = append(skipped, t.Def().Name)
		}
	}
	before, err1 := cs.gen.CountTokens(format.Prompt{Tools: tool.Defs(local)})
	after, err2 := cs.gen.CountTokens(format.Prompt{Tools: tool.Defs(all)})
	if err := errors.Join(err1, err2); err != nil {
		log.Warnf("ツールの定義のトークン数を数えられません: %v", err)
		return
	}
	log.Infof("ツール: 組み込み %d 個 + MCP %d 個。MCP のツールの定義で、プロンプトが約 %d トークン長くなります(ツールの定義の合計は約 %d トークン)",
		len(local), len(all)-len(local), after-before, after)
	if len(skipped) > 0 {
		log.Warnf("MCP のツールのうち、組み込みのツールと名前が重なる %s は出しません", strings.Join(skipped, ", "))
	}
}
