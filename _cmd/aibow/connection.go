package main

// connection.go: 画面から aibow-server につなぐ・ログインする・アプリモードに戻す(0019)。
//
//	ServerConnection … 今のモード(アプリモード / サーバーモードなら aibow-server に誰としてつないでいるか)と、つなぎ先をどこで決めたか
//	CheckServer      … つなぎ先を確かめる(つながるか・ログインが要るか・証明書のフィンガープリント)。信頼できたら、信頼したつなぎ先として connection.json に覚える
//	ConnectServer    … つなぐ先を connection.json に書く(トークンはもらわない。ログインは起動し直したあとのログインの画面で。0047)
//	DisconnectServer … トークンを aibow-server で消し、つなぐ先を connection.json から消す(アプリモードに戻す。信頼したつなぎ先は残す)
//
// 切り替えると生成の口(engine か api/client)も会話の保存先(aibow.db か server.db)も丸ごと替わるので、起動したまま替えず、
// 保存して、起動し直すと切り替わる(起動の流れ main.go は今のまま使える)。アプリは自分で起動し直さない(2026-10-26、ユーザーと決め直した。決定事項 1)。
// 起動し直すまでは前のモードのまま動くので、画面は「起動し直すと切り替わります」を出し続ける(ServerConnection の Pending)。
// 手で書いた値(settings.json・環境変数)があれば、そちらが強いので画面からは変えない(決定事項 5)。
// デスクトップのアプリだけのメソッドなので ui ではなくここに置く(ブラウザの画面用の口からは呼べない)。

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	apiclient "github.com/secondarykey/aibow/api/client"
	"github.com/secondarykey/aibow/config"
	"github.com/secondarykey/aibow/log"
)

// connection は、つなぎ先を決めるのに要るもの(起動のときの設定から)。ChatService が持つ。
type connection struct {
	path         string                // connection.json
	settingsPath string                // settings.json(手で書いたときに、どこに書いてあるかを画面に出す)
	source       string                // つなぎ先をどこで決めたか(serverSettings など。つながないなら空)
	url          string                // 今動いている形のつなぎ先
	file         config.ConnectionFile // 起動のときに読んだ connection.json(今動いている形)

	// 保存したもの(起動し直すと効く)。起動し直す前に、もう一度つなぎ直す・戻すこともあるので、今動いている形とは分けて持つ
	mu           sync.Mutex
	saved        config.ConnectionFile // 今の connection.json の中身(消したら空。信頼したつなぎ先 Known だけのこともある)
	key          string                // saved のトークン(ログアウトのときに aibow-server で消すため)
	pending      string                // 保存したが、まだ効いていない形("server"・"direct"。無ければ空)
	pendingLogin bool                  // 保存したつなぐ先がログインの要る aibow-server か(お知らせの文を分けるため)

	// client は、今動いている aibow-server のクライアント(つないでいなければ nil)。生成も保存もこれを使うので、
	// キーを替えれば起動し直さずにログインし直せる(account.go。0031 の決定事項 44)
	client *apiclient.Client
}

func newConnection(cfg appConfig) *connection {
	c := &connection{path: cfg.connPath, settingsPath: cfg.settingsPath, source: cfg.serverSource, url: cfg.serverURL, file: cfg.conn, saved: cfg.conn}
	if cfg.serverSource == serverScreen {
		c.key = cfg.serverKey
	}
	return c
}

