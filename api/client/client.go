// Package client は、aibow-server(api パッケージ)に HTTP でつなぎ、生成を頼むクライアント(ステップ8。0015)。
//
//	画面のアプリ [ 会話の仕組み → Generator の HTTP 版(ここ)] ──HTTP──→ aibow-server [ engine → llama.cpp → GPU ]
//
// 会話の仕組み(ルートの aibow)から見ると、生成の口 Generator の相手が手元の engine か、ネットワークの先かが変わるだけ。
// そのため、*engine.Engine と同じメソッド(Generate・CountTokens・EnsureContextSize・Info)を持つ。
// 会話の仕組みのほうは何も変えずに、Client を渡せば HTTP 越しで動く。
//
// 標準の OpenAI 互換の形(/v1/chat/completions)に、aibow 専用の口と欄(api.md の「aibow 専用の口と欄」)を足して使う:
//
//	生成            POST /v1/chat/completions(ストリーミング。"aibow": {"return_prompt": true} を付ける)
//	トークン数      POST /aibow/count-tokens
//	モデルの一覧    GET  /models
//	読み込む        POST /models/load
//	降ろす          POST /models/unload
//	GPU の層数      POST /aibow/gpu-layers
//	置き場所        GET /aibow/places・POST /aibow/places/add・POST /aibow/places/remove
//	サーバーの設定  GET /aibow/settings(表示だけ)
//
// 書式(チャットテンプレート)はサーバーが当てる。ここは会話を JSON にして送るだけで、書式を知らない(0015 の 5)。
// 会話 → JSON → 会話 の往復で1文字も変えない(KV キャッシュを今と同じだけ使い回すため。0015 の 8)ことを、テストで確かめている。
//
// ルートの aibow(会話の仕組み)は import しない(api と同じ)。HTTP は標準の net/http。
package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/secondarykey/aibow/engine"
	"github.com/secondarykey/aibow/format"
)

// Client は aibow-server につなぐクライアント。
type Client struct {
	base string // つなぎ先(例: http://127.0.0.1:8089)。後ろの / と /v1 は外してある
	hc   *http.Client
	// key は API キー(SetKey。0017 の 10-2)。空なら付けない(127.0.0.1 だけでログインの要らない aibow-server は、キーなしで通す)
	key string

	// mu は、最後に知ったサーバーの様子を守る。
	// Info は呼ばれる回数が多い(メーター・発言の記録など)ので、毎回は聞きに行かず、
	// 読み込み・生成・一覧のたびに取り直した値を返す(0015 の 10)
	mu   sync.Mutex
	info *engine.ModelInfo // 読み込み中のモデル(知らなければ nil)。会話の仕組みが「読み直すか」を決めるのに使う
	// shown は画面に見せる読み込み中のモデル。サーバーがほかの人のためにモデルを入れ替えている途中(info が nil)も、前に知ったモデルのまま
	// (画面のモデルが空になって、送信が止められないように。2026-10-22)。info と分けるのは、送信の前の読み直しを飛ばさないため
	shown *engine.ModelInfo
	nCtx  int    // サーバーの作業領域の大きさ(知らなければ 0)
	model string // 生成で頼むモデルの ID(LoadModel で決める。空ならサーバーが読み込んでいるモデル)
}

// New はクライアントを作る。つなぎ先は http://127.0.0.1:8089 の形(/v1 が付いていてもよい)。ここではまだつながない。
func New(baseURL string) *Client {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	base = strings.TrimSuffix(base, "/v1")
	// 生成はストリーミングで数分続くことがあるので、全体の時間の上限は付けない(止めるのは ctx)
	return &Client{base: base, hc: &http.Client{}}
}

// URL はつなぎ先(ログと画面に出す)。
func (c *Client) URL() string { return c.base }

// SetKey は、毎回のリクエストに付ける API キーを決める(Authorization: Bearer <キー>)。使い始める前に呼ぶ。
func (c *Client) SetKey(key string) { c.key = strings.TrimSpace(key) }

// TrustCert は、aibow-server の証明書(PEM のファイル)を信頼する(自己署名の証明書で HTTPS を待ち受けているとき。0017 の 10-2)。
// 自己署名の証明書は、どの認証局も「本物」と保証していないので、そのままでは HTTPS のつなぎ方を確かめる段階で断られる。
// サーバーの PC から写した証明書を「信頼する」と決めておくと、その証明書を出すサーバーにだけつなげる(ほかのサーバーのなりすましは断る)。
// OS が信頼している認証局も、そのまま信頼する。使い始める前に呼ぶ。
func (c *Client) TrustCert(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("aibow-server の証明書 %s を読めません: %w", path, err)
	}
	pool, err := x509.SystemCertPool()
	if err != nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(data) {
		return fmt.Errorf("aibow-server の証明書 %s に、PEM の形の証明書がありません", path)
	}
	c.hc = &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}
	return nil
}

// ---- エラー ----------------------------------------------------------------------------

// ErrUnreachable は、aibow-server につながらないとき(起動していない・止めた・つなぎ先が違う)のエラー(errors.Is で見分ける)。
// アプリは起動したまま、サーバーが起動したら起動し直さずに使えるようにする(0015 の 13)。
var ErrUnreachable = errors.New("aibow-server につながりません")

