// Package remote は、会話の保存(aibow.Store)を HTTP 越しに使うための口と、その HTTP 版(0017 の 10-3。表の C)。
//
//	画面のアプリ [ 会話の仕組み・ループ・ツール ] ─ Store の HTTP 版(Client)─ HTTPS ─→ aibow-server [ 保存の口(Handler)→ server.db ]
//
// 画面のアプリを aibow-server につなぐと、生成(api/client)だけでなく保存もサーバーに頼む。ループとツールは手元で動くので、
// HTTP を越えるのは画面の呼び出し(ブラウザの ui.Service)ではなく、会話の仕組みが保存先に頼む操作(Store のメソッド)になる。
// 会話の仕組みから見ると、保存先が SQLite か HTTP の向こうかが変わるだけ(Generator を engine から api/client に替えたのと同じ)。
//
// 形は画面用の口(ui/web.go)にそろえた:
//
//	POST /aibow/store/{メソッド}   本文は引数の JSON の配列(ctx は除く)。返事は戻り値(error を除く)の JSON の配列
//	エラー                         {"error": {"message", "type", "code"}}。code で ErrNotFound などを見分け、Client が同じエラーに戻す
//
// 誰のものかは、サーバーが API キー(か、ログインなしの形ならログインなしの形の利用者)で決める。Client は ctx の利用者を送らない(偽れないように)。
//
// サーバーの側(Handler)とアプリの側(Client)を1つのパッケージに置いた。口の形を両方で同じに保つため。
// api/client はルートの aibow を import しない約束なので、aibow の型を扱うこの口はそちらに入れない。
package remote

import (
	"errors"
	"net/http"

	"github.com/secondarykey/aibow"
)

// Backend は、保存の口が受け持つ保存先(store の SQLite)。会話の保存と「生成中」の印。
type Backend interface {
	aibow.Store
	aibow.ConversationLocks
}

// errorCodes は、保存先のエラーと、口で返す code・HTTP の状態の対応。Client はこれを逆に引いて、同じエラーに戻す
// (会話の仕組みは errors.Is(err, aibow.ErrNotFound) などで見分けているので、HTTP 越しでも同じに効くように)。
var errorCodes = []struct {
	err    error
	code   string
	status int
}{
	{aibow.ErrNotFound, "not_found", http.StatusNotFound},
	{aibow.ErrProfileNotFound, "profile_not_found", http.StatusNotFound},
	{aibow.ErrProfileInUse, "profile_in_use", http.StatusConflict},
	{aibow.ErrBusy, "busy", http.StatusConflict},
	{aibow.ErrNoUser, "no_user", http.StatusUnauthorized},
}

func codeOf(err error) (code string, status int) {
	for _, e := range errorCodes {
		if errors.Is(err, e.err) {
			return e.code, e.status
		}
	}
	return "", http.StatusInternalServerError
}

func errorOf(code string) error {
	for _, e := range errorCodes {
		if e.code == code {
			return e.err
		}
	}
	return nil
}
