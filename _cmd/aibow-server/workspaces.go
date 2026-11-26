package main

// workspaces.go: サーバーモードのアプリが、サーバーのワークスペースを使う口(0067)。
//
//	GET  /_aibow/workspaces            … その人が使えるサーバーのワークスペースの一覧(名前とフォルダの名前・印・説明。パスは返さない)
//	POST /_aibow/workspaces/{名前}/mcp … そのワークスペースの MCP の口(Streamable HTTP。mcp/server のファイルのツール)
//
// ブラウザから使う会話は、aibow-server の中で同じ MCP のサーバーにメモリ内でつなぐ(変えない)。この口はアプリのためのもの。
// アプリは会話の仕組みを手元で動かし、ファイルのツールだけをここに頼む。ツールの中・書く前の確認(差分)はブラウザと同じ。
//
// 守り(決定事項 5・6):
//   - API キーが要る形なら、保存の口(/_aibow/store/)と同じく API キー(アプリのログインのトークン)で誰かを確かめる。無い・違うなら 401
//   - 使えるのは、その人に配られている配布プロファイルのどれかが選んでいるワークスペースだけ。ほかは 403(知らない名前も 403。あるかどうかを漏らさない)
//   - 呼び出しのたびに確かめる(配布をやめたら、次のツールの実行から 403。管理の画面でフォルダを変えたら、次の実行から新しい組)

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/secondarykey/aibow"
	"github.com/secondarykey/aibow/auth"
	"github.com/secondarykey/aibow/config"
	"github.com/secondarykey/aibow/internal/appversion"
	"github.com/secondarykey/aibow/log"
	mcpserver "github.com/secondarykey/aibow/mcp/server"
	"github.com/secondarykey/aibow/tool"
	"github.com/secondarykey/aibow/ui"
)

// grantedLister は、ctx の利用者が使えるサーバーのワークスペースを返す係(ui.Service。テストでは差し替える)。
type grantedLister interface {
	GrantedServerWorkspaces(ctx context.Context) ([]config.ServerWorkspace, error)
}

// workspaceKey は、確かめたワークスペースと呼んだ人を、MCP の口に渡すための ctx の鍵。
type workspaceKey struct{}

type workspaceCall struct {
	ws   config.ServerWorkspace
	user string // ログに出す名前
}

// workspaceInstructions は、つないだアプリに渡す説明(MCP の instructions)。パスは入れない(アプリは環境の説明を自分で書く。決定事項 7・11)。
const workspaceInstructions = "File tools for a shared folder on the aibow-server. Pass the folder argument to choose a folder; omit it for the main folder. Paths are relative to the folder."

// workspacesHandler は /_aibow/workspaces の下を受け持つ。required は API キーが要る形か(要らなければログインなしで使うときの利用者 local)。
func workspacesHandler(lister grantedLister, keys *auth.Keys, required bool) http.Handler {
	mcpH := mcpserver.HTTPHandler(func(r *http.Request) mcpserver.Options {
		c, _ := r.Context().Value(workspaceKey{}).(workspaceCall)
		return mcpserver.Options{
			Folders: toolFolders(c.ws), FilesOnly: true, Version: appversion.Version(), Instructions: workspaceInstructions,
			Wrote: func(_ context.Context, name string, args tool.Args) { logWorkspaceWrite(c, name, args) },
		}
	}, mcpserver.HTTPOptions{
		// API キーが要る形では、キーがブラウザのページから勝手に呼ばれるのを防ぐので、同じ PC のリバースプロキシ越し(Host が外の名前)でも受ける。
		// 要らない形(127.0.0.1 だけ)では、SDK の守り(Host が 127.0.0.1 でなければ断る)を残す
		AnyHost: required,
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /_aibow/workspaces", func(w http.ResponseWriter, r *http.Request) {
		ctx, _, err := workspaceCaller(r, required)
		if err != nil {
			writeWorkspaceError(w, http.StatusUnauthorized, err.Error())
			return
		}
		list, err := lister.GrantedServerWorkspaces(ctx)
		if err != nil {
			log.Errorf("サーバーのワークスペースの一覧を返せません: %v", err)
			writeWorkspaceError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"workspaces": ui.ServerWorkspaceInfos(list)})
	})
	mux.HandleFunc("/_aibow/workspaces/{name}/mcp", func(w http.ResponseWriter, r *http.Request) {
		ctx, who, err := workspaceCaller(r, required)
		if err != nil {
			writeWorkspaceError(w, http.StatusUnauthorized, err.Error())
			return
		}
		name := r.PathValue("name")
		ws, ok, err := grantedWorkspace(ctx, lister, name)
		if err != nil {
			log.Errorf("サーバーのワークスペース %s を使えるか確かめられません: %v", name, err)
			writeWorkspaceError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !ok {
			log.Warnf("サーバーのワークスペース %s は %s に配られていないので、アプリからの呼び出しを断りました(%s)", name, who, remoteHost(r.RemoteAddr))
			writeWorkspaceError(w, http.StatusForbidden,
				fmt.Sprintf("サーバーのワークスペース『%s』は使えません(配布プロファイルで配られていないか、配布をやめました)", name))
			return
		}
		if method := peekMethod(r); method == "initialize" || method == "server/discover" {
			log.Infof("サーバーのワークスペース %s に %s のアプリがつなぎました(フォルダ %d 個)", ws.Name, who, len(ws.Folders))
		}
		mcpH.ServeHTTP(w, r.WithContext(context.WithValue(ctx, workspaceKey{}, workspaceCall{ws: ws, user: who})))
	})
	var h http.Handler = mux
	if required {
		h = keys.RequireKey(mux)
	}
	return h
}