// unreachableError は、つながらなかった理由と対処(つなぎ先・「起動してください」)を持つエラー。
type unreachableError struct {
	url string
	err error
}

func (e *unreachableError) Error() string {
	return fmt.Sprintf("%v(つなぎ先: %s)。aibow-server を起動してください: %v", ErrUnreachable, e.url, e.err)
}

func (e *unreachableError) Is(target error) bool { return target == ErrUnreachable }

// HasRemedy は、対処まで書いてあること(会話の仕組みが「モデルを選び直して」などを書き足さないための約束)。
func (e *unreachableError) HasRemedy() bool { return true }

// APIError は、サーバーが返したエラー(OpenAI と同じ形の {"error": {...}})。
type APIError struct {
	Status  int
	Type    string
	Code    string // 例: context_length_exceeded・model_not_found
	Message string
}

func (e *APIError) Error() string { return e.Message }

// Unwrap は、サーバーが code で知らせたモデルの状態を、手元と同じエラーにする
// (errors.Is(err, engine.ErrNoModel) / errors.Is(err, format.ErrUnsupported) が、直接でも HTTP 越しでも同じに効くように)。
func (e *APIError) Unwrap() error {
	switch e.Code {
	case "model_not_loaded":
		return engine.ErrNoModel
	case "unsupported_format":
		return format.ErrUnsupported
	}
	return nil
}

// Caution は、エラー(赤)ではなく注意として見せるか。管理者が使えないようにしたモデル(code が model_disabled)のとき
// (models.DisabledError と同じ約束。0018 の決定事項 13)。
func (e *APIError) Caution() bool { return e.Code == "model_disabled" }

// errorBody は返事のエラーの形。ストリーミングの途中のエラーも同じ形の塊で来る。
type errorBody struct {
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
}

// unreachable は、つながらなかったことを、理由と対処が分かるエラーにする。
func (c *Client) unreachable(err error) error {
	return &unreachableError{url: c.base, err: err}
}

// ---- やり取りの下回り -------------------------------------------------------------------

// timeout は、生成以外のやり取りの時間の上限。読み込みは順番待ち(ほかの人の生成)の後になるので長めにする
const (
	shortTimeout = 30 * time.Second
	loadTimeout  = 10 * time.Minute
)

// do はリクエストを送り、200 なら返事の本文を返す(閉じるのは呼ぶ側)。200 でなければ本文のエラーを APIError にする。
func (c *Client) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		// < > & を < のような形にしない(会話にはコードが多いので、送る量と読みやすさのため。読めば中身は同じ)
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(body); err != nil {
			return nil, err
		}
		r = &buf
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, r)
	if err != nil {
		return nil, fmt.Errorf("つなぎ先 %s が URL として読めません: %w", c.base, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.key != "" {
		req.Header.Set("Authorization", "Bearer "+c.key)
	}
	res, err := c.hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, c.unreachable(err)
	}
	if res.StatusCode == http.StatusOK {
		return res, nil
	}
	defer res.Body.Close()
	var eb errorBody
	data, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if json.Unmarshal(data, &eb) == nil && eb.Error != nil {
		return nil, &APIError{Status: res.StatusCode, Type: eb.Error.Type, Code: eb.Error.Code, Message: eb.Error.Message}
	}
	return nil, &APIError{Status: res.StatusCode, Message: fmt.Sprintf("aibow-server が %s を返しました(%s %s): %s",
		res.Status, method, path, strings.TrimSpace(string(data)))}
}

// call は、生成以外のやり取り(JSON を送って JSON を受け取る)。out が nil なら本文を読まない。
func (c *Client) call(method, path string, body, out any, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	res, err := c.do(ctx, method, path, body)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return fmt.Errorf("aibow-server の返事が読めません(%s %s): %w", method, path, err)
	}
	return nil
}

// Post は、aibow-server の path に body を JSON で送り、返事を out に読む(out が nil なら読まない)。
// API キー・証明書・つながらないときのエラーは、ほかの呼び出しと同じ。ctx に期限が無ければ storeTimeout で区切る。
// 会話の保存の口(store/remote。0017 の 10-3)が使う。
func (c *Client) Post(ctx context.Context, path string, body, out any) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, storeTimeout)
		defer cancel()
	}
	res, err := c.do(ctx, http.MethodPost, path, body)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return fmt.Errorf("aibow-server の返事が読めません(POST %s): %w", path, err)
	}
	return nil
}

// storeTimeout は、保存の口の1回のやり取りの上限(保存は生成と違って、すぐ終わるはずなので)。
const storeTimeout = 30 * time.Second

// Me は、API キーの持ち主の名前(GET /aibow/auth/key。0017 の 10-4)。aibow-server がキーの要らない形なら login が false(誰でもない)。
// 画面のアプリが、誰としてつないでいるかを画面に出すのに使う。
func (c *Client) Me() (name string, login bool, err error) {
	var me struct {
		Login bool   `json:"login"`
		Name  string `json:"name"`
	}
	if err := c.call(http.MethodGet, "/aibow/auth/key", nil, &me, shortTimeout); err != nil {
		return "", false, err
	}
	return me.Name, me.Login, nil
}
