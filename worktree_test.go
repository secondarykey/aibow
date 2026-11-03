package aibow

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/secondarykey/aibow/format"
	"github.com/secondarykey/aibow/git"
)

// gitIn は、テストの一時リポジトリで git を動かす。
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// newWorktreeConversation は、メインのフォルダが Git のリポジトリ(main.go を1つコミット済み)のワークスペースを選び、
// 「worktree で作業する」を入れた会話を作る。git が無ければテストを飛ばす。
func newWorktreeConversation(t *testing.T, g *fakeGen) (*Conversations, Conversation, string) {
	t.Helper()
	if err := git.Available(); err != nil {
		t.Skip(err)
	}
	repo := filepath.Join(t.TempDir(), "proj")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "init", "-q", "-b", "main")
	gitIn(t, repo, "config", "user.name", "aibow test")
	gitIn(t, repo, "config", "user.email", "test@example.com")
	gitIn(t, repo, "config", "core.autocrlf", "false")
	gitIn(t, repo, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-q", "-m", "first")

	cs, _, c := newToolConversation(t, g)
	c.Settings.Workspace = workspaceFor(t, cs, repo)
	c.Settings.Worktree = true
	if err := cs.UpdateSettings(ctx, c.ID, c.Settings); err != nil {
		t.Fatal(err)
	}
	return cs, c, repo
}

