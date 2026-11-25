package aibow

// prompt.go: モデルに渡す内容を組み立てる。システムプロンプト(会話の設定 + 環境の説明 + 作業の決まり + MCP のサーバーの説明)と、要約と履歴を並べた発言の列と、
// ツールの定義を入れた format.Prompt。書式(チャットテンプレート)を当てるのはエンジンの側(format)で、ここは書式を知らない。
// 作業の決まりの中身は workrules.go。

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/secondarykey/aibow/format"
	"github.com/secondarykey/aibow/git"
	"github.com/secondarykey/aibow/log"
	"github.com/secondarykey/aibow/tool"
)

// systemPrompt は、会話の設定のシステムプロンプトに、環境の説明(environment)と作業の決まり(workingRules。0041)と
// MCP のサーバーの説明(serverInstructions。0066)を足したもの。
func (cs *Conversations) systemPrompt(set ConversationSettings) string {
	env, rules := cs.systemParts(set)
	servers := cs.serverInstructions(set)
	if env == "" && servers == "" {
		return set.System // コードが足すものが無ければ、ユーザーが書いたまま
	}
	return joinSections(set.System, env, rules, servers)
}

// systemParts は、システムプロンプトにコードが足す部分(環境の説明・作業の決まり)。
// 作業の決まりは、環境の説明がある会話(作業フォルダを使うツールがある)にだけ付ける
func (cs *Conversations) systemParts(set ConversationSettings) (env, rules string) {
	env = cs.environment(set)
	if env == "" {
		return "", ""
	}
	return env, workingRules(cs.toolsFor(set))
}

// joinSections は、空でない節を空行で区切ってつなぐ。
func joinSections(parts ...string) string {
	var out []string
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return strings.Join(out, "\n\n")
}

// logSystemParts は、システムプロンプトのうち、環境の説明と作業の決まり・MCP のサーバーの説明がそれぞれ何トークンかをログに出す
// (学習用。0041 の決定事項 9・0066 の決定事項 11)。
// どれも毎回プロンプトの先頭に入るので、会話の上限をその分だけ使う(_docs/tuning.md に書けるように)
func (cs *Conversations) logSystemParts(conv Conversation) {
	set := conv.Settings
	env, rules := cs.systemParts(set)
	servers := cs.serverInstructions(set)
	if env == "" && servers == "" {
		return
	}
	count := func(s string) (int, error) {
		return cs.gen.CountTokens(format.Prompt{Messages: promptMessages(s, "", nil)})
	}
	base, err1 := count(set.System)
	withEnv, err2 := count(joinSections(set.System, env))
	withRules, err3 := count(joinSections(set.System, env, rules))
	withServers, err4 := count(joinSections(set.System, env, rules, servers))
	if err := errors.Join(err1, err2, err3, err4); err != nil {
		log.Warnf("会話 %s: システムプロンプトに足した説明(環境・作業の決まり・MCP のサーバー)のトークン数を数えられません: %v", conv.ID, err)
		return
	}
	var parts []string
	switch {
	case env == "":
	case rules == "":
		parts = append(parts, fmt.Sprintf("環境の説明は約 %d トークン(書き換えるツールが無いので、作業の決まりは付けていません)", withEnv-base))
	default:
		parts = append(parts, fmt.Sprintf("環境の説明は約 %d トークン、作業の決まりは約 %d トークン", withEnv-base, withRules-withEnv))
	}
	if servers != "" {
		parts = append(parts, fmt.Sprintf("MCP のサーバーの説明は約 %d トークン", withServers-withRules))
	}
	log.Infof("会話 %s: システムプロンプトの%s", conv.ID, strings.Join(parts, "、"))
}

// serverInstructionsHeading は、MCP のサーバーの説明の節の頭(0066 の決定事項 3・7)。
// ほかの節(環境の説明・作業の決まり)と同じく英語。ツールの名前の頭がサーバーの名前だと添え、見出しの名前とツールを結び付けやすくする
const serverInstructionsHeading = "# MCP server instructions\n" +
	"The following MCP servers have provided instructions for how to use their tools (each tool name starts with the server name and \"__\"):"

