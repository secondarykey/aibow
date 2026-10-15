package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/secondarykey/aibow/log"
	"github.com/secondarykey/aibow/mcp/internal/meta"
	"github.com/secondarykey/aibow/tool"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestMain: 環境変数 AIBOW_MCP_TEST_SERVER=1 で起動されたら、テストの実行ファイル自身が MCP のサーバーとして動く
// (標準入出力で話す)。本物の子プロセスでのつなぎ方(TestCommandServer)を、Node などを入れずに確かめるため。
//
// "fail" ならエラー出力に理由を書いてすぐ終わり、"hang" なら何も話さずに待ち続ける(起動の失敗と時間切れを確かめるため)。
func TestMain(m *testing.M) {
	switch os.Getenv("AIBOW_MCP_TEST_SERVER") {
	case "1":
		if err := newTestServer().Run(context.Background(), &mcp.StdioTransport{}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	case "fail":
		fmt.Fprintln(os.Stderr, "loading config...")
		fmt.Fprintln(os.Stderr, "error: failed to persist temporary file: access denied")
		os.Exit(1)
	case "hang":
		time.Sleep(time.Hour)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// newTestServer は、テスト用の MCP のサーバー(SDK で作る)。
func newTestServer() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "0.1"}, nil)
	add := func(name, schema string, h mcp.ToolHandler) {
		s.AddTool(&mcp.Tool{Name: name, Description: "Test tool " + name + ".", InputSchema: json.RawMessage(schema)}, h)
	}
	text := func(s string) *mcp.CallToolResult {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
	}
	add("echo", `{"type": "object", "properties": {"paths": {"type": "array", "items": {"type": "string"}}, "loud": {"type": "boolean"}}, "required": ["paths"]}`,
		func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return text(string(req.Params.Arguments)), nil // 届いた引数(JSON)をそのまま返す
		})
	add("fail", `{"type": "object"}`, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		r := text("file not found")
		r.IsError = true
		return r, nil
	})
	add("slow", `{"type": "object"}`, func(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		<-ctx.Done() // 取り消しの知らせ(notifications/cancelled)が届くまで待つ
		return nil, ctx.Err()
	})
	add("where", `{"type": "object"}`, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		wd, _ := os.Getwd()
		return text(wd + "\n" + strings.Join(os.Args[1:], " ")), nil
	})
	return s
}

// newAskServer は、書き換える前にユーザーに確かめるツール ask だけを出すテスト用のサーバー(aibow-mcp と同じ MRTR の形)。
// 1回目は確認を求め、2回目に答え(と送り返された requestState)を見て返す
func newAskServer() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "ask-server", Version: "0.1"}, nil)
	text := func(s string) *mcp.CallToolResult {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
	}
	s.AddTool(&mcp.Tool{Name: "ask", InputSchema: json.RawMessage(`{"type": "object"}`)}, func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		a, ok := req.Params.InputResponses["confirm"].(*mcp.ElicitResult)
		if !ok {
			return &mcp.CallToolResult{
				InputRequests: mcp.InputRequestMap{"confirm": &mcp.ElicitParams{
					Message: "write a.txt?",
					Meta:    mcp.Meta{meta.Preview: map[string]any{"path": "a.txt", "note": "上書き", "diff": "+hello\n"}},
				}},
				RequestState: "state-1",
			}, nil
		}
		if req.Params.RequestState != "state-1" {
			return text("requestState が送り返されていない"), nil
		}
		reason, _ := a.Content["reason"].(string)
		return text(a.Action + ":" + reason), nil
	})
	return s
}

// logs は、このパッケージが aibow/log に出したログのメッセージを集める slog のハンドラ。
type logs struct {
	mu    sync.Mutex
	lines []string
}

// captureLogs は、テストのあいだだけログの出し先を logs にする(終わったら slog.Default() に戻す)。
// 出し先はパッケージ全体で1つなので、t.Parallel のテストでは使えない。
func captureLogs(t *testing.T) *logs {
	t.Helper()
	l := &logs{}
	log.SetLogger(slog.New(l))
	t.Cleanup(func() { log.SetLogger(nil) })
	return l
}

