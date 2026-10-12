package aibow

// store.go: 会話を保存する先の「約束」(interface)。
//
// 会話の仕組み(conversation.go)は、保存先が SQLite かどうかを知らない。
// ここに書いたメソッドを持っていれば何でもよい。本物は store パッケージの SQLite、
// テストではメモリ上の保存先(memstore_test.go)を使う。
// こうしておくと、会話の仕組みのテストに DB ファイルがいらない。

import (
	"errors"
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
type ConversationSettings struct {
	System          string  `json:"system"`           // システムプロンプト。空なら付けない
	MaxTokens       int     `json:"max_tokens"`       // 1回の返事で生成するトークン数の上限(思考 + 答え)
	Temperature     float64 `json:"temperature"`      // 0 なら毎回一番点数の高いトークンを選ぶ
	Think           bool    `json:"think"`            // 思考させるか
	PresencePenalty float64 `json:"presence_penalty"` // 同じ言葉を繰り返しにくくする強さ。0 で無効
}

// StoredMessage は保存された発言1件。
//
// モデルに渡すのは Role と Content だけ(prompt.go の Message)。
// Thinking・Model・Stats は、画面で後から見返すために残している。
type StoredMessage struct {
	ID             uuid.UUID   `json:"id"` // UUIDv7。発言の順番はこの並びで決まる
	ConversationID uuid.UUID   `json:"conversation_id"`
	Role           string      `json:"role"`     // system / user / assistant
	Content        string      `json:"content"`  // 答え。答えが出なかった回は理由の文
	Thinking       string      `json:"thinking"` // 思考。モデルには渡さない
	Model          string      `json:"model"`    // どのモデルの返事か(assistant のみ)
	Stats          *ReplyStats `json:"stats"`    // 生成の記録(assistant のみ)
	CreatedAt      time.Time   `json:"created_at"`
}

// ReplyStats は返事1件の記録。エンジンの GenerateStats に、会話の仕組みで測った値を足したもの。
// 再起動した後も、画面の統計行や「思考の過程(N秒)」を作り直せるように保存する。
type ReplyStats struct {
	GenerateStats
	FirstSec float64 `json:"first_sec"` // 送信から最初の文字が出るまでの秒数
	ThinkSec float64 `json:"think_sec"` // 思考にかかった秒数
	NoAnswer bool    `json:"no_answer"` // 答えが出ないまま終わり、Content が理由の文になっている
}
