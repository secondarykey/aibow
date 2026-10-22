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
	"net"
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
	// Loading は、入れ替えている先のモデルの ID(入れ替えていなければ空)。入れ替えの間は Info が nil になる
	Loading() string
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
	// settings は、表示だけのサーバーの設定(GET /aibow/settings。8-4)。SetSettings で渡す
	settings Settings

	// seat は生成の順番待ちの席(seat.go)。待っている間につながりが切れたら、列から外れる。
	// aibow-server の中のブラウザの会話も同じ席を使う(Seat で渡す。0016 の 11)
	seat *Seat

	// admin は、リクエストを送ってきた人が管理者か(SetAdmin で渡す。0018 の 11-0)。nil なら誰も管理者でない
	admin func(r *http.Request) bool

	now func() time.Time // テストで時刻を固定するため
}

// SetAdmin は、リクエストを送ってきた人が管理者かを決める係を渡す(0018 の 11-0)。
// 誰かを決めるのは aibow-server(API キーの持ち主。auth)なので、api は auth を import せずに、決める係だけを受け取る。
func (s *Server) SetAdmin(f func(r *http.Request) bool) { s.admin = f }

// New は API を作る。起動のときのモデルは gen にもう読み込んである前提(読み込みは _cmd/aibow-server が起動時に行う)。
// mm が nil なら、リクエストの model ではモデルを切り替えない(0012 の形)。nCtx はサーバーの作業領域の大きさ(GET /models で知らせる)。
func New(gen Generator, mm Models, nCtx int, d Defaults) *Server {
	return &Server{gen: gen, models: mm, nCtx: nCtx, defaults: d, seat: NewSeat(), now: time.Now}
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
	// サーバーの PC の事情を変える口(降ろす・GPU の層数・置き場所)は、サーバーの PC からか管理者だけ(pcSide。0017 の 10-2・0018 の 11-0)
	mux.HandleFunc("POST /models/unload", s.pcSide(s.handleUnload))
	mux.HandleFunc("POST /apply-template", s.handleApplyTemplate)
	mux.HandleFunc("POST /aibow/count-tokens", s.handleCountTokens)
	mux.HandleFunc("POST /aibow/gpu-layers", s.pcSide(s.handleGPULayers))
	mux.HandleFunc("GET /aibow/places", s.handlePlaces)
	mux.HandleFunc("POST /aibow/places/add", s.pcSide(s.handleAddPlace))
	mux.HandleFunc("POST /aibow/places/remove", s.pcSide(s.handleRemovePlace))
	mux.HandleFunc("GET /aibow/settings", s.handleSettings)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "invalid_request_error", "",
			"このパスはありません: "+r.Method+" "+r.URL.Path+"(あるのは GET /v1/models・POST /v1/chat/completions と、aibow 専用の "+
				"GET /models・POST /models/load・POST /models/unload・POST /apply-template・POST /aibow/count-tokens・POST /aibow/gpu-layers・"+
				"GET /aibow/places・POST /aibow/places/add・POST /aibow/places/remove・GET /aibow/settings です)")
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

// Seat は生成の順番待ちの席。aibow-server の中のブラウザの会話(ui)に渡し、同じ席で順番を待たせる(0016 の 11)。
func (s *Server) Seat() *Seat { return s.seat }

// takeTurn は順番待ちの席が空くまで待って取る。待っている間につながりが切れたら false(列から外れる)。
// 取れたら、終わったときに releaseTurn で返す。
func (s *Server) takeTurn(r *http.Request) bool {
	start := time.Now()
	if s.seat.Take(r.Context()) {
		return true
	}
	log.Infof("順番を待っている間につながりが切れました(%.2f 秒待った)", time.Since(start).Seconds())
	return false
}

func (s *Server) releaseTurn() { s.seat.Release() }

// writeJSON は v を JSON で返す。
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// writeError は OpenAI と同じ形のエラーを返す:{"error": {"message", "type", "code"}}。
// クライアントは type と code で見分ける(例: 長すぎるときの code は context_length_exceeded)。
// pcSide は、サーバーの PC の上(127.0.0.1・::1)からの呼び出しと、管理者の呼び出しだけを通す
// (0017 の 10-2 で「サーバーの PC からだけ」にし、0018 の決定事項 8 で管理者にも許した)。
// モデルを降ろす・GPU の層数・置き場所は、サーバーの PC と、ほかの人が使っているモデルに関わるので、ほかの人には変えさせない。
// aibow-server が 127.0.0.1 だけで待ち受けているときは、いつも通る。
func (s *Server) pcSide(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !IsLocal(r) && (s.admin == nil || !s.admin(r)) {
			log.Warnf("サーバーの PC からか管理者だけの操作なので断りました: %s %s(%s)", r.Method, r.URL.Path, r.RemoteAddr)
			writeError(w, http.StatusForbidden, "permission_error", "local_only",
				"この操作(モデルを降ろす・GPU の層数・モデルの置き場所)は、aibow-server を動かしている PC からか、管理者だけができます")
			return
		}
		h(w, r)
	}
}

// IsLocal は、リクエストがサーバーの PC の上(127.0.0.1・::1)から来たか。
func IsLocal(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(host)
	return err == nil && ip != nil && ip.IsLoopback()
}

func writeError(w http.ResponseWriter, status int, typ, code, msg string) {
	writeJSON(w, status, errorBody{Error: errorObject{Message: msg, Type: typ, Code: code}})
}
