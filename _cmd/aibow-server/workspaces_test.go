package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"uuid"

	"github.com/secondarykey/aibow"
	"github.com/secondarykey/aibow/auth"
	"github.com/secondarykey/aibow/config"
	"github.com/secondarykey/aibow/mcp/client"
	"github.com/secondarykey/aibow/store"
	"github.com/secondarykey/aibow/tool"
)

// fakeGranted は、利用者ごとに使えるサーバーのワークスペース(配られたかの判定は ui の GrantedServerWorkspaces。ここでは結果だけを決める)。
type fakeGranted struct {
	mu   sync.Mutex
	list map[uuid.UUID][]config.ServerWorkspace
}

func (f *fakeGranted) GrantedServerWorkspaces(ctx context.Context) ([]config.ServerWorkspace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, _ := aibow.UserOf(ctx)
	return config.CloneServerWorkspaces(f.list[id]), nil
}

func (f *fakeGranted) set(user uuid.UUID, list []config.ServerWorkspace) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.list[user] = list
}

// bearer は、毎回のリクエストに API キーを付ける(アプリの api/client の代わり)。
type bearer struct{ key string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if b.key != "" {
		r.Header.Set("Authorization", "Bearer "+b.key)
	}
	return http.DefaultTransport.RoundTrip(r)
}

type workspaceServer struct {
	url       string
	main, ref string // 書けるフォルダと読むだけのフォルダのパス
	bob       uuid.UUID
	bobKey    string
	granted   *fakeGranted
	docs      config.ServerWorkspace
}

func newWorkspaceServer(t *testing.T) *workspaceServer {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	var env config.Env
	if _, err := createUser(ctx, st, initialSettings(&env, 0), "bob", ""); err != nil {
		t.Fatal(err)
	}
	bob, err := st.UserByName(ctx, "bob")
	if err != nil {
		t.Fatal(err)
	}
	key, _, err := renewKey(ctx, st, "bob", auth.DefaultKeyDays, "")
	if err != nil {
		t.Fatal(err)
	}
	// フォルダの名前は、ほかの値にたまたま出てこないものにする(パスを返していないかを、名前が無いことで確かめるため)
	main := filepath.Join(t.TempDir(), "aibow-ws-main")
	ref := filepath.Join(t.TempDir(), "aibow-ws-ref")
	os.Mkdir(main, 0o755)
	os.Mkdir(ref, 0o755)
	os.WriteFile(filepath.Join(ref, "rule.md"), []byte("合言葉は「みかん」\n"), 0o644)
	docs := config.ServerWorkspace{Name: "社内規程", Folders: []config.WorkspaceFolder{
		{Name: "作業", Path: main, Main: true, Writable: true, Description: "下書き"},
		{Name: "規程", Path: ref, Description: "決まった規程"},
	}}
	g := &fakeGranted{list: map[uuid.UUID][]config.ServerWorkspace{bob.ID: {docs}}}
	srv := httptest.NewServer(workspacesHandler(g, auth.NewKeys(st), true))
	t.Cleanup(srv.Close)
	return &workspaceServer{url: srv.URL, main: main, ref: ref, bob: bob.ID, bobKey: key, granted: g, docs: docs}
}

func (s *workspaceServer) get(t *testing.T, key string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, s.url+"/_aibow/workspaces", nil)
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

// 一覧はキーの持ち主に使えるものだけ。名前・印・説明は返し、パスは返さない。キーが無い・違うなら 401
func TestWorkspacesList(t *testing.T) {
	s := newWorkspaceServer(t)
	code, body := s.get(t, s.bobKey)
	if code != http.StatusOK || strings.Contains(body, "aibow-ws-") {
		t.Fatalf("一覧(パスは返さない): %d %s", code, body)
	}
	var got struct {
		Workspaces []struct {
			Name    string `json:"name"`
			Folders []struct {
				Name        string `json:"name"`
				Main        bool   `json:"main"`
				Writable    bool   `json:"writable"`
				Description string `json:"description"`
			} `json:"folders"`
		} `json:"workspaces"`
	}
	json.Unmarshal([]byte(body), &got)
	if len(got.Workspaces) != 1 || got.Workspaces[0].Name != "社内規程" || len(got.Workspaces[0].Folders) != 2 ||
		!got.Workspaces[0].Folders[0].Main || !got.Workspaces[0].Folders[0].Writable || got.Workspaces[0].Folders[1].Writable ||
		got.Workspaces[0].Folders[1].Description != "決まった規程" {
		t.Errorf("一覧: %s", body)
	}
	if code, _ := s.get(t, ""); code != http.StatusUnauthorized {
		t.Errorf("キーが無ければ 401: %d", code)
	}
	if code, _ := s.get(t, "aibow_wrong"); code != http.StatusUnauthorized {
		t.Errorf("キーが違えば 401: %d", code)
	}
}

