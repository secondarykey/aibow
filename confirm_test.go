package aibow

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/secondarykey/aibow/format"
	"github.com/secondarykey/aibow/tool"
)

// newWorkDirConversation は、作業フォルダ(main.go が1つ)を決めた、ツールを使う会話を作る。
func newWorkDirConversation(t *testing.T, g *fakeGen) (*Conversations, Conversation, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cs, _, c := newToolConversation(t, g)
	c.Settings.WorkDir = dir
	if err := cs.UpdateSettings(c.ID, c.Settings); err != nil {
		t.Fatal(err)
	}
	return cs, c, dir
}

// sendAnswering は送信し、確認を求められたら answer で答える(answer が false を返したら答えない)。
func sendAnswering(cs *Conversations, ctx context.Context, c Conversation, text string, answer func(req *ConfirmRequest) bool) []ChatEvent {
	var evs []ChatEvent
	cs.Send(ctx, c.ID, text, func(ev ChatEvent) {
		evs = append(evs, ev)
		if ev.Type == "confirm" {
			answer(ev.Confirm)
		}
	})
	return evs
}

func toolResults(t *testing.T, cs *Conversations, c Conversation) []StoredMessage {
	t.Helper()
	_, ms, _ := cs.Get(c.ID)
	var out []StoredMessage
	for _, m := range ms {
		if m.Role == "tool" {
			out = append(out, m)
		}
	}
	return out
}

func fileText(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return "(無い)"
	}
	return string(b)
}

// 「実行する」と答えると書き換わり、確認の結果が保存される。確認には差分が付く
func TestConfirmApprove(t *testing.T) {
	g := &fakeGen{replies: []fakeReply{
		{toolCalls: []format.ToolCall{toolCall("edit_file", "path", "main.go", "old_string", "package main", "new_string", "package app")}},
		{content: "直しました"},
	}}
	cs, c, dir := newWorkDirConversation(t, g)
	var req *ConfirmRequest
	evs := sendAnswering(cs, context.Background(), c, "パッケージ名を app にして", func(r *ConfirmRequest) bool {
		req = r
		if fileText(t, dir, "main.go") != "package main\n" {
			t.Error("答える前に書き換わっています")
		}
		return cs.Confirm(c.ID, r.ID, true, "") == nil
	})
	if errs := errorsOf(evs); len(errs) != 0 {
		t.Fatalf("エラー: %v", errs)
	}
	if req == nil || req.Call.Name != "edit_file" || req.Preview.Path != "main.go" || !strings.Contains(req.Preview.Diff, "-package main\n") || !strings.Contains(req.Preview.Diff, "+package app\n") {
		t.Errorf("確認: %+v", req)
	}
	if got := fileText(t, dir, "main.go"); got != "package app\n" {
		t.Errorf("書き換わっていません: %q", got)
	}
	rs := toolResults(t, cs, c)
	if len(rs) != 1 || rs[0].ToolResult.Error || rs[0].ToolResult.Confirm == nil || !rs[0].ToolResult.Confirm.Approved {
		t.Errorf("結果: %+v", rs)
	}
	// 2回目の答えは届かない(もう待っていない)
	if err := cs.Confirm(c.ID, req.ID, true, ""); err == nil {
		t.Error("待っていない確認への答えはエラーのはず")
	}
}

// 「拒否」すると実行せず、理由をモデルに返してループを続ける
func TestConfirmReject(t *testing.T) {
	g := &fakeGen{replies: []fakeReply{
		{toolCalls: []format.ToolCall{toolCall("write_file", "path", "main.go", "content", "消しました\n")}},
		{content: "では別の方法にします"},
	}}
	cs, c, dir := newWorkDirConversation(t, g)
	evs := sendAnswering(cs, context.Background(), c, "直して", func(r *ConfirmRequest) bool {
		if r.Preview.Note != "上書き" {
			t.Errorf("上書きの確認のはず: %+v", r.Preview)
		}
		return cs.Confirm(c.ID, r.ID, false, "  全部消さないで  ") == nil
	})
	if errs := errorsOf(evs); len(errs) != 0 {
		t.Fatalf("エラー: %v", errs)
	}
	if got := fileText(t, dir, "main.go"); got != "package main\n" {
		t.Errorf("拒否したのに書き換わっています: %q", got)
	}
	rs := toolResults(t, cs, c)
	if len(rs) != 1 || rs[0].Content != "ユーザーが実行を拒否しました。理由: 全部消さないで" || rs[0].ToolResult.Confirm == nil || rs[0].ToolResult.Confirm.Approved {
		t.Errorf("結果: %+v", rs)
	}
	// 拒否の結果もモデルに渡り、続きを書く
	if last := g.calls[len(g.calls)-1].messages; len(g.calls) != 2 || !strings.Contains(last[len(last)-1].Content, "拒否しました") {
		t.Errorf("拒否の結果がモデルに渡っていません(生成 %d 回)", len(g.calls))
	}
	// 理由が空なら、その旨を書く
	g.replies = []fakeReply{{toolCalls: []format.ToolCall{toolCall("write_file", "path", "a.txt", "content", "x")}}, {content: "はい"}}
	sendAnswering(cs, context.Background(), c, "もう一度", func(r *ConfirmRequest) bool { return cs.Confirm(c.ID, r.ID, false, "") == nil })
	if rs := toolResults(t, cs, c); rs[len(rs)-1].Content != "ユーザーが実行を拒否しました。理由: (書かれていません)" {
		t.Errorf("理由が空のとき: %q", rs[len(rs)-1].Content)
	}
}

