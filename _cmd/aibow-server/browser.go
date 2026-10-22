package main

// browser.go: ブラウザの会話(ステップ9。0016)。
//
// 会話の仕組み(ルートの aibow)と保存(SQLite)を aibow-server の中で動かし、画面用の口(ui.Handler。/aibow/ui/ の下)で出す。
//
//	ブラウザ ──HTTP──→ /aibow/ui/  → ui.Service → 会話の仕組み → engine ┐
//	Cline など ──HTTP──→ /v1/ など → api ─────────────────────→ engine ┴─ 順番待ちの席は1つ(api.Seat)
//
// 生成は同じ engine を直接使い、OpenAI 互換の口と同じ席を通す(HTTP で自分自身に頼まない。0016 の 11)。
// ブラウザの会話ではツールを使わない(0016 の 7)。会話は server.db に保存し、画面のアプリの aibow.db とは別にする(0016 の 13)。

import (
	"net/http"

	"github.com/secondarykey/aibow"
	"github.com/secondarykey/aibow/api"
	"github.com/secondarykey/aibow/engine"
	"github.com/secondarykey/aibow/log"
	"github.com/secondarykey/aibow/store"
	"github.com/secondarykey/aibow/ui"
)

// startBrowser は、ブラウザの会話の DB を開き、会話の仕組みと画面用の口を作る。
// initial は DB にまだ値が無いときの設定、url は aibow-server のアドレス(画面の「つなぎ先」に出す)。
// 戻り値の close は終了のときに呼ぶ(生成を止めて、DB を閉じる)。
func startBrowser(c config, initial aibow.AppSettings, eng *engine.Engine, mm *modelManager, nCtx int, url string, seat *api.Seat) (http.Handler, func(), error) {
	st, err := store.Open(c.DBPath)
	if err != nil {
		return nil, nil, err
	}
	log.Noticef("ブラウザの会話の保存先: %s", absPath(c.DBPath))
	app, notes, err := aibow.LoadAppSettings(st, initial)
	if err != nil {
		st.Close()
		return nil, nil, err
	}
	for _, n := range notes {
		log.Notice(n)
	}
	// 作業領域は起動のときに決めた大きさで固定なので、新しい会話の既定が入らなければ、ここで知らせておく(送信のときにも断る。0016 の 12)
	if d := app.Defaults; d.ContextSize() > nCtx {
		log.Warnf("ブラウザの新しい会話の既定(会話の上限 %d + 返事の上限 %d = %d トークン)が、作業領域(%d トークン)に入りません。"+
			"画面で上限を下げるか、-ctx(設定ファイルの ctx)で作業領域を大きくしてください", d.HistoryTokens, d.ReplyTokens, d.ContextSize(), nCtx)
	}

	events := ui.NewEvents()
	svc, life := ui.New(ui.Options{
		Host:  ui.NewServerHost(eng, mm, nCtx, url),
		Store: st, App: app, Version: appVersion(),
		Notify: events, Seat: seat, WithoutTools: true,
	})
	life.Start() // 起動のときのモデルは main が読み込んだので、ここでは読み込まない(Initial を渡していない)
	return ui.Handler(svc, events), func() { life.Stop(); st.Close() }, nil
}
