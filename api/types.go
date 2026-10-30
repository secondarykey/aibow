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

	// Tools はツールの定義(JSON Schema)。書式がシステムプロンプトに書く(tools.go)
	Tools []requestTool `json:"tools"`
	// ToolChoice は "auto"・"none"・"required"・特定のツールの指定。"none" ならツールを知らせない(useTools)
	ToolChoice json.RawMessage `json:"tool_choice"`

	// Aibow は aibow のクライアント(画面のアプリ)が頼むもの(0015)。書いてあれば、返事に aibow の欄を足す。
	// OpenAI の標準には無い欄なので、Cline などは書かない(書かなければ 0012 の形のまま返す)
	Aibow *aibowRequest `json:"aibow"`
}

// aibowRequest は、aibow のクライアントが頼むもの。
type aibowRequest struct {
	// ReturnPrompt を立てると、モデルに渡した文字列を返す(学習用に画面に出すため。長いので頼んだときだけ)
	ReturnPrompt bool `json:"return_prompt"`
}

// aibowResult は、aibow のクライアントに返す欄(返事の "aibow")。標準の形では表せない、画面が使うもの。
type aibowResult struct {
	// Prompt はモデルに渡した文字列(return_prompt のときだけ。ストリーミングなら生成の前の塊で送る)
	Prompt string `json:"prompt,omitempty"`
	// StopReason は細かい終了の理由(eos / stop / length / context / cancel)。finish_reason では length と context が見分けられないため
	StopReason string `json:"stop_reason,omitempty"`
	// BrokenToolCalls は書式が崩れて読み取れなかった呼び出し。0012 の形では答えの文字列に足していたが、
	// aibow のクライアントは自分のループで「エラーとしてモデルに返して直させる」ので、分けて返す
	BrokenToolCalls []brokenCall `json:"broken_tool_calls,omitempty"`
}

// brokenCall は書式が崩れた呼び出し1つ。Index は、読み取れた呼び出し(tool_calls)と合わせた全体の中での順番。
// 書式は呼び出しを書いた順にプロンプトに戻すので、履歴で送り返すときに元の並びに戻せるようにする。
type brokenCall struct {
	Index int    `json:"index"`
	Name  string `json:"name,omitempty"` // 読み取れた関数名(読めなければ空)
	Raw   string `json:"raw"`            // モデルが書いたまま(次のプロンプトにはこれをそのまま入れる)
	Error string `json:"error"`
}

// messageExtra は、履歴の assistant の発言に aibow のクライアントが足すもの。
type messageExtra struct {
	BrokenToolCalls []brokenCall `json:"broken_tool_calls"`
}

// timings は生成の時間とトークン数。llama.cpp の server と同じ名前・同じ意味にした(0015)。
// prompt_n は今回読み込んだ(KV キャッシュを使い回せなかった)トークン数、cache_n は使い回せた数。足すとプロンプトのトークン数。
// 小さいので、頼まれなくてもいつも返す(Cline などは知らない欄を読まない)。
type timings struct {
	CacheN             int     `json:"cache_n"`
	PromptN            int     `json:"prompt_n"`
	PromptMS           float64 `json:"prompt_ms"`
	PromptPerSecond    float64 `json:"prompt_per_second"`
	PredictedN         int     `json:"predicted_n"`
	PredictedMS        float64 `json:"predicted_ms"`
	PredictedPerSecond float64 `json:"predicted_per_second"`
}

