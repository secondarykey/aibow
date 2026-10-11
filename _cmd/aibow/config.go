package main

// config.go: 起動時の設定を環境変数から読む部分。

import (
	"fmt"
	"log"
	"os"
	"strconv"

	"aibow"
)

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getenvInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		log.Fatalf("%s は整数で指定してください: %q", key, v)
	}
	return n
}

func getenvFloat(key string, def float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		log.Fatalf("%s は数値で指定してください: %q", key, v)
	}
	return f
}

// 既定では、カレントフォルダの中の lib フォルダ(llama.cpp)と models フォルダ(モデル)を使う。
// _cmd/aibow で `go run .` すると、_cmd/aibow/lib と _cmd/aibow/models が使われる。
// lib と models はモデルを動かすこのアプリの持ち物なので、アプリのフォルダに置く。
// 別の場所に置いたとき(worktree から本体の lib を使うときなど)だけ環境変数で指定する。
type appConfig struct {
	engine       aibow.EngineConfig
	catalog      aibow.ModelCatalog
	initialModel string // 起動時に読み込むモデル。空なら一覧の先頭
}

func loadConfig() appConfig {
	c := appConfig{
		engine: aibow.EngineConfig{
			LibPath:         getenv("AIBOW_LIB", "lib"),
			NCtx:            getenvInt("AIBOW_CTX", 16384),
			GPULayers:       getenvInt("AIBOW_GPU_LAYERS", 999), // 999 = 全部 GPU に載せる
			MaxTokens:       getenvInt("AIBOW_MAX_TOKENS", 8192),
			PresencePenalty: float32(getenvFloat("AIBOW_PRESENCE_PENALTY", 1.5)),
			LlamaLog:        os.Getenv("AIBOW_LLAMA_LOG") == "1",
		},
		catalog: aibow.ModelCatalog{
			ModelsDir: getenv("AIBOW_MODELS_DIR", "models"),
			OllamaDir: aibow.OllamaModelsDir(),
		},
		initialModel: os.Getenv("AIBOW_MODEL"),
	}
	if _, err := os.Stat(c.engine.LibPath); err != nil {
		log.Fatalf("llama.cpp のフォルダ %q が見つかりません。README.md の「llama.cpp の取得」を参照してください", c.engine.LibPath)
	}
	return c
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
