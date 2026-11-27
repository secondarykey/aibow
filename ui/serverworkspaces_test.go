package ui

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	apiclient "github.com/secondarykey/aibow/api/client"
)

// サーバーモードのアプリの、サーバーのワークスペースの係(0067):
// 古い aibow-server(一覧が 404)なら使えない(手元のワークスペースのまま)。引けないとき(500 など)は新しい版とみなし、使えるものは無し
func TestAppServerWorkspacesSupported(t *testing.T) {
	var status atomic.Int32
	status.Store(http.StatusOK)
	var gotKey atomic.Pointer[string]
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		k := r.Header.Get("Authorization")
		gotKey.Store(&k)
		if r.URL.Path != "/_aibow/workspaces" {
			http.NotFound(w, r)
			return
		}
		switch s := int(status.Load()); s {
		case http.StatusOK:
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"workspaces":[{"name":"社内規程","folders":[{"name":"規程","main":true,"writable":true},{"name":"議事録","main":false,"writable":false,"description":"議事録"}]}]}`)
		default:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(s)
			io.WriteString(w, `{"error":{"message":"no","type":"invalid_request_error"}}`)
		}
	}))
	t.Cleanup(srv.Close)
	c := apiclient.New(srv.URL)
	c.SetKey("aibow_k1")
	w := newServerWorkspaces(c, "test")
	t.Cleanup(w.close)

	folders, ok, supported := w.Workspace("社内規程")
	if !supported || !ok || len(folders) != 2 || folders[0].Name != "規程" || !folders[0].Main || folders[1].Writable || folders[1].Description != "議事録" || folders[0].Path != "" {
		t.Errorf("新しい aibow-server: %+v %v %v", folders, ok, supported)
	}
	if k := gotKey.Load(); k == nil || *k != "Bearer aibow_k1" {
		t.Errorf("API キーを付ける: %v", k)
	}
	if _, ok, supported := w.Workspace("ほか"); ok || !supported {
		t.Errorf("配られていないものは使えない: %v %v", ok, supported)
	}

	status.Store(http.StatusNotFound)
	w.Refresh(context.Background())
	if _, ok, supported := w.Workspace("社内規程"); ok || supported {
		t.Errorf("古い aibow-server(404): %v %v", ok, supported)
	}
	if list, supported := w.infos(context.Background()); len(list) != 0 || supported {
		t.Errorf("古い aibow-server の画面の一覧: %+v %v", list, supported)
	}

	status.Store(http.StatusInternalServerError)
	w.Refresh(context.Background())
	if _, ok, supported := w.Workspace("社内規程"); ok || !supported {
		t.Errorf("引けないときは、使えるものが無いだけ(手元に戻さない): %v %v", ok, supported)
	}
}