// chatMessage はリクエストの発言1件。
type chatMessage struct {
	Role string `json:"role"`
	// Content は文字列か、部品の配列([{"type": "text", "text": "…"}])。Cline などは配列で送ってくる
	Content json.RawMessage `json:"content"`
	// ReasoningContent は、前の返事の思考(この API が返したもの)。クライアントが履歴に入れて送り返してきたときに読む
	ReasoningContent string `json:"reasoning_content"`

	// ToolCalls は、前の返事のツール呼び出し(この API が返したもの)
	ToolCalls []toolCall `json:"tool_calls"`
	// ToolCallID は tool の役の発言が、どの呼び出しの結果か。書式は呼び出しと結果を順番で対応させるが、
	// Gemma 4 は結果にツールの名前を書くので、直前の返事の tool_calls の id から名前を引く(0026 の決定事項 5)
	ToolCallID string `json:"tool_call_id"`
	// Name は tool の役の発言の、ツールの名前(aibow のクライアントが送る。崩れた呼び出しの結果には id が無いため)
	Name string `json:"name"`

	// Aibow は aibow のクライアントが足す欄(書式が崩れた呼び出し。次のプロンプトには、モデルが書いたままを入れるため)
	Aibow *messageExtra `json:"aibow"`
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
// messages が空でもよい(会話の仕組みは、ツールの説明だけのトークン数を数えることがある。生成するときは handleChat が断る)。
//
//   - system(と、OpenAI の新しい名前 developer)は先頭に置く。先頭に続けて複数あれば1つにつなぐ。
//     Qwen3.5 の公式テンプレートは途中の system をエラーにするので、途中にあればエラーにする
//   - assistant の reasoning_content は思考(Thinking)に、tool_calls は書式の呼び出しに戻す。書式が、ツールのループの途中の返事だけ思考を入れる
//   - tool の役の発言(ツールの結果)は、そのまま書式の tool の発言にする(書式が <tool_response> で包む)
//   - tools は tool.Def にして、モデルに知らせる(tool_choice が "none" なら知らせない)
func toPrompt(req chatRequest, think bool) (format.Prompt, error) {
	defs, err := toDefs(req.Tools)
	if err != nil {
		return format.Prompt{}, err
	}
	if !useTools(req.ToolChoice) {
		defs = nil
	}
	var system []string
	var msgs []format.Message
	names := map[string]string{} // 直前の返事の呼び出しの id → ツールの名前
	for i, m := range req.Messages {
		text, err := m.text()
		if err != nil {
			return format.Prompt{}, fmt.Errorf("messages[%d]: %w", i, err)
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
			calls, err := fromRequestCalls(m.ToolCalls, defs)
			if err == nil && m.Aibow != nil {
				calls, err = mergeBroken(calls, m.Aibow.BrokenToolCalls)
			}
			if err != nil {
				return format.Prompt{}, fmt.Errorf("messages[%d]: %w", i, err)
			}
			msgs = append(msgs, format.Message{Role: "assistant", Content: text, Thinking: m.ReasoningContent, ToolCalls: calls})
			clear(names)
			for _, c := range m.ToolCalls {
				names[c.ID] = c.Function.Name
			}
		case "tool":
			// 名前は id から引く。引けなければ送られた名前、それも無ければ空(書式が呼び出しと順番で組にする)
			name, ok := names[m.ToolCallID]
			if !ok || m.ToolCallID == "" {
				name = m.Name
			}
			msgs = append(msgs, format.Message{Role: "tool", Content: text, Name: name})
		default:
			return format.Prompt{}, fmt.Errorf("messages[%d]: 役 %q には対応していません(system・developer・user・assistant・tool)", i, m.Role)
		}
	}
	if len(system) > 0 {
		msgs = append([]format.Message{{Role: "system", Content: strings.Join(system, "\n\n")}}, msgs...)
	}
	return format.Prompt{Messages: msgs, Tools: defs, Think: think}, nil
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

	// Timings は生成の時間(llama.cpp の server と同じ欄)。ストリーミングなら終了の理由の塊に入れる
	Timings *timings `json:"timings,omitempty"`
	// Aibow は aibow のクライアントが頼んだときだけ入れる欄
	Aibow *aibowResult `json:"aibow,omitempty"`
}

type choice struct {
	Index        int      `json:"index"`
	Message      *message `json:"message,omitempty"`
	Delta        *delta   `json:"delta,omitempty"`
	FinishReason *string  `json:"finish_reason"` // ストリーミングの途中は null
}

// message はストリーミングなしの返事。content は空でも書く。
type message struct {
	Role             string     `json:"role"`
	Content          string     `json:"content"`
	ReasoningContent string     `json:"reasoning_content,omitempty"` // 思考(llama.cpp の server と同じ名前)
	ToolCalls        []toolCall `json:"tool_calls,omitempty"`
}

// delta はストリーミングの増えた分。書くものがある欄だけを出す。
type delta struct {
	Role             string     `json:"role,omitempty"`
	Content          string     `json:"content,omitempty"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	ToolCalls        []toolCall `json:"tool_calls,omitempty"`
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