// MCP の口:書ける・読むだけを断る・ほかのフォルダを読む。書く前の確認はサーバーの差分で1回(アプリの mcp/client でつなぐ)
func TestWorkspacesMCP(t *testing.T) {
	s := newWorkspaceServer(t)
	w := client.NewWorkspaces(s.url, bearer{s.bobKey}, client.Options{Version: "test"})
	t.Cleanup(w.Close)
	tools := w.Tools("社内規程")
	if tool.Find(tools, "run_command") != nil || tool.Find(tools, "git_status") != nil || tool.Find(tools, "write_file") == nil {
		t.Fatalf("ツール(コマンドと Git は出さない): %v", tool.Names(tools))
	}
	ctx := context.Background()
	var asked []tool.Preview
	ask := func(_ context.Context, pv tool.Preview) (tool.Answer, error) {
		asked = append(asked, pv)
		return tool.Answer{Approved: true}, nil
	}
	write := tool.Find(tools, "write_file").(tool.Interactive)
	if _, err := write.RunInteractive(ctx, tool.Args{"path": "a.md", "content": "hello\n"}, ask); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(s.main, "a.md")); string(b) != "hello\n" || len(asked) != 1 || asked[0].Diff == "" {
		t.Errorf("書く: %q, 確認 %+v", b, asked)
	}
	if _, err := write.RunInteractive(ctx, tool.Args{"path": "b.md", "content": "x", "folder": "規程"}, ask); err == nil {
		t.Error("読むだけのフォルダには書けない")
	}
	if out, err := tool.Find(tools, "read_file").Run(ctx, tool.Args{"path": "rule.md", "folder": "規程"}); err != nil || !strings.Contains(out, "みかん") {
		t.Errorf("読む: %q, %v", out, err)
	}

	// 配布をやめたら、次の実行から 403 の文がモデルに返る
	s.granted.set(s.bob, nil)
	_, err := tool.Find(tools, "read_file").Run(ctx, tool.Args{"path": "rule.md", "folder": "規程"})
	if err == nil || !strings.Contains(err.Error(), "サーバーのワークスペース『社内規程』は使えません") {
		t.Errorf("配布をやめたら 403 の文: %v", err)
	}

	// フォルダを足すと、次の実行から使える
	extra := filepath.Join(t.TempDir(), "aibow-ws-extra")
	os.Mkdir(extra, 0o755)
	os.WriteFile(filepath.Join(extra, "new.md"), []byte("new\n"), 0o644)
	docs := s.docs.Clone()
	docs.Folders = append(docs.Folders, config.WorkspaceFolder{Name: "追加", Path: extra})
	s.granted.set(s.bob, []config.ServerWorkspace{docs})
	if out, err := tool.Find(tools, "read_file").Run(ctx, tool.Args{"path": "new.md", "folder": "追加"}); err != nil || out != "1\tnew" {
		t.Errorf("足したフォルダを読む: %q, %v", out, err)
	}
}

// 配られていないワークスペースも、知らない名前も 403(あるかどうかを漏らさない)。キーが違えば 401
func TestWorkspacesMCPRefused(t *testing.T) {
	s := newWorkspaceServer(t)
	post := func(name, key string) int {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, client.WorkspaceURL(s.url, name), strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if code := post("知らない", s.bobKey); code != http.StatusForbidden {
		t.Errorf("知らない名前も 403: %d", code)
	}
	s.granted.set(s.bob, nil)
	if code := post("社内規程", s.bobKey); code != http.StatusForbidden {
		t.Errorf("配られていなければ 403: %d", code)
	}
	if code := post("社内規程", ""); code != http.StatusUnauthorized {
		t.Errorf("キーが無ければ 401: %d", code)
	}

	// アプリの mcp/client からは、つなげずツールが無い(手元のフォルダは使わない。会話の仕組みのテストで確かめる)
	w := client.NewWorkspaces(s.url, bearer{s.bobKey}, client.Options{Version: "test"})
	t.Cleanup(w.Close)
	if tools := w.Tools("社内規程"); len(tools) != 0 {
		t.Errorf("配られていなければツールは無い: %v", tool.Names(tools))
	}
}
