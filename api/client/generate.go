package client

// generate.go: 生成(POST /v1/chat/completions をストリーミングで)と、会話 ⇄ JSON の変換。
//
// 会話 → JSON:
//
//	format.Message      → messages(思考は reasoning_content)
//	読み取れた呼び出し → tool_calls(引数の値はいつも JSON の文字列にする。往復で1文字も変わらないように)
//	崩れた呼び出し     → assistant の "aibow": {"broken_tool_calls"}(全体の中の順番つき。サーバーがモデルの書いたまま書式に戻す)
//	tool.Def            → tools(parameters は書いた順のまま。書式はツールの説明を書いた順にプロンプトに入れるため)
//
// JSON → 会話 は、ストリーミングの塊を集めて format.Output と engine.GenerateStats にする。

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/secondarykey/aibow/engine"
	"github.com/secondarykey/aibow/format"
	"github.com/secondarykey/aibow/tool"
)

// ---- 送る JSON -------------------------------------------------------------------------

type chatRequest struct {
	Model    string        `json:"model,omitempty"` // 空ならサーバーが読み込んでいるモデル
	Messages []chatMessage `json:"messages"`
	Tools    []requestTool `json:"tools,omitempty"`
	Stream   bool          `json:"stream,omitempty"`

	// 数はポインタにして、0(temperature の 0 は greedy)でも書く。トークン数を数えるときは書かない
	Temperature     *float32 `json:"temperature,omitempty"`
	MaxTokens       int      `json:"max_tokens,omitempty"`
	PresencePenalty *float32 `json:"presence_penalty,omitempty"`

	// 思考のオン・オフはいつも書く(書かなければサーバーの既定になり、手元で決めた値と違いうる)
	ChatTemplateKwargs struct {
		EnableThinking bool `json:"enable_thinking"`
	} `json:"chat_template_kwargs"`

	Aibow *aibowRequest `json:"aibow,omitempty"`
}

// aibowRequest は aibow のクライアントだと知らせる欄。モデルに渡した文字列も返してもらう(画面に出すため)
type aibowRequest struct {
	ReturnPrompt bool `json:"return_prompt"`
}

type chatMessage struct {
	Role             string        `json:"role"`
	Content          string        `json:"content"`
	ReasoningContent string        `json:"reasoning_content,omitempty"`
	ToolCalls        []toolCall    `json:"tool_calls,omitempty"`
	ToolCallID       string        `json:"tool_call_id,omitempty"` // 書式は使わない(呼び出しと結果は順番で対応する)が、標準の形に合わせて書く
	Aibow            *messageExtra `json:"aibow,omitempty"`
}

type messageExtra struct {
	BrokenToolCalls []brokenCall `json:"broken_tool_calls"`
}

type brokenCall struct {
	Index int    `json:"index"`
	Name  string `json:"name,omitempty"`
	Raw   string `json:"raw"`
	Error string `json:"error"`
}

type toolCall struct {
	Index    *int   `json:"index,omitempty"` // ストリーミングの塊でだけ来る
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type requestTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters,omitempty"`
	} `json:"function"`
}

