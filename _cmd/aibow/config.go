package main

// config.go: 起動時の設定を環境変数から読む部分。

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/secondarykey/aibow"
	"github.com/secondarykey/aibow/engine"
	"github.com/secondarykey/aibow/format"
	"github.com/secondarykey/aibow/log"
	"github.com/secondarykey/aibow/models"
	"github.com/secondarykey/aibow/ui"
)

// 設定の誤りはログで止めず、まとめて error で返す。
// リリース版(wails3 build)はコンソールが無いので、ログに出しただけでは誰にも見えない。
// main.go が画面に表示する。
type envReader struct {
	errs []error
}

func (r *envReader) str(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func (r *envReader) int(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		r.errs = append(r.errs, fmt.Errorf("%s は整数で指定してください: %q", key, v))
		return def
	}
	return n
}

func (r *envReader) float(key string, def float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		r.errs = append(r.errs, fmt.Errorf("%s は数値で指定してください: %q", key, v))
		return def
	}
	return f
}

// 既定では、カレントフォルダの中の lib フォルダ(llama.cpp)と models フォルダ(モデル)を使う。
// _cmd/aibow で起動すると(wails3 dev / wails3 task run も _cmd/aibow で動く)、
// _cmd/aibow/lib と _cmd/aibow/models が使われる。
// lib と models はモデルを動かすこのアプリの持ち物なので、アプリのフォルダに置く。
// 別の場所に置いたとき(worktree から本体の lib を使うときなど)だけ環境変数で指定する。
type appConfig struct {
	engine       engine.Config
	catalog      models.Catalog
	dbPath       string // 会話を保存する SQLite のファイル
	settingsPath string // 設定ファイル(settings.json。手で書く設定。今は MCP のサーバーだけ)
	toolsPath    string // ツールの扱いの PC の既定(tools.json。画面で変えたものを aibow が書く)
	windowPath   string // ウィンドウの位置と大きさ(window.json。閉じるときに aibow が書く。windowstate.go)
	placesPath   string // 画面で足したモデルの置き場所(model-places.json。直接の形のときだけ使う。ui/modelplaces.go)
	connPath     string // 画面で決めた aibow-server のつなぎ先とログイン(connection.json。aibow が書く。0019)
	initialModel string // AIBOW_MODEL。空なら最後に使ったモデル → 一覧の先頭(書式に対応している最初のモデル)

	// settings は settings.json の中身。settingsErr は読めなかった理由(MCP は使わずに起動し、お知らせで伝える。startMCP)
	settings    aibow.SettingsFile
	settingsErr error
	// serverURL は生成を頼む aibow-server のつなぎ先(settings.json の serverUrl > 環境変数 AIBOW_SERVER_URL > 画面で決めたもの。loadServer)。
	// 空なら今までどおりアプリの中で llama.cpp を動かす。書けば llama.cpp を読み込まず、aibow-server につなぎに行く(0015 の 1・20)
	serverURL string
	// serverKey・serverCert は、aibow-server の API キーと証明書のファイル(settings.json の serverKey・serverCert > 環境変数 AIBOW_SERVER_KEY・AIBOW_SERVER_CERT。0017 の 10-2)。
	// 画面で決めたつなぎ先なら、serverKey はアプリ用のトークン、serverPin は信頼した証明書の指紋(0019)
	serverKey, serverCert, serverPin string
	// serverSource はつなぎ先をどこで決めたか(serverSettings・serverEnv・serverScreen。つながないなら空)。
	// 手で書いた値(settings.json・環境変数)が画面の値より強い(0019 の決定事項 5)
	serverSource string
	// conn は connection.json の中身(画面で決めたつなぎ先。手で書いた値があれば使わないが、画面に出すために読む)
	conn aibow.ConnectionFile

	// app は、画面で変えられるアプリ全体の設定の初期値。
	// DB にまだ値が無いときだけ使う(画面で一度変えると DB の値が優先される)。
	app aibow.AppSettings

	// notices は起動時に画面のトーストで伝えること(使われなくなった環境変数など)。ログにも出す
	notices []string
}

