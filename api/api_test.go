package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/secondarykey/aibow/engine"
	"github.com/secondarykey/aibow/format"
)

// fakeGen はモデルなしで試すための偽物の生成。思考と答えを決まった切れ目で返す。
type fakeGen struct {
	nCtx    uint32
	tokens  int // CountTokens が返す数
	think   []string
	content []string
	stop    string

	mu      sync.Mutex
	prompts []format.Prompt
	opts    []engine.GenerateOptions
	block   chan struct{} // nil でなければ、生成の途中でこれが閉じられるまで待つ
	started chan struct{} // nil でなければ、生成を始めたら送る
}

func (f *fakeGen) Info() *engine.ModelInfo {
	return &engine.ModelInfo{ID: "ollama:qwen3.5:4b", NCtx: f.nCtx}
}

func (f *fakeGen) CountTokens(p format.Prompt) (int, error) { return f.tokens, nil }

func (f *fakeGen) Generate(ctx context.Context, p format.Prompt, opt engine.GenerateOptions,
	onPrompt func(string), onThinking, onContent func(string)) (format.Output, engine.GenerateStats, error) {
	f.mu.Lock()
	f.prompts = append(f.prompts, p)
	f.opts = append(f.opts, opt)
	f.mu.Unlock()
	if f.started != nil {
		f.started <- struct{}{}
	}
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return format.Output{}, engine.GenerateStats{StopReason: "cancel"}, nil
		}
	}
	var out format.Output
	for _, t := range f.think {
		onThinking(t)
		out.Thinking += t
	}
	for _, c := range f.content {
		onContent(c)
		out.Content += c
	}
	return out, engine.GenerateStats{PromptTokens: f.tokens, ReusedTokens: 3, GenTokens: 7, GenSec: 1, StopReason: f.stop}, nil
}

func newTestServer(g *fakeGen) *httptest.Server {
	s := New(g, Defaults{Temperature: 0.7, MaxTokens: 100, PresencePenalty: 1.5, Think: true})
	s.now = func() time.Time { return time.Unix(1700000000, 0) }
	return httptest.NewServer(s.Handler())
}

