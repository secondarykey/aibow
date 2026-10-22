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
//
// 誰の会話かは、ここ(who)だけで決める(0017 の 12)。ログインが要る設定なら cookie のセッションの人(auth)、要らなければログインなしの形の利用者。
// ログインの口は /aibow/auth/ の下(auth.Handler)。

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"uuid"

	"github.com/secondarykey/aibow"
	"github.com/secondarykey/aibow/api"
	"github.com/secondarykey/aibow/auth"
	"github.com/secondarykey/aibow/engine"
	"github.com/secondarykey/aibow/log"
	"github.com/secondarykey/aibow/store"
	"github.com/secondarykey/aibow/ui"
	"github.com/secondarykey/aibow/web"
)

// startBrowser は、ブラウザの会話の DB を開き、会話の仕組みと画面用の口を作る。
// initial は DB にまだ値が無いときの設定、url は aibow-server のアドレス(画面の「つなぎ先」に出す)。
// 戻り値の handler は /aibow/ui/ と /aibow/auth/ の下を受け持つ。keys は API キーの係(利用者は同じ server.db にいるので、ここで作る)。
// close は終了のときに呼ぶ(生成を止めて、DB を閉じる)。
func startBrowser(c config, initial aibow.AppSettings, eng *engine.Engine, mm *modelManager, nCtx int, url string, seat *api.Seat) (http.Handler, *auth.Keys, func(), error) {
	st, err := store.Open(c.DBPath)
	if err != nil {
		return nil, nil, nil, err
	}
	log.Noticef("ブラウザの会話の保存先: %s", absPath(c.DBPath))
	// 起動のときに読む人ごとの設定(新しい会話の既定)は、ログインなしの形の利用者のもの(ログインは 0017 の 10-1)
	app, notes, err := aibow.LoadAppSettings(aibow.WithUser(context.Background(), aibow.LocalUser), st, initial)
	if err != nil {
		st.Close()
		return nil, nil, nil, err
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

	sessions := auth.NewSessions(st)
	mux := http.NewServeMux()
	mux.Handle("/aibow/ui/", ui.Handler(svc, events, who(c.Login)))
	mux.Handle("/aibow/auth/", auth.Handler(sessions, c.Login))
	return sessions.Middleware(mux), auth.NewKeys(st), func() { life.Stop(); st.Close() }, nil
}

// errNotLoggedIn は、ログインが要るのにログインしていないときのエラー(画面はこれを 401 で受け取り、ログインの画面を出す)。
var errNotLoggedIn = errors.New("ログインしてください")

// who は、画面用の口を呼んだ利用者を決める係(ui.Who)。利用者の印を付けるのはここだけ(0017 の 12)。
//   - ログインが要る:cookie のセッションの人(auth.Middleware がリクエストの ctx に入れた人)。いなければ 401
//   - 要らない:いつもログインなしの形の利用者(aibow.LocalUser)。127.0.0.1 だけで待ち受けるので、その PC の人だけが使う
func who(login bool) ui.Who {
	return func(r *http.Request) (uuid.UUID, error) {
		if !login {
			return aibow.LocalUser, nil
		}
		u, ok := auth.UserFrom(r.Context())
		if !ok {
			return uuid.UUID{}, errNotLoggedIn
		}
		return u.ID, nil
	}
}

// browserMarker は、aibow-server が配る index.html に足す印。画面はこれを見て、ブラウザ用の「Go を呼ぶ係」を使う(0016 の 16)。
// 場所(URL)で見分けると、Wails の server モード(確認用の一時ビルド)の画面もブラウザだと思ってしまうので、配る側が印を付ける
const browserMarker = `<meta name="aibow-browser" content="1">`

// screen は画面のファイルを配る(web パッケージに埋め込んだもの。デスクトップのアプリと同じ)。
// index.html だけは、ブラウザの印を足して返す。
func screen() http.Handler {
	files, built := web.Files()
	if built {
		log.Info("ブラウザの画面: 埋め込んだ画面を配ります")
	} else {
		log.Warn("画面がビルドされていません(web/dist/app が無い)。ブラウザには、ビルドの仕方を書いたページだけを出します")
	}
	index, err := fs.ReadFile(files, "index.html")
	if err != nil {
		// web.Files はいつも index.html を持つ(ビルドしていなければ、その代わりのページ)
		panic("画面の index.html が読めません: " + err.Error())
	}
	page := strings.Replace(string(index), "<head>", "<head>\n    "+browserMarker, 1)
	assets := http.FileServerFS(files)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// 画面を作り直したら、開き直すだけで新しいものが読まれるように(assets はファイル名にハッシュが付くので、残っていてよい)
		w.Header().Set("Cache-Control", "no-cache")
		io.WriteString(w, page)
	})
	mux.Handle("GET /assets/", assets)
	return mux
}

// routes は aibow-server の道筋。画面(/ と /assets/)・画面用の口(/aibow/ui/)とログインの口(/aibow/auth/)・それ以外(OpenAI 互換の口と aibow 専用の口)。
// 長いほうのパスが先に当たるので、画面の2つ以外はこれまでどおり api が受け取る(知らないパスの 404 も api の形)。
func routes(apiH, browser, screen http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /{$}", screen)
	mux.Handle("GET /assets/", screen)
	mux.Handle("/aibow/ui/", browser)
	mux.Handle("/aibow/auth/", browser)
	mux.Handle("/", apiH)
	return mux
}
