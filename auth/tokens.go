package auth

// tokens.go: アプリ用のトークン(0019)。
//
// 画面のアプリは cookie を持たないので、ブラウザのようなセッションは使えない。今までは、サーバーの PC で作った API キーを
// アプリの settings.json に手で書き写していた。そこで、アプリの画面で名前とパスワードを打ったら、aibow-server が
// 確かめてトークン(期限付きの合言葉)を出し、アプリはそれを API キーと同じ Authorization: Bearer で送るようにする。
//
//	ブラウザ  … パスワード → セッションの合言葉(cookie)。最後に使ってから 30 日。使うと延びる
//	アプリ    … パスワード → アプリ用のトークン(このファイル)。作ってから 10 日で固定。使っても延びない(ユーザーと決めた。0019 の決定事項 3)
//	Cline など … サーバーの PC で作った API キー(keys.go)。作り直すまで使える
//
// トークンは PC ごとに1本(利用者 + PC の名前)。同じ PC でログインし直すと、その PC の前のトークンは消える(決定事項 9)。
// 保存するのは SHA-256 だけ(セッション・API キーと同じ)。頭を aibowapp_ にして、API キー(aibow_)と見分ける。

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// AppTokenTTL はアプリ用のトークンの期限(作ってから。延ばさない)
	AppTokenTTL = 10 * 24 * time.Hour
	// appTokenPrefix はアプリ用のトークンの頭。API キーの頭(aibow_)とは前から重ならない
	appTokenPrefix = "aibowapp_"
	// maxDevice は PC の名前の長さの上限(文字数。ログと DB に入るので、長すぎるものは切る)
	maxDevice = 64
)

// ErrTokenExpired は、アプリ用のトークンの期限が切れたときのエラー(401。code は token_expired)。
var ErrTokenExpired = errors.New("アプリのログインの期限(10 日)が切れました。設定の画面の「サーバー」で、もう一度ログインしてください")

// AppLogin は、アプリでログインしたときの返事(POST /aibow/auth/token)。
type AppLogin struct {
	Token     string    `json:"token"`
	Name      string    `json:"name"`
	Admin     bool      `json:"admin,omitempty"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// IssueAppToken は名前とパスワードを確かめ、device(PC の名前)のアプリ用のトークンを作る。違っていれば ErrLogin。
func (s *Sessions) IssueAppToken(ctx context.Context, name, password, device string) (AppLogin, error) {
	device = strings.TrimSpace(device)
	if device == "" {
		return AppLogin{}, errors.New("PC の名前がありません(アプリが送ってくるはずのものです)")
	}
	if utf8.RuneCountInString(device) > maxDevice {
		device = string([]rune(device)[:maxDevice])
	}
	u, err := s.check(ctx, name, password)
	if err != nil {
		return AppLogin{}, err
	}
	now := s.now()
	token := appTokenPrefix + newToken()
	t := AppToken{ID: tokenID(token), UserID: u.ID, Device: device, CreatedAt: now, ExpiresAt: now.Add(AppTokenTTL)}
	if err := s.store.SetAppToken(ctx, t); err != nil {
		return AppLogin{}, err
	}
	return AppLogin{Token: token, Name: u.Name, Admin: u.Admin, ExpiresAt: t.ExpiresAt}, nil
}

// RevokeAppToken はアプリ用のトークンを消す(アプリでログアウトしたとき。無くてもエラーにしない)。
func (s *Sessions) RevokeAppToken(ctx context.Context, token string) error {
	return s.store.DeleteAppToken(ctx, tokenID(token))
}

// IsAppToken は、key がアプリ用のトークンの形か(API キーではなく)。
func IsAppToken(key string) bool { return strings.HasPrefix(key, appTokenPrefix) }

// appTokenUser はアプリ用のトークンの持ち主。無ければ ErrBadKey、期限が切れていれば ErrTokenExpired(切れたものは消す)。
func (k *Keys) appTokenUser(ctx context.Context, token string) (User, error) {
	id := tokenID(token)
	t, err := k.store.AppToken(ctx, id)
	if err != nil {
		return User{}, err
	}
	if !k.now().Before(t.ExpiresAt) {
		k.store.DeleteAppToken(ctx, id)
		return User{}, ErrTokenExpired
	}
	u, err := k.store.UserByID(ctx, t.UserID)
	if errors.Is(err, ErrUserNotFound) {
		return User{}, ErrBadKey // 消された利用者(トークンも一緒に消えるので、ふつうは来ない)
	}
	return u, err
}
