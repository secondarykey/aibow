package main

// connection.go: 画面から aibow-server につなぐ・ログインする・アプリの中で動かす形に戻す(0019)。
//
//	ServerConnection … 今の形(アプリの中で動かす / aibow-server に誰としてつないでいるか)と、つなぎ先をどこで決めたか
//	CheckServer      … つなぎ先を確かめる(つながるか・ログインが要るか・証明書の指紋)
//	ConnectServer    … ログインしてアプリ用のトークンをもらい、connection.json に書いて、起動し直す
//	DisconnectServer … トークンを aibow-server で消し、connection.json を消して、起動し直す(アプリの中で動かす形に戻す)
//
// 切り替えると生成の口(engine か api/client)も会話の保存先(aibow.db か server.db)も丸ごと替わるので、起動したまま替えず、
// 保存してアプリが自分で起動し直す(決定事項 1)。起動の流れ(main.go)は今のまま使える。
// 手で書いた値(settings.json・環境変数)があれば、そちらが強いので画面からは変えない(決定事項 5)。
// デスクトップのアプリだけのメソッドなので ui ではなくここに置く(ブラウザの画面用の口からは呼べない)。

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"

	"github.com/secondarykey/aibow"
	apiclient "github.com/secondarykey/aibow/api/client"
	"github.com/secondarykey/aibow/log"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// connection は、つなぎ先を決めるのに要るもの(起動のときの設定から)。ChatService が持つ。
type connection struct {
	path         string               // connection.json
	settingsPath string               // settings.json(手で書いたときに、どこに書いてあるかを画面に出す)
	source       string               // つなぎ先をどこで決めたか(serverSettings など。つながないなら空)
	url          string               // 今のつなぎ先
	file         aibow.ConnectionFile // 起動のときに読んだ connection.json
	key          string               // 今のトークン(画面で決めたとき。ログアウトで消すため)

	// restart は、起動し直すよう頼まれたか(main がウィンドウを閉じたあとに見る)
	restart atomic.Bool
}

func newConnection(cfg appConfig) *connection {
	c := &connection{path: cfg.connPath, settingsPath: cfg.settingsPath, source: cfg.serverSource, url: cfg.serverURL, file: cfg.conn}
	if cfg.serverSource == serverScreen {
		c.key = cfg.serverKey
	}
	return c
}

// ServerConnection は、設定の画面の「サーバー」のタブに出す今の形。
type ServerConnection struct {
	// Mode は "direct"(アプリの中で動かす)か "server"(aibow-server につなぐ)
	Mode string `json:"mode"`
	// Source はつなぎ先をどこで決めたか("settings"・"env"・"screen"。アプリの中で動かすなら空)。"settings" と "env" は画面では変えられない
	Source string `json:"source"`
	// SourcePath は、手で書いたときに、どこに書いてあるか(settings.json のパスか環境変数の名前)
	SourcePath string `json:"sourcePath"`
	URL        string `json:"url"`
	// Fingerprint は信頼した証明書の指紋(画面でつないだ自己署名の証明書のとき)
	Fingerprint string `json:"fingerprint"`
	// User はログインした名前、ExpiresAt はトークンの期限(画面でつないで、ログインしたとき)
	User      string    `json:"user"`
	ExpiresAt time.Time `json:"expiresAt,omitzero"`
	// Expired はトークンの期限が切れているか
	Expired bool `json:"expired"`
	// Plain は、トークンを包まずに保存しているか(この OS では包めない。0019 の決定事項 4)
	Plain bool `json:"plain"`
	// ManualRestart は、つなぎ先を変えたら手で起動し直すか(wails3 dev。決定事項 16)
	ManualRestart bool `json:"manualRestart"`
}

