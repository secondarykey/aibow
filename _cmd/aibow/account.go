package main

// account.go: aibow-server につないでいるときの、自分のアカウント(0031 の決定事項 44〜47)。
//
//	Account        … 今のアカウント(名前・表示名・メールアドレス・管理者か)と、ログインし直す必要があるか
//	Login / Logout … ブラウザと同じく、起動したままログインし直す・ログアウトする(つなぎ先は残す)
//	SetMy…         … 表示名・メールアドレス・パスワードを変える(ブラウザと同じ口を、アプリ用のトークンで呼ぶ)
//	MyUsage        … 自分の使用量(0033。同じくアプリ用のトークンで呼ぶ)
//
// 以前は、ログアウトは「サーバー」のタブの「ログアウトして戻す」(アプリの中で動かす形に戻り、起動し直すと切り替わる)だけだった。
// ブラウザとそろえ、ログアウトしたらその場でログインの画面になるようにした(2026-11-01、ユーザーの指示)。
// 生成の口と会話の保存先は同じクライアント(apiclient.Client)を使っているので、そのキーを替えるだけで起動し直さずに切り替わる。
// アプリの中で動かす形に戻すのは、ログアウトとは別の操作(「サーバー」のタブの DisconnectServer)。

import (
	"errors"
	"net/http"
	"time"

	apiclient "github.com/secondarykey/aibow/api/client"
	"github.com/secondarykey/aibow/auth"
	"github.com/secondarykey/aibow/config"
	"github.com/secondarykey/aibow/log"
	"github.com/secondarykey/aibow/usage"
)