// serverInstructions は、この会話でツールを渡している MCP のサーバーの、使い方の説明(MCP の instructions。0066)。
//
// サーバーは、ツールの説明だけでは伝えきれない前提(データの形・ツールを呼ぶ順番・終わったらすること)を、つないだときの説明に書いてくる。
// ツールの定義と同じく毎回プロンプトに入って会話の上限を使うので、ツールを1つ以上モデルに渡しているサーバーの説明だけを入れる
// (選んでいない・起動していない・ツールを全部「使わない」にしたサーバーは入れない。決定事項 5)。
// 並びはツールと同じサーバーの名前の順。会話の中で変わらないので、システムプロンプトのいちばん後ろに置く(決定事項 2)
func (cs *Conversations) serverInstructions(set ConversationSettings) string {
	if cs.remote == nil {
		return ""
	}
	var servers []string
	for _, t := range cs.toolsFor(set) {
		if r, ok := t.(tool.Remote); ok && !slices.Contains(servers, r.Server()) {
			servers = append(servers, r.Server())
		}
	}
	if len(servers) == 0 {
		return ""
	}
	texts := cs.remote.Instructions(cs.workDir(set), servers)
	var b strings.Builder
	for _, name := range servers {
		text := strings.TrimSpace(texts[name])
		if text == "" {
			continue
		}
		b.WriteString("\n\n## " + name + "\n" + text)
	}
	if b.Len() == 0 {
		return ""
	}
	return serverInstructionsHeading + b.String()
}

// environment は、作業フォルダを決めた会話で、モデルに知らせる環境の説明。
//
// モデルは自分がどこで動いているかを知らないので、作業フォルダ・OS を書いておく(パスの書き方を間違えにくくする)。
// ツールの指示文に合わせて英語。書式に関係ない情報なので、書式ではなく会話の仕組みの側で組み立てる。
// 会話の中で変わらないので、システムプロンプトの後ろ(要約より前)に置き、KV キャッシュの再利用を崩さない。
//
// 使うツールに合わせて書く:時刻・計算のほかにツールが無ければ書かない(作業フォルダを使うツールが無い)。
// シェルと cd の注意は、run_command を使うときだけ書く(使わないツールのことを書かない)。
func (cs *Conversations) environment(set ConversationSettings) string {
	dir := cs.workDir(set)
	if dir == "" {
		return ""
	}
	tools := cs.toolsFor(set)
	usesFolder := slices.ContainsFunc(tools, func(t tool.Tool) bool {
		n := t.Def().Name
		return n != "get_current_time" && n != "calculate"
	})
	if !usesFolder {
		return ""
	}
	command := tool.Find(tools, "run_command") != nil
	shell := tool.ShellNoteFor(tool.Find(tools, "search_files") != nil) // search_files を「使わない」にしたら、今までの findstr の案内のまま
	if folders := cs.toolFolders(set); len(folders) > 1 {
		return foldersEnvironment(folders, command, shell, cs.noWorkDir)
	}
	var b strings.Builder
	b.WriteString("# Environment\n")
	// サーバーのワークスペース(aibow-server)は、サーバーの PC のパスを見せず、フォルダの名前だけを書く
	// (パスはツールの中だけで使う。0024 の決定事項 32・0065 の決定事項 13)。名前はファイルのツールが知っている名前(パスの先頭に付けた書き方を見分ける)と同じ
	name := filepath.Base(cs.mainPath(set, dir)) // worktree でも、本体のフォルダの名前(0039。モデルが呼ぶ名前を変えない)
	if cs.noWorkDir {
		name = tool.MainFolder(cs.toolFolders(set)).Name
		b.WriteString("Working directory: \"" + name + "\" (a shared folder on the server)\n")
	} else {
		b.WriteString("Working directory: " + dir + "\n")
	}
	b.WriteString("OS: " + runtime.GOOS + "\n")
	if command {
		b.WriteString("Shell for run_command: " + shell + "\n")
	}
	b.WriteString("Use paths relative to the working directory.\n")
	// ユーザーが「ytscroll をコミットして」のようにフォルダの名前で呼ぶと、4B は ytscroll を作業フォルダの下のフォルダだと思い、
	// 「cd ytscroll && git status」と書いて失敗した(3 回に 1 回)。すでにその中にいることと、その名前は作業フォルダそのものだと書く
	if command {
		b.WriteString("Commands already run inside the working directory, so do not cd into it. ")
	}
	b.WriteString("\"" + name + "\" refers to the working directory itself.")
	return b.String()
}

