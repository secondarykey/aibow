package config

// server.go: aibow-server の手で書く設定ファイル(server.json)。
//
// 形はアプリの settings.json と同じ考え方で、手で書き、aibow-server は書かない。読むのは起動したときだけ。
// 引数・環境変数と合わせて強さの順(引数 > 設定ファイル > 環境変数 > 既定)で決めるのは aibow-server の側(_cmd/aibow-server の config.go)で、
// ここは「ファイルに何と書いてあるか」を読んで確かめるところまで。
// アプリの settings.json と分けているのは、サーバーの PC とアプリの PC が別になりうるため(ステップ10)。

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	"github.com/secondarykey/aibow/log"
	"github.com/secondarykey/aibow/mcp/client"
)

// ServerFile は server.json の中身。書いていない欄は空(0)で、そのときは環境変数か既定を使う。
type ServerFile struct {
	Port int `json:"port"` // 待ち受けのポート
	Ctx  int `json:"ctx"`  // 作業領域の大きさ
	// Parallel は作業領域の中のシーケンスの数(同時に生成できる数。0023)。作業領域はこの数で等分する(32768 を 2 なら1本 16384)
	Parallel  int    `json:"parallel"`
	Model     string `json:"model"`     // 起動のときに読み込むモデル(ID・名前・GGUF のパス)
	Lib       string `json:"lib"`       // llama.cpp の DLL のフォルダ
	ModelsDir string `json:"modelsDir"` // GGUF を置くフォルダ(Ollama の保存先は別に探す)
	LogLevel  string `json:"logLevel"`  // ログのレベル(LogLevels の名前)。-debug があればそちら(_cmd/aibow-server の log.go の logLevel)
	// Login は、ブラウザの画面にログインを要るようにするか(0017 の 10-1)。書いていなければ環境変数か既定(要る。0020)。
	// false と書いて環境変数より強くできるよう、ポインタにする(ほかの欄は 0 を「書いていない」にしているが、真偽値では false も値なので)
	Login *bool `json:"login"`
	// Host は待ち受けるアドレス(0017 の 10-2)。既定は 127.0.0.1(この PC からだけ)。0.0.0.0 なら全部のネットワーク
	Host string `json:"host"`
	// Addr は host と port をまとめて書く形("0.0.0.0:8089"。0042)。同じファイルに host・port と一緒には書けない
	Addr string `json:"addr"`
	// Base は、全部の道の頭に付けるパス("/aibow"。0042)。書かなければ "/"(今までどおり)
	Base string `json:"base"`
	// TLSCert・TLSKey は HTTPS の証明書と秘密鍵のファイル(aibow-server cert で作れる)。両方書けば HTTPS で待ち受ける
	TLSCert string `json:"tlsCert"`
	TLSKey  string `json:"tlsKey"`
	// MCPServers は、ブラウザの会話で使う MCP のサーバー(0024 の決定事項 7・23)。形はアプリの settings.json の mcpServers と同じ。
	// サーバーの PC で動き、全員で1つを使う(決定事項 9)。最初に使われたときに起動する(決定事項 20)
	MCPServers map[string]client.ServerConfig `json:"mcpServers"`
	// Folders は資料のフォルダ(名前 → 絶対パス。0024 の 12-2)。管理者が配るプロファイルで1つ選ぶと、そのプロファイルの会話の作業フォルダになる。
	// ブラウザには名前だけを見せる。git は管理者がサーバーで手で行う(aibow は触れない。決定事項 6)
	Folders map[string]string `json:"folders"`
	// FetchAllowPrivate は、ブラウザの会話の URL を読むツール(fetch_url)で、サーバーの PC の中・社内のアドレスも読むか(0036 の決定事項 12)。
	// 既定は読まない(モデルが書いた URL で、社内のサーバーやクラウドのメタデータに届かないように)。fetch_url は管理者が「ツール」のタブで有効にするまで使えない
	FetchAllowPrivate bool `json:"fetchAllowPrivate"`
	// TrustedProxies は、信頼するリバースプロキシのアドレス(IP アドレスか CIDR。0044 の決定事項 3)。つないできた相手がここに入っているときだけ、
	// ProxyHeader のヘッダーから元の相手を取り出す。サーバーの PC の上(127.0.0.0/8・::1)は書かなくても入っている(決定事項 2)ので、ほかの PC のプロキシのときだけ書く
	TrustedProxies []string `json:"trustedProxies"`
	// ProxyHeader は、元の相手を読むヘッダー(X-Forwarded-For(既定)・Forwarded・X-Real-IP のどれか1つ。決定事項 4)
	ProxyHeader string `json:"proxyHeader"`
}

// LogLevels は、設定ファイルの logLevel に書ける値。
var LogLevels = map[string]slog.Level{
	"debug":  log.LevelDebug,
	"info":   log.LevelInfo,
	"notice": log.LevelNotice,
	"warn":   log.LevelWarn,
	"error":  log.LevelError,
}

