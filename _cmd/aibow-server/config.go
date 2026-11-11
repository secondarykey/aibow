package main

// config.go: aibow-server の設定(起動の引数・設定ファイル・環境変数)。
//
// 設定ファイルは2つに分ける(アプリの settings.json と tools.json と同じ分け方。0015 の決定事項 19):
//
//	server.json         … 手で書く設定(ポート・作業領域・最初のモデル・llama.cpp とモデルの場所)。aibow-server は書かない
//	server-models.json  … aibow-server が書く値(モデルごとの GPU の層数など)。手で書かなくてよい
//	server-tools.json   … ブラウザの会話のツールの扱いの既定(管理者が設定の画面の「ツール」で変える。形はアプリの tools.json と同じ。0024)
//	server.db           … ブラウザの会話(SQLite。ステップ9。0016 の 13)。画面のアプリの aibow.db とは別のファイル
//	                      (同じ PC で画面のアプリと aibow-server を同時に動かすため)
//
// 置き場所はアプリと同じ %APPDATA%\aibow\(-config で手で書くほうのファイルを指せば、書くほうのファイルもその隣に置く)。
// アプリの settings.json と分けているのは、サーバーの PC とアプリの PC が別になりうるため(ステップ10)。
//
// 同じ設定を何か所にも書けるので、強さの順を決めている:引数 > 設定ファイル > 環境変数 > 既定。
// 「環境変数は初期値で、決めて保存した値が優先」というアプリの考え方と同じで、ファイルに書いた値は環境変数より強い。
// 引数はそのときだけ試すためのものなので、いちばん強い。
//
// server.json の形と読み込み(config.ServerFile・config.LoadServerFile)と環境変数の読み方(config.Env)は、アプリと同じ config パッケージにある。
// ここに置くのは、aibow-server の引数と、強さの順で決めること(serverConfig)だけ。

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/secondarykey/aibow/config"
	"github.com/secondarykey/aibow/mcp/client"
)

// 設定ファイルの名前(アプリの settings.json・tools.json・aibow.db と重ならない名前)
const (
	configFileName = "server.json"
	stateFileName  = "server-models.json"
	toolsFileName  = "server-tools.json"
	dbFileName     = "server.db"
)

// 起動の引数と環境変数の既定(0012 の未決事項をユーザーと決めた。2026-10-19)。
const (
	// defaultPort は待ち受けのポート。よく使われる番号(llama.cpp の server 8080・vLLM 8000・Ollama 11434 など)とぶつからないもの
	defaultPort = 8089
	// defaultCtx は作業領域の大きさ(プロンプト + 返事のトークン数)。アプリ(会話の上限 8192 + 返事の上限 8192)より大きいのは、
	// Cline などのシステムプロンプトが長いため。Qwen3.5 は KV キャッシュを持たない層(線形アテンション)が多く、8GB でも載る見込み
	defaultCtx = 32768
	// defaultParallel は作業領域の中のシーケンスの数(同時に生成できる数。0023)。1 なら今までと同じ(1人ずつ)
	defaultParallel = 1
)

// serverConfig は、強さの順で決めた設定。
type serverConfig struct {
	Port      int
	Ctx       int
	Parallel  int
	Model     string
	Lib       string
	ModelsDir string
	LogLevel  string // 設定ファイルの logLevel(空なら -debug か既定)
	// Login は、ブラウザの画面にログインが要るか(0017 の 10-1)。要らなければ、ブラウザはログインなしの形の利用者(aibow.LocalUser)になる。
	// 既定は要る(0020 の決定事項 1。以前は 127.0.0.1 だけなら要らなかった。0017 の決定事項 8)。login: false を書いたときだけ、127.0.0.1 でログインなしにできる。
	// 127.0.0.1 の外で待ち受けるなら、書いた値によらず要る(0017 の決定事項 2。resolve で決める)
	Login bool
	// Host は待ち受けるアドレス(既定 127.0.0.1)。TLSCert・TLSKey は HTTPS の証明書と秘密鍵(どちらも空なら HTTP)
	Host            string
	TLSCert, TLSKey string
	// Base は全部の道の頭に付けるパス(0042)。"/aibow" の形(最後の / なし)。空なら頭に何も付けない(今までどおり "/" の下)
	Base string

	// MCPServers は、ブラウザの会話で使う MCP のサーバー、Folders は資料のフォルダ(どちらも設定ファイルにだけ書ける。0024)
	MCPServers map[string]client.ServerConfig
	Folders    map[string]string
	// FetchAllowPrivate は、fetch_url で PC の中・社内のアドレスも読むか(設定ファイルにだけ書ける。0036)
	FetchAllowPrivate bool
	// TrustedProxies は信頼するプロキシ(サーバーの PC の上は含まない。newProxyTrust が足す)、ProxyHeader は元の相手を読むヘッダー(設定ファイルにだけ書ける。0044)
	TrustedProxies []netip.Prefix
	ProxyHeader    string

	// File は読んだ設定ファイル(無ければ空)。StatePath は aibow-server が書くファイル(server-models.json)、
	// DBPath はブラウザの会話の DB(server.db)、ToolsPath はツールの扱いの既定(server-tools.json)。どれも設定ファイルの隣
	File      string
	StatePath string
	DBPath    string
	ToolsPath string
}