// foldersEnvironment は、ワークスペースのフォルダが複数のときの環境の説明(0035 の段2)。
//
// フォルダを名前・印(メイン・書ける / 読むだけ・リポジトリ)・パス・説明(ユーザーが書いたもの)で並べ、ファイルのツールの引数 folder で選ぶこと、省けばメインになることを書く。
// run_command も引数 folder でフォルダを選び、省けばメインで動く(読むだけのフォルダでも動かせる)。
// 読むだけのフォルダもコマンドからは書けてしまうが、守りは確認なので、ここに書くのは「read-only」の印まで。
// フォルダが1つのときは今までの形のまま(environment)。4B のプロンプトを変えないため。
//
// onServer は aibow-server のサーバーのワークスペース(0065 の決定事項 13)。サーバーの PC のパスは見せず、パスの代わりに「on the server」と書く
// (名前・印・説明は出す)。git の印も付けない(ブラウザの会話には Git のツールを出さないため。決定事項 5)。
func foldersEnvironment(folders []tool.Folder, command bool, shell string, onServer bool) string {
	var b strings.Builder
	b.WriteString("# Environment\nFolders (pass the folder argument; omit it for the main folder):\n")
	for _, f := range folders {
		if onServer {
			f.Path = "on the server"
			b.WriteString("- " + tool.FolderLine(f, false) + "\n")
			continue
		}
		b.WriteString("- " + tool.FolderLine(f, git.IsRepo(f.Path)) + "\n")
	}
	b.WriteString("OS: " + runtime.GOOS + "\n")
	if command {
		b.WriteString("Shell for run_command: " + shell + "\n")
	}
	b.WriteString("Use paths relative to the folder.\n")
	main := tool.MainFolder(folders).Name
	if command {
		// run_command も folder で選ぶ(2026-11-02)。cd でほかのフォルダに移ろうとすると、メインの下を探したり、
		// cmd.exe では /d が無いと別のドライブに移れなかったりするので、cd ではなく folder を使うよう書く。
		// 呼ぶたびに新しいシェルなので cd は次に持ち越されないことも書く(同じシェルが続くと思い込むと、メインで動いてしまう)。
		// サブフォルダへの cd まで禁じて読めないよう、「一覧のほかのフォルダ」に絞る
		b.WriteString("Each command starts in the main folder \"" + main + "\" unless you pass folder; a cd does not carry over to the next command. " +
			"To run in another folder of the list, pass folder instead of cd. ")
	}
	b.WriteString("Each folder name refers to that folder itself, not to a subfolder.")
	return b.String()
}

// promptMessages は保存した発言を、モデルに渡す形にする。
// 返事の思考も渡す。プロンプトに入れるかどうかは書式が決める(Qwen3.5 は、ツールのループの途中の返事だけ入れる)。
// システムプロンプトは発言としては保存せず、会話の設定から毎回先頭に付ける。
//
// summary はこれまでの会話の要約(compact.go)。システムプロンプトの後ろに見出しを付けて足す。
// システムプロンプトが空でも、要約があれば system を作る。
func promptMessages(system, summary string, history []StoredMessage) []format.Message {
	var out []format.Message
	s := strings.TrimSpace(system)
	if summary != "" {
		if s != "" {
			s += "\n\n"
		}
		s += summaryHeading + "\n" + summary
	}
	if s != "" {
		out = append(out, format.Message{Role: "system", Content: s})
	}
	for _, m := range history {
		// 読み込んだファイルは、発言の文の前に展開する(0030。書式はファイルのことを知らない)
		fm := format.Message{Role: m.Role, Content: withFiles(m.Content, m.Files), Images: imagesOf(m.Files), Thinking: m.Thinking, ToolCalls: m.ToolCalls}
		if m.ToolResult != nil {
			// ツールの結果には、元の呼び出しの名前を付ける(Gemma 4 は結果に名前を書く。0026 の決定事項 5)
			fm.Name = m.ToolResult.Call.Name
		}
		elideForModel(m, &fm) // 今の組の中の compact で省略した一歩は、結果を札に・思考を空にする(0040。elide.go)
		out = append(out, fm)
	}
	return out
}

// promptFor は、会話の設定でモデルに渡す内容(書式を当てる前の構造)を作る。
// 「ツールを使う」が ON ならツールの定義も入れる。
func (cs *Conversations) promptFor(set ConversationSettings, messages []format.Message) format.Prompt {
	return format.Prompt{Messages: messages, Tools: tool.Defs(cs.toolsFor(set)), Think: set.Think}
}