func (l *logs) Enabled(context.Context, slog.Level) bool { return true }
func (l *logs) WithAttrs([]slog.Attr) slog.Handler       { return l }
func (l *logs) WithGroup(string) slog.Handler            { return l }

func (l *logs) Handle(_ context.Context, r slog.Record) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, r.Message)
	return nil
}

func (l *logs) contains(s string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.ContainsFunc(l.lines, func(line string) bool { return strings.Contains(line, s) })
}

// memoryManager は、プロセスを起動せずにテスト用のサーバーにつなぐ Manager(SDK の NewInMemoryTransports)。
// sessions に、サーバーの側のつながりが起動した順に入る(落ちたことにするときに閉じる)。
func memoryManager(t *testing.T, configs map[string]ServerConfig) (*Manager, *logs, *[]*mcp.ServerSession) {
	t.Helper()
	l := captureLogs(t)
	m := New(configs, Options{Version: "test"})
	var sessions []*mcp.ServerSession
	m.transport = func(string, ServerConfig, string, io.Writer) (mcp.Transport, error) {
		ct, st := mcp.NewInMemoryTransports()
		ss, err := newTestServer().Connect(context.Background(), st, nil)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, ss)
		return ct, nil
	}
	t.Cleanup(m.Close)
	return m, l, &sessions
}

func names(tools []tool.Tool) []string {
	var out []string
	for _, t := range tools {
		out = append(out, t.(tool.Remote).Server()+"/"+t.Def().Name)
	}
	return out
}

// ${workdir} を使うサーバーは作業フォルダごとに起動し、使わないサーバーは1つを共有する。Tools は起動しない
func TestStartAndTools(t *testing.T) {
	m, l, _ := memoryManager(t, map[string]ServerConfig{
		"fs":   {Command: "fs-server", Args: []string{workDirVar}},
		"util": {Command: "util-server"},
	})
	ctx := context.Background()

	if got := m.Tools("D:/a", m.Servers()); len(got) != 0 {
		t.Fatalf("起動する前はツールが無い: %v", names(got))
	}
	started, errs := m.Start(ctx, "", m.Servers())
	if len(errs) != 0 || !slices.Equal(started, []string{"util"}) {
		t.Fatalf("作業フォルダが無ければ util だけ起動する: %v %v", started, errs)
	}
	if got := names(m.Tools("D:/a", m.Servers())); len(got) != 4 || got[0] != "util/util__echo" {
		t.Errorf("fs はまだ起動していない: %v", got)
	}

	started, _ = m.Start(ctx, "D:/a", m.Servers())
	if !slices.Equal(started, []string{"fs"}) {
		t.Errorf("fs だけを起動する(util は動いている): %v", started)
	}
	got := names(m.Tools("D:/a", m.Servers()))
	if len(got) != 8 || got[0] != "fs/fs__echo" || got[4] != "util/util__echo" {
		t.Errorf("サーバーの名前の順に並ぶ: %v", got)
	}
	if got := names(m.Tools("D:/b", m.Servers())); len(got) != 4 {
		t.Errorf("別の作業フォルダでは fs はまだ動いていない: %v", got)
	}
	if started, _ := m.Start(ctx, "D:/a", m.Servers()); len(started) != 0 {
		t.Errorf("動いているサーバーは起動し直さない: %v", started)
	}

	// 学習用のログ: 送受信したメッセージ(→ / ←)と、つながったときの版・ツールの数
	for _, want := range []string{"MCP util → ", "MCP util ← ", `"method":"tools/list"`, "ツール 4 個"} {
		if !l.contains(want) {
			t.Errorf("ログに %q がありません", want)
		}
	}
}

