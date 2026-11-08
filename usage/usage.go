// Package usage は、aibow-server の使用量(0033)。生成1回ごとの記録の形と、記録のために生成に付ける付箋。
//
// ローカルLLMの「使用量」はお金ではなく、GPU の席(api.Seat は1つ)をどれだけ使ったか。だれかが長く使うと全員が待つ。
// だから主に見る数字は「GPU の秒数」(プロンプトの秒数 + 生成の秒数 + 画像のエンコードの秒数)にする。
//
// 記録を書くのは aibow-server の「記録付きの engine」(_cmd/aibow-server の usage.go)。生成が通る2本の道
// (OpenAI 互換の口・ブラウザの会話)の両方に書き足さず、engine を渡すところで1か所だけ包む(デコレータの形)。
// 包む側は「だれが・どの道で・何のために」を知らないので、生成を呼ぶ側が ctx に付箋(Job)を付けて運ぶ。
// Generate の引数に足さずに ctx で運ぶのは、間の層(会話の仕組み・api/client)を変えずに通すため(利用者を ctx で運ぶのと同じ考え方。0017 の 12)。
//
// このパッケージは何にも依存しない(会話の仕組み・api・api/client・ui・store のどれからも使うため)。
// メーターの aibow.Usage(会話の上限に対して何トークン使っているか。0009)とは別のもの。こちらは、だれがどれだけ GPU を使ったか。
// はじめは genlog(生成のログ)という名前だったが、log パッケージと紛らわしく役割が読めないので、機能の名前に合わせた(2026-11-08、ユーザーの指示)。
package usage

import (
	"context"
	"sync"
	"time"
	"uuid"
)

// 道(Job.Route)。どこから来た生成か。
const (
	RouteBrowser = "browser" // ブラウザの会話(画面用の口)
	RouteApp     = "app"     // aibow-server につないだ画面のアプリ(OpenAI 互換の口に aibow 欄を付けて来る)
	RouteAPI     = "api"     // それ以外の OpenAI 互換のクライアント(Cline・curl など)
)

// 用途(Job.Purpose)。何のための生成か。ツールのループの1回も「返事」に数える。
const (
	PurposeReply   = "reply"   // 返事
	PurposeSummary = "summary" // compact の要約
)

// Job は生成に付ける付箋。分からない欄は空(ゼロ)のまま。
type Job struct {
	Route        string
	Purpose      string
	Conversation uuid.UUID // 会話の ID(分かるときだけ)
}

type jobKey struct{}

// JobOf は ctx の付箋(付いていなければゼロ)。
func JobOf(ctx context.Context) Job {
	j, _ := ctx.Value(jobKey{}).(Job)
	return j
}

// WithRoute は道を付ける(用途と会話は残す)。道を決めるのは HTTP の口(api と ui の Handler)。
func WithRoute(ctx context.Context, route string) context.Context {
	j := JobOf(ctx)
	j.Route = route
	return context.WithValue(ctx, jobKey{}, j)
}

// WithPurpose は用途と会話を付ける(道は残す)。付けるのは会話の仕組み(返事と要約を生成するところ)と、
// アプリから aibow 欄で知らされた OpenAI 互換の口。
func WithPurpose(ctx context.Context, purpose string, conversation uuid.UUID) context.Context {
	j := JobOf(ctx)
	j.Purpose, j.Conversation = purpose, conversation
	return context.WithValue(ctx, jobKey{}, j)
}

// ---- 順番待ちの秒数 --------------------------------------------------------------------
//
// 順番待ちは、OpenAI 互換の口ではリクエスト(= 生成1回)ごと、ブラウザの会話では送信ごと(ツールのループで何回も生成する)に1回。
// 送信ごとの待ちを生成のたびに書くと何回も数えてしまうので、最初に読んだ1回だけが受け取る(TakeWait)。

type waitKey struct{}

type waited struct {
	mu    sync.Mutex
	d     time.Duration
	taken bool
}

// WithWait は、順番を待った時間を付ける。
func WithWait(ctx context.Context, d time.Duration) context.Context {
	return context.WithValue(ctx, waitKey{}, &waited{d: d})
}

// TakeWait は、付いている待ち時間を1回だけ返す(2回目からと、付いていないときは ok=false)。
func TakeWait(ctx context.Context) (d time.Duration, ok bool) {
	w, _ := ctx.Value(waitKey{}).(*waited)
	if w == nil {
		return 0, false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.taken {
		return 0, false
	}
	w.taken = true
	return w.d, true
}

// ---- 記録の形 --------------------------------------------------------------------------

// Record は生成1回の記録(server.db の generation_log の1行)。会話の中身(発言の文章)は持たない(0033 の決定事項 3)。
// 利用者の名前を写して持つのは、利用者を消したあとも、だれの分かが読めるように(決定事項 7)。
type Record struct {
	ID           uuid.UUID
	At           time.Time // 生成を始めた時刻
	User         uuid.UUID // 呼んだ人(分からなければゼロ)
	UserName     string
	Route        string
	Purpose      string
	Conversation uuid.UUID // 分からなければゼロ
	Model        string    // モデルの ID
	PromptTokens int       // プロンプトのトークン数(KV キャッシュを使い回した分も入る)
	ReusedTokens int       // そのうち使い回した数。実際に計算したのは PromptTokens - ReusedTokens
	GenTokens    int
	ImageTokens  int
	PromptSec    float64
	GenSec       float64
	EncodeSec    float64
	WaitSec      *float64 // 順番待ちの秒数(分からなければ nil)
	StopReason   string   // eos / length / context / cancel / stop、失敗したときは error
}

// GPUSec は GPU を使った秒数。モデルの読み込みの秒数は入れない(読み込みは、会話をした人のせいとは限らないため。決定事項 7)。
func (r Record) GPUSec() float64 { return r.PromptSec + r.GenSec + r.EncodeSec }

// Total は、ある範囲の生成の合計(使用量の画面の表の1行)。Key は何でまとめたか(利用者の ID・モデルの ID・日付 YYYY-MM-DD)。
type Total struct {
	Key string `json:"key"`
	// 利用者でまとめたときだけ。Name は記録に写した名前、DisplayName は今の表示名、Deleted はもういない人(消された)
	Name        string `json:"name,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	Deleted     bool   `json:"deleted,omitempty"`

	Count        int     `json:"count"`     // 生成の回数
	Summaries    int     `json:"summaries"` // うち要約の回数
	GPUSec       float64 `json:"gpu_sec"`
	GenTokens    int     `json:"gen_tokens"`
	PromptTokens int     `json:"prompt_tokens"`
	ReusedTokens int     `json:"reused_tokens"`
	ImageTokens  int     `json:"image_tokens"`
	WaitSec      float64 `json:"wait_sec"` // 順番待ちの秒数の合計(分かった分だけ)
}

// Mine は、本人の使用量(設定の「アカウント」のタブ。0033 の決定事項 13)。
type Mine struct {
	Today Total `json:"today"`
	Week  Total `json:"week"`  // 7日(今日を含む)
	Month Total `json:"month"` // 30日(今日を含む)
}

// Since は、今日を含めて days 日の範囲の始まり(days 日前の 0 時。時刻は now の場所(サーバーの PC)で数える)。
func Since(now time.Time, days int) time.Time {
	y, m, d := now.Date()
	return time.Date(y, m, d-(days-1), 0, 0, 0, 0, now.Location())
}
