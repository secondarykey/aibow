package client

// workspaces.go: サーバーのワークスペースの一覧(GET /_aibow/workspaces)と、その MCP の口に使う HTTP のつなぎ方(0067)。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/secondarykey/aibow/internal/appversion"
)

// ErrNoServerWorkspaces は、aibow-server が古く、サーバーのワークスペースの口が無いとき(404)のエラー(errors.Is で見分ける。0067 の決定事項 16)。
// アプリはそのとき、サーバーのワークスペースを使わず、今までどおり手元のワークスペースを使う(0064 の知らせを出す)。
var ErrNoServerWorkspaces = errors.New("この aibow-server には、アプリからサーバーのワークスペースを使う API がありません(aibow-server を新しくすると使えます)")

// ServerWorkspace は、サーバーのワークスペース1つ(名前とフォルダ。パスは含まない)。
type ServerWorkspace struct {
	Name    string         `json:"name"`
	Folders []ServerFolder `json:"folders"`
}

// ServerFolder は、サーバーのワークスペースのフォルダ1つの名前と印と説明。
type ServerFolder struct {
	Name        string `json:"name"`
	Main        bool   `json:"main"`
	Writable    bool   `json:"writable"`
	Description string `json:"description,omitempty"`
}

// workspacesTimeout は、一覧を引く時間の上限(送信のたびに引くので、短くする)。
const workspacesTimeout = 10 * time.Second

// ServerWorkspaces は、API キーの持ち主が使えるサーバーのワークスペース(配布プロファイルで配られたもの)。
// 古い aibow-server なら ErrNoServerWorkspaces。
func (c *Client) ServerWorkspaces(ctx context.Context) ([]ServerWorkspace, error) {
	ctx, cancel := context.WithTimeout(ctx, workspacesTimeout)
	defer cancel()
	res, err := c.do(ctx, http.MethodGet, "/_aibow/workspaces", nil)
	if err != nil {
		var ae *APIError
		if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
			return nil, ErrNoServerWorkspaces
		}
		return nil, err
	}
	defer res.Body.Close()
	var out struct {
		Workspaces []ServerWorkspace `json:"workspaces"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("aibow-server の返事が読めません(GET /_aibow/workspaces): %w", err)
	}
	return out.Workspaces, nil
}

// Transport は、aibow-server への HTTP のつなぎ方(MCP のクライアントに渡す。サーバーのワークスペースの MCP の口)。
// ほかの呼び出しと同じく、毎回のリクエストで今の API キー(ログインし直したら替わる)とアプリのバージョンを付け、
// 証明書は覚えたもの(TrustCert・つなぎ先を確かめたときのフィンガープリント)で確かめる。
// 401 で断られたら、ほかの呼び出しと同じく OnUnauthorized の係に知らせる(画面がログインし直しを出す)。
func (c *Client) Transport() http.RoundTripper {
	return clientTransport{c}
}

type clientTransport struct{ c *Client }

func (t clientTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context()) // RoundTrip は渡されたリクエストを書き換えてはいけない決まり
	key := t.c.Key()
	if key != "" {
		r.Header.Set("Authorization", "Bearer "+key)
	}
	if v := sentAppVersion(); v != "" {
		r.Header.Set(appversion.AppHeader, v)
	}
	base := t.c.hc.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	res, err := base.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	t.c.noteVersion(res.Header)
	if res.StatusCode == http.StatusUnauthorized && t.c.onUnauth != nil {
		// 本文は MCP のクライアントも読む(断りの文をモデルに渡す)ので、読んだら元に戻す
		data, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		res.Body.Close()
		res.Body = io.NopCloser(bytes.NewReader(data))
		copied := *res
		copied.Body = io.NopCloser(bytes.NewReader(data))
		t.c.onUnauth(key, readError(&copied, r.Method, r.URL.Path))
	}
	return res, nil
}