func post(t *testing.T, url, body string) *http.Response {
	t.Helper()
	res, err := http.Post(url+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestModels(t *testing.T) {
	ts := newTestServer(&fakeGen{nCtx: 100})
	defer ts.Close()
	res, err := http.Get(ts.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var got modelList
	json.NewDecoder(res.Body).Decode(&got)
	if got.Object != "list" || len(got.Data) != 1 || got.Data[0].ID != "ollama:qwen3.5:4b" {
		t.Errorf("一覧が違う: %+v", got)
	}
}

func TestCompleteNonStream(t *testing.T) {
	g := &fakeGen{nCtx: 100, tokens: 10, think: []string{"考え"}, content: []string{"こん", "にちは"}, stop: "eos"}
	ts := newTestServer(g)
	defer ts.Close()

	// content を部品の配列で送る(Cline の形)。system は2つ続けて、つながるか
	res := post(t, ts.URL, `{"model":"x","messages":[
		{"role":"system","content":"A"},{"role":"developer","content":"B"},
		{"role":"user","content":[{"type":"text","text":"や"},{"type":"text","text":"あ"}]},
		{"role":"assistant","content":"はい","reasoning_content":"前の思考"},
		{"role":"user","content":"次"}],
		"temperature":0,"max_completion_tokens":5}`)
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("ステータス %d", res.StatusCode)
	}
	var got completion
	json.NewDecoder(res.Body).Decode(&got)
	if got.Object != "chat.completion" || !strings.HasPrefix(got.ID, "chatcmpl-") || got.Model != "ollama:qwen3.5:4b" {
		t.Errorf("頭が違う: %+v", got)
	}
	m := got.Choices[0].Message
	if m.Content != "こんにちは" || m.ReasoningContent != "考え" || *got.Choices[0].FinishReason != "stop" {
		t.Errorf("返事が違う: %+v %s", m, *got.Choices[0].FinishReason)
	}
	if got.Usage.PromptTokens != 10 || got.Usage.CompletionTokens != 7 || got.Usage.PromptTokensDetails.CachedTokens != 3 {
		t.Errorf("usage が違う: %+v", got.Usage)
	}

	p := g.prompts[0]
	want := []format.Message{
		{Role: "system", Content: "A\n\nB"},
		{Role: "user", Content: "やあ"},
		{Role: "assistant", Content: "はい", Thinking: "前の思考"},
		{Role: "user", Content: "次"},
	}
	if len(p.Messages) != len(want) || !p.Think {
		t.Fatalf("プロンプトが違う: %+v", p)
	}
	for i := range want {
		if p.Messages[i].Role != want[i].Role || p.Messages[i].Content != want[i].Content || p.Messages[i].Thinking != want[i].Thinking {
			t.Errorf("messages[%d] = %+v, want %+v", i, p.Messages[i], want[i])
		}
	}
	// temperature 0 は greedy として渡す(書かれていないときの既定 0.7 にしない)。presence は既定
	if o := g.opts[0]; o.Temperature != 0 || o.MaxTokens != 5 || o.PresencePenalty != 1.5 {
		t.Errorf("生成の設定が違う: %+v", o)
	}
}

func TestStream(t *testing.T) {
	g := &fakeGen{nCtx: 100, tokens: 10, think: []string{"考"}, content: []string{"a", "b"}, stop: "length"}
	ts := newTestServer(g)
	defer ts.Close()

	res := post(t, ts.URL, `{"messages":[{"role":"user","content":"hi"}],"stream":true,
		"stream_options":{"include_usage":true},"chat_template_kwargs":{"enable_thinking":false}}`)
	defer res.Body.Close()
	if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type %q", ct)
	}
	var lines []string
	sc := bufio.NewScanner(res.Body)
	for sc.Scan() {
		if l := sc.Text(); l != "" {
			lines = append(lines, l)
		}
	}
	// 役 → 思考 → 答え ×2 → 終了の理由 → usage → [DONE]
	if len(lines) != 7 || lines[6] != "data: [DONE]" {
		t.Fatalf("塊の数が違う: %q", lines)
	}
	chunks := make([]completion, 6)
	for i := range chunks {
		if err := json.Unmarshal([]byte(strings.TrimPrefix(lines[i], "data: ")), &chunks[i]); err != nil {
			t.Fatalf("%q: %v", lines[i], err)
		}
		if chunks[i].Object != "chat.completion.chunk" {
			t.Errorf("object %q", chunks[i].Object)
		}
	}
	if chunks[0].Choices[0].Delta.Role != "assistant" || chunks[1].Choices[0].Delta.ReasoningContent != "考" ||
		chunks[2].Choices[0].Delta.Content != "a" || chunks[3].Choices[0].Delta.Content != "b" {
		t.Errorf("delta が違う: %q", lines)
	}
	if chunks[2].Choices[0].FinishReason != nil || *chunks[4].Choices[0].FinishReason != "length" {
		t.Errorf("finish_reason が違う: %q", lines)
	}
	if !strings.Contains(lines[5], `"choices":[]`) || chunks[5].Usage.TotalTokens != 17 {
		t.Errorf("usage の塊が違う: %s", lines[5])
	}
	if g.prompts[0].Think {
		t.Error("enable_thinking:false が効いていない")
	}
}

func TestContextLengthExceeded(t *testing.T) {
	g := &fakeGen{nCtx: 100, tokens: 100}
	ts := newTestServer(g)
	defer ts.Close()
	res := post(t, ts.URL, `{"messages":[{"role":"user","content":"hi"}]}`)
	defer res.Body.Close()
	var got errorBody
	json.NewDecoder(res.Body).Decode(&got)
	if res.StatusCode != 400 || got.Error.Code != "context_length_exceeded" || got.Error.Type != "invalid_request_error" {
		t.Errorf("%d %+v", res.StatusCode, got)
	}
	if len(g.prompts) != 0 {
		t.Error("生成してしまった")
	}
}

func TestBadRequests(t *testing.T) {
	ts := newTestServer(&fakeGen{nCtx: 100})
	defer ts.Close()
	for _, body := range []string{
		`{`,
		`{"messages":[]}`,
		`{"messages":[{"role":"user","content":"a"},{"role":"system","content":"b"}]}`,
		`{"messages":[{"role":"user","content":[{"type":"image_url"}]}]}`,
		`{"messages":[{"role":"tool","content":"a","tool_call_id":"1"}]}`,
		`{"messages":[{"role":"user","content":"a"}],"max_tokens":0}`,
	} {
		res := post(t, ts.URL, body)
		res.Body.Close()
		if res.StatusCode != 400 {
			t.Errorf("%s: ステータス %d", body, res.StatusCode)
		}
	}
	res, _ := http.Get(ts.URL + "/v1/embeddings")
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Errorf("知らないパス: %d", res.StatusCode)
	}
}

// 生成中に来たリクエストは断らず、前のが終わるまで待つ
func TestQueue(t *testing.T) {
	g := &fakeGen{nCtx: 100, tokens: 1, content: []string{"x"}, stop: "eos",
		block: make(chan struct{}), started: make(chan struct{}, 2)}
	ts := newTestServer(g)
	defer ts.Close()

	codes := make(chan int, 2)
	req := func() {
		res := post(t, ts.URL, `{"messages":[{"role":"user","content":"hi"}]}`)
		res.Body.Close()
		codes <- res.StatusCode
	}
	go req()
	<-g.started // 1つ目が生成中
	go req()
	select {
	case <-g.started:
		t.Fatal("2つ目が順番を待たずに生成を始めた")
	case <-time.After(100 * time.Millisecond):
	}
	close(g.block) // 1つ目を終わらせる → 2つ目が始まる
	for range 2 {
		if c := <-codes; c != 200 {
			t.Errorf("ステータス %d", c)
		}
	}
}
