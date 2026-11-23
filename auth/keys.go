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
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/secondarykey/aibow/log"
)

// keyPrefix は API キーの頭。見ただけで aibow のキーだと分かるようにする(設定ファイルやログに紛れたときに気づけるように)
const keyPrefix = "aibow_"

// ErrBadKey は、API キーが無い・違うときのエラー。
var ErrBadKey = errors.New("API キーが無いか、違います。aibow-server の PC で aibow-server user key <名前> を実行して作ったキーを、Authorization: Bearer <キー> で送ってください")

// ErrKeyExpired は、API キーの期限が切れたときのエラー(401。code は key_expired。0060)。
// 切れたキーは消さない(管理の画面で「期限切れ」と見え、作り直すか消すかは管理者が決める)
var ErrKeyExpired = errors.New("API キーの期限が切れました。管理者に作り直してもらってください")

// ErrNoAPIKey は、消す API キーが無いときのエラー(0060)
var ErrNoAPIKey = errors.New("API キーはありません")

// KeyTTLs は、API キーの期限として選べる日数(0060 の決定事項 2。0 は無期限)。画面とコマンドで同じ4つ
var KeyTTLs = []int{30, 90, 365, 0}

// DefaultKeyDays は、API キーの期限の既定(日数)。Cline などに貼ったまま忘れて使い続けるのを防ぐため(0060 の決定事項 2)
const DefaultKeyDays = 90

// KeyExpires は、now に作るキーの期限(days が 0 なら無期限でゼロの時刻)。days が KeyTTLs に無ければエラー。
func KeyExpires(now time.Time, days int) (time.Time, error) {
	if !slices.Contains(KeyTTLs, days) {
		return time.Time{}, fmt.Errorf("API キーの期限は 30・90・365 日か、0(無期限)から選んでください(%d は選べません)", days)
	}
	if days == 0 {
		return time.Time{}, nil
	}
	return now.AddDate(0, 0, days), nil
}

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

// User は API キーかアプリ用のトークンの持ち主。違えば ErrBadKey、トークンの期限が切れていれば ErrTokenExpired、
// API キーの期限が切れていれば ErrKeyExpired、持ち主が無効にされていれば ErrUserDisabled(0060。ログに名前を書けるよう、このときだけ持ち主も返す)。
// どちらも同じ口(Authorization: Bearer)で受け付け、使える口も同じ(0019 の決定事項 11)。
func (k *Keys) User(ctx context.Context, key string) (User, error) {
	if IsAppToken(key) {
		return k.appTokenUser(ctx, key)
	}
	if !strings.HasPrefix(key, keyPrefix) {
		return User{}, ErrBadKey
	}
	u, expires, err := k.store.APIKeyOwner(ctx, KeyID(key))
	if errors.Is(err, ErrUserNotFound) {
		return User{}, ErrBadKey
	}
	if err != nil {
		return User{}, err
	}
	// 無効を先に見る(期限も切れていたら、作り直してもらっても使えないので、無効のほうを伝える)
	if u.Disabled {
		return u, ErrUserDisabled
	}
	now := k.now()
	if !expires.IsZero() && !now.Before(expires) {
		return User{}, ErrKeyExpired
	}
	markUsed(ctx, k.store, &u, now)
	return u, nil
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
		if ok && errors.Is(err, ErrKeyExpired) {
			// API キーの期限切れも、違うキーと分けて知らせる(Cline などの画面に「管理者に作り直してもらう」と出るように。0060)
			log.Warnf("API キーの期限が切れていたので断りました: %s %s(%s)", r.Method, r.URL.Path, remote(r))
			writeAPIError(w, http.StatusUnauthorized, "key_expired", err.Error())
			return
		}
		if ok && errors.Is(err, ErrUserDisabled) {
			log.Warnf("無効にされた利用者 %s の API キーかアプリのログインのトークンなので断りました: %s %s(%s)", u.Name, r.Method, r.URL.Path, remote(r))
			writeAPIError(w, http.StatusUnauthorized, "user_disabled", err.Error())
			return
		}
		if !ok || errors.Is(err, ErrBadKey) {
			// アプリのログインのトークンも同じ Bearer で届くので、どちらとも言う(API キーを使っていない人が首をかしげないように。0051 の決定事項 8)
			log.Warnf("API キーかアプリのログインのトークンが無いか違うので断りました: %s %s(%s)", r.Method, r.URL.Path, remote(r))
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

// bearerKey は、WithBearer が ctx に入れた、呼んだときのキー(API キーかアプリ用のトークン)
type bearerKey struct{}

// WithBearer は、cookie でログインしていないリクエストに Authorization: Bearer <API キーかアプリ用のトークン> があれば、
// その持ち主をリクエストの ctx に入れる(UserFrom で読める)。本人が自分の表示名・メールアドレス・パスワードを変える口(/_aibow/auth/)を、
// 画面のアプリからも呼べるようにするため(0031 の決定事項 45)。違うキーは入れずに次へ渡す(断るかは受け取った側が決める)。
// Bearer はブラウザがほかのサイトから勝手に付けて送らないので、cookie と違ってほかのサイトから使われる心配はない。
func (k *Keys) WithBearer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := UserFrom(r.Context()); !ok {
			if key, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
				key = strings.TrimSpace(key)
				if u, err := k.User(r.Context(), key); err == nil {
					ctx := context.WithValue(r.Context(), userKey{}, u)
					r = r.WithContext(context.WithValue(ctx, bearerKey{}, key))
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// bearerOf は、WithBearer が入れたキー(cookie でログインしているなら空)。
func bearerOf(ctx context.Context) string {
	k, _ := ctx.Value(bearerKey{}).(string)
	return k
}

// writeAPIError は OpenAI 互換の形のエラー({"error": {"message", "type", "code"}})。
func writeAPIError(w http.ResponseWriter, status int, code, msg string) {
	typ := "server_error"
	if status < 500 {
		typ = "invalid_request_error"
	}
	writeJSON(w, status, map[string]any{"error": map[string]string{"message": msg, "type": typ, "code": code}})
}

// MeHandler は GET /_aibow/auth/key。API キーの持ち主(画面のアプリが、誰としてつないでいるかを画面に出すため。0017 の 10-4)。
// 返事はブラウザの /_aibow/auth/me と同じ形(Me)。キーが要らない形(127.0.0.1 だけでログインなし)なら、誰でもないので {"login": false}。
// キーが無い・違うときは 401(RequireKey と同じ)。
func (k *Keys) MeHandler(required bool) http.Handler {
	if !required {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, Me{})
		})
	}
	return k.RequireKey(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, _ := UserFrom(r.Context())
		writeJSON(w, http.StatusOK, meOf(u))
	}))
}