// flags は起動の引数。書かれたかどうかは set で見る(書かれていない引数は、設定ファイル・環境変数より弱いため)。
type flags struct {
	config, model, lib, modelsDir *string
	host, addr, base              *string
	tlsCert, tlsKey               *string
	ctx, port, parallel           *int
	version, debug, login         *bool
	set                           map[string]bool
}

// defineFlags は引数を決める。既定の値は空にしておき、どこから値を取るかは resolve で決める。
func defineFlags(fs *flag.FlagSet) *flags {
	return &flags{
		config:    fs.String("config", "", "手で書く設定ファイル(既定 %APPDATA%\\aibow\\"+configFileName+")。aibow-server が書く "+stateFileName+" はその隣に置く"),
		model:     fs.String("model", "", "読み込むモデル(ID・名前・GGUF のパス。例: qwen3.5:4b)。空なら一覧の先頭から、書式に対応している最初のモデル。設定ファイルの model・環境変数 AIBOW_MODEL"),
		ctx:       fs.Int("ctx", 0, fmt.Sprintf("作業領域の大きさ(プロンプト + 返事のトークン数。既定 %d)。設定ファイルの ctx・環境変数 AIBOW_SERVER_CTX", defaultCtx)),
		parallel:  fs.Int("parallel", 0, fmt.Sprintf("作業領域の中のシーケンスの数(同時に生成できる数。既定 %d)。作業領域(ctx)はこの数で等分する。設定ファイルの parallel・環境変数 AIBOW_SERVER_PARALLEL", defaultParallel)),
		port:      fs.Int("port", 0, fmt.Sprintf("待ち受けのポート(127.0.0.1 だけで待ち受ける。既定 %d)。設定ファイルの port・環境変数 AIBOW_SERVER_PORT", defaultPort)),
		lib:       fs.String("lib", "", "llama.cpp の DLL のフォルダ(既定 lib)。設定ファイルの lib・環境変数 AIBOW_LIB"),
		modelsDir: fs.String("models", "", "GGUF を置くフォルダ(既定 models)。設定ファイルの modelsDir・環境変数 AIBOW_MODELS_DIR"),
		version:   fs.Bool("version", false, "バージョンを表示して終わる"),
		debug:     fs.Bool("debug", false, "ログを Debug レベルまで出す(設定ファイルの logLevel より強い。どちらも無ければ go run は Info、exe は Notice)"),
		login:     fs.Bool("login", false, "ログインを要るようにする(既定で要る。-login=false なら、127.0.0.1 だけで待ち受けるときにログインなし。利用者は aibow-server user add で作る)。設定ファイルの login・環境変数 AIBOW_SERVER_LOGIN"),
		host:      fs.String("host", "", "待ち受けるアドレス(既定 127.0.0.1。0.0.0.0 なら全部のネットワーク。127.0.0.1 の外ではログインと API キーが要る)。設定ファイルの host・環境変数 AIBOW_SERVER_HOST"),
		addr:      fs.String("addr", "", "待ち受けるアドレスとポートをまとめて書く(例 0.0.0.0:8089。:8089 はポートだけを決め、アドレスは 127.0.0.1 のまま)。-host・-port と一緒には書けない。設定ファイルの addr・環境変数 AIBOW_SERVER_ADDR"),
		base:      fs.String("base", "", "全部の道の頭に付けるパス(例 /aibow なら、画面は /aibow/・OpenAI 互換の口は /aibow/v1。既定 /)。設定ファイルの base・環境変数 AIBOW_SERVER_BASE"),
		tlsCert:   fs.String("tls-cert", "", "HTTPS の証明書のファイル(aibow-server cert で作れる)。設定ファイルの tlsCert・環境変数 AIBOW_SERVER_TLS_CERT"),
		tlsKey:    fs.String("tls-key", "", "HTTPS の秘密鍵のファイル。設定ファイルの tlsKey・環境変数 AIBOW_SERVER_TLS_KEY"),
	}
}