// newChatRequest は会話を JSON の形にする(生成とトークン数で同じ形を使う。違うとトークン数がずれるため)。
func newChatRequest(p format.Prompt) (chatRequest, error) {
	req := chatRequest{Messages: make([]chatMessage, 0, len(p.Messages))}
	req.ChatTemplateKwargs.EnableThinking = p.Think
	for _, d := range p.Tools {
		var t requestTool
		t.Type = "function"
		t.Function.Name, t.Function.Description = d.Name, d.Description
		if len(d.Parameters) > 0 {
			t.Function.Parameters = d.Parameters
		}
		req.Tools = append(req.Tools, t)
	}
	var pending []string // まだ結果の来ていない呼び出しの id(tool の発言に順番に付ける)
	for i, m := range p.Messages {
		cm := chatMessage{Role: m.Role, Content: m.Content, ReasoningContent: m.Thinking}
		switch m.Role {
		case "assistant":
			pending = pending[:0]
			for j, c := range m.ToolCalls {
				if c.Raw != "" {
					// 書式が崩れた呼び出し。書式はモデルが書いたまま(Raw)をプロンプトに戻すので、Raw があればこちら
					if cm.Aibow == nil {
						cm.Aibow = &messageExtra{}
					}
					cm.Aibow.BrokenToolCalls = append(cm.Aibow.BrokenToolCalls, brokenCall{Index: j, Name: c.Name, Raw: c.Raw, Error: c.Error})
					continue
				}
				var tc toolCall
				tc.ID, tc.Type = fmt.Sprintf("call_%d_%d", i, j), "function"
				tc.Function.Name, tc.Function.Arguments = c.Name, argumentsJSON(c.Args)
				cm.ToolCalls = append(cm.ToolCalls, tc)
				pending = append(pending, tc.ID)
			}
		case "tool":
			if len(pending) > 0 {
				cm.ToolCallID, pending = pending[0], pending[1:]
			}
		case "system", "user":
		default:
			return chatRequest{}, fmt.Errorf("発言の役 %q は aibow-server に送れません", m.Role)
		}
		req.Messages = append(req.Messages, cm)
	}
	return req, nil
}

// argumentsJSON は引数を arguments(JSON のオブジェクトの文字列)にする。書いた順のまま、値はいつも JSON の文字列にする。
// サーバーは文字列の値を中身のまま書式に戻すので、モデルが書いた文字と1文字も変わらない(数や配列を JSON の値にすると、
// 書き方の揺れ(空白・True)を直す余地が出てしまう)。
func argumentsJSON(args []tool.Arg) string {
	var b strings.Builder
	b.WriteString("{")
	for i, a := range args {
		if i > 0 {
			b.WriteString(",")
		}
		b.Write(jsonString(a.Name))
		b.WriteString(":")
		b.Write(jsonString(a.Value))
	}
	b.WriteString("}")
	return b.String()
}

func jsonString(s string) []byte {
	b, _ := json.Marshal(s) // 文字列の Marshal は失敗しない
	return b
}

// argsFromJSON は arguments(JSON のオブジェクトの文字列)を、書いた順の引数にする。
// 文字列の値は中身の文字列に、それ以外(数・真偽値・配列・オブジェクト・null)は JSON の文字のまま値にする。
// サーバーの api パッケージの argsFromJSON と同じ決まり(サーバーは、モデルが書いた文字をそのまま JSON の値にして返すので、それに戻る)。
func argsFromJSON(arguments string) ([]tool.Arg, error) {
	args := []tool.Arg{}
	if strings.TrimSpace(arguments) == "" {
		return args, nil
	}
	dec := json.NewDecoder(strings.NewReader(arguments))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, fmt.Errorf("JSON のオブジェクトではありません: %q", arguments)
	}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, err
		}
		name, _ := t.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		value := string(raw)
		var s string
		if json.Unmarshal(raw, &s) == nil {
			value = s
		}
		args = append(args, tool.Arg{Name: name, Value: value})
	}
	return args, nil
}

// ---- 受け取る JSON ---------------------------------------------------------------------

