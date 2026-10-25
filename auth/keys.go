package auth

// keys.go: API キー(0017 の決定事項 9。10-2)。
//
// ブラウザはログインして cookie のセッションで「誰か」を伝えるが、デスクトップのアプリと Cline などは画面を持たないので、
// 利用者ごとの API キーを毎回のリクエストに付けてくる(Authorization: Bearer <キー>。OpenAI の API と同じ形)。
// サーバーは何も覚えず、毎回キーから利用者を引く(ステートレスな OpenAI 互換の口に合う)。
//
// キーは1人1本(ユーザーと決めた)。aibow-server user key <名前> で作り、作り直すと前のキーは使えなくなる。
// 保存するのは SHA-256 だけ(セッションの合言葉と同じ。キー自体が十分に長いでたらめな値なので、塩を付けて重ねる必要は無い)。

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/secondarykey/aibow/log"
)

// keyPrefix は API キーの頭。見ただけで aibow のキーだと分かるようにする(設定ファイルやログに紛れたときに気づけるように)
const keyPrefix = "aibow_"

// ErrBadKey は、API キーが無い・違うときのエラー。
var ErrBadKey = errors.New("API キーが無いか、違います。aibow-server の PC で aibow-server user key <名前> を実行して作ったキーを、Authorization: Bearer <キー> で送ってください")

// NewAPIKey は API キーを作る(aibow_ + 32 バイトのでたらめな値)。
func NewAPIKey() string {
	b := make([]byte, 32)
	rand.Read(b)
	return keyPrefix + base64.RawURLEncoding.EncodeToString(b)
}

// KeyID は、API キーを保存する形(SHA-256 の16進)にする。
func KeyID(key string) string { return tokenID(key) }

// Keys は API キー(とアプリ用のトークン。tokens.go)から利用者を引く係。
type Keys struct {
	store Store
	now   func() time.Time // アプリ用のトークンの期限を見る(テストで時刻を進めるため)
}

func NewKeys(st Store) *Keys { return &Keys{store: st, now: time.Now} }

// User は API キーかアプリ用のトークンの持ち主。違えば ErrBadKey、トークンの期限が切れていれば ErrTokenExpired。
// どちらも同じ口(Authorization: Bearer)で受け付け、使える口も同じ(0019 の決定事項 11)。
func (k *Keys) User(ctx context.Context, key string) (User, error) {
	if IsAppToken(key) {
		return k.appTokenUser(ctx, key)
	}
	if !strings.HasPrefix(key, keyPrefix) {
		return User{}, ErrBadKey
	}
	u, err := k.store.UserByAPIKey(ctx, KeyID(key))
	if errors.Is(err, ErrUserNotFound) {
		return User{}, ErrBadKey
	}
	return u, err
}

// RequireKey は、Authorization: Bearer <API キー> を確かめ、持ち主をリクエストの ctx に入れてから next に渡す(UserFrom で読める)。
// 無い・違うときは 401 で断る(OpenAI の API と同じ形のエラー。Cline などがそのまま画面に出せる)。
func (k *Keys) RequireKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		u, err := k.User(r.Context(), strings.TrimSpace(key))
		if ok && errors.Is(err, ErrTokenExpired) {
			// 期限切れは、キーの間違いと分けて知らせる(アプリが「もう一度ログインして」と出せるように)
			log.Warnf("アプリのログインの期限が切れていたので断りました: %s %s(%s)", r.Method, r.URL.Path, remote(r))
			writeAPIError(w, http.StatusUnauthorized, "token_expired", err.Error())
			return
		}
		if !ok || errors.Is(err, ErrBadKey) {
			log.Warnf("API キーが無いか違うので断りました: %s %s(%s)", r.Method, r.URL.Path, remote(r))
			writeAPIError(w, http.StatusUnauthorized, "invalid_api_key", ErrBadKey.Error())
			return
		}
		if err != nil {
			log.Errorf("API キーを確かめられません: %v", err)
			writeAPIError(w, http.StatusInternalServerError, "", "API キーを確かめられません: "+err.Error())
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey{}, u)))
	})
}

// writeAPIError は OpenAI 互換の形のエラー({"error": {"message", "type", "code"}})。
func writeAPIError(w http.ResponseWriter, status int, code, msg string) {
	typ := "server_error"
	if status < 500 {
		typ = "invalid_request_error"
	}
	writeJSON(w, status, map[string]any{"error": map[string]string{"message": msg, "type": typ, "code": code}})
}

// MeHandler は GET /aibow/auth/key。API キーの持ち主(画面のアプリが、誰としてつないでいるかを画面に出すため。0017 の 10-4)。
// 返事はブラウザの /aibow/auth/me と同じ形(Me)。キーが要らない形(127.0.0.1 だけでログインなし)なら、誰でもないので {"login": false}。
// キーが無い・違うときは 401(RequireKey と同じ)。
func (k *Keys) MeHandler(required bool) http.Handler {
	if !required {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, Me{})
		})
	}
	return k.RequireKey(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, _ := UserFrom(r.Context())
		writeJSON(w, http.StatusOK, Me{Login: true, Name: u.Name, Admin: u.Admin})
	}))
}
