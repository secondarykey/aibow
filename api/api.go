// Package api は、aibow の「頭」(llama.cpp で動かすモデルと書式)を、OpenAI 互換の HTTP の API として出す(ステップ6。0012)。
//
//	Cline など [ 会話の仕組み + 手 ] ⇄ HTTP(OpenAI 互換)⇄ aibow-server [ 頭(モデル + 書式)]
//
// 会話の仕組み(履歴の保存・compact・エージェントループ・ツールの実行)は持たない。それはクライアントの仕事。
// クライアントは毎回、履歴の全部を送ってくる(API はステートレス)。サーバーは何も覚えないので、
// 前回と同じ先頭部分の KV キャッシュを使い回せるか(engine の reuseCache)が速さを決める。どれだけ使い回せたかはログに出す。
//
// 使うのは engine(生成)と format(書式の型)だけで、ルートの aibow(会話の仕組み)は import しない。
// HTTP は標準ライブラリの net/http で書く。起動するだけの exe は _cmd/aibow-server。
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/secondarykey/aibow/engine"
	"github.com/secondarykey/aibow/format"
	"github.com/secondarykey/aibow/log"
)

// Generator は、API が使う生成の部分。本物は *engine.Engine。テストでは偽物に差し替える。
// interface は使う側に置く(ルートの aibow の Generator と同じ考え方)。
type Generator interface {
	Generate(ctx context.Context, p format.Prompt, opt engine.GenerateOptions,
		onPrompt func(prompt string), onThinking, onContent func(text string)) (format.Output, engine.GenerateStats, error)
	CountTokens(p format.Prompt) (int, error)
	// ApplyTemplate は書式を当てた文字列(POST /apply-template)
	ApplyTemplate(p format.Prompt) (string, error)
	Info() *engine.ModelInfo
}

// Defaults は、リクエストに書かれていないときに使う値。アプリの「新しい会話の既定」と同じ値を _cmd/aibow-server が渡す。
type Defaults struct {
	Temperature     float32
	MaxTokens       int // 返事の上限(思考 + 答え)
	PresencePenalty float32
	Think           bool // 思考させるか(リクエストの chat_template_kwargs.enable_thinking で変えられる)
}

// Server は API の本体。
type Server struct {
	gen      Generator
	models   Models // モデルの一覧と読み込みの係(nil なら読み込み中のモデルだけを使う)
	defaults Defaults
	// nCtx はサーバーの作業領域の大きさ。起動のときに決めた大きさで固定し、クライアントからは変えさせない
	// (共有するサーバーで1人の都合で作り直すと、ほかの人の KV キャッシュが消えるため。0015 の 3)
	nCtx int

	// turn は生成の順番。生成は作業領域が1つなので1つずつしかできない。
	// 席が1つのチャンネルにして、空くまで待たせる(順番待ち。断らない)。
	// 待っている間につながりが切れたら、列から外れる(select で r.Context().Done() も待つ)
	turn chan struct{}

	now func() time.Time // テストで時刻を固定するため
}

// New は API を作る。起動のときのモデルは gen にもう読み込んである前提(読み込みは _cmd/aibow-server が起動時に行う)。
// mm が nil なら、リクエストの model ではモデルを切り替えない(0012 の形)。nCtx はサーバーの作業領域の大きさ(GET /models で知らせる)。
func New(gen Generator, mm Models, nCtx int, d Defaults) *Server {
	return &Server{gen: gen, models: mm, nCtx: nCtx, defaults: d, turn: make(chan struct{}, 1), now: time.Now}
}

// Handler は HTTP の道筋(パスと関数の対応)を返す。
//
// Go 1.22 からの ServeMux は "GET /v1/models" のようにメソッドも書ける。メソッドが違うと 405 を返してくれる。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	// 標準の口(OpenAI 互換。Cline などから見える形は 0012 のまま)
	mux.HandleFunc("GET /v1/models", s.handleModels)
	mux.HandleFunc("POST /v1/chat/completions", s.handleChat)
	// aibow 専用の口(0015)。/v1/ と分け、意味が同じものは llama.cpp の server の名前にそろえる
	mux.HandleFunc("GET /models", s.handleServerModels)
	mux.HandleFunc("POST /models/load", s.handleLoad)
	mux.HandleFunc("POST /apply-template", s.handleApplyTemplate)
	mux.HandleFunc("POST /aibow/count-tokens", s.handleCountTokens)
	mux.HandleFunc("POST /aibow/gpu-layers", s.handleGPULayers)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "invalid_request_error", "",
			"このパスはありません: "+r.Method+" "+r.URL.Path+"(あるのは GET /v1/models・POST /v1/chat/completions と、aibow 専用の "+
				"GET /models・POST /models/load・POST /apply-template・POST /aibow/count-tokens・POST /aibow/gpu-layers です)")
	})
	return mux
}

// handleModels は GET /v1/models。サーバーで使えるモデルの一覧を全部返す(0015 の 2)。
//
// 0012 では読み込み中のモデル1つだけを返し、リクエストの model では切り替えなかった。
// ステップ8で、リクエストの model が違えば読み直すようにしたので、選べるもの全部を返す。
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, modelList{Object: "list", Data: s.modelObjects()})
}

// takeTurn は順番待ちの席が空くまで待って取る。待っている間につながりが切れたら false(列から外れる)。
// 取れたら、終わったときに releaseTurn で返す。
func (s *Server) takeTurn(r *http.Request) bool {
	start := time.Now()
	select {
	case s.turn <- struct{}{}:
		return true
	case <-r.Context().Done():
		log.Infof("順番を待っている間につながりが切れました(%.2f 秒待った)", time.Since(start).Seconds())
		return false
	}
}

func (s *Server) releaseTurn() { <-s.turn }

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
