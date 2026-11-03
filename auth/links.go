package auth

// links.go: パスワードは本人が決める(0022)。
//
// 以前は aibow-server がでたらめなパスワードを作って一度だけ見せ、本人はそれをずっと使っていた。本人が決める手段が無く、
// 管理者が自分のパスワードを作り直すと、表示を読む前にログインの画面に戻って入れなくなった(2026-10-26)。
// そこで、管理者(画面とコマンド)はパスワードを作らず、本人がパスワードを決めるリンクを出す:
//
//	管理者 ── 招待・再設定のリンク(http://…/#reset=<合言葉>。1 時間・一度だけ)──→ 本人 ── 開いてパスワードを決める → そのままログイン
//	本人   ── 今のパスワード + 新しいパスワード ──→ 変える(ChangePassword)
//
// 合言葉は URL の # の後ろに置く(# の後ろはサーバーに送られず、アクセスのログに残らない。画面が読んで本文で送る)。
// 保存するのは SHA-256 だけ(セッション・API キーと同じ)。リンクは持っている人なら誰でもパスワードを決められる鍵なので、期限を短くし、一度で消す。

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
	"uuid"
)

const (
	// LinkTTL はパスワードを決めるリンクの期限(ユーザーと決めた。0022 の決定事項 2)
	LinkTTL = time.Hour
	// MinPassword は本人が決めるパスワードの長さの下限(文字数。決定事項 5)
	MinPassword = 12
)

var (
	// ErrBadLink は、リンクが無い・使い終わった・出し直されたときのエラー
	ErrBadLink = errors.New("このリンクは使えません(使い終わったか、出し直されました)。管理者にもう一度出してもらってください")
	// ErrLinkExpired は、リンクの期限(1 時間)が切れたときのエラー
	ErrLinkExpired = errors.New("このリンクの期限(1 時間)が切れました。管理者にもう一度出してもらってください")
	// ErrWrongPassword は、パスワードを変えるときに今のパスワードが違うとき
	ErrWrongPassword = errors.New("今のパスワードが違います")
)

// ruleError は、本人が決めたパスワードが決まりに合わないときのエラー(画面にそのまま出す。400)。
type ruleError struct{ msg string }

func (e *ruleError) Error() string { return e.msg }

func isPasswordRule(err error) bool {
	var re *ruleError
	return errors.As(err, &re)
}

// CheckNewPassword は、本人が決めるパスワードの決まりを確かめる(12 文字以上・名前と同じでない)。
func CheckNewPassword(name, password string) error {
	if n := utf8.RuneCountInString(password); n < MinPassword {
		return &ruleError{fmt.Sprintf("パスワードは %d 文字以上にしてください(今は %d 文字)", MinPassword, n)}
	}
	if strings.EqualFold(password, name) {
		return &ruleError{"パスワードを名前と同じにはできません"}
	}
	return nil
}

// IssueLink は、その人のパスワードを決めるリンクの合言葉を作る(前のリンクは使えなくなる)。合言葉と期限を返す。
// 今のパスワードを使えなくするか(再設定)は、呼ぶ側が決める。
func (s *Sessions) IssueLink(ctx context.Context, user uuid.UUID) (token string, expires time.Time, err error) {
	now := s.now()
	token = newToken()
	expires = now.Add(LinkTTL)
	if err := s.store.SetPasswordLink(ctx, PasswordLink{ID: tokenID(token), UserID: user, CreatedAt: now, ExpiresAt: expires}); err != nil {
		return "", time.Time{}, err
	}
	return token, expires, nil
}

// LinkUser は、リンクの持ち主(パスワードを決める画面に名前を出すため)。使えなければ ErrBadLink か ErrLinkExpired。
func (s *Sessions) LinkUser(ctx context.Context, token string) (User, error) {
	l, err := s.link(ctx, token)
	if err != nil {
		return User{}, err
	}
	u, err := s.store.UserByID(ctx, l.UserID)
	if errors.Is(err, ErrUserNotFound) {
		return User{}, ErrBadLink
	}
	return u, err
}

// UseLink は、リンクでパスワードを決め、そのままログインする(セッションの合言葉を返す)。
// リンクは消し、その人のほかのセッションとアプリ用のトークンも消す(決定事項 9)。
func (s *Sessions) UseLink(ctx context.Context, token, password string) (session string, u User, err error) {
	if u, err = s.LinkUser(ctx, token); err != nil {
		return "", User{}, err
	}
	if err := CheckNewPassword(u.Name, password); err != nil {
		return "", User{}, err
	}
	if err := s.setPassword(ctx, u, password); err != nil {
		return "", User{}, err
	}
	if err := s.store.DeletePasswordLink(ctx, tokenID(token)); err != nil {
		return "", User{}, err
	}
	session, err = s.newSession(ctx, u)
	return session, u, err
}

