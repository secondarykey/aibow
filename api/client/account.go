package client

// account.go: 画面のアプリから、自分のアカウント(表示名・メールアドレス・パスワード)を変える(0031 の決定事項 45)。
// ブラウザと同じ口(/aibow/auth/)を、アプリ用のトークン(Bearer)で呼ぶ(aibow-server の auth.Keys.WithBearer が持ち主を決める)。

import (
	"net/http"

	"github.com/secondarykey/aibow/auth"
)

// Account は、キーの持ち主(GET /aibow/auth/key。名前・表示名・メールアドレス・管理者か)。
// キーの要らない aibow-server なら Login が false(誰でもない)。
func (c *Client) Account() (auth.Me, error) {
	var me auth.Me
	err := c.call(http.MethodGet, "/aibow/auth/key", nil, &me, shortTimeout)
	return me, err
}

// SetDisplayName は、キーの持ち主の表示名を変える(空なら無し)。
func (c *Client) SetDisplayName(name string) (auth.Me, error) {
	var me auth.Me
	err := c.call(http.MethodPost, "/aibow/auth/display", map[string]string{"display_name": name}, &me, shortTimeout)
	return me, err
}

// SetEmail は、キーの持ち主のメールアドレスを変える(空なら無し)。
func (c *Client) SetEmail(email string) (auth.Me, error) {
	var me auth.Me
	err := c.call(http.MethodPost, "/aibow/auth/email", map[string]string{"email": email}, &me, shortTimeout)
	return me, err
}

// ChangePassword は、キーの持ち主のパスワードを変える。アプリ用のトークンで呼んだときは、新しいトークン(Token)が返る
// (前のトークンは使えなくなるので、呼んだ側が SetKey で替えて保存する)。API キーで呼んだときの Token は空。
func (c *Client) ChangePassword(current, password string) (auth.AppLogin, error) {
	var out auth.AppLogin
	err := c.call(http.MethodPost, "/aibow/auth/password", map[string]string{"current": current, "password": password}, &out, shortTimeout)
	return out, err
}