// 切り替えを入れた会話では、読むだけなら worktree を作らず、最初の書き換えの前に作る。書き換えは worktree に入り、本体は変わらない。
// write_file・edit_file は確認せず、run_command は確認する。本体の git status に .aibow/ は出ない(0039 の完了の条件 1・2・3・5)
func TestWorktreeWritesGoToWorktree(t *testing.T) {
	g := &fakeGen{replies: []fakeReply{
		{toolCalls: []format.ToolCall{toolCall("read_file", "path", "main.go")}},
		{toolCalls: []format.ToolCall{toolCall("write_file", "path", "new.txt", "content", "new\n")}},
		{toolCalls: []format.ToolCall{toolCall("edit_file", "path", "main.go", "old_string", "package main", "new_string", "package app")}},
		{toolCalls: []format.ToolCall{toolCall("run_command", "command", "echo hi")}},
		{content: "直しました"},
	}}
	cs, c, repo := newWorktreeConversation(t, g)
	// 本体にコミットしていない変更を1つ置いておく(worktree には入らない、と知らせる)
	if err := os.WriteFile(filepath.Join(repo, "memo.txt"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var asked []string
	var worktreeEvents []string
	evs := sendAnswering(cs, ctx, c, "直して", func(r *ConfirmRequest) bool {
		asked = append(asked, r.Call.Name)
		return cs.Confirm(ctx, c.ID, r.ID, true, "") == nil
	})
	for _, ev := range evs {
		if ev.Type == "worktree" {
			worktreeEvents = append(worktreeEvents, ev.Text)
		}
	}
	if errs := errorsOf(evs); len(errs) != 0 {
		t.Fatalf("エラー: %v", errs)
	}
	if !slices.Equal(asked, []string{"run_command"}) {
		t.Errorf("確認したツール = %v, want [run_command]", asked)
	}
	if len(worktreeEvents) != 1 {
		t.Fatalf("worktree を作ったお知らせ = %v(1回のはず)", worktreeEvents)
	}

	wt, _, ok := cs.workspaces.Worktree(c.ID.String())
	if !ok {
		t.Fatal("worktree の結び付けがありません")
	}
	short := strings.ReplaceAll(c.ID.String(), "-", "")[:8]
	if want := filepath.Join(repo, ".aibow", "worktrees", short); wt.Path != want || wt.Branch != "aibow/"+short || wt.Repo != repo || wt.Base == "" || wt.BaseBranch != "main" || wt.BaseSubject != "first" {
		t.Errorf("worktree = %+v(path は %s のはず)", wt, want)
	}
	if fileText(t, repo, "main.go") != "package main\n" || fileText(t, repo, "new.txt") != "(無い)" {
		t.Error("本体が書き換わりました")
	}
	if fileText(t, wt.Path, "main.go") != "package app\n" || fileText(t, wt.Path, "new.txt") != "new\n" {
		t.Error("worktree に書き換えが入っていません")
	}
	if st := gitIn(t, repo, "status", "--porcelain"); strings.Contains(st, ".aibow") {
		t.Errorf("本体の git status に .aibow が出ました: %q", st)
	}

	rs := toolResults(t, cs, c)
	if len(rs) != 4 {
		t.Fatalf("結果 %d 件", len(rs))
	}
	if rs[0].ToolResult.Notice != "" {
		t.Error("読むだけで worktree を作りました")
	}
	note := rs[1].ToolResult.Notice
	if !strings.Contains(note, "worktree を作りました") || !strings.Contains(note, "main の "+wt.Base[:7]+" から") || !strings.Contains(note, "コミットしていない変更 1 件") || !strings.Contains(note, "node_modules") {
		t.Errorf("作ったときのお知らせ: %q", note)
	}
	if strings.Contains(rs[1].Content, "worktree") {
		t.Errorf("モデルに渡す結果に worktree のことが入りました: %q", rs[1].Content)
	}
	if rs[2].ToolResult.Notice != "" || rs[3].ToolResult.Notice != "" {
		t.Error("2回目からもお知らせが付きました")
	}

	// 作ったあとの送信の環境の説明は worktree を指すが、モデルが呼ぶ名前は本体のフォルダのまま
	last := g.calls[len(g.calls)-1].prompt
	if !strings.Contains(last, "Working directory: "+wt.Path) || !strings.Contains(last, `"proj" refers to the working directory itself.`) {
		t.Errorf("環境の説明:\n%s", last)
	}
}

// 差分の一覧・選んだファイルだけを取り込む・取り込み済みの印・捨てる(0039 の完了の条件 6・7・8)
func TestWorktreeApplyAndDiscard(t *testing.T) {
	g := &fakeGen{replies: []fakeReply{
		{toolCalls: []format.ToolCall{
			toolCall("write_file", "path", "new.txt", "content", "new\n"),
			toolCall("edit_file", "path", "main.go", "old_string", "package main", "new_string", "package app"),
		}},
		{content: "直しました"},
	}}
	cs, c, repo := newWorktreeConversation(t, g)
	send(t, cs, c, "直して")

	changes, err := cs.WorktreeChanges(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, ch := range changes {
		got = append(got, ch.Status+" "+ch.Path)
	}
	if !slices.Equal(got, []string{"modified main.go", "added new.txt"}) {
		t.Fatalf("差分 = %v", got)
	}
	diff, err := cs.WorktreeDiff(ctx, c.ID, "main.go")
	if err != nil || !strings.Contains(diff, "-package main") || !strings.Contains(diff, "+package app") || strings.Contains(diff, "new.txt") {
		t.Fatalf("main.go の差分: %v\n%s", err, diff)
	}

	if err := cs.ApplyWorktree(ctx, c.ID, []string{"new.txt"}); err != nil {
		t.Fatal(err)
	}
	if fileText(t, repo, "new.txt") != "new\n" || fileText(t, repo, "main.go") != "package main\n" {
		t.Error("選んだファイルだけが入っていません")
	}
	if idx := gitIn(t, repo, "diff", "--cached", "--name-only"); idx != "" {
		t.Errorf("本体の索引が変わりました: %q", idx)
	}
	changes, _ = cs.WorktreeChanges(ctx, c.ID)
	if changes[0].Applied || !changes[1].Applied {
		t.Errorf("取り込み済みの印: %+v", changes)
	}

	// 本体の同じ行を変えておくと当たらず、何も変わらない
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var ae *git.ApplyError
	if err := cs.ApplyWorktree(ctx, c.ID, []string{"main.go"}); !errors.As(err, &ae) || !slices.Equal(ae.Files, []string{"main.go"}) {
		t.Fatalf("当たらないときのエラー: %v", err)
	}
	if fileText(t, repo, "main.go") != "package other\n" {
		t.Error("当たらないのに本体が変わりました")
	}

	// 作ったあとは、切り替えを外せず、ワークスペースも変えられない
	got2, _, _ := cs.Get(ctx, c.ID)
	set := got2.Settings
	set.Worktree = false
	if err := cs.UpdateSettings(ctx, c.ID, set); err != nil {
		t.Fatal(err)
	}
	if got3, _, _ := cs.Get(ctx, c.ID); !got3.Settings.Worktree {
		t.Error("作ったあとに切り替えが外れました")
	}
	set.Workspace = ""
	if err := cs.UpdateSettings(ctx, c.ID, set); err == nil {
		t.Error("worktree で作業中なのに、ワークスペースを変えられました")
	}

	wt, _, _ := cs.workspaces.Worktree(c.ID.String())
	if err := cs.DiscardWorktree(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt.Path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("worktree のフォルダが残りました: %v", err)
	}
	if out := gitIn(t, repo, "branch", "--list", wt.Branch); strings.TrimSpace(out) != "" {
		t.Errorf("ブランチが残りました: %q", out)
	}
	after, _, _ := cs.Get(ctx, c.ID)
	if after.Settings.Worktree {
		t.Error("捨てたのに切り替えが入ったまま")
	}
	if cs.workDir(after.Settings) != repo {
		t.Errorf("捨てたあとの作業フォルダ = %s(本体 %s のはず)", cs.workDir(after.Settings), repo)
	}
}

// 会話を消すと worktree とブランチも消える
func TestWorktreeRemovedWithConversation(t *testing.T) {
	g := &fakeGen{replies: []fakeReply{
		{toolCalls: []format.ToolCall{toolCall("write_file", "path", "new.txt", "content", "new\n")}},
		{content: "書きました"},
	}}
	cs, c, repo := newWorktreeConversation(t, g)
	send(t, cs, c, "書いて")
	wt, _, ok := cs.workspaces.Worktree(c.ID.String())
	if !ok {
		t.Fatal("worktree を作っていません")
	}
	if err := cs.Delete(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt.Path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("worktree のフォルダが残りました: %v", err)
	}
	if out := gitIn(t, repo, "branch", "--list", wt.Branch); strings.TrimSpace(out) != "" {
		t.Errorf("ブランチが残りました: %q", out)
	}
	if _, _, ok := cs.workspaces.Worktree(c.ID.String()); ok {
		t.Error("結び付けが残りました")
	}
}

// 確認の既定:worktree の会話では write_file・edit_file を確認しない。会話で「確認する」にすればそちらが勝つ。
// Git のリポジトリでないフォルダでは、切り替えを入れても本体を直接書くので、確認する(0039 の完了の条件 5)
func TestWorktreeConfirmDefaults(t *testing.T) {
	cs, c, repo := newWorktreeConversation(t, &fakeGen{})
	policy := func(set ConversationSettings, name string) ToolPolicy {
		for _, g := range cs.ToolStates(set) {
			for _, ts := range g.Tools {
				if ts.Name == name {
					return ts.Policy
				}
			}
		}
		t.Fatalf("ツール %s がありません", name)
		return ""
	}
	conv, _, _ := cs.Get(ctx, c.ID)
	set := conv.Settings
	for name, want := range map[string]ToolPolicy{"write_file": ToolAllow, "edit_file": ToolAllow, "run_command": ToolAsk} {
		if got := policy(set, name); got != want {
			t.Errorf("worktree の会話の %s = %s, want %s", name, got, want)
		}
	}
	set.ToolPolicies = map[string]ToolPolicy{"write_file": ToolAsk}
	if got := policy(set, "write_file"); got != ToolAsk {
		t.Errorf("会話で確認するにした write_file = %s", got)
	}
	set.ToolPolicies = nil
	set.Worktree = false
	if got := policy(set, "write_file"); got != ToolAsk {
		t.Errorf("切り替えを外した write_file = %s", got)
	}

	plain := ConversationSettings{Workspace: workspaceFor(t, cs, t.TempDir()), Worktree: true}
	if got := policy(plain, "write_file"); got != ToolAsk {
		t.Errorf("リポジトリでないフォルダの write_file = %s", got)
	}
	if st := cs.WorktreeStateOf(ctx, c.ID, plain); st.Usable || !strings.Contains(st.Reason, "Git のリポジトリではありません") {
		t.Errorf("リポジトリでないときの状態: %+v", st)
	}
	// 作る前は、本体の今のブランチとコミット(作るときの元)を添える。切り替えを入れていなければ添えない(git を呼ばない)
	head := strings.TrimSpace(gitIn(t, repo, "rev-parse", "HEAD"))
	if st := cs.WorktreeStateOf(ctx, c.ID, conv.Settings); !st.Usable || st.Active || st.BaseBranch != "main" || st.Base != head || st.BaseSubject != "first" {
		t.Errorf("作る前の状態: %+v(元は main の %s のはず)", st, head)
	}
	off := conv.Settings
	off.Worktree = false
	if st := cs.WorktreeStateOf(ctx, c.ID, off); st.Base != "" || st.BaseBranch != "" {
		t.Errorf("切り替えを入れていないのに元を添えた: %+v", st)
	}
}
