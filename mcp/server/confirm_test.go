package server

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/secondarykey/aibow/mcp/internal/meta"
)

// connectWith は、クライアントの設定を決めてサーバーにつなぐ(connect の、確認に答えるクライアント版)。
func connectWith(t *testing.T, dir string, opts *mcp.ClientOptions) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	ss, err := New(Options{WorkDir: dir}).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, opts).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

// answering は、確認(elicitation)に answer で答えるクライアントの設定。聞かれた内容を asked に入れる。
// 送り直し(MRTR)は SDK に任せる(Claude Code など、ほかのクライアントと同じ形)
func answering(asked *[]*mcp.ElicitParams, answer func(*mcp.ElicitParams) *mcp.ElicitResult) *mcp.ClientOptions {
	return &mcp.ClientOptions{
		ElicitationHandler: func(_ context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			*asked = append(*asked, req.Params)
			return answer(req.Params), nil
		},
	}
}

func readFile(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return ""
	}
	return string(b)
}

// 書き換える前に差分を見せて確かめ、受け入れたら書く。差分は _meta(aibow のカード用)と message(ほかのクライアント用)の両方に入る
func TestConfirmAccept(t *testing.T) {
	dir := t.TempDir()
	var asked []*mcp.ElicitParams
	cs := connectWith(t, dir, answering(&asked, func(*mcp.ElicitParams) *mcp.ElicitResult {
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{}}
	}))
	text, isErr := call(t, cs, "write_file", map[string]any{"path": "new.txt", "content": "hello\n"})
	if isErr || readFile(t, dir, "new.txt") != "hello\n" {
		t.Fatalf("受け入れたら書く: %q (isError %v)", text, isErr)
	}
	if len(asked) != 1 {
		t.Fatalf("聞いた回数: %d", len(asked))
	}
	q := asked[0]
	if !strings.Contains(q.Message, "write_file") || !strings.Contains(q.Message, "+hello") {
		t.Errorf("message に差分: %q", q.Message)
	}
	pv, _ := q.Meta[meta.Preview].(map[string]any)
	if pv["path"] != "new.txt" || !strings.Contains(pv["diff"].(string), "+hello") {
		t.Errorf("_meta に確認の内容: %v", q.Meta)
	}
}

// 拒否したら書かず、理由をモデルに返す(エラーではない)
func TestConfirmDecline(t *testing.T) {
	dir := t.TempDir()
	var asked []*mcp.ElicitParams
	cs := connectWith(t, dir, answering(&asked, func(*mcp.ElicitParams) *mcp.ElicitResult {
		return &mcp.ElicitResult{Action: "decline", Content: map[string]any{"reason": "名前が違う"}}
	}))
	text, isErr := call(t, cs, "write_file", map[string]any{"path": "new.txt", "content": "hello"})
	if isErr || !strings.Contains(text, "拒否しました") || !strings.Contains(text, "名前が違う") {
		t.Errorf("拒否の結果: %q (isError %v)", text, isErr)
	}
	if _, err := os.Stat(filepath.Join(dir, "new.txt")); !os.IsNotExist(err) {
		t.Error("拒否したのに書いた")
	}
}

// 確認(elicitation)に対応していないクライアントには、書き換えずにエラーを返す(0007 の決定事項 6)。読むツールは使える
func TestConfirmNoElicitation(t *testing.T) {
	dir := t.TempDir()
	cs := connect(t, Options{WorkDir: dir}) // ElicitationHandler の無いクライアント
	for _, c := range []struct {
		tool string
		args map[string]any
	}{
		{"write_file", map[string]any{"path": "new.txt", "content": "hello"}},
		{"run_command", map[string]any{"command": "echo hi> made.txt"}},
	} {
		text, isErr := call(t, cs, c.tool, c.args)
		if !isErr || !strings.Contains(text, "書き換えません") {
			t.Errorf("%s = %q (isError %v)", c.tool, text, isErr)
		}
	}
	for _, f := range []string{"new.txt", "made.txt"} {
		if _, err := os.Stat(filepath.Join(dir, f)); !os.IsNotExist(err) {
			t.Errorf("%s が作られています(実行してはいけない)", f)
		}
	}
	if _, isErr := call(t, cs, "list_files", nil); isErr {
		t.Error("読むツールは確認しないので使える")
	}
}

// 確認のあとでファイルが変わったら、見せた差分と違うので書き換えない(サーバーは覚えていないので、requestState で確かめる)
func TestConfirmChangedAfterAsking(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 送り直しを自分で書くクライアント(aibow と同じ形)。間でファイルを書き換えるため
	cs := connectWith(t, dir, &mcp.ClientOptions{
		Capabilities:   &mcp.ClientCapabilities{Elicitation: &mcp.ElicitationCapabilities{Form: &mcp.FormElicitationCapabilities{}}},
		MultiRoundTrip: &mcp.MultiRoundTripOptions{Disabled: true},
	})
	ctx := context.Background()
	args := map[string]any{"path": "a.txt", "old_string": "one", "new_string": "two"}
	first, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "edit_file", Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	if !first.NeedsInput() || first.RequestState == "" || first.InputRequests[confirmKey] == nil {
		t.Fatalf("1回目は確認を求める(input_required): %+v", first)
	}

	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\nthree\n"), 0o644) // 確認の間にほかで書き換えられた
	retry := &mcp.CallToolParams{Name: "edit_file", Arguments: args, RequestState: first.RequestState,
		InputResponses: mcp.InputResponseMap{confirmKey: &mcp.ElicitResult{Action: "accept"}}}
	res, err := cs.CallTool(ctx, retry)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || readFile(t, dir, "a.txt") != "one\nthree\n" {
		t.Errorf("変わっていたら書き換えない: %+v / %q", res, readFile(t, dir, "a.txt"))
	}

	// 合言葉が合えば書き換える(作り直した差分が、見せたものと同じ)
	again, _ := cs.CallTool(ctx, &mcp.CallToolParams{Name: "edit_file", Arguments: args})
	retry.RequestState = again.RequestState
	if res, err := cs.CallTool(ctx, retry); err != nil || res.IsError || readFile(t, dir, "a.txt") != "two\nthree\n" {
		t.Errorf("合言葉が合えば書き換える: %+v %v / %q", res, err, readFile(t, dir, "a.txt"))
	}
}

// クライアントが止めると(notifications/cancelled)、サーバーの中の run_command の子プロセスまで止まる
func TestStopRunCommand(t *testing.T) {
	if testing.Short() {
		t.Skip("数秒かかる")
	}
	dir := t.TempDir()
	var asked []*mcp.ElicitParams
	cs := connectWith(t, dir, answering(&asked, func(*mcp.ElicitParams) *mcp.ElicitResult {
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{}}
	}))
	// 3 秒待ってからファイルを作るコマンド。途中で止めれば、ファイルは作られない
	command := "sleep 3 && echo late > late.txt"
	if runtime.GOOS == "windows" {
		command = "ping -n 4 127.0.0.1 >nul && echo late> late.txt"
	}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(500*time.Millisecond, cancel)
	start := time.Now()
	_, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "run_command", Arguments: map[string]any{"command": command}})
	if err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("止めたらすぐ返る: %v(%v)", err, time.Since(start))
	}
	time.Sleep(4 * time.Second) // 止まっていなければ、この間にファイルができる
	if _, err := os.Stat(filepath.Join(dir, "late.txt")); !os.IsNotExist(err) {
		t.Error("止めたのにコマンドが最後まで動いた(子プロセスが残った)")
	}
}