// 確認を待っている間に停止できる。待っていた呼び出しにも、後ろの呼び出しにも「停止」の結果を残す
func TestConfirmStopWhileWaiting(t *testing.T) {
	g := &fakeGen{replies: []fakeReply{
		{toolCalls: []format.ToolCall{
			toolCall("write_file", "path", "a.txt", "content", "x"),
			toolCall("write_file", "path", "b.txt", "content", "y"),
		}},
		{content: "ここは生成されない"},
	}}
	cs, c, dir := newWorkDirConversation(t, g)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	evs := sendAnswering(cs, ctx, c, "作って", func(r *ConfirmRequest) bool {
		cancel() // 答えずに停止ボタンを押した
		return false
	})
	if errs := errorsOf(evs); len(errs) != 0 {
		t.Errorf("停止はエラーにしない: %v", errs)
	}
	rs := toolResults(t, cs, c)
	if len(rs) != 2 || !strings.Contains(rs[0].Content, "停止") || !strings.Contains(rs[1].Content, "停止") {
		t.Errorf("結果: %+v", rs)
	}
	if fileText(t, dir, "a.txt") != "(無い)" || len(g.calls) != 1 {
		t.Errorf("停止したのに実行・生成しています(生成 %d 回)", len(g.calls))
	}
	if cs.pending != nil {
		t.Error("待っている確認が残っています")
	}
}

// 確認の内容を作れない(置き換える場所が見つからない)ときは、聞かずにエラーをモデルに返す
func TestConfirmNotAskedWhenPreviewFails(t *testing.T) {
	g := &fakeGen{replies: []fakeReply{
		{toolCalls: []format.ToolCall{toolCall("edit_file", "path", "main.go", "old_string", "1\tpackage main", "new_string", "x")}},
		{content: "読み直します"},
	}}
	cs, c, _ := newWorkDirConversation(t, g)
	asked := false
	sendAnswering(cs, context.Background(), c, "直して", func(*ConfirmRequest) bool { asked = true; return false })
	rs := toolResults(t, cs, c)
	if asked || len(rs) != 1 || !rs[0].ToolResult.Error || !strings.Contains(rs[0].Content, "見つかりません") || rs[0].ToolResult.Confirm != nil {
		t.Errorf("聞いた: %v / 結果: %+v", asked, rs)
	}
}

// run_command も実行前に確認し、実行すると出力と終了コードがモデルに渡る
func TestRunCommandThroughAgent(t *testing.T) {
	g := &fakeGen{replies: []fakeReply{
		{toolCalls: []format.ToolCall{toolCall("run_command", "command", "echo hi")}},
		{content: "hi と出ました"},
	}}
	cs, c, _ := newWorkDirConversation(t, g)
	var req *ConfirmRequest
	sendAnswering(cs, context.Background(), c, "echo して", func(r *ConfirmRequest) bool {
		req = r
		return cs.Confirm(c.ID, r.ID, true, "") == nil
	})
	if req == nil || req.Preview.Command != "echo hi" || req.Preview.Note != "コマンド" {
		t.Errorf("確認: %+v", req)
	}
	if rs := toolResults(t, cs, c); len(rs) != 1 || rs[0].Content != "hi\n(終了コード 0)" {
		t.Errorf("結果: %+v", rs)
	}
	// 環境の説明にシェルの行が入る
	if p := g.calls[0].prompt; !strings.Contains(p, "Shell for run_command: ") {
		t.Errorf("環境の説明にシェルがありません")
	}
}

// limitedTool は時間の上限を自分で決め、止められたら後片付けをして「ここまでの結果」を返すツール(run_command の代わり)
type limitedTool struct{ fakeTool }

func (limitedTool) TimeLimit() time.Duration { return 50 * time.Millisecond }

// 上限を自分で決めるツールは、時間切れの後に返してくる結果(ここまでの出力など)を受け取る
func TestTimeLimiterResultAfterTimeout(t *testing.T) {
	lt := limitedTool{fakeTool{name: "limited", run: func(ctx context.Context, _ tool.Args) (string, error) {
		<-ctx.Done()
		time.Sleep(20 * time.Millisecond) // 後片付け
		return "", errors.New("時間の上限を超えたため止めました。ここまでの出力: 途中")
	}}}
	g := &fakeGen{replies: []fakeReply{{toolCalls: []format.ToolCall{toolCall("limited")}}, {content: "途中でした"}}}
	cs, _, c := newToolConversation(t, g, lt)
	send(t, cs, c, "やって")
	if rs := toolResults(t, cs, c); len(rs) != 1 || !strings.Contains(rs[0].Content, "ここまでの出力: 途中") {
		t.Errorf("結果: %+v", rs)
	}
}
