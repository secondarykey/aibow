package aibow

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/secondarykey/aibow/format"
)

// 作業の決まり(0041)は、作業フォルダがあり書き換えるツールがある会話にだけ付き、
// 確かめる行は run_command があるときだけ出る(完了の条件 1)。
// 作業フォルダの無い会話・読むだけの会話のシステムプロンプトは、決まりを足す前と1文字も変わらない
func TestWorkingRulesOnlyWithEditTools(t *testing.T) {
	dir := t.TempDir()
	g := &fakeGen{content: "はい"}
	cs, _, c := newToolConversation(t, g)
	set := c.Settings
	prefs := &ToolPrefs{}
	cs.SetToolPrefs(prefs)

	const check = "check your work: run the tests the user asked for"
	steps := []struct {
		name      string
		workDir   string
		prefs     func()
		wantRules bool
		wantCheck bool
	}{
		{"作業フォルダなし", "", func() {}, false, false},
		{"作業フォルダあり", dir, func() {}, true, true},
		{"run_command を使わない", dir, func() { prefs.SetToolPolicy("run_command", ToolOff) }, true, false},
		{"write_file だけ使わない", dir, func() { prefs.SetToolPolicy("write_file", ToolOff) }, true, false},
		{"書き換えるツールを使わない(読むだけ)", dir, func() { prefs.SetToolPolicy("edit_file", ToolOff) }, false, false},
		{"組み込みのツールを使わない", dir, func() { prefs.SetBuiltinPolicy(ToolOff) }, false, false},
	}
	for i, s := range steps {
		set.Workspace = workspaceFor(t, cs, s.workDir)
		s.prefs()
		if err := cs.UpdateSettings(ctx, c.ID, set); err != nil {
			t.Fatal(err)
		}
		send(t, cs, c, "質問")
		prompt := g.calls[i].prompt
		if got := strings.Contains(prompt, "# Working rules"); got != s.wantRules {
			t.Errorf("%s: 作業の決まり %v(期待: %v)\n%s", s.name, got, s.wantRules, prompt)
		}
		if got := strings.Contains(prompt, check); got != s.wantCheck {
			t.Errorf("%s: 確かめる行 %v(期待: %v)", s.name, got, s.wantCheck)
		}
		if s.wantRules && !s.wantCheck && (!strings.Contains(prompt, "not only in your thinking: what you changed.") || strings.Contains(prompt, "result of the check")) {
			t.Errorf("%s: 確かめる道具が無いのに、確かめた結果を求めています\n%s", s.name, prompt)
		}

		// 決まりの付かない会話は、決まりを足す前の組み立て(System + 環境の説明)と同じ
		if !s.wantRules {
			used := cs.forUse(set)
			want := used.System
			if env := cs.environment(used); env != "" {
				want = strings.TrimSpace(used.System) + "\n\n" + env
			}
			if got := cs.systemPrompt(used); got != want {
				t.Errorf("%s: システムプロンプトが変わりました\n%s\n期待:\n%s", s.name, got, want)
			}
		}
	}
}

// 決まりの文は短く保つ(目安は 150 トークン以内。決定事項 5)。
// fakeGen はトークンの代わりに文字数を数えるので、ここでは英語の文字数で見る(英語はおよそ 4 文字で 1 トークン)
func TestWorkingRulesAreShort(t *testing.T) {
	cs, _ := newTestConversations(t, &fakeGen{})
	set := testDefaults
	set.Workspace = workspaceFor(t, cs, t.TempDir())
	rules := workingRules(cs.toolsFor(set))
	if n := len(rules); n == 0 || n > 150*5 {
		t.Errorf("作業の決まりが %d 文字です:\n%s", n, rules)
	}
}

// リポジトリのワークスペースでは、読むだけの Git のツールが出て、決まりの確かめる行に git_diff の一文が付く(0041 の段2)。
// worktree の会話では、git_diff は worktree の中で動き、会話で変えたものだけが出る(本体のコミットしていない変更は出ない。決定事項 13)
func TestGitToolsInWorktreeConversation(t *testing.T) {
	g := &fakeGen{replies: []fakeReply{
		{content: "Greet to Hi"}, // worktree の名前を書かせる1回
		{toolCalls: []format.ToolCall{toolCall("edit_file", "path", "main.go", "old_string", "package main", "new_string", "package app")}},
		{toolCalls: []format.ToolCall{toolCall("git_diff")}},
		{toolCalls: []format.ToolCall{toolCall("git_status")}},
		{content: "直しました"},
	}}
	cs, c, repo := newWorktreeConversation(t, g)
	if err := os.WriteFile(filepath.Join(repo, "memo.txt"), []byte("本体だけのメモ\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	evs := sendAnswering(cs, ctx, c, "直して", func(r *ConfirmRequest) bool { return cs.Confirm(ctx, c.ID, r.ID, true, "") == nil })
	if errs := errorsOf(evs); len(errs) != 0 {
		t.Fatalf("エラー: %v", errs)
	}

	first := g.calls[1] // 名前を書かせる回の次が、最初の返事の生成
	if names := toolNamesOf(first.tools); !slices.Contains(names, "git_status") || !slices.Contains(names, "git_diff") {
		t.Errorf("Git のツールがありません: %v", names)
	}
	if !strings.Contains(first.prompt, "and fix what fails. You can review your changes with git_diff.") {
		t.Errorf("決まりに git_diff の一文がありません:\n%s", first.prompt)
	}

	rs := toolResults(t, cs, c)
	if len(rs) != 3 {
		t.Fatalf("結果 %d 件", len(rs))
	}
	if d := rs[1].Content; !strings.Contains(d, "+package app") || strings.Contains(d, "memo.txt") {
		t.Errorf("git_diff の結果:\n%s", d)
	}
	if s := rs[2].Content; s != "modified: main.go" {
		t.Errorf("git_status の結果: %q(worktree の中だけのはず)", s)
	}
	if rs[1].ToolResult.Confirm != nil || rs[2].ToolResult.Confirm != nil {
		t.Error("読むだけの Git のツールで確認しました")
	}
}
