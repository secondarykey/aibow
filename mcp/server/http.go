package server

// http.go: MCP のサーバーを Streamable HTTP で出す口(0067)。
//
// aibow の中の組み込みのツールは、同じサーバー(New)にメモリ内のつなぎ方でつないでいる(mcp/client の Local)。
// aibow-server がサーバーモードのアプリにサーバーのワークスペースを出すときは、同じサーバーを HTTP の口に載せるだけ。
// ツールの中・書き換える前の確認(MRTR。confirm.go)・引数 folder・読むだけのフォルダは変わらない。
//
//	アプリ ──POST(JSON-RPC 1つ)──→ HTTPHandler → New(options(リクエスト)) → tools/call
//	アプリ ←────── 返事(JSON)──────
//
// ステートレス(SDK の StreamableHTTPOptions.Stateless)にする。リクエストのたびにサーバーを作り、往復の間に何も覚えない:
//   - 書く前の確認は「同じ引数 + 答え + 合言葉(requestState)」で送り直す形なので、セッションが要らない(confirm.go)
//   - フォルダの組はリクエストのたびに options で引くので、管理の画面でフォルダを変えたら、次の呼び出しから新しい組になる
//     (セッションが前の組を持ち続けることが無いので、作り直す仕組みも要らない。0067 の決定事項 8)
//   - MCP の 2026-07-28 版(最初の挨拶 initialize が無い版)は、SDK ではステートレスの口でだけ話せる

import (
	"context"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// HTTPOptions は HTTP の口の設定。
type HTTPOptions struct {
	// AnyHost は、127.0.0.1 で受けたリクエストの Host が 127.0.0.1 でなくても断らない(SDK の DNS リバインディングの守りを外す)。
	// 同じ PC のリバースプロキシ(nginx など)越しに受けると、Host は外の名前になる。
	// ほかの守り(API キーなど)で、ブラウザのページから勝手に呼ばれないようにしているときだけ true にする
	AnyHost bool
}

// serverKey は、リクエストの ctx に入れるそのリクエストのサーバー(HTTPHandler が作り、SDK の getServer が読む)。
type serverKey struct{}

// HTTPHandler は、options が返す設定のサーバーを、Streamable HTTP(ステートレス)で出す口。
//
// options はリクエストのたびに1回呼ぶ。誰が呼んでよいか・どのフォルダを出すかは、呼ぶ側が先に確かめ、
// その結果をリクエストの ctx に入れてから渡す(options は ctx から読むだけにする)。
// SDK は1回のリクエストの中で getServer を2回呼ぶ(話せる版を確かめるときと、実行するとき)ので、作ったサーバーを ctx に入れて同じものを使う。
func HTTPHandler(options func(*http.Request) Options, o HTTPOptions) http.Handler {
	h := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		s, _ := r.Context().Value(serverKey{}).(*mcp.Server)
		return s
	}, &mcp.StreamableHTTPOptions{
		Stateless: true,
		// 返事は SSE でなく JSON 1つにする(途中の知らせは送らないので、つなぎっぱなしの返事が要らない。プロキシでも困りにくい)
		JSONResponse:               true,
		DisableLocalhostProtection: o.AnyHost,
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s := New(options(r))
		h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), serverKey{}, s)))
	})
}
