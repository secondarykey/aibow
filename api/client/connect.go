package client

// connect.go: 画面から aibow-server につなぐときの下調べ・ログイン・ログアウト(0019)。
//
//	Probe       … つなぎ先を確かめる:つながるか・ログインが要るか・HTTPS なら証明書(OS が信頼しているか、自己署名なら指紋)
//	PinCert     … 人が指紋で確かめた証明書を信頼する(TOFU。覚えた指紋と違う証明書を出すサーバーにはつながない)
//	AppLogin    … 名前とパスワードでアプリ用のトークンをもらう(POST /aibow/auth/token)
//	RevokeToken … アプリ用のトークンを消す(POST /aibow/auth/token/revoke)
//
// 自己署名の証明書は、どの認証局も保証していないので、ふつうの確かめ方(OS が信頼する認証局までたどる)では断られる。
// そこで、初めてつなぐときは証明書の中身を確かめずに受け取って指紋だけを見せ(この時点では何も送らない)、
// 人が aibow-server の出す指紋と同じだと確かめたら、その指紋の証明書だけを信頼する。

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/secondarykey/aibow/auth"
)

// Probe は、つなぎ先を確かめた結果。
type Probe struct {
	URL   string `json:"url"`   // つなぎ先(後ろの / と /v1 を外したもの)
	HTTPS bool   `json:"https"` // HTTPS か
	// Fingerprint は、サーバーが出した証明書の SHA-256 の指紋(HTTPS のとき)
	Fingerprint string `json:"fingerprint,omitempty"`
	// SystemTrusted は、OS が信頼している認証局の証明書か(そうなら指紋を確かめなくてよい)
	SystemTrusted bool `json:"systemTrusted,omitempty"`
	// Trusted は、つないでよい証明書か(HTTP・OS が信頼している・指紋が覚えたものと同じ)。false なら、ほかの欄は調べていない
	Trusted bool `json:"trusted"`
	// Login は、ログインが要る aibow-server か(Trusted のときだけ調べる)
	Login bool `json:"login"`
}

// ErrCertChanged は、覚えた指紋と違う証明書をサーバーが出したときのエラー(errors.Is で見分ける)。
var ErrCertChanged = errors.New("aibow-server の証明書が、信頼したときのものと違います")

// certChangedError は、覚えた指紋と、サーバーが出した指紋を持つエラー。
type certChangedError struct {
	want, got string
}

func (e *certChangedError) Error() string {
	return fmt.Sprintf("%v(覚えた指紋: %s・サーバーが出した指紋: %s)。aibow-server で証明書を作り直したなら、設定の画面の「サーバー」でつなぎ直して指紋を確かめてください。"+
		"作り直していないなら、ほかのサーバーがなりすましているかもしれないので、つながないでください", ErrCertChanged, e.want, e.got)
}

func (e *certChangedError) Is(target error) bool { return target == ErrCertChanged }

// HasRemedy は、対処まで書いてあること。
func (e *certChangedError) HasRemedy() bool { return true }

// PinCert は、指紋が fingerprint の証明書を信頼する(自己署名の証明書。0019 の決定事項 6)。OS が信頼している証明書も、そのまま信頼する。
// 空なら何もしない(OS が信頼している証明書だけ)。使い始める前に呼ぶ。
func (c *Client) PinCert(fingerprint string) {
	if fingerprint == "" {
		return
	}
	c.hc = &http.Client{Transport: &http.Transport{TLSClientConfig: pinnedTLS(fingerprint)}}
}

// pinnedTLS は、指紋が fingerprint の証明書か、OS が信頼している証明書だけを受け付ける TLS の設定。
// InsecureSkipVerify はふつうの確かめ方を止めるだけで、代わりに VerifyConnection で確かめる(止めっぱなしにはしない)。
func pinnedTLS(fingerprint string) *tls.Config {
	return &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true,
		VerifyConnection: func(cs tls.ConnectionState) error {
			leaf := cs.PeerCertificates[0]
			got := auth.Fingerprint(leaf.Raw)
			if got == fingerprint || systemTrusted(cs.PeerCertificates, cs.ServerName) {
				return nil
			}
			return &certChangedError{want: fingerprint, got: got}
		},
	}
}

