package main

// wailslog.go: Wails に渡すログの出口(0057 の決定事項 13)。
//
// Wails は、Go のメソッドがエラーを返すたびに「Binding call failed: Bound method returned an error: <エラーの文>」を ERROR で書く。
// aibow-server につながらない間は、画面が一覧を取り直すたびに6本ほど同時に呼ぶので、同じ ERROR が6件ずつ積み重なっていた。
// つながらないこと・証明書が違うことは ui/models.go が Warn で1回書き、401(ログインの期限が切れた)は connection.go がログインの画面に戻すときに1回書くので、
// この3つの理由の行だけ Debug に落とす。ほかの「Binding call failed」は今のまま ERROR で残す(思わぬ失敗を見落とさないため)。
//
// Wails はエラーの型も属性も渡さず、文(slog.Record の Message)にエラーの文を入れてくるだけなので、文の中身で見分ける
// (2026-11-24 に Wails v3.0.0 の messageprocessor_call.go・transport_http.go の httpError で確かめた)。

import (
	"context"
	"log/slog"
	"strings"

	apiclient "github.com/secondarykey/aibow/api/client"
	"github.com/secondarykey/aibow/auth"
	"github.com/secondarykey/aibow/log"
)

// bindingFailed は、Wails が Go のメソッドのエラーを書くときの文の頭(Wails の errs.BindingCallFailedError)。
const bindingFailed = "Binding call failed"

// quietReasons は、Debug に落とすエラーの文(aibow-server につながらない・証明書が違う・401 で断られた)。
var quietReasons = []string{
	apiclient.ErrUnreachable.Error(),
	apiclient.ErrCertChanged.Error(),
	auth.ErrTokenExpired.Error(),
	auth.ErrBadKey.Error(),
}

// wailsLogger は、base の出口を、上の行だけレベルを落とす形で包む。
func wailsLogger(base *slog.Logger) *slog.Logger {
	return slog.New(quietBindingHandler{base.Handler()})
}

// quietBindingHandler は、aibow-server の事情による「Binding call failed」だけ Debug に落とす slog.Handler。
type quietBindingHandler struct{ slog.Handler }

func (h quietBindingHandler) Handle(ctx context.Context, r slog.Record) error {
	if r.Level >= slog.LevelError && quietBinding(r.Message) {
		r.Level = log.LevelDebug
		if !h.Handler.Enabled(ctx, r.Level) {
			return nil
		}
	}
	return h.Handler.Handle(ctx, r)
}

func (h quietBindingHandler) WithAttrs(as []slog.Attr) slog.Handler {
	return quietBindingHandler{h.Handler.WithAttrs(as)}
}

func (h quietBindingHandler) WithGroup(name string) slog.Handler {
	return quietBindingHandler{h.Handler.WithGroup(name)}
}

// quietBinding は、Debug に落とす行か。
func quietBinding(msg string) bool {
	if !strings.HasPrefix(msg, bindingFailed) {
		return false
	}
	for _, r := range quietReasons {
		if strings.Contains(msg, r) {
			return true
		}
	}
	return false
}
