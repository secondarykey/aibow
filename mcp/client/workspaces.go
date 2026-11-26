package client

// workspaces.go: aibow-server のサーバーのワークスペースのツールを、HTTP の MCP の口越しに渡す部分(0067)。
//
//	サーバーモードのアプリ [ 会話の仕組み ] ⇄ MCP(Streamable HTTP) ⇄ aibow-server の /_aibow/workspaces/{名前}/mcp [ mcp/server のファイルのツール ]
//
// 組み込みのツール(local.go)と同じ自作のサーバーに、つなぎ方だけを Streamable HTTP に替えてつなぐ。
// モデルと会話の仕組みからは、組み込みのファイルのツールと同じに見せる(名前に前置きを付けない・グループは組み込み・
// 書き換えるツールは tool.SelfConfirming で、サーバーが差分を送ってきたときに1回だけ聞く)。
// aibow-server は利用者が serverUrl で選んだ自分の組織のサーバーなので、送ってきた差分を信頼する
// (settings.json の url のサーバーは名乗りを偽れるので、こうはしない。0067 の決定事項 10)。
//
// つながりはワークスペースの名前ごとに1つ。要る最初のときにつなぎ、Close まで残す。切れたら次の送信でつなぎ直す(Retry)。
// サーバーはステートレスなので、aibow-server を起動し直しても、つながりはそのまま使える。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/secondarykey/aibow/log"
	"github.com/secondarykey/aibow/mcp/internal/meta"
	mcpserver "github.com/secondarykey/aibow/mcp/server"
	"github.com/secondarykey/aibow/tool"
)

// workspaceGrace は、サーバーのワークスペースのツールの時間の上限に足す時間(aibow の中のサーバーより、HTTP の行き来の分だけ長く待つ)。
const workspaceGrace = 10 * time.Second

// workspaceConnectTimeout は、つなぐ(挨拶とツールの一覧)の時間の上限。
const workspaceConnectTimeout = 15 * time.Second

// Workspaces は、aibow-server のサーバーのワークスペースのツールを渡す係(サーバーモードのアプリ)。
type Workspaces struct {
	opt  Options
	base string            // aibow-server のつなぎ先(http://host:port。後ろの / なし)
	rt   http.RoundTripper // API キー・証明書・アプリのバージョンは呼ぶ側が付ける(api/client の Transport。毎回のリクエストで今のキーを読む)

	mu       sync.Mutex
	sessions map[string]*workspaceSession // ワークスペースの名前 → つながり
	failed   map[string]bool              // つなげなかったワークスペース(Retry まで、つなぎ直さない。メーターの計算などで何度も試さないため)
	closed   bool
}

type workspaceSession struct {
	session *mcp.ClientSession
	tools   []tool.Tool
}

// NewWorkspaces は、つなぎ先 base の aibow-server のサーバーのワークスペースの係を作る。まだつながない。
func NewWorkspaces(base string, rt http.RoundTripper, opt Options) *Workspaces {
	if rt == nil {
		rt = http.DefaultTransport
	}
	return &Workspaces{opt: opt, base: strings.TrimRight(base, "/"), rt: rt, sessions: map[string]*workspaceSession{}, failed: map[string]bool{}}
}

// WorkspaceURL は、サーバーのワークスペース name の MCP の口の URL。
func WorkspaceURL(base, name string) string {
	return strings.TrimRight(base, "/") + "/_aibow/workspaces/" + url.PathEscape(name) + "/mcp"
}

// workspaceLabel は、ログとモデルへのエラーに出すつなぎ先の名前。
func workspaceLabel(name string) string {
	return "サーバーのワークスペース「" + name + "」"
}

// Tools は、サーバーのワークスペース name のツール(ファイルのツールと update_plan)。つなげなければ空(ログに出す)。
func (w *Workspaces) Tools(name string) []tool.Tool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if s, ok := w.sessions[name]; ok {
		return s.tools
	}
	if w.closed || w.failed[name] {
		return nil
	}
	s, err := w.connect(name)
	if err != nil {
		w.failed[name] = true
		log.Warnf("%s のツールを使えません。次の送信でつなぎ直します: %v", workspaceLabel(name), err)
		return nil
	}
	w.sessions[name] = s
	return s.tools
}

// Retry は、つなげなかったワークスペースを、次に要るときにまたつなぐようにする(送信のたびに呼ぶ)。
func (w *Workspaces) Retry() {
	w.mu.Lock()
	defer w.mu.Unlock()
	clear(w.failed)
}