// ChangePassword は、ログインしている本人がパスワードを変える。今のパスワードが違えば ErrWrongPassword。
// ほかのセッションとアプリ用のトークンは消し、今の画面には新しいセッションの合言葉を返す(ログインし直さなくてよい。決定事項 10)。
func (s *Sessions) ChangePassword(ctx context.Context, u User, current, password string) (session string, err error) {
	if u, err = s.changePassword(ctx, u, current, password); err != nil {
		return "", err
	}
	return s.newSession(ctx, u)
}

// ChangePasswordByKey は、画面のアプリ(API キーかアプリ用のトークン。key)から本人がパスワードを変える(0031 の決定事項 45)。
// ほかのセッションとアプリ用のトークンは消す(ブラウザから変えたときと同じ)。呼んだのがアプリ用のトークンなら、
// 同じ PC の名前で新しいトークンを出して返す(変えたアプリはつながったまま。ブラウザに新しい cookie を渡すのと同じ考え方)。
// API キーは消えないので、そのときの Token は空。
func (s *Sessions) ChangePasswordByKey(ctx context.Context, u User, current, password, key string) (AppLogin, error) {
	device := ""
	if IsAppToken(key) {
		t, err := s.store.AppToken(ctx, tokenID(key))
		if err != nil {
			return AppLogin{}, err
		}
		device = t.Device
	}
	u, err := s.changePassword(ctx, u, current, password)
	if err != nil {
		return AppLogin{}, err
	}
	if device == "" {
		return AppLogin{Name: u.Name, Admin: u.Admin}, nil
	}
	now := s.now()
	token := appTokenPrefix + newToken()
	t := AppToken{ID: tokenID(token), UserID: u.ID, Device: device, CreatedAt: now, ExpiresAt: now.Add(AppTokenTTL)}
	if err := s.store.SetAppToken(ctx, t); err != nil {
		return AppLogin{}, err
	}
	return AppLogin{Token: token, Name: u.Name, Admin: u.Admin, ExpiresAt: t.ExpiresAt}, nil
}

// changePassword は、今のパスワードを確かめて新しいパスワードにする(ほかのセッションとアプリ用のトークンは消える)。
func (s *Sessions) changePassword(ctx context.Context, u User, current, password string) (User, error) {
	// ログインしてから変わっているかもしれないので、保存先から読み直して確かめる
	u, err := s.store.UserByID(ctx, u.ID)
	if err != nil {
		return u, err
	}
	if !CheckPassword(u.Password, current) {
		return u, ErrWrongPassword
	}
	if err := CheckNewPassword(u.Name, password); err != nil {
		return u, err
	}
	if current == password {
		return u, &ruleError{"新しいパスワードが今のパスワードと同じです"}
	}
	return u, s.setPassword(ctx, u, password)
}

// setPassword はパスワードを書き換え、その人のセッションとアプリ用のトークンを全部消す。
func (s *Sessions) setPassword(ctx context.Context, u User, password string) error {
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	if err := s.store.SetPassword(ctx, u.ID, hash, s.now()); err != nil {
		return err
	}
	return s.store.DeleteUserSessions(ctx, u.ID)
}

// newSession はセッションを作って合言葉を返す(ログインと同じ)。
func (s *Sessions) newSession(ctx context.Context, u User) (string, error) {
	now := s.now()
	markUsed(ctx, s.store, &u, now) // ログインも「使った」に数える(0031 の決定事項 21)
	token := newToken()
	if err := s.store.CreateSession(ctx, Session{ID: tokenID(token), UserID: u.ID, CreatedAt: now, ExpiresAt: now.Add(SessionTTL)}); err != nil {
		return "", err
	}
	return token, nil
}

// link はリンク1つ。期限が切れていれば消して ErrLinkExpired。
func (s *Sessions) link(ctx context.Context, token string) (PasswordLink, error) {
	if token == "" {
		return PasswordLink{}, ErrBadLink
	}
	id := tokenID(token)
	l, err := s.store.PasswordLink(ctx, id)
	if err != nil {
		return PasswordLink{}, err
	}
	if !s.now().Before(l.ExpiresAt) {
		s.store.DeletePasswordLink(ctx, id)
		return PasswordLink{}, ErrLinkExpired
	}
	return l, nil
}
