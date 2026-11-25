package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/secondarykey/aibow"
	"github.com/secondarykey/aibow/config"
	"github.com/secondarykey/aibow/format"
)

// 管理の画面で、並列数・作業領域・サーバーのワークスペースを見て変える(0061・0065)のテスト。

// newServerConfigWeb は、サーバーのワークスペース docs を alice が bob に配った形に、書き戻す server.json を足したもの。
// 並列数は server.json から(変えられる)、作業領域は lock(引数で決めた形)。
func newServerConfigWeb(t *testing.T, lock string) (*folderWeb, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "server.json")
	os.WriteFile(path, []byte("{\n  \"port\": 8090,\n  \"parallel\": 1\n}\n"), 0o644)
	w := newFolderWeb(t, func(o *Options) {
		o.Server = &ServerConfig{
			Path:     path,
			Parallel: ServerValue{Running: 1, Fallback: 1},
			Ctx:      ServerValue{Running: 32768, Fallback: 32768, Lock: lock},
		}
	})
	return w, path
}

// 管理者でない人は、見ることも変えることもできない(403)
func TestWebServerConfigAdminOnly(t *testing.T) {
	w, _ := newServerConfigWeb(t, "")
	w.user.Store(w.bob)
	for _, c := range []struct {
		method string
		args   []any
	}{
		{"ServerSettings", nil},
		{"SaveServerSettings", []any{2, 32768}},
		{"AddServerWorkspace", []any{"more", w.dir}},
		{"SaveServerWorkspace", []any{config.ServerWorkspace{Name: "docs", Folders: []config.WorkspaceFolder{{Path: w.dir, Name: "docs", Main: true}}}}},
		{"RemoveServerWorkspace", []any{"docs"}},
	} {
		if status, body := w.call(t, context.Background(), c.method, c.args...); status != http.StatusForbidden || strings.Contains(string(body), filepath.Base(w.dir)) {
			t.Errorf("%s: %d %s", c.method, status, body)
		}
	}
}

// 並列数を保存すると server.json に書かれ(ほかの鍵は残る)、今の値はそのまま。引数で決めた作業領域は変えられない
func TestWebServerConfigSave(t *testing.T) {
	w, path := newServerConfigWeb(t, "起動の引数 -ctx で決めています")
	w.user.Store(w.alice)
	w.admin.Store(true)

	var v ServerSettingsView
	w.mustCall(t, &v, "ServerSettings")
	if v.File != path || !v.Found || v.Parallel.Running != 1 || v.Parallel.Next != 1 || v.Ctx.Lock == "" || v.PerSeq != 32768 {
		t.Errorf("最初: %+v", v)
	}
	if len(v.Workspaces) != 1 || v.Workspaces[0].Folders[0].Path != w.dir || !slices.Equal(v.Workspaces[0].UsedBy, []string{"仕様の相談"}) {
		t.Errorf("サーバーのワークスペース(管理者にはパスと選んでいるプロファイル): %+v", v.Workspaces)
	}

	w.mustCall(t, &v, "SaveServerSettings", 2, 32768)
	if v.Parallel.Running != 1 || v.Parallel.Next != 2 {
		t.Errorf("保存したあと(今の値は起動し直すまで変わらない): %+v", v.Parallel)
	}
	if f, _, err := config.LoadServerFile(path); err != nil || f.Parallel != 2 || f.Port != 8090 || f.Ctx != 0 {
		t.Errorf("server.json: %+v %v", f, err)
	}
	if status, body := w.call(t, context.Background(), "SaveServerSettings", 2, 65536); status == http.StatusOK || !strings.Contains(string(body), "-ctx") {
		t.Errorf("引数で決めた作業領域は変えられない: %d %s", status, body)
	}
	if status, _ := w.call(t, context.Background(), "SaveServerSettings", 0, 32768); status == http.StatusOK {
		t.Error("並列数 0 は断る")
	}
}