// AccountInfo は、画面のアプリの今のアカウント(設定の画面の「アカウント」のタブと、左下の名前)。
type AccountInfo struct {
	// Login は、aibow-server につないでいて、そこがログインの要る形か(要らない形・アプリの中で動かすときは false)
	Login       bool   `json:"login"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Email       string `json:"email"`
	Admin       bool   `json:"admin"`
	// NeedLogin は、画面でつないだのにログインしていない(ログアウトした・トークンの期限が切れた)。画面はログインの画面を出す
	NeedLogin bool `json:"needLogin"`
	// LastUser は、最後にログインした名前(ログインの画面に入れておく)
	LastUser string `json:"lastUser"`
	// CanLogout は、ログアウトできるか(画面でログインしたとき。settings.json に手で書いた API キーではログアウトしない)
	CanLogout bool `json:"canLogout"`
}

// Account は今のアカウント。ログインし直す必要があれば NeedLogin(エラーにしない。画面がログインの画面を出すため)。
// ui.Service の Account(名前だけ)を、この形で置き換える。
func (s *ChatService) Account() (AccountInfo, error) {
	c := s.conn
	if c.client == nil {
		return AccountInfo{}, nil // アプリの中で動かしている
	}
	screen := c.source == serverScreen
	me, err := c.client.Account()
	if err != nil {
		// 画面でつないだときの 401(トークンが無い・期限が切れた・消された)は、ログインし直せばよい
		var ae *apiclient.APIError
		if screen && errors.As(err, &ae) && ae.Status == http.StatusUnauthorized {
			c.mu.Lock()
			last := c.saved.Trusted().User
			c.mu.Unlock()
			return AccountInfo{Login: true, NeedLogin: true, LastUser: last}, nil
		}
		return AccountInfo{}, err
	}
	return AccountInfo{
		Login: me.Login, Name: me.Name, DisplayName: me.DisplayName, Email: me.Email, Admin: me.Admin,
		CanLogout: screen && auth.IsAppToken(c.client.Key()),
	}, nil
}

// Login は、画面でつないだ aibow-server に、名前とパスワードでログインし直す(ログアウトしたあと・期限が切れたあと)。
// アプリ用のトークンをもらって、今動いているクライアントのキーを替え、connection.json に書く(起動し直さない)。
func (s *ChatService) Login(name, password string) (AccountInfo, error) {
	c := s.conn
	if c.source != serverScreen || c.client == nil {
		return AccountInfo{}, errors.New("ログインし直せるのは、設定の画面の「サーバー」で aibow-server につないだときだけです")
	}
	c.mu.Lock()
	if c.pending != "" {
		c.mu.Unlock()
		return AccountInfo{}, errors.New("つなぎ先を変えて、まだ起動し直していません。起動し直してからログインしてください")
	}
	f := config.ConnectionFile{ServerURL: c.url, Fingerprint: c.file.Fingerprint}
	token, err := login(&f, ConnectRequest{URL: c.url, Fingerprint: c.file.Fingerprint, Name: name, Password: password})
	if err != nil {
		c.mu.Unlock()
		return AccountInfo{}, err
	}
	f.Known = &config.KnownServer{URL: f.ServerURL, Fingerprint: f.Fingerprint, User: f.User}
	if err := f.Save(c.path); err != nil {
		c.mu.Unlock()
		return AccountInfo{}, err
	}
	c.client.SetKey(token)
	c.saved, c.file, c.key = f, f, token
	c.mu.Unlock()
	log.Noticef("aibow-server にログインしました: %s(%s)", f.ServerURL, f.User)
	return s.Account()
}

// Logout は、aibow-server からログアウトする:トークンを aibow-server で消し、connection.json からも消す。
// つなぎ先は残すので、そのままログインの画面になり、次に起動したときもログインから始まる(ブラウザと同じ)。
func (s *ChatService) Logout() error {
	c := s.conn
	if c.source != serverScreen || c.client == nil {
		return errors.New("ログアウトできるのは、設定の画面の「サーバー」で aibow-server にログインしたときだけです(settings.json に手で書いた API キーは消しません)")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.revoke()
	f := c.saved
	f.Token, f.Protection, f.ExpiresAt = "", "", time.Time{}
	if err := f.Save(c.path); err != nil {
		return err
	}
	c.client.SetKey("")
	c.saved, c.key = f, ""
	c.file.Token, c.file.Protection, c.file.ExpiresAt = "", "", time.Time{}
	return nil
}

// SetMyDisplayName は、自分の表示名を変える(空なら無し)。
func (s *ChatService) SetMyDisplayName(name string) (AccountInfo, error) {
	if err := s.needClient(); err != nil {
		return AccountInfo{}, err
	}
	if _, err := s.conn.client.SetDisplayName(name); err != nil {
		return AccountInfo{}, err
	}
	return s.Account()
}

// MyUsage は、aibow-server での自分の使用量(今日・7日・30日。設定の画面の「アカウント」のタブ。0033)。
func (s *ChatService) MyUsage() (usage.Mine, error) {
	if s.conn.client == nil {
		return usage.Mine{}, errors.New("使用量は、aibow-server につないでいるときだけ見られます")
	}
	return s.conn.client.MyUsage()
}

// SetMyEmail は、自分のメールアドレスを変える(空なら無し)。
func (s *ChatService) SetMyEmail(email string) (AccountInfo, error) {
	if err := s.needClient(); err != nil {
		return AccountInfo{}, err
	}
	if _, err := s.conn.client.SetEmail(email); err != nil {
		return AccountInfo{}, err
	}
	return s.Account()
}

// ChangeMyPassword は、自分のパスワードを変える。ほかのログイン中の画面とアプリはログインし直しになるが、
// このアプリは aibow-server が返した新しいトークンに替えて、つながったままにする(ブラウザが新しい cookie をもらうのと同じ)。
func (s *ChatService) ChangeMyPassword(current, password string) error {
	if err := s.needClient(); err != nil {
		return err
	}
	c := s.conn
	got, err := c.client.ChangePassword(current, password)
	if err != nil {
		return err
	}
	if got.Token == "" {
		return nil // 手で書いた API キーで呼んだ(キーは変わらない)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.client.SetKey(got.Token)
	c.key = got.Token
	if c.source == serverScreen && c.pending == "" {
		f := c.saved
		f.ExpiresAt = got.ExpiresAt
		if err := f.SetToken(got.Token); err != nil {
			return err
		}
		if err := f.Save(c.path); err != nil {
			return err
		}
		c.saved = f
		c.file.ExpiresAt = got.ExpiresAt
	}
	log.Noticef("aibow-server のパスワードを変えました(%s。このアプリは新しいトークンでつながったままです)", got.Name)
	return nil
}

// needClient は、aibow-server につないでいるか(アカウントはつないでいるときだけ)。
func (s *ChatService) needClient() error {
	if s.conn.client == nil {
		return errors.New("アカウントは、aibow-server につないでいるときだけ変えられます")
	}
	return nil
}
