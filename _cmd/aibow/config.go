package main

// config.go: 起動時の設定を環境変数から読む部分。

import (
	"errors"
	"fmt"
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
	initialModel string // 起動時に読み込むモデル。空なら一覧の先頭
}

func loadConfig() (appConfig, error) {
	var env envReader
	c := appConfig{
		engine: aibow.EngineConfig{
			LibPath:         env.str("AIBOW_LIB", "lib"),
			NCtx:            env.int("AIBOW_CTX", 16384),
			GPULayers:       env.int("AIBOW_GPU_LAYERS", 999), // 999 = 全部 GPU に載せる
			MaxTokens:       env.int("AIBOW_MAX_TOKENS", 8192),
			PresencePenalty: float32(env.float("AIBOW_PRESENCE_PENALTY", 1.5)),
			LlamaLog:        os.Getenv("AIBOW_LLAMA_LOG") == "1",
		},
		catalog: aibow.ModelCatalog{
			ModelsDir: env.str("AIBOW_MODELS_DIR", "models"),
			OllamaDir: aibow.OllamaModelsDir(),
		},
		initialModel: os.Getenv("AIBOW_MODEL"),
	}
	if _, err := os.Stat(c.engine.LibPath); err != nil {
		// 相対パスのままだと「どこを探したのか」が分からないので、絶対パスで出す
		env.errs = append(env.errs, fmt.Errorf("llama.cpp のフォルダが見つかりません: %s\n"+
			"(相対パスはカレントフォルダが基準です。_cmd/aibow で起動するか、AIBOW_LIB で場所を指定してください。"+
			"取得方法は README.md の「llama.cpp の取得」を参照)", absPath(c.engine.LibPath)))
	}
	return c, errors.Join(env.errs...)
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

func pickInitialModel(cfg appConfig, models []aibow.ModelEntry) (aibow.ModelEntry, error) {
	if cfg.initialModel != "" {
		return cfg.catalog.Find(cfg.initialModel)
	}
	if len(models) == 0 {
		return aibow.ModelEntry{}, fmt.Errorf("モデルが1つも見つかりません。models フォルダに GGUF を置くか、ollama pull してください")
	}
	return models[0], nil
}