// ServerConnection は今の形を返す。
func (s *ChatService) ServerConnection() ServerConnection {
	c := s.conn
	v := ServerConnection{Mode: "direct", Source: c.source, URL: c.url, ManualRestart: isDev}
	if c.url != "" {
		v.Mode = "server"
	}
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

// CheckServer は、つなぎ先 url を確かめる(つながるか・ログインが要るか・証明書)。fingerprint は、人が画面で確かめて信頼した指紋(まだなら空)。
// 自己署名の証明書で、まだ信頼していなければ、指紋だけを返す(trusted が false。画面が指紋を見せ、人が確かめる)。
func (s *ChatService) CheckServer(url, fingerprint string) (apiclient.Probe, error) {
	if err := s.conn.editable(); err != nil {
		return apiclient.Probe{}, err
	}
	if fingerprint == "" && sameServer(url, s.conn.file.ServerURL) {
		fingerprint = s.conn.file.Fingerprint // 前に信頼した指紋(つなぎ直すとき)
	}
	return apiclient.ProbeServer(context.Background(), url, fingerprint)
}

// ConnectRequest は ConnectServer に渡すもの。
type ConnectRequest struct {
	URL string `json:"url"`
	// Fingerprint は、人が確かめて信頼した証明書の指紋(自己署名の証明書のとき。CheckServer が返したもの)
	Fingerprint string `json:"fingerprint"`
	// Name・Password はログイン(ログインの要る aibow-server のとき)。パスワードはトークンをもらうのに使うだけで、覚えない
	Name     string `json:"name"`
	Password string `json:"password"`
}

// ConnectResult は、つなぎ先を変えたあとどうなるか。
type ConnectResult struct {
	// Restart は、アプリが自分で起動し直すか(false なら、手で起動し直す。wails3 dev)
	Restart bool `json:"restart"`
}

// ConnectServer は aibow-server につなぐ:もう一度確かめ、ログインが要ればアプリ用のトークンをもらって、connection.json に書き、起動し直す。
// 確かめられないつなぎ先は書かない(起動し直してから「つながりません」となるのを避ける。決定事項 8)。
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
		return ConnectResult{}, fmt.Errorf("aibow-server の証明書の指紋(%s)を確かめてから、つないでください", p.Fingerprint)
	}
	f := aibow.ConnectionFile{ServerURL: p.URL}
	if p.HTTPS && !p.SystemTrusted {
		f.Fingerprint = p.Fingerprint
	}
	if p.Login {
		if err := login(&f, req); err != nil {
			return ConnectResult{}, err
		}
	}
	// ほかの aibow-server につなぎ替えるときは、前のトークンを消しておく(同じサーバーなら、同じ PC の前のトークンは今ので置き換わった)
	if c.key != "" && !sameServer(c.file.ServerURL, f.ServerURL) {
		c.revoke()
	}
	if err := f.Save(c.path); err != nil {
		return ConnectResult{}, err
	}
	log.Noticef("aibow-server につなぐように決めました: %s(%s。%s)", f.ServerURL, cmp.Or(f.User, "ログインなし"), absPath(c.path))
	return ConnectResult{Restart: s.restart()}, nil
}

// login は名前とパスワードでアプリ用のトークンをもらい、f に入れる(包むのは SetToken)。もらったトークンで本当につながるかも確かめる。
func login(f *aibow.ConnectionFile, req ConnectRequest) error {
	if strings.TrimSpace(req.Name) == "" || req.Password == "" {
		return errors.New("この aibow-server はログインが要ります。名前とパスワードを入れてください")
	}
	cl := apiclient.New(f.ServerURL)
	cl.PinCert(f.Fingerprint)
	got, err := cl.AppLogin(strings.TrimSpace(req.Name), req.Password, deviceName())
	if err != nil {
		return err
	}
	cl.SetKey(got.Token)
	if _, _, err := cl.Me(); err != nil {
		return fmt.Errorf("ログインはできましたが、もらったトークンでつながりません: %w", err)
	}
	f.User, f.ExpiresAt = got.Name, got.ExpiresAt
	return f.SetToken(got.Token)
}

// DisconnectServer はアプリの中で動かす形に戻す:トークンを aibow-server で消し(つながらなければ手元だけ)、connection.json を消して、起動し直す。
func (s *ChatService) DisconnectServer() (ConnectResult, error) {
	c := s.conn
	if err := c.editable(); err != nil {
		return ConnectResult{}, err
	}
	c.revoke()
	if err := aibow.RemoveConnectionFile(c.path); err != nil {
		return ConnectResult{}, err
	}
	log.Noticef("aibow-server につなぐのをやめ、アプリの中で動かす形に戻しました(%s を消しました)", absPath(c.path))
	return ConnectResult{Restart: s.restart()}, nil
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

// revoke は、今のトークンを aibow-server で消す(ログアウト)。つながらなくても止めない(手元の connection.json は呼ぶ側が消す・書き換える)。
func (c *connection) revoke() {
	if c.key == "" {
		return
	}
	cl := apiclient.New(c.file.ServerURL)
	cl.PinCert(c.file.Fingerprint)
	cl.SetKey(c.key)
	if err := cl.RevokeToken(); err != nil {
		log.Warnf("aibow-server でアプリ用のトークンを消せませんでした(手元からは消します。期限の 10 日で使えなくなります): %v", err)
		return
	}
	log.Noticef("aibow-server からログアウトしました: %s(%s)", c.file.ServerURL, c.file.User)
}

// restart は、ウィンドウを閉じて起動し直すよう頼む。wails3 dev では起動し直さず false(画面が「起動し直してください」と出す。決定事項 16)。
// 返事が画面に届いてから閉じるよう、少し待ってから閉じる。
func (s *ChatService) restart() bool {
	if isDev {
		return false
	}
	s.conn.restart.Store(true)
	go func() {
		time.Sleep(500 * time.Millisecond)
		application.Get().Quit()
	}()
	return true
}

// relaunch は、このアプリをもう一度起動する(同じ引数・同じカレントフォルダ・同じ環境変数)。古いほうは、このあと終わる。
func relaunch() {
	exe, err := os.Executable()
	if err != nil {
		log.Errorf("起動し直せません(自分の場所が分かりません。手で起動してください): %v", err)
		return
	}
	cmd := exec.Command(exe, os.Args[1:]...)
	if err := cmd.Start(); err != nil {
		log.Errorf("起動し直せません(手で起動してください): %v", err)
		return
	}
	log.Noticef("起動し直しました(新しいプロセス: %d)", cmd.Process.Pid)
	cmd.Process.Release()
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
