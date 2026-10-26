package ui

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/secondarykey/aibow"
	"github.com/secondarykey/aibow/auth"
	"github.com/secondarykey/aibow/format"
	"github.com/secondarykey/aibow/tool"
)

// ブラウザの会話のツール(0024 の 12-1a)のテスト。確認のカードは SSE で届き、答えは画面用の口で返す。

// calcCall は、計算のツールの呼び出し(偽物の engine に書かせる)。
func calcCall(expr string) []format.ToolCall {
	return []format.ToolCall{{Call: tool.Call{Name: "calculate", Args: []tool.Arg{{Name: "expression", Value: expr}}}}}
}

// askCalc は、会話 c で計算のツールを「確認する」にする(ふだんは確認しないツールで、確認の往復を確かめるため。利用者が足せる扱い)。
func (w *testWeb) askCalc(t *testing.T, c aibow.Conversation) {
	t.Helper()
	s := c.Settings
	s.ToolPolicies = map[string]aibow.ToolPolicy{"calculate": aibow.ToolAsk}
	w.mustCall(t, nil, "UpdateConversationSettings", c.ID, s)
}

// sendAsync は送信を別の goroutine で呼ぶ(生成が終わるまで返らないため)。返った状態を流す。
func (w *testWeb) sendAsync(t *testing.T, ctx context.Context, id uuid.UUID, text string) <-chan int {
	done := make(chan int, 1)
	go func() {
		status, _ := w.call(t, ctx, "Send", id, text)
		done <- status
	}()
	return done
}