// resolve は、引数 > 設定ファイル > 環境変数 > 既定 の順で設定を決める。
// 使われなくなった環境変数は notices に入れる(起動のときに Warn で書く)。
func resolve(fs *flag.FlagSet, f *flags, env *config.Env) (serverConfig, []string, error) {
	f.set = map[string]bool{}
	fs.Visit(func(fl *flag.Flag) { f.set[fl.Name] = true })

	path, explicit := *f.config, f.set["config"]
	if !explicit {
		dir, err := os.UserConfigDir()
		if err != nil {
			return serverConfig{}, nil, fmt.Errorf("設定ファイルの置き場所(%%APPDATA%%)が決められません。-config で設定ファイルを指定してください: %w", err)
		}
		path = filepath.Join(dir, "aibow", configFileName)
	}
	file, found, err := config.LoadServerFile(path)
	if err != nil {
		return serverConfig{}, nil, err
	}
	if explicit && !found {
		return serverConfig{}, nil, fmt.Errorf("-config の設定ファイルが見つかりません: %s", absPath(path))
	}

	dir := filepath.Dir(path)
	c := serverConfig{StatePath: filepath.Join(dir, stateFileName), DBPath: filepath.Join(dir, dbFileName), ToolsPath: filepath.Join(dir, toolsFileName),
		MCPServers: file.MCPServers, Folders: file.Folders, FetchAllowPrivate: file.FetchAllowPrivate}
	if found {
		c.File = path
	}
	// 信頼するプロキシ(0044)。LoadServerFile で確かめてあるので、ここでは読み直すだけ
	if c.TrustedProxies, err = config.ParseTrustedProxies(file.TrustedProxies); err != nil {
		return serverConfig{}, nil, err
	}
	if c.ProxyHeader, err = config.ProxyHeaderName(file.ProxyHeader); err != nil {
		return serverConfig{}, nil, err
	}
	// 待ち受けるアドレスとポートは、host・port に分けても、addr にまとめても書ける(0042)。どちらで書いても、書いた所の強さで効く
	l, err := listenLevels(f, file, path, env)
	if err != nil {
		return serverConfig{}, nil, err
	}
	c.Port = pick(l[0].portSet, l[0].port, l[1].port, l[2].port, defaultPort)
	c.Ctx = pick(f.set["ctx"], *f.ctx, file.Ctx, env.Int("AIBOW_SERVER_CTX", 0), defaultCtx)
	c.Parallel = pick(f.set["parallel"], *f.parallel, file.Parallel, env.Int("AIBOW_SERVER_PARALLEL", 0), defaultParallel)
	c.Model = pick(f.set["model"], *f.model, file.Model, os.Getenv("AIBOW_MODEL"), "")
	c.Lib = pick(f.set["lib"], *f.lib, file.Lib, os.Getenv("AIBOW_LIB"), "lib")
	c.ModelsDir = pick(f.set["models"], *f.modelsDir, file.ModelsDir, os.Getenv("AIBOW_MODELS_DIR"), "models")
	c.LogLevel = file.LogLevel
	switch {
	case f.set["login"]:
		c.Login = *f.login
	case file.Login != nil:
		c.Login = *file.Login
	default:
		c.Login = env.Bool("AIBOW_SERVER_LOGIN", true) // 何も書かなければ要る(0020)
	}
	c.Host = pick(l[0].hostSet, l[0].host, l[1].host, l[2].host, "127.0.0.1")
	base := pick(f.set["base"], *f.base, file.Base, os.Getenv("AIBOW_SERVER_BASE"), "")
	if c.Base, err = cleanBase(base); err != nil {
		return serverConfig{}, nil, err
	}
	c.TLSCert = pick(f.set["tls-cert"], *f.tlsCert, file.TLSCert, os.Getenv("AIBOW_SERVER_TLS_CERT"), "")
	c.TLSKey = pick(f.set["tls-key"], *f.tlsKey, file.TLSKey, os.Getenv("AIBOW_SERVER_TLS_KEY"), "")
	if (c.TLSCert == "") != (c.TLSKey == "") {
		return serverConfig{}, nil, errors.New("HTTPS には、証明書(tlsCert)と秘密鍵(tlsKey)の両方が要ります。片方だけ書かれています")
	}

	// aibow-api から名前を変えたとき(0015 の 18)に、環境変数の名前も変えた。古い名前は読まずに注意だけ出す
	// (黙って無視すると、書いたつもりの値が効かない理由が分からないため。アプリが AIBOW_CTX をやめたときと同じやり方)
	var notices []string
	// 127.0.0.1 の外で待ち受けるなら、ログインを要るようにする(認証が無いまま LAN に出さない。0017 の決定事項 2)
	if !c.Local() && !c.Login {
		c.Login = true
		notices = append(notices, fmt.Sprintf("127.0.0.1 の外(%s)で待ち受けるので、ログインを要るようにします(login の設定より強い)", c.Host))
	}
	for old, now := range map[string]string{"AIBOW_API_PORT": "AIBOW_SERVER_PORT", "AIBOW_API_CTX": "AIBOW_SERVER_CTX"} {
		if v := os.Getenv(old); v != "" {
			notices = append(notices, fmt.Sprintf("環境変数 %s(%s)は使われなくなりました。%s に名前を変えてください(aibow-api を aibow-server に名前を変えたため)", old, v, now))
		}
	}
	return c, notices, nil
}

