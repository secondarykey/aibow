package main

import (
	"bytes"
	stdlog "log"

	"github.com/secondarykey/aibow/log"
)

// httpErrorLog は http.Server の ErrorLog に渡すロガー。
//
// http.Server は自分のエラー(TLS のハンドシェイクの失敗・リクエストの読み違いなど)を標準の log に書く。
// ErrorLog を決めなければ標準の log に行き、slog.SetDefault のあとは出すレベル(Root)で記録される(log.Init)。
// そのうち「http: TLS handshake error from …」だけは Debug に落とす:自己署名の証明書で HTTPS にすると、
// ブラウザ・curl などが証明書を信頼しないまま一度つなぐたびに「remote error: tls: unknown certificate」で出て、
// exe の Notice のログが埋まるため(証明書を信頼してもらうまでは必ず出る、相手の側の事情)。
// 消しはしない(Debug にすれば見られる。つながらない相手を調べるときに使う)。
// ほかの行は今までどおり、標準の log の出口(出すレベル)にそのまま渡す。
func httpErrorLog() *stdlog.Logger {
	return stdlog.New(httpErrorWriter{}, "", 0)
}

type httpErrorWriter struct{}

func (httpErrorWriter) Write(p []byte) (int, error) {
	if isTLSHandshakeError(p) {
		log.Debug(string(bytes.TrimRight(p, "\n")))
		return len(p), nil
	}
	// 書く時に出口を取る(SetLevel でレベルが変わっても、標準の log と同じに扱われるように)
	return stdlog.Writer().Write(p)
}

// isTLSHandshakeError は、http.Server が書くハンドシェイクの失敗の行か(net/http の server.go の文言)。
func isTLSHandshakeError(p []byte) bool {
	return bytes.HasPrefix(p, []byte("http: TLS handshake error from "))
}
