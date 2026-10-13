package aibow

// store.go: 会話を保存する先の「約束」(interface)。
//
// 会話の仕組み(conversation.go)は、保存先が SQLite かどうかを知らない。
// ここに書いたメソッドを持っていれば何でもよい。本物は store パッケージの SQLite、
// テストではメモリ上の保存先(memstore_test.go)を使う。
// こうしておくと、会話の仕組みのテストに DB ファイルがいらない。

import (
	"errors"
	"fmt"
	"path/filepath"
	"time"
	"uuid"
)

// Store は会話とアプリ設定の保存先。
type Store interface {
	// ListConversations は会話の一覧を、更新の新しい順に返す(発言は含まない)。
	ListConversations() ([]Conversation, error)
	// Conversation は会話1つと、その発言を古い順に返す。無ければ ErrNotFound。
	Conversation(id uuid.UUID) (Conversation, []StoredMessage, error)
	CreateConversation(c Conversation) error
	// UpdateConversation はタイトル・設定・更新日時を書き換える。無ければ ErrNotFound。
	UpdateConversation(c Conversation) error
	// TouchConversation は発言が増えたときに呼ぶ。更新日時を at にし、タイトルが空のときだけ autoTitle を入れる。
	// 会話を丸ごと書き戻さないのは、生成中に画面で変えたタイトルや設定を古い値で上書きしないため。無ければ ErrNotFound。
	TouchConversation(id uuid.UUID, at time.Time, autoTitle string) error
	// DeleteConversation は会話を消す。発言もいっしょに消える。
	DeleteConversation(id uuid.UUID) error
	AddMessage(m StoredMessage) error

	// AddCompaction は要約を1件保存する(compact の summary)。
	AddCompaction(c Compaction) error
	// Compactions は会話の要約を古い順に返す。モデルに渡すのは最新の1件だけで、古いものは画面の区切りに使う。
	Compactions(conv uuid.UUID) ([]Compaction, error)

	// AppSetting はアプリ全体の設定を1つ読む。まだ保存されていなければ ok=false。
	AppSetting(key string) (value string, ok bool, err error)
	SetAppSetting(key, value string) error
}

// ErrNotFound は、指定した会話が保存先に無いときのエラー。
var ErrNotFound = errors.New("会話が見つかりません(削除された可能性があります)")

// Conversation は会話1つ。発言の列は StoredMessage として別に持つ。
type Conversation struct {
	ID        uuid.UUID            `json:"id"` // UUIDv7。作った順に並ぶ
	Title     string               `json:"title"`
	Settings  ConversationSettings `json:"settings"`
	CreatedAt time.Time            `json:"created_at"`
	UpdatedAt time.Time            `json:"updated_at"`
}

// ConversationSettings は会話ごとの設定。変えると次の送信から効く。
//
// 返事の上限(以前の max_tokens)はアプリ全体の設定に移した(WorkspaceSettings)。
// 古い会話の JSON に max_tokens が残っていても、読むときに無視される。
type ConversationSettings struct {
	System          string  `json:"system"`           // システムプロンプト。空なら付けない
	Temperature     float64 `json:"temperature"`      // 0 なら毎回一番点数の高いトークンを選ぶ
	Think           bool    `json:"think"`            // 思考させるか
	PresencePenalty float64 `json:"presence_penalty"` // 同じ言葉を繰り返しにくくする強さ。0 で無効
	Compact         string  `json:"compact"`          // 会話の上限を超えたときの方式: none / truncate / summary
	// Tools はツールを使うか。OFF ならツールの説明をプロンプトに入れない(説明だけで毎回 500 トークンほど増えるため、比べられるように)。
	// この項目より前に保存された会話は値が無いので OFF のまま。新しい会話の既定は ON
	Tools bool `json:"tools"`
	// WorkDir は作業フォルダ(絶対パス)。空なら決めていない。決めた会話で「ツールを使う」が ON なら、
	// その中のファイルを扱うツール(tool.Files)をモデルに出す。
	// 新しい会話の既定には入れない(前の会話のフォルダを引き継いで、別のフォルダを書き換えてしまうのを防ぐため)
	WorkDir string `json:"work_dir"`
}

// compact の方式(compact.go)
const (
	CompactNone     = "none"     // 何もしない。上限を超えたら生成する前に止める
	CompactTruncate = "truncate" // 古い発言から順に、モデルに渡すのをやめる
	CompactSummary  = "summary"  // 古い部分をモデル自身に要約させ、「要約 + 最近の発言」を渡す
)

// normalized は、この機能より前に保存された設定(compact が空)を、新しい会話の既定と同じ summary にする。
func (s ConversationSettings) normalized() ConversationSettings {
	switch s.Compact {
	case CompactNone, CompactTruncate, CompactSummary:
	default:
		s.Compact = CompactSummary
	}
	return s
}

