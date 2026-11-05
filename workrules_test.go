package aibow

import (
	"strings"
	"testing"
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

	const check = "with run_command, and fix what fails."
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
		if s.wantRules && !s.wantCheck && (!strings.Contains(prompt, "- End with a short answer: what you changed.") || strings.Contains(prompt, "result of the check")) {
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
