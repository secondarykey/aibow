// aibow: ローカルLLMを自分で動かして、仕組みを学ぶためのチャットアプリ
//
//	ブラウザ(static/index.html) ⇄ HTTP ⇄ aibow ⇄ 関数呼び出し ⇄ llama.cpp(DLL) ⇄ GPU
//
// Ollama のような別のサーバーは使わず、aibow の中で llama.cpp を直接動かしている。
// HTTP は「ブラウザの画面と aibow をつなぐため」だけに使っている。
//
// ファイルの役割:
//
//	main.go   … 設定の読み込み、画面の配信、ブラウザとのやり取り(このファイル)
//	engine.go … llama.cpp を呼んで文章を生成する部分
//	prompt.go … 会話をモデル用の書式(ChatML)の文字列にする部分
//	stream.go … 生成された文字を、思考と答えに振り分ける部分
//	models.go … 使えるモデル(models フォルダと Ollama)の一覧を作る部分
package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strconv"
)

// static フォルダの中身を実行ファイルに埋め込む。
//
//go:embed static
var staticFiles embed.FS

// ---- 設定(環境変数) ---------------------------------------------------------------

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

// 既定では、プロジェクトの中の lib フォルダ(llama.cpp)と models フォルダ(モデル)を使う。
// 別の場所に置いたときだけ環境変数で指定する。
type appConfig struct {
	engine       EngineConfig
	catalog      ModelCatalog
	initialModel string // 起動時に読み込むモデル。空なら一覧の先頭
	addr         string
}

func loadConfig() appConfig {
	c := appConfig{
		engine: EngineConfig{
			LibPath:         getenv("AIBOW_LIB", "lib"),
			NCtx:            getenvInt("AIBOW_CTX", 16384),
			GPULayers:       getenvInt("AIBOW_GPU_LAYERS", 999), // 999 = 全部 GPU に載せる
			MaxTokens:       getenvInt("AIBOW_MAX_TOKENS", 8192),
			PresencePenalty: float32(getenvFloat("AIBOW_PRESENCE_PENALTY", 1.5)),
			LlamaLog:        os.Getenv("AIBOW_LLAMA_LOG") == "1",
		},
		catalog: ModelCatalog{
			ModelsDir: getenv("AIBOW_MODELS_DIR", "models"),
			OllamaDir: ollamaModelsDir(),
		},
		initialModel: os.Getenv("AIBOW_MODEL"),
		addr:         getenv("ADDR", "localhost:8000"),
	}
	if _, err := os.Stat(c.engine.LibPath); err != nil {
		log.Fatalf("llama.cpp のフォルダ %q が見つかりません。README.md の「llama.cpp の取得」を参照してください", c.engine.LibPath)
	}
	return c
}

// ---- 起動 ---------------------------------------------------------------------------

func main() {
	cfg := loadConfig()

	engine, err := NewEngine(cfg.engine)
	if err != nil {
		log.Fatal(err)
	}
	defer engine.Close()
	for _, d := range engine.Devices {
		log.Printf("計算装置: %s", d)
	}
	if !engine.HasGPU {
		log.Printf("※ GPU が見つかりません。CPU だけで計算するので遅くなります(README の「llama.cpp の取得」を確認)")
	}

	// 使えるモデルの一覧
	models := cfg.catalog.List()
	log.Printf("モデルの置き場所: %s / Ollama: %s", cfg.catalog.ModelsDir, cfg.catalog.OllamaDir)
	for _, m := range models {
		log.Printf("  %-8s %-40s %.1f GB", m.Source, m.Name, m.SizeGB)
	}

	// 起動時のモデルを読み込む。見つからなくても起動はして、画面で選べるようにする。
	if m, err := pickInitialModel(cfg, models); err != nil {
		log.Printf("※ %v", err)
	} else {
		loadAndLog(engine, m)
	}

	mux := http.NewServeMux()
	staticRoot, _ := fs.Sub(staticFiles, "static")
	mux.Handle("GET /", http.FileServerFS(staticRoot))
	mux.HandleFunc("GET /api/models", modelsHandler(engine, cfg.catalog))
	mux.HandleFunc("POST /api/model", switchModelHandler(engine, cfg.catalog))
	mux.HandleFunc("POST /api/chat", chatHandler(engine))

	log.Printf("aibow 起動: http://%s", cfg.addr)
	log.Fatal(http.ListenAndServe(cfg.addr, mux))
}