func (w *testWeb) addUser(t *testing.T, name string) *uuid.UUID {
	t.Helper()
	now := time.Now()
	u := auth.User{ID: uuid.NewV7(), Name: name, CreatedAt: now, UpdatedAt: now}
	if err := w.st.CreateUser(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	uctx := aibow.WithUser(context.Background(), u.ID)
	app, _, _ := aibow.LoadAppSettings(uctx, w.st, InitialAppSettings(InitialValues{GPULayers: 999, PresencePenalty: 1.5, HistoryTokens: 8192, ReplyTokens: 8192}))
	if _, err := aibow.NewConversations(w.st, nil, app).PrepareProfiles(uctx); err != nil {
		t.Fatal(err)
	}
	return &u.ID
}

// toolResult は、会話 id の最後のツールの結果。
func (w *testWeb) toolResult(t *testing.T, id uuid.UUID) aibow.StoredMessage {
	t.Helper()
	var d ConversationDetail
	w.mustCall(t, &d, "Conversation", id)
	for i := len(d.Messages) - 1; i >= 0; i-- {
		if d.Messages[i].Role == "tool" {
			return d.Messages[i]
		}
	}
	t.Fatalf("ツールの結果がありません: %+v", d.Messages)
	return aibow.StoredMessage{}
}

// 確認のカードが SSE で届き、ConfirmTool の答えで実行する。「この会話では今後聞かない」は次の呼び出しから効く
func TestWebConfirm(t *testing.T) {
	w := newTestWeb(t)
	events := w.openEvents(t)
	c := w.newConversation(t)
	w.askCalc(t, c)
	w.eng.calls = [][]format.ToolCall{calcCall("1+2"), calcCall("3*4")}

	done := w.sendAsync(t, context.Background(), c.ID, "計算して")
	got := until(t, events, "confirm")
	req := got[len(got)-1].Confirm
	if req == nil || req.Call.Name != "calculate" {
		t.Fatalf("確認のカード: %+v", got[len(got)-1])
	}
	// つなぎ直した画面は、待っている確認を取り直せる
	var pending *aibow.ConfirmRequest
	w.mustCall(t, &pending, "PendingConfirm", c.ID)
	if pending == nil || pending.ID != req.ID {
		t.Fatalf("PendingConfirm: %+v", pending)
	}
	// 待っている間も席を持っている(0024 の決定事項 16)
	if w.seat.TryTake() {
		t.Fatal("確認を待っている間に席が空いている")
	}

	w.mustCall(t, nil, "ConfirmTool", c.ID, req.ID, true, "", aibow.ToolAllow)
	got = until(t, events, "tool_result")
	if m := got[len(got)-1].Message; m == nil || m.Content != "3" || m.ToolResult.Confirm == nil || !m.ToolResult.Confirm.Approved {
		t.Fatalf("計算の結果: %+v", got[len(got)-1].Message)
	}
	// 2回目は「この会話では今後聞かない」にしたので、聞かずに実行する
	got = until(t, events, "done")
	for _, e := range got {
		if e.Type == "confirm" {
			t.Errorf("今後聞かないにしたのに聞いた: %+v", e.Confirm)
		}
	}
	if status := <-done; status != http.StatusOK {
		t.Errorf("Send: %d", status)
	}
	if r := w.toolResult(t, c.ID); r.Content != "12" {
		t.Errorf("2回目の結果: %q", r.Content)
	}
	w.mustCall(t, &pending, "PendingConfirm", c.ID)
	if pending != nil {
		t.Errorf("終わったのに待っている確認がある: %+v", pending)
	}
	var d ConversationDetail
	w.mustCall(t, &d, "Conversation", c.ID)
	if d.Conversation.Settings.ToolPolicies["calculate"] != aibow.ToolAllow {
		t.Errorf("カードからの「今後聞かない」が会話に残るはず: %+v", d.Conversation.Settings.ToolPolicies)
	}
	// カードで決めた「確認しない」は、会話の設定を保存し直しても残せる
	w.mustCall(t, nil, "UpdateConversationSettings", c.ID, d.Conversation.Settings)
}

// 拒否すると実行せず、理由をモデルに返す
func TestWebConfirmReject(t *testing.T) {
	w := newTestWeb(t)
	events := w.openEvents(t)
	c := w.newConversation(t)
	w.askCalc(t, c)
	w.eng.calls = [][]format.ToolCall{calcCall("1+2")}

	done := w.sendAsync(t, context.Background(), c.ID, "計算して")
	got := until(t, events, "confirm")
	w.mustCall(t, nil, "ConfirmTool", c.ID, got[len(got)-1].Confirm.ID, false, "暗算で", "")
	until(t, events, "done")
	<-done
	if r := w.toolResult(t, c.ID); !strings.Contains(r.Content, "拒否") || !strings.Contains(r.Content, "暗算で") {
		t.Errorf("拒否の結果: %q", r.Content)
	}
}

// ほかの人の会話の確認は、見えも答えられもしない
func TestWebConfirmOtherUser(t *testing.T) {
	w := newTestWeb(t)
	bob, carol := w.addUser(t, "bob"), w.addUser(t, "carol")
	w.user.Store(bob)
	events := w.openEvents(t)
	c := w.newConversation(t)
	w.askCalc(t, c)
	w.eng.calls = [][]format.ToolCall{calcCall("1+2")}

	done := w.sendAsync(t, context.Background(), c.ID, "計算して")
	got := until(t, events, "confirm")
	req := got[len(got)-1].Confirm

	w.user.Store(carol)
	if status, body := w.call(t, context.Background(), "PendingConfirm", c.ID); status == http.StatusOK {
		t.Errorf("ほかの人の確認が見えた: %s", body)
	}
	if status, body := w.call(t, context.Background(), "ConfirmTool", c.ID, req.ID, true, "", ""); status == http.StatusOK {
		t.Errorf("ほかの人の確認に答えられた: %s", body)
	}

	w.user.Store(bob)
	w.mustCall(t, nil, "ConfirmTool", c.ID, req.ID, true, "", "")
	until(t, events, "done")
	<-done
	if r := w.toolResult(t, c.ID); r.Content != "3" {
		t.Errorf("持ち主の答えで実行するはず: %q", r.Content)
	}
}

// 確認を待っている間に送信の呼び出しが切れたら(タブを閉じた・読み直した)、停止と同じ(0024 の決定事項 15)
func TestWebConfirmClosed(t *testing.T) {
	w := newTestWeb(t)
	events := w.openEvents(t)
	c := w.newConversation(t)
	w.askCalc(t, c)
	w.eng.calls = [][]format.ToolCall{calcCall("1+2")}

	ctx, cancel := context.WithCancel(context.Background())
	done := w.sendAsync(t, ctx, c.ID, "計算して")
	until(t, events, "confirm")
	cancel()
	until(t, events, "done")
	<-done
	if r := w.toolResult(t, c.ID); !strings.Contains(r.Content, "停止されたため実行していません") {
		t.Errorf("止めたときの結果: %q", r.Content)
	}
	if !w.seat.TryTake() {
		t.Error("止めたのに席が返っていない")
	}
}

// 確認に confirmLimit 答えがなければ、送信を止めて席を返す(0024 の決定事項 16)
func TestWebConfirmLimit(t *testing.T) {
	old := confirmLimit
	confirmLimit = 200 * time.Millisecond
	t.Cleanup(func() { confirmLimit = old })

	w := newTestWeb(t)
	events := w.openEvents(t)
	c := w.newConversation(t)
	w.askCalc(t, c)
	w.eng.calls = [][]format.ToolCall{calcCall("1+2")}

	done := w.sendAsync(t, context.Background(), c.ID, "計算して")
	got := until(t, events, "done")
	var notice *aibow.ChatEvent
	for i, e := range got {
		if e.Type == "error" {
			notice = &got[i]
		}
	}
	if notice == nil || !notice.Caution || !strings.Contains(notice.Error, "答えがなかったので、送信を止めました") {
		t.Errorf("打ち切りの知らせ: %+v(届いたもの: %v)", notice, types(got))
	}
	<-done
	if r := w.toolResult(t, c.ID); !strings.Contains(r.Content, "停止されたため実行していません") {
		t.Errorf("打ち切ったときの結果: %q", r.Content)
	}
	if !w.seat.TryTake() {
		t.Error("打ち切ったのに席が返っていない")
	}
}

// 答えたら打ち切りのタイマーは止まる(答えたあとの実行と続きの生成を止めない)
func TestWebConfirmAnsweredBeforeLimit(t *testing.T) {
	old := confirmLimit
	confirmLimit = 300 * time.Millisecond
	t.Cleanup(func() { confirmLimit = old })

	w := newTestWeb(t)
	events := w.openEvents(t)
	c := w.newConversation(t)
	w.askCalc(t, c)
	w.eng.calls = [][]format.ToolCall{calcCall("1+2")}
	w.eng.wait, w.eng.started = true, make(chan struct{}) // 答えたあとの生成を、上限を過ぎるまで続ける

	ctx, cancel := context.WithCancel(context.Background())
	done := w.sendAsync(t, ctx, c.ID, "計算して")
	got := until(t, events, "confirm")
	w.mustCall(t, nil, "ConfirmTool", c.ID, got[len(got)-1].Confirm.ID, true, "", "")
	<-w.eng.started
	time.Sleep(2 * confirmLimit)
	cancel() // 偽物の engine は止めるまで書き続ける。タイマーで止まっていたら、ここより前に done が来ている
	got = until(t, events, "done")
	for _, e := range got {
		if e.Type == "error" {
			t.Errorf("答えたのに打ち切った: %+v", e)
		}
	}
	<-done
}

// ブラウザの会話は、会話に保存した作業フォルダを使わない(サーバーの PC のファイルのツールとコマンドを出さない。0024 の決定事項 17)
func TestWebIgnoresWorkDir(t *testing.T) {
	w := newTestWeb(t)
	events := w.openEvents(t)
	dir := t.TempDir()

	// ブラウザからは作業フォルダを書けない(作る会話にも、設定を変えるときにも)
	var c aibow.Conversation
	w.mustCall(t, &c, "NewConversation", uuid.UUID{}, aibow.ConversationSettings{WorkDir: dir, HistoryTokens: 8192, ReplyTokens: 8192})
	ctx := aibow.WithUser(context.Background(), aibow.LocalUser)
	stored, _, err := w.st.Conversation(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Settings.WorkDir != "" {
		t.Errorf("作る会話に作業フォルダが入った: %q", stored.Settings.WorkDir)
	}

	// サーバー越しのアプリで作った会話には、その PC の作業フォルダが入っている
	stored.Settings.WorkDir = dir
	if err := w.st.UpdateConversation(ctx, stored); err != nil {
		t.Fatal(err)
	}
	var d ConversationDetail
	w.mustCall(t, &d, "Conversation", c.ID)
	if d.Conversation.Settings.WorkDir != "" {
		t.Errorf("ブラウザに作業フォルダを見せている: %q", d.Conversation.Settings.WorkDir)
	}
	var groups []aibow.ToolGroupState
	s := d.Conversation.Settings
	s.WorkDir = dir // 画面から送られてきた設定に書いてあっても
	w.mustCall(t, &groups, "ToolStates", c.ID, s)
	for _, g := range groups {
		for _, tl := range g.Tools {
			if tl.Name != "get_current_time" && tl.Name != "calculate" {
				t.Errorf("作業フォルダのツールが見えている: %s", tl.Name)
			}
		}
	}
	w.mustCall(t, nil, "UpdateConversationSettings", c.ID, s)
	if stored, _, _ = w.st.Conversation(ctx, c.ID); stored.Settings.WorkDir != dir {
		t.Errorf("保存してある作業フォルダは残すはず: %q", stored.Settings.WorkDir)
	}

	w.mustCall(t, nil, "Send", c.ID, "ファイルを見せて")
	until(t, events, "done")
	last := len(w.eng.tools) - 1
	if strings.Join(w.eng.tools[last], ",") != "calculate,get_current_time" {
		t.Errorf("作業フォルダのツールを知らせている: %v", w.eng.tools[last])
	}
	if p := w.eng.prompts[last]; strings.Contains(p, "Working directory") || strings.Contains(p, dir) {
		t.Errorf("システムプロンプトに作業フォルダが入っている: %q", p)
	}
}

// ブラウザの利用者は、会話の設定で「確認しない」を足せない(足せるのは確認のカードから。0024 の決定事項 18)
func TestWebNoNewAllow(t *testing.T) {
	w := newTestWeb(t)
	c := w.newConversation(t)
	s := c.Settings
	s.ToolPolicies = map[string]aibow.ToolPolicy{"calculate": aibow.ToolAllow}
	status, body := w.call(t, context.Background(), "UpdateConversationSettings", c.ID, s)
	if status == http.StatusOK || !strings.Contains(string(body), "確認のカード") {
		t.Errorf("確認しないを足せた: %d %s", status, body)
	}
	status, body = w.call(t, context.Background(), "NewConversation", uuid.UUID{}, s)
	if status == http.StatusOK {
		t.Errorf("確認しないの会話を作れた: %s", body)
	}
	// 使わない・確認するは足せる
	s.ToolPolicies = map[string]aibow.ToolPolicy{"calculate": aibow.ToolAsk, "get_current_time": aibow.ToolOff}
	w.mustCall(t, nil, "UpdateConversationSettings", c.ID, s)
}
