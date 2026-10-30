package api

import (
	"bufio"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/secondarykey/aibow/engine"
	"github.com/secondarykey/aibow/format"
	"github.com/secondarykey/aibow/tool"
)

// fakeGen はモデルなしで試すための偽物の生成。思考と答えを決まった切れ目で返す。
type fakeGen struct {
	nCtx    uint32
	tokens  int // CountTokens が返す数
	think   []string
	content []string
	stop    string
	calls   []format.ToolCall // モデルが書いたことにする呼び出し
	id      string            // 読み込み中のモデル(空なら ollama:qwen3.5:4b)。fakeModels が書き換える
	none    bool              // モデルを降ろした(Info が nil)。fakeModels が書き換える
	loading string            // 入れ替えている先のモデル(Loading が返す)

	mu      sync.Mutex
	prompts []format.Prompt
	opts    []engine.GenerateOptions
	block   chan struct{} // nil でなければ、生成の途中でこれが閉じられるまで待つ
	started chan struct{} // nil でなければ、生成を始めたら送る
}

func (f *fakeGen) Loading() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.loading
}

func (f *fakeGen) Info() *engine.ModelInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.none {
		return nil
	}
	return &engine.ModelInfo{ID: cmp.Or(f.id, "ollama:qwen3.5:4b"), NCtx: f.nCtx}
}

func (f *fakeGen) CountTokens(p format.Prompt) (int, error) { return f.tokens, nil }

func (f *fakeGen) ApplyTemplate(p format.Prompt) (string, error) {
	return fmt.Sprintf("<%d 件の発言>", len(p.Messages)), nil
}

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
	onPrompt("fake prompt")
	var out format.Output
	for _, t := range f.think {
		onThinking(t)
		out.Thinking += t
	}
	for _, c := range f.content {
		onContent(c)
		out.Content += c
	}
	out.ToolCalls = f.calls
	return out, engine.GenerateStats{PromptTokens: f.tokens, ReusedTokens: 3, GenTokens: 7, GenSec: 1, StopReason: f.stop}, nil
}