// workspaceCaller は、口を呼んだ人(利用者を入れた ctx と、ログに出す名前)。
// API キーが要る形なら RequireKey が入れたキーの持ち主(いなければ断る。RequireKey が先に断るので、ふつうは来ない)、要らなければ local。
func workspaceCaller(r *http.Request, required bool) (context.Context, string, error) {
	if u, ok := auth.UserFrom(r.Context()); ok {
		return aibow.WithUser(r.Context(), u.ID), u.Name, nil
	}
	if required {
		return nil, "", auth.ErrBadKey
	}
	return aibow.WithUser(r.Context(), aibow.LocalUser), "local", nil // 名前は server.db の利用者 local と同じ
}

// grantedWorkspace は、ctx の利用者が使えるワークスペースのうち、名前が name のもの。
func grantedWorkspace(ctx context.Context, lister grantedLister, name string) (config.ServerWorkspace, bool, error) {
	list, err := lister.GrantedServerWorkspaces(ctx)
	if err != nil {
		return config.ServerWorkspace{}, false, err
	}
	for _, w := range list {
		if w.Name == name {
			return w, true, nil
		}
	}
	return config.ServerWorkspace{}, false, nil
}

// toolFolders は、サーバーのワークスペースのフォルダを、ファイルのツールのフォルダにする。
func toolFolders(ws config.ServerWorkspace) []tool.Folder {
	out := make([]tool.Folder, 0, len(ws.Folders))
	for _, f := range ws.Folders {
		out = append(out, tool.Folder{Name: f.Name, Path: f.Path, Main: f.Main, Writable: f.Writable, Description: f.Description})
	}
	return out
}

// logWorkspaceWrite は、アプリがサーバーのワークスペースに書いたことを Notice で残す(0065 の決定事項 23 にそろえる。0067 の決定事項 9)。
// 会話はこの口からは分からないので書かない(つながりはワークスペースごとで、会話をまたぐため)。
func logWorkspaceWrite(c workspaceCall, name string, args tool.Args) {
	folder := c.ws.MainFolder().Name
	if f := strings.TrimSpace(args.String("folder")); f != "" {
		folder = f
		for _, wf := range c.ws.Folders {
			if strings.EqualFold(wf.Name, f) {
				folder = wf.Name
			}
		}
	}
	log.Noticef("サーバーのワークスペース %s のフォルダ %s に書きました: %s %s(%s、アプリから)", c.ws.Name, folder, name, args.String("path"), c.user)
}

// peekMethod は、MCP の口に届いた JSON-RPC のメソッドの名前(読めなければ空)。本文は読んだあと元に戻す。
// つないだこと(initialize)を1行だけログに出すため。ツールの呼び出しは mcp/server がログに出す。
func peekMethod(r *http.Request) string {
	if r.Body == nil {
		return ""
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
	r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(data))
	if err != nil {
		return ""
	}
	var m struct {
		Method string `json:"method"`
	}
	json.Unmarshal(data, &m)
	return m.Method
}

// writeWorkspaceError は、断りの返事(OpenAI 互換の API と同じ形の {"error": {"message"}})。アプリの mcp/client が文を取り出してモデルに渡す。
func writeWorkspaceError(w http.ResponseWriter, status int, msg string) {
	typ := "server_error"
	if status < 500 {
		typ = "invalid_request_error"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": msg, "type": typ}})
}
