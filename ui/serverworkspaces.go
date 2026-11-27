package ui

// serverworkspaces.go: サーバーモードのアプリが、aibow-server のサーバーのワークスペースを使う係(aibow.ServerWorkspaceSource。0067)。
//
//	一覧(名前・フォルダの名前・印・説明) … GET /_aibow/workspaces(api/client。送信のたびに引く)
//	ファイルのツール                     … /_aibow/workspaces/{名前}/mcp(mcp/client の Workspaces。MCP の Streamable HTTP)
//
// どちらも api/client の API キーと証明書の扱いのまま(ログインし直したら、次のリクエストから新しいキー)。

import (
	"context"
	"errors"
	"sync"

	apiclient "github.com/secondarykey/aibow/api/client"
	"github.com/secondarykey/aibow/log"
	"github.com/secondarykey/aibow/mcp/client"
	"github.com/secondarykey/aibow/tool"
)

type serverWorkspaces struct {
	c   *apiclient.Client
	mcp *client.Workspaces

	mu        sync.Mutex
	fetched   bool                        // 一覧を一度でも引いたか(引く前に要るときは、そのとき引く)
	supported bool                        // aibow-server がアプリから使える新しい版か(古ければ 404)
	list      []apiclient.ServerWorkspace // 最後に引けた一覧(引けなかったら空)
	lastErr   string                      // 最後にログに出した引けなかった理由(同じ理由を送信のたびに出さない)
}

func newServerWorkspaces(c *apiclient.Client, version string) *serverWorkspaces {
	return &serverWorkspaces{c: c, mcp: client.NewWorkspaces(c.URL(), c.Transport(), client.Options{Version: version})}
}

// Workspace は、ワークスペース name のフォルダ(パスは空)。まだ一覧を引いていなければ引く。
func (w *serverWorkspaces) Workspace(name string) ([]tool.Folder, bool, bool) {
	w.mu.Lock()
	fetched := w.fetched
	w.mu.Unlock()
	if !fetched {
		w.Refresh(context.Background())
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.supported {
		return nil, false, false
	}
	for _, ws := range w.list {
		if ws.Name == name {
			return toolFoldersOf(ws), true, true
		}
	}
	return nil, false, true
}

func toolFoldersOf(ws apiclient.ServerWorkspace) []tool.Folder {
	out := make([]tool.Folder, 0, len(ws.Folders))
	for _, f := range ws.Folders {
		out = append(out, tool.Folder{Name: f.Name, Main: f.Main, Writable: f.Writable, Description: f.Description})
	}
	return out
}

// Tools は、ワークスペース name のファイルのツール(aibow-server の MCP の口越し)。
func (w *serverWorkspaces) Tools(name string) []tool.Tool {
	return w.mcp.Tools(name)
}

// Refresh は、一覧を引き直し、つなげなかったワークスペースをまたつなぐようにする。
//
// 引けなかったとき(つながらない・ログインの期限切れなど)は、aibow-server は新しい版とみなし、使えるワークスペースは無しにする
// (古い版とみなすと、手元のワークスペースのファイルを触ってしまうため。0067 の決定事項 12)。古い版と分かるのは 404 のときだけ。
func (w *serverWorkspaces) Refresh(ctx context.Context) {
	list, err := w.c.ServerWorkspaces(ctx)
	w.mu.Lock()
	w.fetched = true
	switch {
	case errors.Is(err, apiclient.ErrNoServerWorkspaces):
		w.supported, w.list = false, nil
	case err != nil:
		w.supported, w.list = true, nil
	default:
		w.supported, w.list = true, list
	}
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	changed := msg != w.lastErr
	w.lastErr = msg
	w.mu.Unlock()
	switch {
	case !changed:
	case errors.Is(err, apiclient.ErrNoServerWorkspaces):
		log.Infof("サーバーのワークスペースは、この aibow-server ではブラウザからだけ使えます(aibow-server を新しくすると、アプリからも使えます)")
	case err != nil:
		log.Warnf("サーバーのワークスペースの一覧を aibow-server から引けません。その会話ではファイルのツールを使わずに続けます: %v", err)
	}
	w.mcp.Retry()
}

// infos は、画面に出す一覧(引き直したもの)。古い aibow-server なら supported が false。
func (w *serverWorkspaces) infos(ctx context.Context) (list []ServerWorkspaceInfo, supported bool) {
	w.Refresh(ctx)
	w.mu.Lock()
	defer w.mu.Unlock()
	list = []ServerWorkspaceInfo{}
	for _, ws := range w.list {
		info := ServerWorkspaceInfo{Name: ws.Name, Folders: []ServerFolderInfo{}}
		for _, f := range ws.Folders {
			info.Folders = append(info.Folders, ServerFolderInfo{Name: f.Name, Main: f.Main, Writable: f.Writable, Description: f.Description})
		}
		list = append(list, info)
	}
	return list, w.supported
}

func (w *serverWorkspaces) close() {
	w.mcp.Close()
}