// connect は、サーバーのワークスペース name の口につなぎ、ツールの一覧を受け取る。
func (w *Workspaces) connect(name string) (*workspaceSession, error) {
	ctx, cancel := context.WithTimeout(context.Background(), workspaceConnectTimeout)
	defer cancel()
	begin := time.Now()
	label := workspaceLabel(name)
	hc := &http.Client{Transport: refusalTransport{base: w.rt}}
	var t mcp.Transport = &mcp.StreamableClientTransport{Endpoint: WorkspaceURL(w.base, name), HTTPClient: hc, DisableStandaloneSSE: true}
	t = &mcp.LoggingTransport{Transport: t, Writer: &lineLogger{prefix: "MCP " + label + " ", rpc: true}}
	session, err := newClient(w.opt.Version).Connect(ctx, t, nil)
	if err != nil {
		return nil, plainError(err)
	}
	s := &workspaceSession{session: session}
	broken := func(err error) { w.drop(name, s, err) }
	// 書き換えるツールかどうかは、同じ名前の tool のツールで見る(定義の中身はサーバーが出したもの。folder の説明にフォルダの名前が入るため)
	known := mcpserver.Options{Folders: tool.Single("."), FilesOnly: true}.Tools()
	for mt, err := range session.Tools(ctx, nil) {
		if err != nil {
			session.Close()
			return nil, plainError(err)
		}
		orig := tool.Find(known, mt.Name)
		if orig == nil {
			log.Warnf("MCP %s: aibow の知らないツール %s を出しているので使いません(aibow-server のほうが新しい版かもしれません)", label, mt.Name)
			continue
		}
		rt, err := newRemoteTool(label, mcpserver.TimeLimit(orig)+workspaceGrace, session, mt, broken, context.Background())
		if err != nil {
			session.Close()
			return nil, err
		}
		def := rt.def
		def.Name = mt.Name // 組み込みのツールと同じ名前で見せる(前置きを付けない)
		s.tools = append(s.tools, asBuiltin(def, orig, rt))
	}
	log.Infof("MCP %s: aibow-server につなぎました(%.2f 秒・ツール %d 個: %s)", label, time.Since(begin).Seconds(), len(s.tools), tool.Names(s.tools))
	return s, nil
}

// drop は、つながりが切れたワークスペースを一覧から外す(次に要るときにつなぎ直す)。
func (w *Workspaces) drop(name string, s *workspaceSession, err error) {
	w.mu.Lock()
	gone := w.sessions[name] == s
	if gone {
		delete(w.sessions, name)
	}
	w.mu.Unlock()
	if gone {
		log.Warnf("MCP %s: aibow-server とのつながりが切れました(%v)。次の送信でつなぎ直します", workspaceLabel(name), err)
		go s.session.Close()
	}
}

// Close は、全部のつながりを閉じる(アプリを閉じるとき)。
func (w *Workspaces) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	for name, s := range w.sessions {
		s.session.Close()
		delete(w.sessions, name)
	}
}

// plainError は、つなぐときのエラーが HTTP の口の断り(refusalTransport)なら、断りの文だけにする。
func plainError(err error) error {
	if msg := refused(err); msg != "" {
		return errors.New(msg)
	}
	return err
}

// refusalTransport は、HTTP の口が 401・403・404 で断ったとき、断りの文を JSON-RPC のエラー(meta.RefusedCode)の返事に書き直す。
//
// SDK は、200 番台でない返事の本文が JSON-RPC のエラーなら、それを呼び出しのエラーにして、つながりはそのまま残す。
// そうでなければ(aibow-server の断りは OpenAI 互換の API と同じ形の {"error": {"message"}})、つながりが切れたとみなしてしまう。
// 配布をやめた(403)のはつながりの問題ではないので、ここで JSON-RPC のエラーにして、断りの文をそのままモデルに渡す(remoteTool.call)。
type refusalTransport struct {
	base http.RoundTripper
}

func (t refusalTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	res, err := t.base.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	switch res.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
	default:
		return res, nil
	}
	data, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	res.Body.Close()
	msg := refusalText(res.StatusCode, data)
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      "refused", // SDK は id を突き合わせない(返事の形であればよい)
		"error":   map[string]any{"code": meta.RefusedCode, "message": msg},
	})
	res.Body = io.NopCloser(bytes.NewReader(body))
	res.ContentLength = int64(len(body))
	res.Header = res.Header.Clone()
	res.Header.Set("Content-Type", "application/json")
	res.Header.Del("Content-Length")
	return res, nil
}

// refusalText は、断りの返事の本文から、断りの文を取り出す(OpenAI 互換の形・JSON-RPC のエラー・ただの文のどれでも)。
func refusalText(status int, data []byte) string {
	// OpenAI 互換の形も JSON-RPC のエラーも、文は error.message にある
	var e struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &e) == nil && e.Error != nil && e.Error.Message != "" {
		return e.Error.Message
	}
	if text := strings.TrimSpace(string(data)); text != "" && status != http.StatusNotFound {
		return text
	}
	if status == http.StatusNotFound {
		return "aibow-server にサーバーのワークスペースの API がありません(aibow-server を新しくすると使えます)"
	}
	return fmt.Sprintf("aibow-server が断りました(%d %s)", status, http.StatusText(status))
}