// systemTrusted は、証明書が OS の信頼する認証局までたどれて、名前(host)が合っているか。
func systemTrusted(certs []*x509.Certificate, host string) bool {
	inter := x509.NewCertPool()
	for _, c := range certs[1:] {
		inter.AddCert(c)
	}
	_, err := certs[0].Verify(x509.VerifyOptions{DNSName: host, Intermediates: inter})
	return err == nil
}

// ProbeServer は、つなぎ先 rawURL を確かめる。pinned は覚えている指紋(無ければ空)。
// 自己署名で指紋がまだ確かめられていなければ、証明書の指紋だけを返す(Trusted が false。HTTP では何も送らない)。
func ProbeServer(ctx context.Context, rawURL, pinned string) (Probe, error) {
	c := New(rawURL)
	p := Probe{URL: c.base}
	u, err := url.Parse(c.base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return p, fmt.Errorf("aibow-server のつなぎ先は http://127.0.0.1:8089 のように書いてください: %q", rawURL)
	}
	if u.Scheme == "https" {
		p.HTTPS = true
		certs, err := peerCerts(ctx, u)
		if err != nil {
			return p, c.unreachable(err)
		}
		p.Fingerprint = auth.Fingerprint(certs[0].Raw)
		p.SystemTrusted = systemTrusted(certs, u.Hostname())
		if !p.SystemTrusted && p.Fingerprint != pinned {
			return p, nil // 人が指紋を確かめるまで、何も送らない
		}
		c.PinCert(p.Fingerprint)
	}
	p.Trusted = true
	var me struct {
		Login *bool `json:"login"`
	}
	if err := c.call(http.MethodGet, "/aibow/auth/me", nil, &me, shortTimeout); err != nil {
		var ae *APIError
		if errors.As(err, &ae) && ae.Status == http.StatusNotFound {
			return p, fmt.Errorf("%s は aibow-server ではないか、ログインの口の無い古い aibow-server です", c.base)
		}
		return p, err
	}
	if me.Login == nil {
		return p, fmt.Errorf("%s の返事が aibow-server のものではありません", c.base)
	}
	p.Login = *me.Login
	return p, nil
}

// peerCerts は、u のサーバーと TLS でつなぎ、出してきた証明書を受け取る(中身はまだ信頼しない。指紋を見せるため)。
func peerCerts(ctx context.Context, u *url.URL) ([]*x509.Certificate, error) {
	host := u.Host
	if u.Port() == "" {
		host = net.JoinHostPort(u.Hostname(), "443")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	d := tls.Dialer{Config: &tls.Config{InsecureSkipVerify: true, ServerName: u.Hostname(), MinVersion: tls.VersionTLS12}}
	conn, err := d.DialContext(ctx, "tcp", host)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	certs := conn.(*tls.Conn).ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return nil, errors.New("サーバーが証明書を出しませんでした")
	}
	return certs, nil
}

// AppLogin は、名前とパスワードでアプリ用のトークンをもらう(device はこの PC の名前)。違えば 401 の APIError。
func (c *Client) AppLogin(name, password, device string) (auth.AppLogin, error) {
	var out auth.AppLogin
	body := map[string]string{"name": name, "password": password, "device": device}
	if err := c.call(http.MethodPost, "/aibow/auth/token", body, &out, shortTimeout); err != nil {
		return auth.AppLogin{}, err
	}
	if out.Token == "" {
		return auth.AppLogin{}, errors.New("aibow-server がトークンを返しませんでした")
	}
	return out, nil
}

// RevokeToken は、SetKey で決めたアプリ用のトークンを aibow-server で消す(ログアウト)。
func (c *Client) RevokeToken() error {
	if !auth.IsAppToken(c.key) {
		return nil // 手で書いた API キーは消さない(Cline などでも使っているかもしれないので)
	}
	return c.call(http.MethodPost, "/aibow/auth/token/revoke", nil, nil, shortTimeout)
}