// 会話で選んだサーバーだけを起動し、ツールを出す。知らない名前(settings.json から消したサーバー)は無視する
func TestSelectedServers(t *testing.T) {
	m, _, _ := memoryManager(t, map[string]ServerConfig{
		"a": {Command: "a-server"},
		"b": {Command: "b-server"},
	})
	ctx := context.Background()
	if started, _ := m.Start(ctx, "", nil); len(started) != 0 {
		t.Errorf("選んでいなければ起動しない: %v", started)
	}
	if started, _ := m.Start(ctx, "", []string{"b", "removed"}); !slices.Equal(started, []string{"b"}) {
		t.Errorf("選んだサーバーだけ起動する: %v", started)
	}
	m.Start(ctx, "", []string{"a"})
	if got := names(m.Tools("", []string{"b"})); len(got) != 4 || got[0] != "b/b__echo" {
		t.Errorf("起動していても、選んでいないサーバーのツールは出さない: %v", got)
	}
	if got := m.Servers(); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("Servers: %v", got)
	}
}

// 設定の画面のために、一時的に起動してツールの一覧を受け取る(動いているサーバーの一覧には入れない)。
// ${workdir} を使うサーバーには一時フォルダを渡し、終わったら消す
func TestListTools(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	captureLogs(t) // ログは見ないが、テストの出力に混ぜない
	m := New(map[string]ServerConfig{
		"self": {Command: exe, Args: []string{"--dir=" + workDirVar}, Env: map[string]string{"AIBOW_MCP_TEST_SERVER": "1"}},
	}, Options{})
	defer m.Close()

	tools, err := m.ListTools(context.Background(), "self")
	if err != nil || len(tools) != 4 || tools[0].Def().Name != "self__echo" {
		t.Fatalf("%v %v", names(tools), err)
	}
	if len(m.Tools("", m.Servers())) != 0 {
		t.Error("一覧を受け取るための起動は、動いているサーバーに入れない")
	}
	if _, err := m.ListTools(context.Background(), "nothing"); err == nil {
		t.Error("知らないサーバーはエラー")
	}
	left, _ := filepath.Glob(filepath.Join(os.TempDir(), "aibow-mcp-list-*"))
	for _, d := range left {
		if fi, err := os.Stat(d); err == nil && time.Since(fi.ModTime()) < time.Minute {
			t.Errorf("一時フォルダが残っている: %s", d)
		}
	}
}

// 引数は CheckArgs で JSON Schema の型に変換され(配列・真偽値)、その型のまま tools/call で届く
func TestRun(t *testing.T) {
	m, _, _ := memoryManager(t, map[string]ServerConfig{"util": {Command: "util-server"}})
	ctx := context.Background()
	m.Start(ctx, "", m.Servers())
	tools := m.Tools("", m.Servers())

	echo := tool.Find(tools, "util__echo")
	args, err := tool.CheckArgs(echo.Def(), []tool.Arg{{Name: "paths", Value: `["a.go", "b.go"]`}, {Name: "loud", Value: "True"}})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := echo.Run(ctx, args); err != nil || got != `{"loud":true,"paths":["a.go","b.go"]}` {
		t.Errorf("echo: %q, %v", got, err)
	}
	if _, err := tool.CheckArgs(echo.Def(), nil); err == nil {
		t.Error("inputSchema の required も確かめる")
	}

	if got, err := tool.Find(tools, "util__fail").Run(ctx, nil); err == nil || err.Error() != "file not found" {
		t.Errorf("isError の結果はエラーにする: %q, %v", got, err)
	}

	slow := tool.Find(tools, "util__slow")
	tctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if _, err := slow.Run(tctx, nil); err == nil || !strings.Contains(err.Error(), "時間の上限") {
		t.Errorf("時間切れ: %v", err)
	}
	sctx, stop := context.WithCancel(ctx)
	time.AfterFunc(100*time.Millisecond, stop)
	if _, err := slow.Run(sctx, nil); err == nil || err.Error() != "停止されました" {
		t.Errorf("停止: %v", err)
	}

	c := echo.(tool.Confirmable)
	pv, err := c.Preview(args)
	if err != nil || pv.Server != "util" || !strings.Contains(pv.Args, `"paths": [`) {
		t.Errorf("確認には、サーバーの名前と整えた引数を出す: %+v, %v", pv, err)
	}
	if echo.(tool.TimeLimiter).TimeLimit() != defaultCallTimeout {
		t.Error("時間の上限はツールが決める(callTimeout を書かなければ既定)")
	}
}

