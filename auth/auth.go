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
	Password string `json:"-"`
	// Admin は管理者か(0018 の 11-0)。管理者は、127.0.0.1 の外からもサーバーの PC の事情(モデル・層の数・置き場所)を変えられ、利用者を管理できる
	Admin     bool      `json:"admin"`
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

// AppToken はアプリ用のトークン1つ(0019。tokens.go)。
type AppToken struct {
	// ID はトークンの SHA-256。トークンそのものは保存しない(セッションと同じ)
	ID     string
	UserID uuid.UUID
	// Device はログインした PC の名前。利用者と PC の名前の組で1本(同じ PC でログインし直すと置き換わる)
	Device    string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// PasswordLink は、パスワードを決めるリンク1つ(0022。links.go)。
type PasswordLink struct {
	// ID は合言葉の SHA-256。合言葉そのものは保存しない
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
	// SetAdmin は管理者の印を付ける / 外す。いなければ ErrUserNotFound
	SetAdmin(ctx context.Context, id uuid.UUID, admin bool, at time.Time) error

	CreateSession(ctx context.Context, s Session) error
	// Session はセッション1つ。無ければ ErrNoSession(期限は見ない。見るのは Sessions)
	Session(ctx context.Context, id string) (Session, error)
	ExtendSession(ctx context.Context, id string, expires time.Time) error
	DeleteSession(ctx context.Context, id string) error
	// DeleteUserSessions はその人のセッションとアプリ用のトークンを全部消す(パスワードを作り直したとき)
	DeleteUserSessions(ctx context.Context, user uuid.UUID) error
	// DeleteExpiredSessions は期限の切れたセッションを消し、消した数を返す
	DeleteExpiredSessions(ctx context.Context, now time.Time) (int, error)

	// SetAPIKey は、利用者の API キー(KeyID の形)を置き換える(1人1本。前のキーは使えなくなる)。いなければ ErrUserNotFound
	SetAPIKey(ctx context.Context, user uuid.UUID, id string, at time.Time) error
	// UserByAPIKey は API キー(KeyID の形)の持ち主。無ければ ErrUserNotFound
	UserByAPIKey(ctx context.Context, id string) (User, error)
	// HasAPIKey は、利用者が API キーを持っているか(一覧に出すため)
	HasAPIKey(ctx context.Context, user uuid.UUID) (bool, error)

	// SetAppToken はアプリ用のトークンを足す。同じ利用者・同じ PC の名前のトークンがあれば置き換える(PC ごとに1本。0019 の決定事項 9)
	SetAppToken(ctx context.Context, t AppToken) error
	// AppToken はトークン1つ(ID は SHA-256)。無ければ ErrBadKey(期限は見ない。見るのは Keys)
	AppToken(ctx context.Context, id string) (AppToken, error)
	// DeleteAppToken はトークンを消す(無くてもエラーにしない)
	DeleteAppToken(ctx context.Context, id string) error

	// SetPasswordLink はパスワードを決めるリンクを足す。その人のリンクがあれば置き換える(1人1本。0022)
	SetPasswordLink(ctx context.Context, l PasswordLink) error
	// PasswordLink はリンク1つ(ID は SHA-256)。無ければ ErrBadLink(期限は見ない)
	PasswordLink(ctx context.Context, id string) (PasswordLink, error)
	// DeletePasswordLink はリンクを消す(無くてもエラーにしない)
	DeletePasswordLink(ctx context.Context, id string) error
}

// HasAdmin は、ログインできる管理者(パスワードのある管理者)がいるか(0020)。招待のリンクをまだ使っていない管理者は、まだいないものとする(0022 の決定事項 14)。
func HasAdmin(ctx context.Context, st Store) (bool, error) {
	users, err := st.Users(ctx)
	if err != nil {
		return false, err
	}
	for _, u := range users {
		if u.Admin && u.Password != "" {
			return true, nil
		}
	}
	return false, nil
}

var (
	ErrUserNotFound = errors.New("その名前の利用者はいません")
	ErrUserExists   = errors.New("その名前の利用者はもういます")
	ErrNoSession    = errors.New("ログインしていません(ログインの期限が切れたか、ログアウトしました)")
	// ErrLogin はログインできなかったときのエラー。名前が違うのかパスワードが違うのかは言わない(いる名前を探られないように)
	ErrLogin = errors.New("名前かパスワードが違います")
)