// ServerConnection は、設定の画面の「サーバー」のタブに出す今のモード。
type ServerConnection struct {
	// Mode は "direct"(アプリモード)か "server"(サーバーモード。aibow-server につなぐ)
	Mode string `json:"mode"`
	// Source はつなぎ先をどこで決めたか("settings"・"env"・"screen"。アプリモードなら空)。"settings" と "env" は画面では変えられない
	Source string `json:"source"`
	// SourcePath は、手で書いたときに、どこに書いてあるか(settings.json のパスか環境変数の名前)
	SourcePath string `json:"sourcePath"`
	URL        string `json:"url"`
	// Fingerprint は信頼した証明書のフィンガープリント(画面でつないだ自己署名の証明書のとき)
	Fingerprint string `json:"fingerprint"`
	// User はログインした名前、ExpiresAt はトークンの期限(画面でつないで、ログインしたとき)
	User      string    `json:"user"`
	ExpiresAt time.Time `json:"expiresAt,omitzero"`
	// Expired はトークンの期限が切れているか
	Expired bool `json:"expired"`
	// Plain は、トークンを包まずに保存しているか(この OS では包めない。0019 の決定事項 4)
	Plain bool `json:"plain"`
	// Pending は、保存したがまだ効いていない形("server"・"direct"。起動し直すと切り替わる)。PendingURL はそのつなぎ先
	Pending    string `json:"pending"`
	PendingURL string `json:"pendingUrl"`
	// PendingLogin は、保存したつなぐ先がログインの要る aibow-server か(起動し直したら、ログインの画面でログインする。0047)
	PendingLogin bool `json:"pendingLogin"`
	// KnownURL・KnownUser は、信頼した最後のつなぎ先と、そこに最後にログインした名前(タブの入力欄に入れておく。つないでいなくても残る)
	KnownURL  string `json:"knownUrl"`
	KnownUser string `json:"knownUser"`
}

// ServerConnection は今のモードを返す。
func (s *ChatService) ServerConnection() ServerConnection {
	c := s.conn
	v := ServerConnection{Mode: "direct", Source: c.source, URL: c.url}
	if c.url != "" {
		v.Mode = "server"
	}
	c.mu.Lock()
	v.Pending, v.PendingURL, v.PendingLogin = c.pending, c.saved.ServerURL, c.pendingLogin
	known := c.saved.Trusted()
	v.KnownURL, v.KnownUser = known.URL, known.User
	c.mu.Unlock()
	switch c.source {
	case serverSettings:
		v.SourcePath = absPath(c.settingsPath)
	case serverEnv:
		v.SourcePath = "AIBOW_SERVER_URL"
	case serverScreen:
		f := c.file
		v.Fingerprint, v.User, v.ExpiresAt = f.Fingerprint, f.User, f.ExpiresAt
		v.Expired = !f.ExpiresAt.IsZero() && !time.Now().Before(f.ExpiresAt)
		v.Plain = f.Token != "" && !f.TokenProtected()
	}
	return v
}

// CheckServer は、つなぎ先 url を確かめる(つながるか・ログインが要るか・証明書)。fingerprint は、人が画面で確かめて信頼したフィンガープリント(まだなら空)。
// 自己署名の証明書で、まだ信頼していなければ、フィンガープリントだけを返す(trusted が false。画面がフィンガープリントを見せ、人が確かめる)。
// 信頼できたら、信頼したつなぎ先として覚える(ログインせずに閉じても、次は信頼し直さずに続けられるように。次の起動でつなぐ先にはしない)。
func (s *ChatService) CheckServer(url, fingerprint string) (apiclient.Probe, error) {
	c := s.conn
	if err := c.editable(); err != nil {
		return apiclient.Probe{}, err
	}
	c.mu.Lock()
	known := c.saved.Trusted()
	c.mu.Unlock()
	if fingerprint == "" && sameServer(url, known.URL) {
		fingerprint = known.Fingerprint // 前に信頼したフィンガープリント(つなぎ直すとき・信頼だけして閉じたとき・ログアウトしたあと)
	}
	p, err := apiclient.ProbeServer(context.Background(), url, fingerprint)
	if err == nil && p.Trusted {
		c.remember(p)
	}
	return p, err
}

// remember は、信頼できたつなぎ先を connection.json の Known に覚える。書けなくても確かめた結果は返す(覚えないだけ)。
func (c *connection) remember(p apiclient.Probe) {
	c.mu.Lock()
	defer c.mu.Unlock()
	old := c.saved.Trusted()
	k := config.KnownServer{URL: p.URL, Fingerprint: pinnedFingerprint(p)}
	if sameServer(old.URL, k.URL) {
		k.User = old.User // 同じサーバーなら、最後にログインした名前を残す
	}
	if c.saved.Known != nil && *c.saved.Known == k {
		return
	}
	f := c.saved
	f.Known = &k
	if err := f.Save(c.path); err != nil {
		log.Warnf("信頼したつなぎ先を覚えられませんでした(つなぐときは、もう一度確かめます): %v", err)
		return
	}
	c.saved = f
	log.Noticef("信頼したつなぎ先を覚えました: %s(%s)", k.URL, absPath(c.path))
}