// asDefaults は「新しい会話の既定の設定」にするときの形。作業フォルダは外す(会話ごとに毎回選ぶ)。
func (s ConversationSettings) asDefaults() ConversationSettings {
	s = s.normalized()
	s.WorkDir = ""
	return s
}

// validate は、保存する前に設定を確かめる。
func (s ConversationSettings) validate() error {
	// 作業フォルダは画面のダイアログで選ぶので、ふつうは絶対パス。相対パスだと、アプリを起動した場所で意味が変わってしまう
	if s.WorkDir != "" && !filepath.IsAbs(s.WorkDir) {
		return fmt.Errorf("作業フォルダは絶対パスで指定してください(%s)", s.WorkDir)
	}
	return nil
}

// StoredMessage は保存された発言1件。
//
// モデルに渡すのは Role・Content・Thinking・ToolCalls(format.go の Message)。思考をプロンプトに入れるかは書式が決める。
// Model・Stats・ToolResult は、画面で後から見返すために残している。
//
// 1回の送信でツールを使うと、発言は次のように並ぶ(どれも1件ずつ保存する):
//
//	user → assistant(ToolCalls あり)→ tool → tool → assistant(ToolCalls あり)→ tool → assistant(答え)
type StoredMessage struct {
	ID             uuid.UUID   `json:"id"` // UUIDv7。発言の順番はこの並びで決まる
	ConversationID uuid.UUID   `json:"conversation_id"`
	Role           string      `json:"role"`                  // system / user / assistant / tool
	Content        string      `json:"content"`               // 答え。答えが出なかった回は理由の文。tool はツールの結果の文
	Thinking       string      `json:"thinking"`              // 思考
	Model          string      `json:"model"`                 // どのモデルの返事か(assistant のみ)
	Stats          *ReplyStats `json:"stats"`                 // 生成の記録(assistant のみ)
	ToolCalls      []ToolCall  `json:"tool_calls,omitempty"`  // その回のツール呼び出し(assistant のみ)
	ToolResult     *ToolResult `json:"tool_result,omitempty"` // どの呼び出しの結果か(tool のみ)
	CreatedAt      time.Time   `json:"created_at"`
}

// ToolResult はツールの結果1件の記録(結果の文そのものは StoredMessage.Content)。
type ToolResult struct {
	Call  ToolCall `json:"call"`  // どの呼び出しの結果か(書式に依存しない形。名前・引数)
	Error bool     `json:"error"` // エラーだったか(Content は「エラー: …」)
	Sec   float64  `json:"sec"`   // 実行にかかった秒数
}

// Compaction は要約1件(compact の summary)。
//
// 保存するのは「どの発言までを要約したか」と「要約の文」だけ。会話の上限に依存する値(どこから渡すか)は
// 保存せず、毎回その時点の上限とモデルで計算し直す。
type Compaction struct {
	ID             uuid.UUID     `json:"id"` // UUIDv7。最新の要約はこの並びで決まる
	ConversationID uuid.UUID     `json:"conversation_id"`
	UptoMessageID  uuid.UUID     `json:"upto_message_id"` // この発言(を含む)までを要約した
	Summary        string        `json:"summary"`         // 要約の文。前回までの要約も含んだ、積み重ねた結果
	Model          string        `json:"model"`           // 要約を作ったモデル
	Stats          *CompactStats `json:"stats"`           // 記録用。判断には使わない
	CreatedAt      time.Time     `json:"created_at"`
}

// CompactStats は要約1件の記録。画面の区切りを開いたときに見せる。
// トークン数は、そのときのモデルで数えた値(モデルが変わると変わるので、判断には使わない)。
type CompactStats struct {
	GenerateStats         // 要約の生成の記録(要約を作るのに読ませたトークン数・生成したトークン数など)
	BeforeTokens  int     `json:"before_tokens"` // 要約する前の、モデルに渡す文字列のトークン数
	AfterTokens   int     `json:"after_tokens"`  // 要約したあとの、モデルに渡す文字列のトークン数
	Messages      int     `json:"messages"`      // 今回の要約に入れた発言の数
	Sec           float64 `json:"sec"`           // 要約にかかった秒数
}

// ReplyStats は返事1件の記録。エンジンの GenerateStats に、会話の仕組みで測った値を足したもの。
// 再起動した後も、画面の統計行や「思考の過程(N秒)」を作り直せるように保存する。
type ReplyStats struct {
	GenerateStats
	FirstSec float64 `json:"first_sec"` // 送信から最初の文字が出るまでの秒数
	ThinkSec float64 `json:"think_sec"` // 思考にかかった秒数
	NoAnswer bool    `json:"no_answer"` // 答えが出ないまま終わり、Content が理由の文になっている
}