func newTestServer(g *fakeGen) *httptest.Server {
	s := New(g, nil, 100, Defaults{Temperature: 0.7, MaxTokens: 100, PresencePenalty: 1.5, Think: true})
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
		`{"messages":[{"role":"user","content":"a"}],"tools":[{"type":"retrieval"}]}`,
		`{"messages":[{"role":"assistant","content":"","tool_calls":[{"type":"function","function":{"name":"f","arguments":"[1]"}}]}]}`,
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

const toolsJSON = `"tools":[{"type":"function","function":{"name":"read_file","description":"Read a file",
	"parameters":{"type":"object","properties":{"path":{"type":"string"},"n":{"type":"integer"},"flags":{"type":"array"},"ok":{"type":["boolean","null"]}},"required":["path"]}}}]`

// モデルが書いた呼び出し(値は tool.Arg の形。真偽の引数に書いた True は型に合わないので JSON の文字列)を返す偽物。1つは書式が崩れている
func toolGen() *fakeGen {
	return &fakeGen{nCtx: 100, tokens: 10, content: []string{"読みます"}, stop: "stop", calls: []format.ToolCall{
		{Call: tool.Call{Name: "read_file", Args: []tool.Arg{
			{Name: "path", Value: "a.go"}, {Name: "n", Value: "5"}, {Name: "flags", Value: `["x", "y"]`}, {Name: "ok", Value: `"True"`}, {Name: "memo", Value: "7"},
		}}},
		{Raw: "<tool_call>\n<function=read_file>", Error: "書式が崩れていて…"},
	}}
}

func TestToolCalls(t *testing.T) {
	g := toolGen()
	ts := newTestServer(g)
	defer ts.Close()

	// 履歴に、前の呼び出しとその結果を入れる。引数の数・配列は JSON の値のまま来る
	res := post(t, ts.URL, `{"messages":[
		{"role":"user","content":"読んで"},
		{"role":"assistant","content":null,"reasoning_content":"読もう","tool_calls":[{"id":"call_1","type":"function",
			"function":{"name":"read_file","arguments":"{\"path\":\"b.go\",\"n\":3,\"flags\":[\"z\", 1],\"ok\":false}"}}]},
		{"role":"tool","tool_call_id":"call_1","content":"package b"}],`+toolsJSON+`}`)
	defer res.Body.Close()
	var got completion
	json.NewDecoder(res.Body).Decode(&got)

	p := g.prompts[0]
	if len(p.Tools) != 1 || p.Tools[0].Name != "read_file" || !strings.Contains(string(p.Tools[0].Parameters), `"required":["path"]`) {
		t.Errorf("ツールの定義が違う: %+v", p.Tools)
	}
	a := p.Messages[1]
	wantArgs := []tool.Arg{{Name: "path", Value: "b.go"}, {Name: "n", Value: "3"}, {Name: "flags", Value: `["z", 1]`}, {Name: "ok", Value: "false"}}
	if a.Thinking != "読もう" || len(a.ToolCalls) != 1 || a.ToolCalls[0].Name != "read_file" || !slices.Equal(a.ToolCalls[0].Args, wantArgs) {
		t.Errorf("履歴の呼び出しが違う: %+v", a)
	}
	if m := p.Messages[2]; m.Role != "tool" || m.Content != "package b" || m.Name != "read_file" { // 名前は tool_call_id から引く(Gemma 4 が書く)
		t.Errorf("ツールの結果が違う: %+v", m)
	}

	c := got.Choices[0]
	if *c.FinishReason != "tool_calls" || len(c.Message.ToolCalls) != 1 {
		t.Fatalf("返事が違う: %+v", c)
	}
	// 型の決まった値はモデルが書いた文字のまま JSON の値に。知らない引数(memo)は文字列のまま
	tc := c.Message.ToolCalls[0]
	if !strings.HasPrefix(tc.ID, "call_") || tc.Type != "function" || tc.Function.Name != "read_file" ||
		tc.Function.Arguments != `{"path":"a.go","n":5,"flags":["x", "y"],"ok":true,"memo":"7"}` || tc.Index != nil {
		t.Errorf("呼び出しが違う: %+v", tc)
	}
	// 崩れた呼び出しは、答えの後ろに文字列で
	if c.Message.Content != "読みます\n\n<tool_call>\n<function=read_file>" {
		t.Errorf("答えが違う: %q", c.Message.Content)
	}
}

func TestToolCallsStream(t *testing.T) {
	g := toolGen()
	g.calls = append(g.calls[:1], g.calls[0]) // 崩れたものの代わりに、2つ目の呼び出し
	ts := newTestServer(g)
	defer ts.Close()

	res := post(t, ts.URL, `{"messages":[{"role":"user","content":"読んで"}],"stream":true,`+toolsJSON+`}`)
	defer res.Body.Close()
	var chunks []completion
	sc := bufio.NewScanner(res.Body)
	for sc.Scan() {
		if l, ok := strings.CutPrefix(sc.Text(), "data: {"); ok {
			var c completion
			json.Unmarshal([]byte("{"+l), &c)
			chunks = append(chunks, c)
		}
	}
	// 役 → 答え → 呼び出し ×2(1つずつ、index 付き)→ 終了の理由
	if len(chunks) != 5 {
		t.Fatalf("塊の数 %d", len(chunks))
	}
	for i, n := range []int{2, 3} {
		d := chunks[n].Choices[0].Delta
		if len(d.ToolCalls) != 1 || d.ToolCalls[0].Index == nil || *d.ToolCalls[0].Index != i || d.ToolCalls[0].Function.Name != "read_file" {
			t.Errorf("塊 %d の呼び出しが違う: %+v", n, d)
		}
	}
	if *chunks[4].Choices[0].FinishReason != "tool_calls" {
		t.Errorf("finish_reason %s", *chunks[4].Choices[0].FinishReason)
	}
}

// tool_choice が none なら、ツールをモデルに知らせない
func TestToolChoiceNone(t *testing.T) {
	g := &fakeGen{nCtx: 100, tokens: 1, stop: "eos"}
	ts := newTestServer(g)
	defer ts.Close()
	res := post(t, ts.URL, `{"messages":[{"role":"user","content":"a"}],"tool_choice":"none",`+toolsJSON+`}`)
	res.Body.Close()
	if len(g.prompts[0].Tools) != 0 {
		t.Error("ツールを知らせてしまった")
	}
}