// サーバーが確認を求めてきたら(MRTR)、ask で聞いて答えと requestState を付けて送り直す。確認を待つ時間は時間の上限に数えない
func TestRunInteractive(t *testing.T) {
	m, l, _ := memoryManager(t, map[string]ServerConfig{"util": {Command: "util-server", CallTimeout: 1}})
	m.transport = func(string, ServerConfig, string, io.Writer) (mcp.Transport, error) {
		ct, st := mcp.NewInMemoryTransports()
		if _, err := newAskServer().Connect(context.Background(), st, nil); err != nil {
			return nil, err
		}
		return ct, nil
	}
	ctx := context.Background()
	m.Start(ctx, "", m.Servers())
	ask := tool.Find(m.Tools("", m.Servers()), "util__ask").(tool.Interactive)

	var got []tool.Preview
	answer := func(a tool.Answer, wait time.Duration) tool.Ask {
		return func(_ context.Context, pv tool.Preview) (tool.Answer, error) {
			got = append(got, pv)
			time.Sleep(wait)
			return a, nil
		}
	}
	// 上限(1 秒)より長く待っても、確認を待つ時間は数えないので止まらない
	if out, err := ask.RunInteractive(ctx, nil, answer(tool.Answer{Approved: true}, 1200*time.Millisecond)); err != nil || out != "accept:" {
		t.Errorf("受け入れ: %q, %v", out, err)
	}
	if len(got) != 1 || got[0].Path != "a.txt" || got[0].Diff != "+hello\n" || got[0].Server != "util" {
		t.Errorf("_meta の確認の内容をカードに出す: %+v", got)
	}
	if out, err := ask.RunInteractive(ctx, nil, answer(tool.Answer{Reason: "違う"}, 0)); err != nil || out != "decline:違う" {
		t.Errorf("拒否と理由: %q, %v", out, err)
	}
	if _, err := ask.(tool.Tool).Run(ctx, nil); err == nil || !strings.Contains(err.Error(), "聞く手段がありません") {
		t.Errorf("聞く手段が無ければエラー: %v", err)
	}
	if !l.contains("ユーザーの確認を求めています") || !l.contains("確認の答え: accept") {
		t.Error("確認の往復をログに出す")
	}
}

// _meta に確認の内容が無いサーバーは、聞いてきた文をそのまま出す
func TestPreviewOfMessage(t *testing.T) {
	pv := previewOf("other", &mcp.ElicitParams{Message: "本当に消しますか?"})
	if pv.Server != "other" || pv.Args != "本当に消しますか?" || pv.Diff != "" {
		t.Errorf("message を出す: %+v", pv)
	}
}

// settings.json の callTimeout は、そのサーバーのツールの時間の上限になる(サーバーごと)
func TestCallTimeout(t *testing.T) {
	m, _, _ := memoryManager(t, map[string]ServerConfig{
		"long":  {Command: "util-server", CallTimeout: 130},
		"short": {Command: "util-server"},
	})
	ctx := context.Background()
	m.Start(ctx, "", m.Servers())
	tools := m.Tools("", m.Servers())
	if got := tool.Find(tools, "long__slow").(tool.TimeLimiter).TimeLimit(); got != 130*time.Second {
		t.Errorf("long の上限 = %v", got)
	}
	if got := tool.Find(tools, "short__slow").(tool.TimeLimiter).TimeLimit(); got != defaultCallTimeout {
		t.Errorf("short の上限 = %v", got)
	}
}