// listen は、1つの所(引数・設定ファイル・環境変数)に書かれた待ち受けるアドレスとポート。
// hostSet・portSet は引数のときだけ使う(引数は「書いたかどうか」で決める。ほかの所は空(0)が「書いていない」)。
type listen struct {
	host             string
	port             int
	hostSet, portSet bool
}

// listenLevels は、引数・設定ファイル・環境変数の順に、待ち受けるアドレスとポートを読む(0042)。
// addr は host と port をまとめて書く形で、書いた所の host・port の代わりになる。同じ所に addr と host・port の両方を書くとエラー
// (どちらが効くのかを決めておいても、書いた人が迷うだけなので)。
func listenLevels(f *flags, file config.ServerFile, path string, env *config.Env) ([3]listen, error) {
	var l [3]listen
	// 引数
	if f.set["addr"] {
		if f.set["host"] || f.set["port"] {
			return l, errors.New("-addr と -host・-port は一緒に書けません。どちらかにしてください")
		}
		h, p, err := splitAddr("-addr", *f.addr)
		if err != nil {
			return l, err
		}
		l[0] = listen{host: h, port: p, hostSet: h != "", portSet: true}
	} else {
		l[0] = listen{host: *f.host, port: *f.port, hostSet: f.set["host"], portSet: f.set["port"]}
	}
	// 設定ファイル
	if file.Addr != "" {
		if file.Host != "" || file.Port != 0 {
			return l, fmt.Errorf("設定ファイル %s に addr と host・port の両方が書かれています。どちらかにしてください", path)
		}
		h, p, err := splitAddr("設定ファイル "+path+" の addr", file.Addr)
		if err != nil {
			return l, err
		}
		l[1] = listen{host: h, port: p}
	} else {
		l[1] = listen{host: file.Host, port: file.Port}
	}
	// 環境変数
	if v := os.Getenv("AIBOW_SERVER_ADDR"); v != "" {
		if os.Getenv("AIBOW_SERVER_HOST") != "" || os.Getenv("AIBOW_SERVER_PORT") != "" {
			return l, errors.New("環境変数 AIBOW_SERVER_ADDR と AIBOW_SERVER_HOST・AIBOW_SERVER_PORT は一緒に決められません。どちらかにしてください")
		}
		h, p, err := splitAddr("環境変数 AIBOW_SERVER_ADDR", v)
		if err != nil {
			return l, err
		}
		l[2] = listen{host: h, port: p}
	} else {
		l[2] = listen{host: os.Getenv("AIBOW_SERVER_HOST"), port: env.Int("AIBOW_SERVER_PORT", 0)}
	}
	return l, nil
}