func loadConfig() (appConfig, error) {
	var env envReader
	c := appConfig{
		engine: engine.Config{
			LibPath:  env.str("AIBOW_LIB", "lib"),
			LlamaLog: os.Getenv("AIBOW_LLAMA_LOG") == "1",
		},
		catalog: models.Catalog{
			ModelsDir: env.str("AIBOW_MODELS_DIR", "models"),
			OllamaDir: models.OllamaDir(),
		},
		dbPath:       env.str("AIBOW_DB", configFile("aibow.db")),
		settingsPath: env.str("AIBOW_SETTINGS", configFile("settings.json")),
		toolsPath:    env.str("AIBOW_TOOLS", configFile("tools.json")),
		windowPath:   env.str("AIBOW_WINDOW", configFile("window.json")),
		placesPath:   env.str("AIBOW_MODEL_PLACES", configFile("model-places.json")),
		connPath:     env.str("AIBOW_CONNECTION", configFile("connection.json")),
		initialModel: os.Getenv("AIBOW_MODEL"),
		// 既定のシステムプロンプトなど、環境変数で変えない初期値は ui にある(aibow-server のブラウザの会話と同じ既定にするため)
		app: ui.InitialAppSettings(ui.InitialValues{
			GPULayers:       env.int("AIBOW_GPU_LAYERS", 999), // 999 = 全部 GPU に載せる
			PresencePenalty: env.float("AIBOW_PRESENCE_PENALTY", 1.5),
			HistoryTokens:   env.int("AIBOW_HISTORY_TOKENS", 8192),
			ReplyTokens:     env.int("AIBOW_MAX_TOKENS", 8192),
		}),
	}
	// settings.json は MCP のサーバーとつなぎ先を書く場所。読めなくても起動は止めない(MCP は使わない。つなぎ先は環境変数を使う)
	c.settings, c.settingsErr = aibow.LoadSettingsFile(c.settingsPath)
	c.loadServer(&env)

	// AIBOW_CTX(コンテキスト長)は、会話の上限 + 返事の上限から aibow が計算するようになったので使わない
	if v := os.Getenv("AIBOW_CTX"); v != "" {
		c.notices = append(c.notices, fmt.Sprintf("環境変数 AIBOW_CTX(%s)は使われなくなりました。"+
			"作業領域の大きさは「会話の上限 + 返事の上限」から計算します。"+
			"会話の上限は AIBOW_HISTORY_TOKENS、返事の上限は AIBOW_MAX_TOKENS か、画面の設定で変えてください", v))
	}
	for _, n := range []struct {
		key string
		v   int
	}{{"AIBOW_HISTORY_TOKENS", c.app.Defaults.HistoryTokens}, {"AIBOW_MAX_TOKENS", c.app.Defaults.ReplyTokens}} {
		if n.v <= 0 {
			env.errs = append(env.errs, fmt.Errorf("%s は 1 以上で指定してください: %d", n.key, n.v))
		}
	}
	// aibow-server につなぐときは llama.cpp を読み込まないので、lib が無くてもよい。
	// 初めての PC で画面からつなぐときは、ここで起動に失敗してから設定の画面の「サーバー」のタブでつなぐ(タブは起動に失敗しても使える。0019)
	if _, err := os.Stat(c.engine.LibPath); err != nil && c.serverURL == "" {
		// 相対パスのままだと「どこを探したのか」が分からないので、絶対パスで出す
		env.errs = append(env.errs, fmt.Errorf("llama.cpp のフォルダが見つかりません: %s\n"+
			"(相対パスはカレントフォルダが基準です。_cmd/aibow で起動するか、AIBOW_LIB で場所を指定してください。"+
			"取得方法は README.md の「llama.cpp の取得」を参照。"+
			"aibow-server につないで使うなら llama.cpp は要りません。設定の画面の「サーバー」のタブでつなぎ、起動し直してください)", absPath(c.engine.LibPath)))
	}
	if c.dbPath == "" {
		env.errs = append(env.errs, errors.New("会話を保存する場所が決められません。AIBOW_DB で DB ファイルの場所を指定してください"))
	}
	return c, errors.Join(env.errs...)
}

// つなぎ先をどこで決めたか(appConfig.serverSource)。
const (
	serverSettings = "settings" // settings.json の serverUrl(手で書いた)
	serverEnv      = "env"      // 環境変数 AIBOW_SERVER_URL(手で書いた)
	serverScreen   = "screen"   // 画面で決めた(connection.json)
)