// サーバーが落ちたら一覧から外し、次の Start で起動し直す
func TestRestartAfterServerDies(t *testing.T) {
	m, l, sessions := memoryManager(t, map[string]ServerConfig{"util": {Command: "util-server"}})
	ctx := context.Background()
	m.Start(ctx, "", m.Servers())
	(*sessions)[0].Close()

	// 一覧から外してからログを書く(drop)ので、両方がそろうまで待つ(一覧が空になった直後に見ると、まだログが無いことがある)
	deadline := time.Now().Add(5 * time.Second)
	for len(m.Tools("", m.Servers())) != 0 || !l.contains("つながりが切れました") {
		if time.Now().After(deadline) {
			t.Fatalf("落ちたサーバーのツールが残っているか、落ちたことをログに出していない: %v", names(m.Tools("", m.Servers())))
		}
		time.Sleep(10 * time.Millisecond)
	}
	if started, errs := m.Start(ctx, "", m.Servers()); !slices.Equal(started, []string{"util"}) || len(errs) != 0 {
		t.Errorf("起動し直す: %v %v", started, errs)
	}
}

// 起動できないサーバーはエラーを返し、ほかのサーバーは起動する
func TestStartError(t *testing.T) {
	captureLogs(t) // ログは見ないが、テストの出力に混ぜない
	m := New(map[string]ServerConfig{
		"missing": {Command: filepath.Join(t.TempDir(), "no-such-server")},
	}, Options{})
	defer m.Close()
	started, errs := m.Start(context.Background(), "", m.Servers())
	if len(started) != 0 || len(errs) != 1 || !strings.Contains(errs[0].Error(), "MCP のサーバー missing を起動できません") {
		t.Errorf("%v %v", started, errs)
	}
}

// 起動に失敗したら、サーバーのエラー出力の最後の行をエラーに添える。時間切れなら startupTimeout の延ばし方を書く
func TestStartFailureReason(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	captureLogs(t) // ログは見ないが、テストの出力に混ぜない
	m := New(map[string]ServerConfig{
		"broken": {Command: exe, Env: map[string]string{"AIBOW_MCP_TEST_SERVER": "fail"}},
		"slow":   {Command: exe, Env: map[string]string{"AIBOW_MCP_TEST_SERVER": "hang"}, StartupTimeout: 1},
	}, Options{})
	defer m.Close()

	begin := time.Now()
	_, errs := m.Start(context.Background(), "", m.Servers())
	if len(errs) != 2 {
		t.Fatalf("2つとも起動できない: %v", errs)
	}
	if got := errs[0].Error(); !strings.Contains(got, "MCP のサーバー broken を起動できません") ||
		!strings.Contains(got, "サーバーのエラー出力の最後の行: error: failed to persist temporary file: access denied") {
		t.Errorf("エラー出力の最後の行を添える: %s", got)
	}
	if got := errs[1].Error(); !strings.Contains(got, "起動の上限(1 秒)までにつながりませんでした") || !strings.Contains(got, "startupTimeout") {
		t.Errorf("時間切れ: %s", got)
	}
	if d := time.Since(begin); d > time.Second+closeWait+5*time.Second {
		t.Errorf("startupTimeout の 1 秒で止める: %v", d)
	}
}