// ワークスペースを足すとその場で選べ、フォルダのパスを変えると次のツールの実行から新しいパスを読む。選んでいるプロファイルがあれば消せない(0065 の完了の条件 4)
func TestWebServerConfigWorkspaces(t *testing.T) {
	w, path := newServerConfigWeb(t, "")
	w.user.Store(w.alice)
	w.admin.Store(true)

	// 足す。最初のフォルダはメイン・書ける・名前はフォルダ名
	more := filepath.Join(t.TempDir(), "aibow-more-docs")
	os.Mkdir(more, 0o755)
	os.WriteFile(filepath.Join(more, "README.md"), []byte("合言葉は「りんご」\n"), 0o644)
	var list []ServerWorkspaceView
	w.mustCall(t, &list, "AddServerWorkspace", " 社内規程 ", more)
	if len(list) != 2 || list[1].Name != "社内規程" || len(list[1].Folders) != 1 {
		t.Fatalf("足したあと: %+v", list)
	}
	if f := list[1].Folders[0]; f.Path != more || f.Name != "aibow-more-docs" || !f.Main || !f.Writable {
		t.Errorf("最初のフォルダ: %+v", f)
	}
	var infos []ServerWorkspaceInfo
	w.mustCall(t, &infos, "ServerWorkspaces")
	if len(infos) != 2 || infos[1].Name != "社内規程" {
		t.Errorf("起動し直さずに選べるはず: %+v", infos)
	}
	if f, _, err := config.LoadServerFile(path); err != nil || len(f.Workspaces) != 2 || f.Workspaces[1].Folders[0].Path != more || f.Workspaces[0].Folders[0].Path != w.dir || f.Port != 8090 {
		t.Errorf("server.json: %+v %v", f, err)
	}
	for _, bad := range [][]any{{"社内規程", more}, {"", more}, {"x", "relative"}, {"x", filepath.Join(more, "none")}} {
		if status, _ := w.call(t, context.Background(), "AddServerWorkspace", bad...); status == http.StatusOK {
			t.Errorf("断るはず: %v", bad)
		}
	}

	// フォルダの組を変える。メインが無い・フォルダが無い・名前が重なる・無いフォルダ・無いワークスペースは断る
	docs := config.WorkspaceFolder{Path: w.dir, Name: "docs", Main: true, Writable: true}
	for _, bad := range []config.ServerWorkspace{
		{Name: "docs"},
		{Name: "docs", Folders: []config.WorkspaceFolder{{Path: w.dir, Name: "docs"}}},
		{Name: "docs", Folders: []config.WorkspaceFolder{docs, {Path: more, Name: "DOCS"}}},
		{Name: "docs", Folders: []config.WorkspaceFolder{docs, {Path: filepath.Join(more, "none"), Name: "none"}}},
		{Name: "無い", Folders: []config.WorkspaceFolder{docs}},
	} {
		if status, _ := w.call(t, context.Background(), "SaveServerWorkspace", bad); status == http.StatusOK {
			t.Errorf("断るはず: %+v", bad)
		}
	}

	// 配られたプロファイルの会話は、パスを変えたあとのツールの実行から新しいパスを読む
	w.mustCall(t, &list, "SaveServerWorkspace", config.ServerWorkspace{Name: "docs", Folders: []config.WorkspaceFolder{{Path: more, Name: "docs", Main: true, Writable: true}}})
	if list[0].Folders[0].Path != more {
		t.Errorf("パスを変えたあと: %+v", list[0])
	}
	w.admin.Store(false)
	w.user.Store(w.bob)
	events := w.openEvents(t)
	var c aibow.Conversation
	w.mustCall(t, &c, "NewConversation", w.profile.ID, nil)
	w.eng.calls = [][]format.ToolCall{readCall("README.md")}
	w.mustCall(t, nil, "Send", c.ID, "README を読んで", nil)
	until(t, events, "done")
	if r := w.toolResult(t, c.ID); !strings.Contains(r.Content, "りんご") {
		t.Errorf("新しいパスを読むはず: %q", r.Content)
	}

	// 選んでいるプロファイルがあれば消せない。名前が出る
	w.user.Store(w.alice)
	w.admin.Store(true)
	if status, body := w.call(t, context.Background(), "RemoveServerWorkspace", "docs"); status == http.StatusOK || !strings.Contains(string(body), "仕様の相談") {
		t.Errorf("使っているワークスペースは消せない: %d %s", status, body)
	}
	w.mustCall(t, &list, "RemoveServerWorkspace", "社内規程")
	if len(list) != 1 || list[0].Name != "docs" {
		t.Errorf("消したあと: %+v", list)
	}
	if f, _, _ := config.LoadServerFile(path); len(f.Workspaces) != 1 {
		t.Errorf("server.json からも消える: %+v", f.Workspaces)
	}
}

// server.json が前の形(folders)のときも今までどおり使え、管理の画面で最初に保存すると workspaces に書き直して folders を消す(0065 の完了の条件 3)
func TestWebServerConfigMigratesFolders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.json")
	w := newFolderWeb(t, func(o *Options) {
		raw, _ := json.Marshal(map[string]string{"docs": o.ServerWorkspaces[0].Folders[0].Path})
		os.WriteFile(path, []byte(`{"port": 8090, "folders": `+string(raw)+`}`), 0o644)
		f, _, err := config.LoadServerFile(path)
		if err != nil {
			t.Fatal(err)
		}
		list, migrated := f.ServerWorkspaces()
		if !migrated {
			t.Fatal("folders から移したはず")
		}
		o.ServerWorkspaces = list // aibow-server の起動と同じ
		o.Server = &ServerConfig{Path: path, Parallel: ServerValue{Running: 1, Fallback: 1}, Ctx: ServerValue{Running: 32768, Fallback: 32768}}
	})
	w.user.Store(w.alice)
	w.admin.Store(true)
	more := t.TempDir()
	w.mustCall(t, nil, "AddServerWorkspace", "社内規程", more)
	f, _, err := config.LoadServerFile(path)
	if err != nil || len(f.Folders) != 0 || len(f.Workspaces) != 2 || f.Workspaces[0].Name != "docs" || !f.Workspaces[0].Folders[0].Writable || f.Port != 8090 {
		t.Errorf("workspaces に書き直して folders を消すはず: %+v %v", f, err)
	}
}
