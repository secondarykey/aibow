package api

// types.go: OpenAI 互換の API でやり取りする JSON の形と、リクエストを書式の形(format.Prompt)にする部分。
//
// OpenAI の API の形は「メッセージの配列」だが、モデルが読むのは1本の文字列(書式)。
// その間の変換は、aibow の会話の仕組みと同じく format パッケージがする。ここは JSON と format.Message の間をつなぐだけ。

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/secondarykey/aibow/format"
)

// ---- リクエスト ------------------------------------------------------------------------

// chatRequest は POST /v1/chat/completions の本文。使う欄だけを読む(ほかの欄は無視する)。
//
// 数の欄をポインタにしているのは、「書かれていない」と「0 が書かれている」を見分けるため
// (temperature の 0 は greedy の意味なので、書かれていないときの既定 0.7 と区別が要る)。
type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`

	// StreamOptions.IncludeUsage を立てると、ストリーミングの最後に使ったトークン数の塊を1つ足す(OpenAI と同じ)
	StreamOptions *struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options"`

	Temperature         *float32 `json:"temperature"`
	MaxTokens           *int     `json:"max_tokens"`
	MaxCompletionTokens *int     `json:"max_completion_tokens"` // max_tokens の新しい名前。両方あればこちら
	PresencePenalty     *float32 `json:"presence_penalty"`

	// ChatTemplateKwargs はチャットテンプレートに渡す値。思考のオン・オフ(enable_thinking)だけを読む。
	// OpenAI の標準には無い欄で、llama.cpp の server・vLLM と同じ名前にした(0012。2026-10-19 にユーザーと決めた)
	ChatTemplateKwargs struct {
		EnableThinking *bool `json:"enable_thinking"`
	} `json:"chat_template_kwargs"`

	// Tools はツールの定義。ツール呼び出しの変換は 6-2 で作るので、今は数だけを見てログに出す
	Tools []json.RawMessage `json:"tools"`
}

// chatMessage はリクエストの発言1件。
type chatMessage struct {
	Role string `json:"role"`
	// Content は文字列か、部品の配列([{"type": "text", "text": "…"}])。Cline などは配列で送ってくる
	Content json.RawMessage `json:"content"`
	// ReasoningContent は、前の返事の思考(この API が返したもの)。クライアントが履歴に入れて送り返してきたときに読む
	ReasoningContent string `json:"reasoning_content"`

	// ToolCalls・ToolCallID はツール呼び出し(6-2)。今は来たらエラーにする
	ToolCalls  []json.RawMessage `json:"tool_calls"`
	ToolCallID string            `json:"tool_call_id"`
}

// contentPart は Content を配列で送ってきたときの部品1つ。
type contentPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// text は Content を1本の文字列にする。部品の配列なら、文字の部品をつなぐ(画像などは扱わない)。
func (m chatMessage) text() (string, error) {
	raw := strings.TrimSpace(string(m.Content))
	if raw == "" || raw == "null" {
		return "", nil // ツール呼び出しだけの返事などは content が null
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(m.Content, &s); err != nil {
			return "", fmt.Errorf("content が読めません: %w", err)
		}
		return s, nil
	}
	var parts []contentPart
	if err := json.Unmarshal(m.Content, &parts); err != nil {
		return "", fmt.Errorf("content は文字列か、部品の配列にしてください: %w", err)
	}
	var b strings.Builder
	for _, p := range parts {
		if p.Type != "text" {
			return "", fmt.Errorf("content の部品の種類 %q には対応していません(文字 text だけです)", p.Type)
		}
		b.WriteString(p.Text)
	}
	return b.String(), nil
}

// toPrompt はリクエストを書式の形(format.Prompt)にする。読めない発言があればエラー(400 で返す)。
//
//   - system(と、OpenAI の新しい名前 developer)は先頭に置く。先頭に続けて複数あれば1つにつなぐ。
//     Qwen3.5 の公式テンプレートは途中の system をエラーにするので、途中にあればエラーにする
//   - assistant の reasoning_content は思考(Thinking)に戻す。書式が、ツールのループの途中の返事だけ思考を入れる
//   - ツール呼び出し(tool の役・tool_calls)は 6-2 で作るので、今はエラーにする
func toPrompt(req chatRequest, think bool) (format.Prompt, error) {
	if len(req.Messages) == 0 {
		return format.Prompt{}, fmt.Errorf("messages が空です")
	}
	var system []string
	var msgs []format.Message
	for i, m := range req.Messages {
		text, err := m.text()
		if err != nil {
			return format.Prompt{}, fmt.Errorf("messages[%d]: %w", i, err)
		}
		if len(m.ToolCalls) > 0 || m.Role == "tool" {
			return format.Prompt{}, fmt.Errorf("messages[%d]: ツール呼び出し(tool_calls・tool の役)にはまだ対応していません", i)
		}
		switch m.Role {
		case "system", "developer":
			if len(msgs) > 0 {
				return format.Prompt{}, fmt.Errorf("messages[%d]: %s の発言は先頭にだけ置けます(Qwen3.5 の書式の決まり)", i, m.Role)
			}
			system = append(system, text)
		case "user":
			msgs = append(msgs, format.Message{Role: "user", Content: text})
		case "assistant":
			msgs = append(msgs, format.Message{Role: "assistant", Content: text, Thinking: m.ReasoningContent})
		default:
			return format.Prompt{}, fmt.Errorf("messages[%d]: 役 %q には対応していません(system・developer・user・assistant)", i, m.Role)
		}
	}
	if len(system) > 0 {
		msgs = append([]format.Message{{Role: "system", Content: strings.Join(system, "\n\n")}}, msgs...)
	}
	return format.Prompt{Messages: msgs, Think: think}, nil
}

// ---- レスポンス ------------------------------------------------------------------------

// completion は返事。ストリーミングなしなら object は chat.completion で、choices に message が入る。
// ストリーミングなら object は chat.completion.chunk で、choices に delta(増えた分)が入る塊を何回も送る。
type completion struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []choice `json:"choices"` // 使ったトークン数だけの最後の塊は空の配列([])
	Usage   *usage   `json:"usage,omitempty"`
}

type choice struct {
	Index        int      `json:"index"`
	Message      *message `json:"message,omitempty"`
	Delta        *delta   `json:"delta,omitempty"`
	FinishReason *string  `json:"finish_reason"` // ストリーミングの途中は null
}

// message はストリーミングなしの返事。content は空でも書く。
type message struct {
	Role             string `json:"role"`
	Content          string `json:"content"`
	ReasoningContent string `json:"reasoning_content,omitempty"` // 思考(llama.cpp の server と同じ名前)
}

// delta はストリーミングの増えた分。書くものがある欄だけを出す。
type delta struct {
	Role             string `json:"role,omitempty"`
	Content          string `json:"content,omitempty"`
	ReasoningContent string `json:"reasoning_content,omitempty"`
}

// usage は使ったトークン数。completion_tokens は思考と答えの合計。
// prompt_tokens_details.cached_tokens は、プロンプトのうち KV キャッシュを使い回せた数(OpenAI の「キャッシュされた入力」と同じ欄)
type usage struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	TotalTokens         int `json:"total_tokens"`
	PromptTokensDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

type modelList struct {
	Object string        `json:"object"`
	Data   []modelObject `json:"data"`
}

type modelObject struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

type errorBody struct {
	Error errorObject `json:"error"`
}

type errorObject struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code,omitempty"`
}