// splitAddr は "アドレス:ポート" を分ける。アドレスを書かない ":8089" はポートだけを決めたことにする
// (Go の net.Listen では ":8089" は全部のネットワークだが、ここでは黙って PC の外に出さないよう、アドレスは 127.0.0.1 のまま。
// 全部のネットワークにするなら 0.0.0.0:8089 と書く)。where はエラーに出す、どこに書いた値か。
func splitAddr(where, addr string) (host string, port int, err error) {
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, fmt.Errorf("%s は アドレス:ポート の形で書いてください(例 0.0.0.0:8089・127.0.0.1:8089・[::1]:8089): %q", where, addr)
	}
	port, err = strconv.Atoi(p)
	if err != nil || port <= 0 || port > 65535 {
		return "", 0, fmt.Errorf("%s のポートは 1〜65535 の数にしてください: %q", where, addr)
	}
	return h, port, nil
}

// cleanBase は、道の頭に付けるパス(base。0042)を "/aibow" の形にそろえる。"" と "/" は頭なし(空)。
// 前後の / は書いても書かなくてもよい("aibow"・"/aibow/" も "/aibow")。
// 使えるのは英数字と - _ . ~ と、区切りの / だけ(URL のまま書ける文字。%・?・# や空白を入れると、画面やリンクの URL が壊れるため)。
func cleanBase(base string) (string, error) {
	b := strings.Trim(strings.TrimSpace(base), "/")
	if b == "" {
		return "", nil
	}
	// Git Bash(MSYS)は、引数の /aibow を Windows のパス(C:/Program Files/Git/aibow)に書き換えてから渡す(2026-11-08 に確かめた)。
	// 書いた人には /aibow と書いたようにしか見えないので、何が起きたかを言う。先頭の / を書かなければ書き換えられない
	if len(b) >= 2 && b[1] == ':' {
		return "", fmt.Errorf("base が Windows のパスになっています: %q。Git Bash は引数の /aibow をパスに書き換えるので、-base aibow と先頭の / なしで書いてください(設定ファイルの base は書き換えられません)", base)
	}
	for seg := range strings.SplitSeq(b, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "", fmt.Errorf("base は /aibow のようなパスで書いてください(空の段・. ・.. は使えません): %q", base)
		}
		if strings.IndexFunc(seg, func(r rune) bool {
			return !('a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || '0' <= r && r <= '9' || strings.ContainsRune("-_.~", r))
		}) >= 0 {
			return "", fmt.Errorf("base に使えるのは英数字と - _ . ~ と区切りの / だけです: %q", base)
		}
	}
	return "/" + b, nil
}

// URL は、つなぐ側が書く aibow-server のアドレス(http://127.0.0.1:8089/aibow など。最後の / なし)。
// 全部のネットワークで待ち受けるとき(0.0.0.0 など)も、書いたアドレスのまま(ほかの PC からの書き方は logAddresses が添える)。
func (c serverConfig) URL() string {
	return c.Scheme() + "://" + net.JoinHostPort(c.Host, strconv.Itoa(c.Port)) + c.Base
}

// Local は、この PC からだけ(ループバックのアドレス)で待ち受けるか。
func (c serverConfig) Local() bool {
	if c.Host == "localhost" {
		return true
	}
	ip := net.ParseIP(c.Host)
	return ip != nil && ip.IsLoopback()
}

// KeysRequired は、OpenAI 互換の口と aibow 専用の口に API キーが要るか(0017 の決定事項 9)。
// 127.0.0.1 の外では要る。127.0.0.1 だけでも、ログインが要る設定なら要る(ブラウザだけ守っても、同じ会話を口から読めては意味が無いため)
func (c serverConfig) KeysRequired() bool { return !c.Local() || c.Login }

// Scheme は待ち受ける形(証明書があれば https)。
func (c serverConfig) Scheme() string {
	if c.TLSCert != "" {
		return "https"
	}
	return "http"
}

// pick は、引数(書かれていれば)> 設定ファイル > 環境変数 > 既定 の順で、最初に決まっている値を返す。空(0)は「決めていない」。
func pick[T comparable](argSet bool, arg, file, env, def T) T {
	var zero T
	switch {
	case argSet:
		return arg
	case file != zero:
		return file
	case env != zero:
		return env
	}
	return def
}

// absPath はログに出すためにパスを絶対パスにする(相対パスのままだと、どこを探したのかが分からないため)。
func absPath(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}
