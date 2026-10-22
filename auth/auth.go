// Package auth は、aibow-server の利用者とログイン(ステップ10。0017 の 10-1)。
//
// 認証(この人は誰か)だけを受け持つ。ログインした人に何を許すか(認可。今は「自分のものだけ」)は、
// 保存先(会話・プロファイルを持ち主で分ける。store)と aibow-server が決める。
//
//	パスワード … そのものは保存せず、塩(salt)を付けて何度もハッシュを重ねた値を保存する(password.go)
//	セッション … ログインしたら、推測できない合言葉(トークン)を作って cookie で渡し、サーバーは合言葉のハッシュと持ち主を覚える(sessions.go)。
//	             ブラウザは以後のリクエストに cookie を付けてくるので、毎回パスワードを送らなくてよい
//	HTTP      … ログイン・ログアウトの口と、cookie からログインした人を決めてリクエストの ctx に入れる係(http.go)
//
// このパッケージはルートの aibow を import しない(会話の仕組みと関係なく、誰かを決めるだけなので)。
package auth

import (
	"context"
	"errors"
	"time"
	"uuid"
)

// User は利用者1人。
type User struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
	// Password はパスワードのハッシュ(HashPassword の形)。空ならログインできない(パスワードをまだ作っていない)
	Password  string    `json:"-"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Session はログインしている間の印1つ。
type Session struct {
	// ID は合言葉(cookie の値)の SHA-256。合言葉そのものは保存しない(DB をのぞかれても、その人のふりができないように)
	ID        string
	UserID    uuid.UUID
	CreatedAt time.Time
	ExpiresAt time.Time
}

// Store は利用者とセッションの保存先(本物は store パッケージの SQLite。server.db)。
type Store interface {
	// CreateUser は利用者を足す。同じ名前がいれば ErrUserExists
	CreateUser(ctx context.Context, u User) error
	// Users は利用者の一覧(作った順)
	Users(ctx context.Context) ([]User, error)
	// UserByName・UserByID は利用者1人。いなければ ErrUserNotFound
	UserByName(ctx context.Context, name string) (User, error)
	UserByID(ctx context.Context, id uuid.UUID) (User, error)
	// SetPassword はパスワードのハッシュを書き換える。いなければ ErrUserNotFound
	SetPassword(ctx context.Context, id uuid.UUID, hash string, at time.Time) error
	// DeleteUser は利用者と、その人の会話・プロファイル・設定・セッションを消す。いなければ ErrUserNotFound
	DeleteUser(ctx context.Context, id uuid.UUID) error

	CreateSession(ctx context.Context, s Session) error
	// Session はセッション1つ。無ければ ErrNoSession(期限は見ない。見るのは Sessions)
	Session(ctx context.Context, id string) (Session, error)
	ExtendSession(ctx context.Context, id string, expires time.Time) error
	DeleteSession(ctx context.Context, id string) error
	// DeleteUserSessions はその人のセッションを全部消す(パスワードを作り直したとき)
	DeleteUserSessions(ctx context.Context, user uuid.UUID) error
	// DeleteExpiredSessions は期限の切れたセッションを消し、消した数を返す
	DeleteExpiredSessions(ctx context.Context, now time.Time) (int, error)
}

var (
	ErrUserNotFound = errors.New("その名前の利用者はいません")
	ErrUserExists   = errors.New("その名前の利用者はもういます")
	ErrNoSession    = errors.New("ログインしていません(ログインの期限が切れたか、ログアウトしました)")
	// ErrLogin はログインできなかったときのエラー。名前が違うのかパスワードが違うのかは言わない(いる名前を探られないように)
	ErrLogin = errors.New("名前かパスワードが違います")
)
