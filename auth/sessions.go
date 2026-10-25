package auth

// sessions.go: ログインしている間の印(セッション)。
//
// ブラウザは、ログインしたあとのリクエストに毎回パスワードを付けるわけにはいかない。そこで、ログインに成功したら
// サーバーが推測できない合言葉(トークン)を作って cookie で渡し、「この合言葉はこの人」と覚えておく。
// サーバーが状態(誰がログインしているか)を覚えている点が、毎回全部を送る OpenAI 互換の口(ステートレス)と違うところ。
//
// 合言葉はそのまま保存せず、SHA-256 にして保存する(DB をのぞかれても、cookie を作ってその人のふりはできない)。
// 合言葉自体が十分に長いでたらめな値なので、パスワードのように塩を付けて重ねる必要は無い(片っ端から試しても当たらない)。
//
// 期限は「最後に使ってから 30 日」(0017 の 10-1 でユーザーと決めた)。使うたびに延ばすが、毎回 DB に書かないよう、
// 前に延ばしてから1日以上たったときだけ延ばす。セッションは server.db にあるので、aibow-server を起動し直しても切れない。

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

const (
	// SessionTTL は、最後に使ってからセッションが切れるまで
	SessionTTL = 30 * 24 * time.Hour
	// extendEvery は、期限を延ばす間隔(これより短い間に何回使っても、DB には書かない)
	extendEvery = 24 * time.Hour
)

// Sessions はログインとセッションを扱う係。
type Sessions struct {
	store Store
	now   func() time.Time // テストで時刻を進めるため
}

func NewSessions(st Store) *Sessions {
	return &Sessions{store: st, now: time.Now}
}

// Login は名前とパスワードを確かめ、セッションを作って合言葉(cookie に入れる値)を返す。
// 違っていれば ErrLogin(名前とパスワードのどちらが違うかは言わない)。
func (s *Sessions) Login(ctx context.Context, name, password string) (token string, u User, err error) {
	if u, err = s.check(ctx, name, password); err != nil {
		return "", User{}, err
	}
	now := s.now()
	// ついでに、期限の切れたセッションを片付ける(ログインのときだけなので、手間は小さい)
	if _, err := s.store.DeleteExpiredSessions(ctx, now); err != nil {
		return "", User{}, err
	}
	if token, err = s.newSession(ctx, u); err != nil {
		return "", User{}, err
	}
	return token, u, nil
}

// check は名前とパスワードを確かめる(ブラウザのログインとアプリのログインで同じ)。違っていれば ErrLogin。
func (s *Sessions) check(ctx context.Context, name, password string) (User, error) {
	u, err := s.store.UserByName(ctx, strings.TrimSpace(name))
	if errors.Is(err, ErrUserNotFound) {
		CheckPassword(dummyHash, password) // いる名前と同じだけ時間をかける
		return User{}, ErrLogin
	}
	if err != nil {
		return User{}, err
	}
	if !CheckPassword(u.Password, password) {
		return User{}, ErrLogin
	}
	return u, nil
}

// Logout はセッションを消す(無くてもエラーにしない)。
func (s *Sessions) Logout(ctx context.Context, token string) error {
	return s.store.DeleteSession(ctx, tokenID(token))
}

// User は合言葉の持ち主。期限が切れていれば ErrNoSession。
// 前に延ばしてから extendEvery 以上たっていれば期限を延ばし、extended=true(cookie の期限も延ばすため)。
func (s *Sessions) User(ctx context.Context, token string) (u User, extended bool, err error) {
	if token == "" {
		return User{}, false, ErrNoSession
	}
	id := tokenID(token)
	ss, err := s.store.Session(ctx, id)
	if err != nil {
		return User{}, false, err
	}
	now := s.now()
	if !now.Before(ss.ExpiresAt) {
		s.store.DeleteSession(ctx, id)
		return User{}, false, ErrNoSession
	}
	u, err = s.store.UserByID(ctx, ss.UserID)
	if errors.Is(err, ErrUserNotFound) {
		return User{}, false, ErrNoSession // 消された利用者(セッションも一緒に消えるので、ふつうは来ない)
	}
	if err != nil {
		return User{}, false, err
	}
	// 期限を延ばすのは、前に延ばしてから1日以上たったとき(期限 = 最後に延ばした時刻 + 30 日 なので、残りで見る)
	if ss.ExpiresAt.Sub(now) <= SessionTTL-extendEvery {
		if err := s.store.ExtendSession(ctx, id, now.Add(SessionTTL)); err != nil {
			return User{}, false, err
		}
		extended = true
	}
	return u, extended, nil
}

// newToken はセッションの合言葉(32 バイトのでたらめな値。256 ビット)を作る。cookie に入れやすいよう URL で使える base64 にする。
func newToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// tokenID は、合言葉を保存する形(SHA-256 の16進)にする。
func tokenID(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