// chunk はストリーミングの塊1つ。途中のエラーも同じ行で {"error": …} として来る。
type chunk struct {
	Model   string `json:"model"`
	Choices []struct {
		Delta struct {
			Content          string     `json:"content"`
			ReasoningContent string     `json:"reasoning_content"`
			ToolCalls        []toolCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Timings *struct {
		CacheN      int     `json:"cache_n"`
		PromptN     int     `json:"prompt_n"`
		PromptMS    float64 `json:"prompt_ms"`
		PredictedN  int     `json:"predicted_n"`
		PredictedMS float64 `json:"predicted_ms"`
	} `json:"timings"`
	Aibow *struct {
		Prompt          string       `json:"prompt"`
		StopReason      string       `json:"stop_reason"`
		BrokenToolCalls []brokenCall `json:"broken_tool_calls"`
	} `json:"aibow"`
	errorBody
}

// ---- 生成 ------------------------------------------------------------------------------

// Generate は会話の続きを、aibow-server に生成させる(engine.Engine の Generate と同じ約束)。
// 文字が届くたびに onThinking / onContent を呼び、モデルに渡した文字列は生成の前に onPrompt で渡す。
// ctx を止める(画面の停止ボタン)と、つながりを切る。サーバーはそれを見て生成を止める。
// そのときは、そこまでに届いた分を StopReason "cancel" で返す(エラーにしない。engine と同じ)。
func (c *Client) Generate(ctx context.Context, p format.Prompt, opt engine.GenerateOptions,
	onPrompt func(prompt string), onThinking, onContent func(text string)) (format.Output, engine.GenerateStats, error) {
	var st engine.GenerateStats
	req, err := newChatRequest(p)
	if err != nil {
		return format.Output{}, st, err
	}
	c.mu.Lock()
	req.Model = c.model
	known := c.info
	c.mu.Unlock()
	req.Stream = true
	req.Temperature, req.MaxTokens, req.PresencePenalty = &opt.Temperature, opt.MaxTokens, &opt.PresencePenalty
	req.Aibow = &aibowRequest{ReturnPrompt: true}

	res, err := c.do(ctx, http.MethodPost, "/v1/chat/completions", req)
	if err != nil {
		if ctx.Err() != nil {
			st.StopReason = "cancel"
			return format.Output{}, st, nil
		}
		return format.Output{}, st, err
	}
	defer res.Body.Close()

	r := newStreamReader(onPrompt, onThinking, onContent)
	err = r.read(res.Body)
	switch {
	case ctx.Err() != nil:
		// 停止ボタン。届いた分だけを返す(ツール呼び出しは生成の後に届くので、ここでは無い)
		st.StopReason = "cancel"
		return format.Output{Thinking: r.thinking.String(), Content: r.content.String()}, st, nil
	case err != nil:
		return format.Output{}, st, err
	}
	out, st, err := r.result()
	if err != nil {
		return format.Output{}, st, err
	}
	// 読み込み中のモデルが知っていたものと違えば(ほかの人が切り替えた・知らなかった)、取り直す
	if known == nil || known.ID != r.model {
		c.Models()
	}
	return out, st, nil
}

// streamReader はストリーミングの塊を集める。
type streamReader struct {
	onPrompt, onThinking, onContent func(string)

	model             string
	thinking, content strings.Builder
	calls             map[int]*toolCall // index → 呼び出し(arguments は塊をつなげる。aibow-server は1回で全部を送る)
	broken            []brokenCall
	finished          bool
	stopReason        string
	stats             engine.GenerateStats
}

func newStreamReader(onPrompt, onThinking, onContent func(string)) *streamReader {
	return &streamReader{onPrompt: onPrompt, onThinking: onThinking, onContent: onContent, calls: map[int]*toolCall{}}
}

// read は「data: <JSON>」の行を [DONE] まで読む。
func (r *streamReader) read(body io.Reader) error {
	sc := bufio.NewScanner(body)
	// モデルに渡した文字列の塊は長い(作業領域 32K トークンで数百 KB)ので、1行の上限を大きくする
	sc.Buffer(make([]byte, 64<<10), 64<<20)
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data: ")
		if !ok {
			continue // 空行(塊の区切り)など
		}
		if data == "[DONE]" {
			return nil
		}
		var ch chunk
		if err := json.Unmarshal([]byte(data), &ch); err != nil {
			return fmt.Errorf("aibow-server の返事の塊が読めません: %w", err)
		}
		if ch.Error != nil {
			return &APIError{Status: http.StatusOK, Type: ch.Error.Type, Code: ch.Error.Code, Message: ch.Error.Message}
		}
		r.push(ch)
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("aibow-server とのつながりが途中で切れました: %w", err)
	}
	return fmt.Errorf("aibow-server の返事が終わりの印([DONE])の前に切れました")
}

// push は塊1つを振り分ける。
func (r *streamReader) push(ch chunk) {
	if ch.Model != "" {
		r.model = ch.Model
	}
	if ch.Aibow != nil {
		if ch.Aibow.Prompt != "" {
			r.onPrompt(ch.Aibow.Prompt)
		}
		if ch.Aibow.StopReason != "" {
			r.stopReason = ch.Aibow.StopReason
		}
		r.broken = append(r.broken, ch.Aibow.BrokenToolCalls...)
	}
	if t := ch.Timings; t != nil {
		// timings は llama.cpp と同じ意味:prompt_n は読み込んだ数、cache_n は使い回せた数。engine の記録はプロンプト全体の数を持つ
		r.stats = engine.GenerateStats{
			PromptTokens: t.PromptN + t.CacheN, ReusedTokens: t.CacheN, PromptSec: t.PromptMS / 1000,
			GenTokens: t.PredictedN, GenSec: t.PredictedMS / 1000,
		}
	}
	for _, c := range ch.Choices {
		d := c.Delta
		if d.ReasoningContent != "" {
			r.thinking.WriteString(d.ReasoningContent)
			r.onThinking(d.ReasoningContent)
		}
		if d.Content != "" {
			r.content.WriteString(d.Content)
			r.onContent(d.Content)
		}
		for _, tc := range d.ToolCalls {
			i := 0
			if tc.Index != nil {
				i = *tc.Index
			}
			if prev, ok := r.calls[i]; ok {
				prev.Function.Arguments += tc.Function.Arguments
				continue
			}
			copied := tc
			r.calls[i] = &copied
		}
		if c.FinishReason != nil {
			r.finished = true
		}
	}
}

// result は集めた塊を、書式で振り分けた形(format.Output)と生成の記録にする。
// 崩れた呼び出しは、全体の中の順番(index)の位置に戻す(engine が返すのと同じ並びにするため)。
func (r *streamReader) result() (format.Output, engine.GenerateStats, error) {
	st := r.stats
	st.StopReason = r.stopReason
	if !r.finished || st.StopReason == "" {
		return format.Output{}, st, fmt.Errorf("aibow-server の返事に終了の理由がありません(aibow-server が古い版かもしれません)")
	}
	out := format.Output{Thinking: r.thinking.String(), Content: r.content.String()}

	var good []format.ToolCall
	for _, i := range slices.Sorted(maps.Keys(r.calls)) {
		tc := r.calls[i]
		args, err := argsFromJSON(tc.Function.Arguments)
		if err != nil {
			return format.Output{}, st, fmt.Errorf("ツール %s の呼び出しの arguments が読めません: %w", tc.Function.Name, err)
		}
		good = append(good, format.ToolCall{Call: tool.Call{Name: tc.Function.Name, Args: args}})
	}
	n := len(good) + len(r.broken)
	if n == 0 {
		return out, st, nil
	}
	out.ToolCalls = make([]format.ToolCall, n)
	placed := make([]bool, n)
	for _, b := range r.broken {
		if b.Index < 0 || b.Index >= n || placed[b.Index] {
			return format.Output{}, st, fmt.Errorf("崩れた呼び出しの順番(index %d)が合いません", b.Index)
		}
		out.ToolCalls[b.Index] = format.ToolCall{Call: tool.Call{Name: b.Name}, Raw: b.Raw, Error: b.Error}
		placed[b.Index] = true
	}
	j := 0
	for i := range out.ToolCalls {
		if !placed[i] {
			out.ToolCalls[i] = good[j]
			j++
		}
	}
	return out, st, nil
}
