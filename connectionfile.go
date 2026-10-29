package aibow

// connectionfile.go: 画面で決めた aibow-server のつなぎ先(connection.json。0019)。
//
// 設定の置き場所の分け方(settingsfile.go)に、もう1つ足す:
//
//	画面で変える設定           … DB
//	手で書く設定               … settings.json(serverUrl など。手で書いてあれば、こちらが強い。0019 の決定事項 5)
//	画面で変えるが、その PC の人が決めること … tools.json(ツールの扱い)と、このファイル(つなぎ先・ログイン)
//
// つなぎ先は「その PC でどう起動するか」なので会話の DB には入れない(つないだ先の DB に、つなぎ方を置くことになってしまう)。
// アプリ用のトークンはそのまま置かず、OS の仕組みで包む(internal/secret。Windows の DPAPI)。
// 読むのは起動したときだけ。画面で変えたら、アプリが起動し直す(決定事項 1)。

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/secondarykey/aibow/internal/secret"
)

// ConnectionFile は connection.json の中身。
type ConnectionFile struct {
	// ServerURL は aibow-server のつなぎ先(例: https://192.168.1.10:8089)。空なら、アプリの中で動かす
	ServerURL string `json:"serverUrl"`
	// Fingerprint は、信頼した証明書の SHA-256 の指紋(自己署名の証明書のとき。初めてつなぐときに人が確かめて覚えたもの。決定事項 6)。
	// 空なら、OS が信頼している証明書だけを信頼する(HTTP のときも空)
	Fingerprint string `json:"fingerprint,omitempty"`
	// User はログインした名前(画面に出すだけ。誰かを決めるのはトークン)
	User string `json:"user,omitempty"`
	// Token はアプリ用のトークンを包んだもの(base64)。Protection が包み方。ログインの要らない aibow-server なら空
	Token      string `json:"token,omitempty"`
	Protection string `json:"protection,omitempty"`
	// ExpiresAt はトークンの期限(作ってから 10 日。画面に出す)
	ExpiresAt time.Time `json:"expiresAt,omitzero"`

	// Known は、画面で確かめて信頼した最後のつなぎ先。上の ServerURL(次の起動でつなぐ先)とは別に持つ。
	// 信頼だけしてログインせずに閉じたとき・ログアウトしたときも残し、次は信頼し直さずにログインから続けられるようにする
	// (SSH の known_hosts と同じく、信頼した記録はつなぐのをやめても要る。2026-10-29、ユーザーと決めた)。覚えるのは1つだけ(決定事項 8)
	Known *KnownServer `json:"known,omitempty"`
}

// KnownServer は、信頼したつなぎ先(ConnectionFile.Known)。秘密は入れない(フィンガープリントは秘密ではない)。
type KnownServer struct {
	URL string `json:"url"`
	// Fingerprint は信頼した証明書の指紋(自己署名の証明書のとき。OS が信頼している証明書・HTTP なら空)
	Fingerprint string `json:"fingerprint,omitempty"`
	// User は最後にログインした名前(タブに入れておくだけ)
	User string `json:"user,omitempty"`
}

// Trusted は、覚えている信頼したつなぎ先(無ければ空)。Known の無い前の版のファイルなら、つないでいる先を使う。
func (c ConnectionFile) Trusted() KnownServer {
	if c.Known != nil {
		return *c.Known
	}
	return KnownServer{URL: c.ServerURL, Fingerprint: c.Fingerprint, User: c.User}
}

// LoadConnectionFile は connection.json を読む。無ければ空(アプリの中で動かす)。
func LoadConnectionFile(path string) (ConnectionFile, error) {
	var c ConnectionFile
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, fmt.Errorf("つなぎ先のファイル %s を読めません: %w", path, err)
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return ConnectionFile{}, fmt.Errorf("つなぎ先のファイル %s を読めません(%s。画面でつなぎ直すと書き直します): %w", path, jsonErrorLine(data, err), err)
	}
	if err := CheckServerURL(c.ServerURL); err != nil {
		return ConnectionFile{}, fmt.Errorf("つなぎ先のファイル %s の serverUrl: %w", path, err)
	}
	if c.Known != nil {
		if err := CheckServerURL(c.Known.URL); err != nil {
			return ConnectionFile{}, fmt.Errorf("つなぎ先のファイル %s の known.url: %w", path, err)
		}
	}
	return c, nil
}

// Save は connection.json に書く。トークンが入っているので、ほかの人に読ませない権限で書く(Windows では効かないが、包んである)。
// 途中で落ちても壊れたファイルが残らないよう、隣に書いてから置き換える。
func (c ConnectionFile) Save(path string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("つなぎ先のファイルのフォルダを作れません: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("つなぎ先のファイル %s を書けません: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("つなぎ先のファイル %s を書けません: %w", path, err)
	}
	return nil
}

// RemoveConnectionFile は connection.json を消す(アプリの中で動かす形に戻す)。無くてもエラーにしない。
func RemoveConnectionFile(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("つなぎ先のファイル %s を消せません: %w", path, err)
	}
	return nil
}

// SetToken はトークンを OS の仕組みで包んで入れる(空なら消す)。
func (c *ConnectionFile) SetToken(token string) error {
	if token == "" {
		c.Token, c.Protection = "", ""
		return nil
	}
	sealed, err := secret.Protect([]byte(token))
	if err != nil {
		return err
	}
	c.Token, c.Protection = base64.StdEncoding.EncodeToString(sealed), secret.Kind
	return nil
}

// PlainToken は包んだトークンを戻す。ほかの PC から写したファイルなどで戻せなければエラー(画面でログインし直す)。
func (c ConnectionFile) PlainToken() (string, error) {
	if c.Token == "" {
		return "", nil
	}
	sealed, err := base64.StdEncoding.DecodeString(c.Token)
	if err != nil {
		return "", fmt.Errorf("つなぎ先のファイルのトークンが読めません: %w", err)
	}
	switch c.Protection {
	case secret.KindPlain:
		return string(sealed), nil
	case secret.KindDPAPI:
		if secret.Kind != secret.KindDPAPI {
			return "", errors.New("つなぎ先のファイルのトークンは Windows で包んだもので、この OS では戻せません")
		}
		plain, err := secret.Unprotect(sealed)
		if err != nil {
			return "", err
		}
		return string(plain), nil
	}
	return "", fmt.Errorf("つなぎ先のファイルのトークンの包み方 %q を知りません", c.Protection)
}

// TokenProtected は、トークンを OS の仕組みで包んでいるか(画面に「平文で保存しています」と出すため)。
func (c ConnectionFile) TokenProtected() bool {
	return c.Token != "" && c.Protection != secret.KindPlain
}
