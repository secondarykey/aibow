package main

// config.go: 起動時の設定を環境変数から読む部分。

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"

	"aibow"
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
	engine       aibow.EngineConfig
	catalog      aibow.ModelCatalog
	dbPath       string // 会話を保存する SQLite のファイル
	settingsPath string // 設定ファイル(settings.json。手で書く設定。今は MCP のサーバーだけ)
	toolsPath    string // ツールの扱いの PC の既定(tools.json。画面で変えたものを aibow が書く)
	initialModel string // AIBOW_MODEL。空なら最後に使ったモデル → 一覧の先頭

	// app は、画面で変えられるアプリ全体の設定の初期値。
	// DB にまだ値が無いときだけ使う(画面で一度変えると DB の値が優先される)。
	app aibow.AppSettings

	// notices は起動時に画面のトーストで伝えること(使われなくなった環境変数など)。ログにも出す
	notices []string
}

// defaultSystemPrompt は新しい会話の既定のシステムプロンプト(画面で変えられる)。
const defaultSystemPrompt = "あなたは親切なアシスタントです。日本語で簡潔に答えてください。"

func loadConfig() (appConfig, error) {
	var env envReader
	c := appConfig{
		engine: aibow.EngineConfig{
			LibPath:  env.str("AIBOW_LIB", "lib"),
			LlamaLog: os.Getenv("AIBOW_LLAMA_LOG") == "1",
		},
		catalog: aibow.ModelCatalog{
			ModelsDir: env.str("AIBOW_MODELS_DIR", "models"),
			OllamaDir: aibow.OllamaModelsDir(),
		},
		dbPath:       env.str("AIBOW_DB", configFile("aibow.db")),
		settingsPath: env.str("AIBOW_SETTINGS", configFile("settings.json")),
		toolsPath:    env.str("AIBOW_TOOLS", configFile("tools.json")),
		initialModel: os.Getenv("AIBOW_MODEL"),
		app: aibow.AppSettings{
			Workspace: aibow.WorkspaceSettings{
				GPULayers:     env.int("AIBOW_GPU_LAYERS", 999), // 999 = 全部 GPU に載せる
				HistoryTokens: env.int("AIBOW_HISTORY_TOKENS", 8192),
				ReplyTokens:   env.int("AIBOW_MAX_TOKENS", 8192),
			},
			Defaults: aibow.ConversationSettings{
				System:          defaultSystemPrompt,
				Temperature:     0.7,
				Think:           true,
				PresencePenalty: env.float("AIBOW_PRESENCE_PENALTY", 1.5),
				Compact:         aibow.CompactSummary,
			},
		},
	}
	// AIBOW_CTX(コンテキスト長)は、会話の上限 + 返事の上限から aibow が計算するようになったので使わない
	if v := os.Getenv("AIBOW_CTX"); v != "" {
		c.notices = append(c.notices, fmt.Sprintf("環境変数 AIBOW_CTX(%s)は使われなくなりました。"+
			"作業領域の大きさは「会話の上限 + 返事の上限」から計算します。"+
			"会話の上限は AIBOW_HISTORY_TOKENS、返事の上限は AIBOW_MAX_TOKENS か、画面の設定で変えてください", v))
	}
	for _, n := range []struct {
		key string
		v   int
	}{{"AIBOW_HISTORY_TOKENS", c.app.Workspace.HistoryTokens}, {"AIBOW_MAX_TOKENS", c.app.Workspace.ReplyTokens}} {
		if n.v <= 0 {
			env.errs = append(env.errs, fmt.Errorf("%s は 1 以上で指定してください: %d", n.key, n.v))
		}
	}
	if _, err := os.Stat(c.engine.LibPath); err != nil {
		// 相対パスのままだと「どこを探したのか」が分からないので、絶対パスで出す
		env.errs = append(env.errs, fmt.Errorf("llama.cpp のフォルダが見つかりません: %s\n"+
			"(相対パスはカレントフォルダが基準です。_cmd/aibow で起動するか、AIBOW_LIB で場所を指定してください。"+
			"取得方法は README.md の「llama.cpp の取得」を参照)", absPath(c.engine.LibPath)))
	}
	if c.dbPath == "" {
		env.errs = append(env.errs, errors.New("会話を保存する場所が決められません。AIBOW_DB で DB ファイルの場所を指定してください"))
	}
	return c, errors.Join(env.errs...)
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
// AIBOW_MODEL → 最後に使ったモデル → 一覧の先頭、の順。
func pickInitialModel(cfg appConfig, app aibow.AppSettings, models []aibow.ModelEntry) (aibow.ModelEntry, error) {
	if cfg.initialModel != "" {
		return cfg.catalog.Find(cfg.initialModel)
	}
	if app.Workspace.Model != "" {
		m, err := cfg.catalog.Find(app.Workspace.Model)
		if err == nil {
			return m, nil
		}
		// 消したモデルなどで見つからなくても、一覧の先頭で起動する
		log.Printf("※ 最後に使ったモデルが見つかりません(%v)。一覧の先頭を使います", err)
	}
	if len(models) == 0 {
		return aibow.ModelEntry{}, fmt.Errorf("モデルが1つも見つかりません。models フォルダに GGUF を置くか、ollama pull してください")
	}
	return models[0], nil
}
