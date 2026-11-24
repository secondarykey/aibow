package ui

import (
	"context"
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

// 管理の画面で、並列数・作業領域・資料のフォルダを見て変える(0061)のテスト。

// newServerConfigWeb は、資料のフォルダ docs を alice が bob に配った形に、書き戻す server.json を足したもの。
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
		{"AddServerFolder", []any{"more", w.dir}},
		{"SetServerFolderPath", []any{"docs", w.dir}},
		{"RemoveServerFolder", []any{"docs"}},
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
	if len(v.Folders) != 1 || v.Folders[0].Path != w.dir || !slices.Equal(v.Folders[0].UsedBy, []string{"仕様の相談"}) {
		t.Errorf("資料のフォルダ(管理者にはパスと選んでいるプロファイル): %+v", v.Folders)
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

// 資料のフォルダを足すとその場で選べ、パスを変えると次のツールの実行から新しいパスを読む。選んでいるプロファイルがあれば消せない
func TestWebServerConfigFolders(t *testing.T) {
	w, path := newServerConfigWeb(t, "")
	w.user.Store(w.alice)
	w.admin.Store(true)

	// 足す
	more := filepath.Join(t.TempDir(), "aibow-more-docs")
	os.Mkdir(more, 0o755)
	os.WriteFile(filepath.Join(more, "README.md"), []byte("合言葉は「りんご」\n"), 0o644)
	var folders []ServerFolder
	w.mustCall(t, &folders, "AddServerFolder", " 社内規程 ", more)
	if len(folders) != 2 || folders[1].Name != "社内規程" || folders[1].Path != more {
		t.Errorf("足したあと: %+v", folders)
	}
	var names []string
	w.mustCall(t, &names, "Folders")
	if !slices.Equal(names, []string{"docs", "社内規程"}) {
		t.Errorf("起動し直さずに選べるはず: %v", names)
	}
	if f, _, err := config.LoadServerFile(path); err != nil || f.Folders["社内規程"] != more || f.Folders["docs"] != w.dir || f.Port != 8090 {
		t.Errorf("server.json: %+v %v", f, err)
	}
	for _, bad := range [][]any{{"社内規程", more}, {"", more}, {"x", "relative"}, {"x", filepath.Join(more, "none")}} {
		if status, _ := w.call(t, context.Background(), "AddServerFolder", bad...); status == http.StatusOK {
			t.Errorf("断るはず: %v", bad)
		}
	}

	// 配られたプロファイルの会話は、パスを変えたあとのツールの実行から新しいパスを読む
	w.mustCall(t, &folders, "SetServerFolderPath", "docs", more)
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
	if status, body := w.call(t, context.Background(), "RemoveServerFolder", "docs"); status == http.StatusOK || !strings.Contains(string(body), "仕様の相談") {
		t.Errorf("使っているフォルダは消せない: %d %s", status, body)
	}
	w.mustCall(t, &folders, "RemoveServerFolder", "社内規程")
	if len(folders) != 1 || folders[0].Name != "docs" {
		t.Errorf("消したあと: %+v", folders)
	}
	if f, _, _ := config.LoadServerFile(path); len(f.Folders) != 1 {
		t.Errorf("server.json からも消える: %+v", f.Folders)
	}
}