// loadServer は aibow-server のつなぎ先を決める。強い順に settings.json の serverUrl > 環境変数 AIBOW_SERVER_URL(0015 の 20)> 画面で決めたもの(connection.json)。
// 手で書いた値があれば、画面の値は使わない(書いたのに効かない、が起きないように。0019 の決定事項 5)。どれも無ければアプリの中で動かす。
func (c *appConfig) loadServer(env *envReader) {
	envURL := os.Getenv("AIBOW_SERVER_URL")
	if err := aibow.CheckServerURL(envURL); err != nil {
		env.errs = append(env.errs, fmt.Errorf("AIBOW_SERVER_URL: %w", err))
		envURL = ""
	}
	// 画面に出すため、手で書いてあっても読んでおく(読めなければお知らせで伝え、無いものとして起動する)
	conn, err := aibow.LoadConnectionFile(c.connPath)
	if err != nil {
		c.notices = append(c.notices, err.Error())
	}
	c.conn = conn
	switch {
	case c.settings.ServerURL != "":
		c.serverURL, c.serverSource = c.settings.ServerURL, serverSettings
	case envURL != "":
		c.serverURL, c.serverSource = envURL, serverEnv
	case conn.ServerURL != "":
		c.serverURL, c.serverSource, c.serverPin = conn.ServerURL, serverScreen, conn.Fingerprint
		token, err := conn.PlainToken()
		if err != nil {
			c.notices = append(c.notices, err.Error()+"。設定の画面の「サーバー」で、もう一度ログインしてください")
		}
		c.serverKey = token
		if conn.Token != "" && !conn.TokenProtected() {
			log.Warnf("aibow-server のアプリ用のトークンを、包まずに保存しています(この OS では包めません): %s", absPath(c.connPath))
		}
		return
	default:
		return
	}
	c.serverKey = cmp.Or(c.settings.ServerKey, os.Getenv("AIBOW_SERVER_KEY"))
	c.serverCert = cmp.Or(c.settings.ServerCert, os.Getenv("AIBOW_SERVER_CERT"))
}

// configFile は、DB ファイル・設定ファイルの既定の場所。Windows では %APPDATA%\aibow\<name>。
// lib / models と違ってアプリのフォルダに置かないのは、会話と設定は利用者のデータで、
// アプリを入れ直したり、別のフォルダから起動したりしても残ってほしいから。
func configFile(name string) string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "aibow", name)
}

// absPath はログに出すためにパスを絶対パスにする。空や変換できないときはそのまま返す
// (空のまま Abs に渡すとカレントフォルダになり、設定されているように見えてしまう)。
func absPath(p string) string {
	if p == "" {
		return p
	}
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

// pickInitialModel は起動時に読み込むモデルを決める。
// AIBOW_MODEL → 最後に使ったモデル → 一覧の先頭(書式に対応している最初のモデル)、の順。
// 指定したモデルと最後に使ったモデルは、書式に未対応でもそのまま読み込む(画面のお知らせで未対応と伝わる。選んだのは利用者なので)。
func pickInitialModel(cfg appConfig, app aibow.AppSettings, list []models.Entry) (models.Entry, error) {
	if cfg.initialModel != "" {
		return cfg.catalog.Find(cfg.initialModel)
	}
	if app.Workspace.Model != "" {
		m, err := cfg.catalog.Find(app.Workspace.Model)
		if err == nil {
			return m, nil
		}
		// 消したモデルなどで見つからなくても、一覧の先頭で起動する
		log.Warnf("最後に使ったモデルが見つかりません(%v)。一覧の先頭を使います", err)
	}
	if len(list) == 0 {
		return models.Entry{}, fmt.Errorf("モデルが1つも見つかりません。models フォルダに GGUF を置くか、ollama pull してください")
	}
	// models フォルダに未対応のモデル(gemma4 など)を置いただけで、それを読み込んで始まらないように、種類を GGUF の先頭だけ読んで確かめる
	for _, m := range list {
		arch, err := models.Arch(m.Path)
		if err == nil && format.For(arch) != nil {
			return m, nil
		}
		log.Infof("モデル %s の書式(種類: %s)には未対応なので、起動のときのモデルにしません", m.Name, cmp.Or(arch, "読めない"))
	}
	return list[0], nil // 対応しているモデルが無ければ先頭(画面のお知らせで未対応と伝わる)
}
