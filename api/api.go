// Package api は、aibow の「頭」(llama.cpp で動かすモデルと書式)を、OpenAI 互換の HTTP の API として出す(ステップ6。0012)。
//
//	Cline など [ 会話の仕組み + 手 ] ⇄ HTTP(OpenAI 互換)⇄ aibow-api [ 頭(モデル + 書式)]
//
// 会話の仕組み(履歴の保存・compact・エージェントループ・ツールの実行)は持たない。それはクライアントの仕事。
// クライアントは毎回、履歴の全部を送ってくる(API はステートレス)。サーバーは何も覚えないので、
// 前回と同じ先頭部分の KV キャッシュを使い回せるか(engine の reuseCache)が速さを決める。どれだけ使い回せたかはログに出す。
//
// 使うのは engine(生成)と format(書式の型)だけで、ルートの aibow(会話の仕組み)は import しない。
// HTTP は標準ライブラリの net/http で書く。起動するだけの exe は _cmd/aibow-api。
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/secondarykey/aibow/engine"
	"github.com/secondarykey/aibow/format"
)

// Generator は、API が使う生成の部分。本物は *engine.Engine。テストでは偽物に差し替える。
// interface は使う側に置く(ルートの aibow の Generator と同じ考え方)。
type Generator interface {
	Generate(ctx context.Context, p format.Prompt, opt engine.GenerateOptions,
		onPrompt func(prompt string), onThinking, onContent func(text string)) (format.Output, engine.GenerateStats, error)
	CountTokens(p format.Prompt) (int, error)
	Info() *engine.ModelInfo
}

// Defaults は、リクエストに書かれていないときに使う値。アプリの「新しい会話の既定」と同じ値を _cmd/aibow-api が渡す。
type Defaults struct {
	Temperature     float32
	MaxTokens       int // 返事の上限(思考 + 答え)
	PresencePenalty float32
	Think           bool // 思考させるか(リクエストの chat_template_kwargs.enable_thinking で変えられる)
}

// Server は API の本体。
type Server struct {
	gen      Generator
	defaults Defaults

	// turn は生成の順番。生成は作業領域が1つなので1つずつしかできない。
	// 席が1つのチャンネルにして、空くまで待たせる(順番待ち。断らない)。
	// 待っている間につながりが切れたら、列から外れる(select で r.Context().Done() も待つ)
	turn chan struct{}

	now func() time.Time // テストで時刻を固定するため
}

// New は API を作る。モデルは gen にもう読み込んである前提(読み込みは _cmd/aibow-api が起動時に行う)。
func New(gen Generator, d Defaults) *Server {
	return &Server{gen: gen, defaults: d, turn: make(chan struct{}, 1), now: time.Now}
}

// Handler は HTTP の道筋(パスと関数の対応)を返す。
//
// Go 1.22 からの ServeMux は "GET /v1/models" のようにメソッドも書ける。メソッドが違うと 405 を返してくれる。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/models", s.handleModels)
	mux.HandleFunc("POST /v1/chat/completions", s.handleChat)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "invalid_request_error", "",
			"このパスはありません: "+r.Method+" "+r.URL.Path+"(あるのは GET /v1/models と POST /v1/chat/completions だけです)")
	})
	return mux
}

// handleModels は GET /v1/models。読み込み中のモデル1つだけを返す。
//
// リクエストの model ではモデルを切り替えない(0012 の未決事項をユーザーと決めた。2026-10-19)。
// 切り替えは数秒〜十数秒かかり、KV キャッシュも捨てるため。使うモデルは起動の引数で決める。
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	info := s.gen.Info()
	data := []modelObject{}
	if info != nil {
		data = append(data, modelObject{ID: info.ID, Object: "model", Created: s.now().Unix(), OwnedBy: "aibow"})
	}
	writeJSON(w, http.StatusOK, modelList{Object: "list", Data: data})
}

// writeJSON は v を JSON で返す。
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// writeError は OpenAI と同じ形のエラーを返す:{"error": {"message", "type", "code"}}。
// クライアントは type と code で見分ける(例: 長すぎるときの code は context_length_exceeded)。
func writeError(w http.ResponseWriter, status int, typ, code, msg string) {
	writeJSON(w, status, errorBody{Error: errorObject{Message: msg, Type: typ, Code: code}})
}