// 本物の子プロセス(このテストの実行ファイル自身)を、stdio でつなぐ。
// ${workdir} の置き換え・カレントフォルダ・閉じたらプロセスが終わること(Windows はジョブオブジェクトごと)を確かめる
func TestCommandServer(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	l := captureLogs(t)
	m := New(map[string]ServerConfig{
		"self": {Command: exe, Args: []string{"--dir=" + workDirVar}, Env: map[string]string{"AIBOW_MCP_TEST_SERVER": "1"}},
	}, Options{Version: "test"})

	if _, errs := m.Start(context.Background(), dir, m.Servers()); len(errs) != 0 {
		t.Fatal(errs)
	}
	got, err := tool.Find(m.Tools(dir, m.Servers()), "self__where").Run(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	wd, args, _ := strings.Cut(got, "\n")
	if !sameDir(wd, dir) || args != "--dir="+dir {
		t.Errorf("カレントフォルダと引数は作業フォルダ: %q", got)
	}

	start := time.Now()
	m.Close()
	if d := time.Since(start); d > closeWait+3*time.Second {
		t.Errorf("閉じるのに時間がかかりすぎ: %v", d)
	}
	if len(m.Tools(dir, m.Servers())) != 0 {
		t.Error("閉じたらツールは無い")
	}
	if started, _ := m.Start(context.Background(), dir, m.Servers()); len(started) != 0 {
		t.Error("閉じた後は起動しない")
	}
	if !l.contains("MCP self: つながりました") || !l.contains("MCP self: 止めました") {
		t.Errorf("起動と停止をログに出す: %v", l.lines)
	}
	if testing.Verbose() { // go test -v で、実際のやり取りを見られる
		for _, line := range l.lines {
			t.Log(line)
		}
	}
}

// sameDir は、短い名前(Windows の一時フォルダは RUNNER~1 のような 8.3 形式のことがある)の違いを無視して比べる。
func sameDir(a, b string) bool {
	sa, err1 := os.Stat(a)
	sb, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(sa, sb)
}

func TestValidate(t *testing.T) {
	for name, c := range map[string]ServerConfig{
		"command も url も無い": {Args: []string{"x"}},
		"両方ある":              {Command: "npx", URL: "http://localhost:3000/mcp"},
		"古い SSE":            {Type: "sse", URL: "http://localhost:3000/sse"},
		"知らない type":         {Type: "websocket", URL: "ws://localhost:3000"},
		"http なのに url が無い":  {Type: "http", Command: "x"},
		"stdio なのに url":     {Type: "stdio", URL: "http://localhost:3000/mcp"},
		"URL でない":           {URL: "localhost:3000/mcp"},
		"url に作業フォルダ":       {URL: "http://localhost:3000/mcp?root=${workdir}"},
		"headers に作業フォルダ":   {URL: "http://localhost:3000/mcp", Headers: map[string]string{"X-Root": workDirVar}},
	} {
		if err := c.Validate(); err == nil {
			t.Errorf("%s: エラーになるはずです", name)
		}
	}
	for name, c := range map[string]ServerConfig{
		"stdio":      {Command: "npx", Type: "stdio"},
		"url だけ":     {URL: "http://intra.example:3000/mcp"},
		"type: http": {Type: "http", URL: "https://intra.example/mcp", Headers: map[string]string{"Authorization": "Bearer x"}},
	} {
		if err := c.Validate(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := (ServerConfig{Command: "npx", StartupTimeout: -1}).Validate(); err == nil {
		t.Error("startupTimeout は 1 以上")
	}
	if err := (ServerConfig{Command: "npx", CallTimeout: -1}).Validate(); err == nil {
		t.Error("callTimeout は 1 以上")
	}
	if got := (ServerConfig{Command: "npx", StartupTimeout: 180}).startupTimeout(); got != 180*time.Second {
		t.Errorf("startupTimeout: %v", got)
	}
	if got := (ServerConfig{Command: "npx"}).startupTimeout(); got != defaultStartupTimeout {
		t.Errorf("既定の上限: %v", got)
	}
	if (ServerConfig{Command: "npx", Args: []string{"-y", "x"}}).UsesWorkDir() {
		t.Error("${workdir} が無ければ共有する")
	}
	if !(ServerConfig{Command: "x", Env: map[string]string{"ROOT": workDirVar}}).UsesWorkDir() {
		t.Error("env の ${workdir} も見る")
	}
	if (ServerConfig{URL: "http://localhost/mcp"}).UsesWorkDir() {
		t.Error("url のサーバーは作業フォルダごとにしない")
	}
}

// url のサーバー(Streamable HTTP)につなぐ。headers を毎回付け、サーバーが止まったら一覧から外す
func TestHTTPServer(t *testing.T) {
	var mu sync.Mutex
	var auth []string
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return newTestServer() }, nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auth = append(auth, r.Header.Get("Authorization"))
		mu.Unlock()
		mcpHandler.ServeHTTP(w, r)
	}))
	defer srv.Close()

	l := captureLogs(t)
	m := New(map[string]ServerConfig{
		"intra": {URL: srv.URL, Headers: map[string]string{"Authorization": "Bearer secret"}},
	}, Options{Version: "test"})
	defer m.Close()

	ctx := context.Background()
	if started, errs := m.Start(ctx, "", m.Servers()); !slices.Equal(started, []string{"intra"}) || len(errs) != 0 {
		t.Fatalf("%v %v", started, errs)
	}
	if got := names(m.Tools("D:/a", m.Servers())); len(got) != 4 {
		t.Errorf("url のサーバーは作業フォルダに関係なく共有する: %v", got)
	}
	echo := tool.Find(m.Tools("", m.Servers()), "intra__echo")
	if got, err := echo.Run(ctx, tool.Args{"paths": []any{"a"}}); err != nil || got != `{"paths":["a"]}` {
		t.Errorf("echo: %q, %v", got, err)
	}
	mu.Lock()
	if len(auth) == 0 || slices.ContainsFunc(auth, func(a string) bool { return a != "Bearer secret" }) {
		t.Errorf("headers は毎回付ける: %q", auth)
	}
	mu.Unlock()
	if !l.contains("MCP intra: つなぎます: "+srv.URL) || l.contains("secret") {
		t.Error("つなぐ先はログに出し、headers(API キーなど)は出さない")
	}

	// サーバーが JSON-RPC のエラーで答えた(知らないツール)だけなら、つながりは生きているので外さない
	e := echo.(*remoteTool)
	unknown := &remoteTool{server: e.server, session: e.session, def: tool.Def{Name: "intra__no_such_tool"}, name: "no_such_tool", broken: e.broken}
	if _, err := unknown.Run(ctx, nil); err == nil || strings.Contains(err.Error(), "つながりが切れました") {
		t.Errorf("サーバーの答えのエラー: %v", err)
	}
	if got := m.Tools("", m.Servers()); len(got) != 4 {
		t.Errorf("つながりは外さない: %v", names(got))
	}

	// サーバーが止まった: 呼び出しが失敗したら一覧から外し、次の Start でつなぎ直す
	srv.CloseClientConnections()
	srv.Close()
	if _, err := echo.Run(ctx, tool.Args{"paths": []any{"a"}}); err == nil || !strings.Contains(err.Error(), "つながりが切れました") {
		t.Errorf("止まったサーバー: %v", err)
	}
	if got := m.Tools("", m.Servers()); len(got) != 0 {
		t.Errorf("つながりが切れたサーバーのツールは外す: %v", names(got))
	}
}