// pinnedFingerprint は、覚える証明書のフィンガープリント。自己署名の証明書のときだけ(OS が信頼している証明書・HTTP なら空)。
func pinnedFingerprint(p apiclient.Probe) string {
	if p.HTTPS && !p.SystemTrusted {
		return p.Fingerprint
	}
	return ""
}

// ConnectRequest は ConnectServer に渡すもの。
// 名前とパスワードは持たない。ログインは、起動し直したあとのログインの画面で行う(Login。0047 の決定事項 5)
type ConnectRequest struct {
	URL string `json:"url"`
	// Fingerprint は、人が確かめて信頼した証明書のフィンガープリント(自己署名の証明書のとき。CheckServer が返したもの)
	Fingerprint string `json:"fingerprint"`
}

// ConnectResult は ConnectServer の結果。
type ConnectResult struct {
	// Same は、今つないでいるのと同じ aibow-server だったので、何も変えなかったか(0047 の決定事項 7)
	Same bool `json:"same"`
	// URL は確かめたつなぎ先(後ろの / などをそろえたもの)
	URL string `json:"url"`
}

// ConnectServer は aibow-server につなぐように決める(画面の「接続」):もう一度確かめ、つなぐ先と証明書のフィンガープリントを connection.json に書く。
// トークンはもらわない。起動し直すとサーバーモードになり、ログインが要る aibow-server なら、ログインの画面でログインする(0047 の決定事項 1・4)。
// 確かめられないつなぎ先は書かない(起動し直してから「つながりません」となるのを避ける。0019 の決定事項 8)。
//
// 「ログインする」と「つなぐ」を分けたのは、ログインの入口をブラウザと同じログインの画面の1つにそろえるため。
// つなぐ(どこのサーバーか)はその PC の人が決めることなので PC のファイルに書き、ログイン(誰か)はサーバーとのやり取りなのでログインの画面で行う。
func (s *ChatService) ConnectServer(req ConnectRequest) (ConnectResult, error) {
	c := s.conn
	if err := c.editable(); err != nil {
		return ConnectResult{}, err
	}
	p, err := apiclient.ProbeServer(context.Background(), req.URL, req.Fingerprint)
	if err != nil {
		return ConnectResult{}, err
	}
	if !p.Trusted {
		return ConnectResult{}, fmt.Errorf("aibow-server の証明書のフィンガープリント(%s)を確かめてから、つないでください", p.Fingerprint)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// 今つないで動いているのと同じ aibow-server で、つなぎ先も変えていなければ、何もしない(ログインし直したいときは、ログアウトしてログインの画面で)
	if c.url != "" && c.pending == "" && sameServer(c.url, p.URL) {
		return ConnectResult{Same: true, URL: p.URL}, nil
	}
	f := config.ConnectionFile{ServerURL: p.URL, Fingerprint: pinnedFingerprint(p)}
	known := config.KnownServer{URL: f.ServerURL, Fingerprint: f.Fingerprint}
	if old := c.saved.Trusted(); sameServer(old.URL, known.URL) {
		known.User = old.User // 同じサーバーなら、最後にログインした名前を残す(ログインの画面の名前の欄に入れる)
	}
	f.Known = &known
	// ほかの aibow-server につなぎ替えるときは、今のトークンを aibow-server で消しておく(ログインの画面からなら、トークンは無いので何も送らない)
	if !sameServer(c.saved.ServerURL, f.ServerURL) {
		c.revoke()
	}
	if err := f.Save(c.path); err != nil {
		return ConnectResult{}, err
	}
	c.saved, c.key, c.pendingLogin = f, "", p.Login
	c.pending = "server"
	if c.url != "" && sameServer(c.url, f.ServerURL) {
		c.pending = "" // 今動いているのと同じつなぎ先に戻した(戻すのを取りやめた)。起動し直さなくても、ログインの画面でログインできる
	}
	how := "ログインなし"
	if p.Login {
		how = "ログインが要る。起動し直したら、ログインの画面でログインする"
	}
	log.Noticef("aibow-server につなぐように決めました: %s(%s。%s)。起動し直すと切り替わります", f.ServerURL, how, absPath(c.path))
	return ConnectResult{URL: p.URL}, nil
}

// login は名前とパスワードでアプリ用のトークンをもらい、f に入れる(包むのは SetToken)。もらったトークンで本当につながるかも確かめる。
// もらったトークンも返す(ログアウトするとき、aibow-server で消すため)。ログインの画面の Login(account.go)だけが使う(0047)。
func login(f *config.ConnectionFile, name, password string) (string, error) {
	if strings.TrimSpace(name) == "" || password == "" {
		return "", errors.New("この aibow-server はログインが要ります。名前とパスワードを入れてください")
	}
	cl := apiclient.New(f.ServerURL)
	cl.PinCert(f.Fingerprint)
	got, err := cl.AppLogin(strings.TrimSpace(name), password, deviceName())
	if err != nil {
		return "", err
	}
	cl.SetKey(got.Token)
	if _, _, err := cl.Me(); err != nil {
		return "", fmt.Errorf("ログインのあと、aibow-server につながりませんでした。もう一度ログインしてください(もらったトークンが通りませんでした: %w)", err)
	}
	f.User, f.ExpiresAt = got.Name, got.ExpiresAt
	return got.Token, f.SetToken(got.Token)
}

// DisconnectServer はアプリモードに戻すように決める:トークンを aibow-server で消し(つながらなければ手元だけ)、
// つなぐ先とトークンを connection.json から消す。信頼したつなぎ先(Known)は残す(次につなぐときに、信頼し直さずにログインから続けられるように)。
// 起動し直すと切り替わる。
func (s *ChatService) DisconnectServer() error {
	c := s.conn
	if err := c.editable(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.revoke()
	var f config.ConnectionFile
	if k := c.saved.Trusted(); k.URL != "" {
		f.Known = &k
		if err := f.Save(c.path); err != nil {
			return err
		}
	} else if err := config.RemoveConnectionFile(c.path); err != nil {
		return err
	}
	c.saved, c.key, c.pending, c.pendingLogin = f, "", "direct", false
	if c.url == "" {
		c.pending = "" // 今もアプリの中で動いている(起動し直す前につないだのを取りやめた)ので、起動し直さなくてよい
	}
	log.Noticef("アプリモードに戻すように決めました(%s からつなぐ先とトークンを消し、信頼したつなぎ先は残しました)。起動し直すと切り替わります", absPath(c.path))
	return nil
}

// editable は、画面でつなぎ先を変えてよいか(手で書いてあれば変えない)。
func (c *connection) editable() error {
	switch c.source {
	case serverSettings:
		return fmt.Errorf("つなぎ先は settings.json(%s)の serverUrl に手で書いてあるので、画面では変えられません。画面で決めるときは、serverUrl を消して起動し直してください", absPath(c.settingsPath))
	case serverEnv:
		return errors.New("つなぎ先は環境変数 AIBOW_SERVER_URL で決めてあるので、画面では変えられません。画面で決めるときは、AIBOW_SERVER_URL を外して起動し直してください")
	}
	return nil
}

// revoke は、保存してあるトークンを aibow-server で消す(ログアウト)。つながらなくても止めない(手元の connection.json は呼ぶ側が消す・書き換える)。
// mu を持って呼ぶ。
func (c *connection) revoke() {
	if c.key == "" {
		return
	}
	cl := apiclient.New(c.saved.ServerURL)
	cl.PinCert(c.saved.Fingerprint)
	cl.SetKey(c.key)
	if err := cl.RevokeToken(); err != nil {
		log.Warnf("aibow-server でアプリ用のトークンを消せませんでした(手元からは消します。期限の 10 日で使えなくなります): %v", err)
		return
	}
	log.Noticef("aibow-server からログアウトしました: %s(%s)", c.saved.ServerURL, c.saved.User)
}

// deviceName は、aibow-server に送るこの PC の名前(トークンは PC ごとに1本。決定事項 9)。
func deviceName() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "aibow"
}

// sameServer は、2つのつなぎ先が同じか(後ろの / と /v1 の違いは同じとみなす)。
func sameServer(a, b string) bool {
	return a != "" && apiclient.New(a).URL() == apiclient.New(b).URL()
}

// sourceLabel は、つなぎ先を決めた場所(ログに出す)。
func sourceLabel(cfg appConfig) string {
	switch cfg.serverSource {
	case serverSettings:
		return "settings.json " + absPath(cfg.settingsPath)
	case serverEnv:
		return "環境変数 AIBOW_SERVER_URL"
	case serverScreen:
		return "画面 " + absPath(cfg.connPath)
	}
	return "なし"
}