func pickInitialModel(cfg appConfig, models []ModelEntry) (ModelEntry, error) {
	if cfg.initialModel != "" {
		return cfg.catalog.Find(cfg.initialModel)
	}
	if len(models) == 0 {
		return ModelEntry{}, fmt.Errorf("モデルが1つも見つかりません。models フォルダに GGUF を置くか、ollama pull してください")
	}
	return models[0], nil
}

func loadAndLog(engine *Engine, m ModelEntry) (*ModelInfo, error) {
	log.Printf("モデルを読み込み中: %s (%s)", m.Name, m.Path)
	i, err := engine.LoadModel(m)
	if err != nil {
		log.Printf("※ %v", err)
		return nil, err
	}
	log.Printf("読み込み完了 (%.1f 秒): %s / %.2fB パラメータ / %.2f GB / %d 層 / GPU に載せる層: %d / コンテキスト長: %d",
		i.LoadSec, i.Desc, i.ParamsB, i.SizeGB, i.Layers, i.GPULayers, i.NCtx)
	return i, nil
}

// ---- モデルの一覧と切り替え -------------------------------------------------------------

// GET /api/models: 使えるモデルの一覧と、今読み込んでいるモデル
func modelsHandler(engine *Engine, catalog ModelCatalog) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"models":  catalog.List(),
			"current": engine.Info(), // 未読み込みなら null
			"gpu":     engine.HasGPU,
		})
	}
}

// POST /api/model {"id": "ollama:qwen3.5:9b"}: モデルを切り替える。読み込み終わるまで返らない。
func switchModelHandler(engine *Engine, catalog ModelCatalog) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "リクエストのJSONが不正です"})
			return
		}
		m, err := catalog.Find(req.ID)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		info, err := loadAndLog(engine, m)
		if err != nil {
			// 失敗しても元のモデルに戻っているので、今のモデルも一緒に返す
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error(), "current": engine.Info()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"current": info})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// ---- ブラウザとのやり取り -------------------------------------------------------------

// ブラウザ → aibow
type ChatRequest struct {
	Messages    []Message `json:"messages"`
	Think       *bool     `json:"think"`
	Temperature *float64  `json:"temperature"`
}

// aibow → ブラウザ。Server-Sent Events(SSE)で1つずつ送る。
//
//	prompt   : モデルに渡した文字列(学習用に画面に表示する)
//	thinking : 思考の断片
//	content  : 答えの断片
//	stats    : 統計(トークン数、速度など)
//	error    : エラー
//	done     : 終わり
type event struct {
	Type    string         `json:"type"`
	Text    string         `json:"text,omitempty"`
	Message string         `json:"message,omitempty"`
	Stats   *GenerateStats `json:"stats,omitempty"`
}

func chatHandler(engine *Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "リクエストのJSONが不正です: "+err.Error(), http.StatusBadRequest)
			return
		}
		think := req.Think == nil || *req.Think
		temp := 0.7
		if req.Temperature != nil {
			temp = *req.Temperature
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		flusher, _ := w.(http.Flusher)
		send := func(ev event) {
			b, _ := json.Marshal(ev)
			fmt.Fprintf(w, "data: %s\n\n", b)
			if flusher != nil {
				flusher.Flush() // 溜めずに、すぐブラウザへ送る
			}
		}

		// r.Context() は、ブラウザが接続を切る(停止ボタン)とキャンセルされる。
		// エンジンはそれを見て生成を止めるので、GPU が無駄に動き続けない。
		st, err := engine.Generate(r.Context(), req.Messages, think, float32(temp),
			func(p string) { send(event{Type: "prompt", Text: p}) },
			func(t string) { send(event{Type: "thinking", Text: t}) },
			func(t string) { send(event{Type: "content", Text: t}) },
		)
		if err != nil {
			log.Printf("エラー: %v", err)
			send(event{Type: "error", Message: err.Error()})
			return
		}

		log.Printf("プロンプト %d トークン(再利用 %d)%.2f 秒 / 生成 %d トークン %.2f 秒(%.1f tok/s)/ 終了理由: %s",
			st.PromptTokens, st.ReusedTokens, st.PromptSec, st.GenTokens, st.GenSec, tokPerSec(st), st.StopReason)
		send(event{Type: "stats", Stats: &st})
		send(event{Type: "done"})
	}
}

func tokPerSec(st GenerateStats) float64 {
	if st.GenSec == 0 {
		return 0
	}
	return float64(st.GenTokens) / st.GenSec
}