// MCP のツールの名前には、サーバーの名前を前に付ける(名前空間の代わり)。使えない文字は _ にする
func TestToolName(t *testing.T) {
	for server, want := range map[string]string{
		"filesystem":   "filesystem__read_file",
		"Blender":      "Blender__read_file",
		"my server.v2": "my_server_v2__read_file",
		"ブレンダー":        "_______read_file", // 1文字ずつ _ にする(5 文字 + 区切りの __)
	} {
		if got := ToolName(server, "read_file"); got != want {
			t.Errorf("%q: %q(期待: %q)", server, got, want)
		}
	}
}

func TestResultText(t *testing.T) {
	got := resultText([]mcp.Content{
		&mcp.TextContent{Text: "a"},
		&mcp.ImageContent{MIMEType: "image/png", Data: []byte{1}},
		&mcp.EmbeddedResource{Resource: &mcp.ResourceContents{URI: "file:///x", Text: "body"}},
	})
	if got != "a\n(画像 image/png は渡せません)\nbody" {
		t.Errorf("%q", got)
	}
	long := strings.Repeat("あ", resultMaxChars+10)
	if s := truncate(long); !strings.HasPrefix(s, strings.Repeat("あ", resultMaxChars)+"\n…(結果が長いため、8010 文字のうち") {
		t.Errorf("長い結果は切る: %q", s[len(s)-80:])
	}
}

func TestLineLogger(t *testing.T) {
	l := captureLogs(t)
	w := &lineLogger{prefix: "MCP x "}
	fmt.Fprint(w, "write: {\"a\":1}\nread: {\"b\"")
	fmt.Fprint(w, ":2}\r\n\nplain\n")
	want := []string{`MCP x → {"a":1}`, `MCP x ← {"b":2}`, "MCP x plain"}
	if !slices.Equal(l.lines, want) {
		t.Errorf("%q", l.lines)
	}
}