// LoadServerFile は server.json を読む。無ければ found が false(エラーにしない)。
// 知らない欄はエラーにする(書き間違えた欄が黙って効かないと、理由が分からないため)。
func LoadServerFile(path string) (file ServerFile, found bool, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return ServerFile{}, false, nil
	}
	if err != nil {
		return ServerFile{}, false, fmt.Errorf("設定ファイル %s を読めません: %w", path, err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&file); err != nil {
		return ServerFile{}, false, fmt.Errorf("設定ファイル %s を読めません(%s): %w", path, JSONErrorLine(data, err), err)
	}
	if _, ok := LogLevels[file.LogLevel]; file.LogLevel != "" && !ok {
		return ServerFile{}, false, fmt.Errorf("設定ファイル %s の logLevel は debug・info・notice・warn・error のどれかにしてください: %q", path, file.LogLevel)
	}
	if file.Port < 0 || file.Ctx < 0 {
		return ServerFile{}, false, fmt.Errorf("設定ファイル %s の port と ctx は 1 以上にしてください: %d / %d", path, file.Port, file.Ctx)
	}
	if err := CheckMCPServers(path, file.MCPServers); err != nil {
		return ServerFile{}, false, err
	}
	if err := checkFolders(path, file.Folders); err != nil {
		return ServerFile{}, false, err
	}
	if _, err := ParseTrustedProxies(file.TrustedProxies); err != nil {
		return ServerFile{}, false, fmt.Errorf("設定ファイル %s の trustedProxies: %w", path, err)
	}
	if _, err := ProxyHeaderName(file.ProxyHeader); err != nil {
		return ServerFile{}, false, fmt.Errorf("設定ファイル %s の proxyHeader: %w", path, err)
	}
	return file, true, nil
}

// checkFolders は、設定ファイル path の folders を確かめる。名前が空でなく、パスが絶対パスで、フォルダがあること。
// 起動のときに分かるようにする(書き間違えたフォルダを、会話で読もうとしたときに初めて知るのでは遅いため)。
func checkFolders(path string, folders map[string]string) error {
	for name, dir := range folders {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("設定ファイル %s の folders に、名前が空のフォルダがあります", path)
		}
		if !filepath.IsAbs(dir) {
			return fmt.Errorf("設定ファイル %s の folders の %s は、絶対パスで書いてください: %s", path, name, dir)
		}
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			return fmt.Errorf("設定ファイル %s の folders の %s のフォルダがありません: %s", path, name, dir)
		}
	}
	return nil
}

// ProxyHeaders は、server.json の proxyHeader に書けるヘッダー(0044 の決定事項 4)。先頭が既定。
// nginx・IIS(ARR)・Apache・Caddy のどれも X-Forwarded-For を書けるので、それを既定にする。
var ProxyHeaders = []string{"X-Forwarded-For", "Forwarded", "X-Real-IP"}

// ProxyHeaderName は、proxyHeader に書いた名前を、ProxyHeaders の書き方にそろえる(大文字小文字は問わない。空なら既定)。
// 知らない名前はエラー(書き間違えたまま、元の相手を見ないで動かないように)。
func ProxyHeaderName(name string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return ProxyHeaders[0], nil
	}
	for _, h := range ProxyHeaders {
		if strings.EqualFold(strings.TrimSpace(name), h) {
			return h, nil
		}
	}
	return "", fmt.Errorf("%s のどれかにしてください: %q", strings.Join(ProxyHeaders, "・"), name)
}

// ParseTrustedProxies は、trustedProxies に書いた IP アドレスか CIDR を読む(0044 の決定事項 3)。
// IP アドレスはそのアドレスだけ(/32・/128)。IPv4 を IPv6 の形で書いた値(::ffff:10.0.0.5)は IPv4 にそろえる
// (比べる相手の元の相手も IPv4 にそろえるため。決定事項 9)。名前(proxy.example.local)は読まない(やらないこと)。
func ParseTrustedProxies(list []string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, s := range list {
		s = strings.TrimSpace(s)
		var p netip.Prefix
		if strings.Contains(s, "/") {
			var err error
			if p, err = netip.ParsePrefix(s); err != nil {
				return nil, fmt.Errorf("IP アドレスか CIDR(10.0.0.5・192.168.10.0/24・2001:db8::/32 など)で書いてください: %q", s)
			}
			if a := p.Addr(); a.Is4In6() && p.Bits() >= 96 {
				p = netip.PrefixFrom(a.Unmap(), p.Bits()-96)
			}
		} else {
			a, err := netip.ParseAddr(s)
			if err != nil || a.Zone() != "" {
				return nil, fmt.Errorf("IP アドレスか CIDR(10.0.0.5・192.168.10.0/24・2001:db8::/32 など)で書いてください: %q", s)
			}
			a = a.Unmap()
			p = netip.PrefixFrom(a, a.BitLen())
		}
		out = append(out, p.Masked())
	}
	return out, nil
}
