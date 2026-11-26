package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	mcpserver "github.com/secondarykey/aibow/mcp/server"
	"github.com/secondarykey/aibow/tool"
)

// fakeWorkspaceServer は、aibow-server のサーバーのワークスペースの口の代わり(キーを確かめ、配っているかで 403 にする)。
type fakeWorkspaceServer struct {
	mu      sync.Mutex
	key     string
	granted bool
	folders []tool.Folder
	calls   atomic.Int32
}

func (f *fakeWorkspaceServer) handler() http.Handler {
	mcpH := mcpserver.HTTPHandler(func(*http.Request) mcpserver.Options {
		f.mu.Lock()
		defer f.mu.Unlock()
		return mcpserver.Options{Folders: slices.Clone(f.folders), FilesOnly: true, Instructions: "files on the server"}
	}, mcpserver.HTTPOptions{})
	mux := http.NewServeMux()
	mux.HandleFunc("/_aibow/workspaces/{name}/mcp", func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		f.mu.Lock()
		key, granted := f.key, f.granted
		f.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer "+key {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":{"message":"API キーが違います","type":"invalid_request_error","code":"invalid_api_key"}}`))
			return
		}
		if !granted || r.PathValue("name") != "規程" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"error":{"message":"サーバーのワークスペース『規程』は使えません","type":"invalid_request_error","code":"workspace_forbidden"}}`))
			return
		}
		mcpH.ServeHTTP(w, r)
	})
	return mux
}

// keyTransport は、毎回のリクエストで今のキーを付ける(api/client の Transport の代わり)。
type keyTransport struct{ key *atomic.Pointer[string] }

func (t keyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+*t.key.Load())
	return http.DefaultTransport.RoundTrip(r)
}

func newFakeWorkspace(t *testing.T) (*fakeWorkspaceServer, string, string, *Workspaces, *atomic.Pointer[string]) {
	t.Helper()
	main, ref := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(ref, "rule.md"), []byte("rule\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &fakeWorkspaceServer{key: "k1", granted: true, folders: []tool.Folder{
		{Name: "作業", Path: main, Main: true, Writable: true},
		{Name: "規程集", Path: ref},
	}}
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	var key atomic.Pointer[string]
	k := "k1"
	key.Store(&k)
	w := NewWorkspaces(srv.URL, keyTransport{&key}, Options{Version: "test"})
	t.Cleanup(w.Close)
	return f, main, ref, w, &key
}

// サーバーのワークスペースのツールは、組み込みと同じ名前で見せ、ファイルのツールと update_plan だけ(コマンド・Git・時刻は出さない)
func TestWorkspacesTools(t *testing.T) {
	_, _, _, w, _ := newFakeWorkspace(t)
	tools := w.Tools("規程")
	var names []string
	for _, x := range tools {
		names = append(names, x.Def().Name)
		if _, remote := x.(tool.Remote); remote {
			t.Errorf("%s は MCP のサーバーのツールに見せない", x.Def().Name)
		}
	}
	slices.Sort(names)
	if want := []string{"edit_file", "list_files", "read_file", "search_files", "update_plan", "write_file"}; !slices.Equal(names, want) {
		t.Errorf("ツール: %v", names)
	}
	for _, name := range []string{"write_file", "edit_file"} {
		if _, ok := tool.Find(tools, name).(tool.SelfConfirming); !ok {
			t.Errorf("%s は確認をサーバーに任せる", name)
		}
	}
	if d := tool.Find(tools, "read_file").Def(); !strings.Contains(string(d.Parameters), "規程集") {
		t.Errorf("定義はサーバーが出したもの(folder にフォルダの名前が入る): %s", d.Parameters)
	}
	if w.Tools("規程")[0] != tools[0] {
		t.Error("同じワークスペースなら、つなぎ直さない")
	}
}

// 書くときはサーバーの差分で1回聞き、読むだけのフォルダには書けない。読むほうは folder でほかのフォルダを読める
func TestWorkspacesWrite(t *testing.T) {
	_, main, _, w, _ := newFakeWorkspace(t)
	tools := w.Tools("規程")
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
	if len(asked) != 1 || asked[0].Diff == "" || asked[0].Server != "" || strings.Contains(asked[0].Path, main) {
		t.Errorf("差分で1回聞く(サーバーの名前とパスは出さない): %+v", asked)
	}
	if b, _ := os.ReadFile(filepath.Join(main, "a.md")); string(b) != "hello\n" {
		t.Errorf("ファイル: %q", b)
	}
	if _, err := write.RunInteractive(ctx, tool.Args{"path": "b.md", "content": "x", "folder": "規程集"}, ask); err == nil {
		t.Error("読むだけのフォルダには書けない")
	}
	read := tool.Find(tools, "read_file")
	if out, err := read.Run(ctx, tool.Args{"path": "rule.md", "folder": "規程集"}); err != nil || out != "1\trule" {
		t.Errorf("ほかのフォルダを読む: %q, %v", out, err)
	}
}

// 配布をやめたら(403)、次の実行から断りの文がそのまま返り、つながりは切れたことにしない。キーを替えれば次のリクエストから効く
func TestWorkspacesRefused(t *testing.T) {
	f, _, _, w, key := newFakeWorkspace(t)
	tools := w.Tools("規程")
	read := tool.Find(tools, "list_files")
	ctx := context.Background()

	f.mu.Lock()
	f.granted = false
	f.mu.Unlock()
	if _, err := read.Run(ctx, tool.Args{}); err == nil || err.Error() != "サーバーのワークスペース『規程』は使えません" {
		t.Errorf("403 の文をそのまま返す: %v", err)
	}
	if w.Tools("規程")[0] != tools[0] {
		t.Error("断られても、つながりは切れたことにしない")
	}

	f.mu.Lock()
	f.granted, f.key = true, "k2"
	f.mu.Unlock()
	if _, err := read.Run(ctx, tool.Args{}); err == nil || !strings.Contains(err.Error(), "API キー") {
		t.Errorf("キーが違えば 401 の文: %v", err)
	}
	k := "k2"
	key.Store(&k)
	if _, err := read.Run(ctx, tool.Args{}); err != nil {
		t.Errorf("キーを替えれば、次のリクエストから通る: %v", err)
	}

	// フォルダを足すと、次の実行から使える(サーバーはリクエストのたびにフォルダの組を引く)
	extra := t.TempDir()
	os.WriteFile(filepath.Join(extra, "new.md"), []byte("new\n"), 0o644)
	f.mu.Lock()
	f.folders = append(f.folders, tool.Folder{Name: "追加", Path: extra})
	f.mu.Unlock()
	if out, err := tool.Find(tools, "read_file").Run(ctx, tool.Args{"path": "new.md", "folder": "追加"}); err != nil || out != "1\tnew" {
		t.Errorf("足したフォルダを次の実行から読める: %q, %v", out, err)
	}
}

// つなげなかったら空を返し、Retry(次の送信)まで試し直さない
func TestWorkspacesRetry(t *testing.T) {
	f, _, _, w, _ := newFakeWorkspace(t)
	f.mu.Lock()
	f.granted = false
	f.mu.Unlock()
	if tools := w.Tools("規程"); len(tools) != 0 {
		t.Fatalf("配られていなければツールは無い: %v", tools)
	}
	n := f.calls.Load()
	w.Tools("規程")
	if f.calls.Load() != n {
		t.Error("Retry まで試し直さない")
	}
	f.mu.Lock()
	f.granted = true
	f.mu.Unlock()
	w.Retry()
	if tools := w.Tools("規程"); len(tools) == 0 {
		t.Error("Retry のあとはつなぎ直す")
	}
}
