package client

// client_test.go: 本物のサーバーの口(api パッケージ)を httptest で立て、その下の生成だけを偽物にしてテストする(モデル不要)。
// 会話 → JSON → 会話 の往復で1文字も変わらないこと(KV キャッシュを今と同じだけ使い回すため。0015 の 8)を、
// サーバーが受け取った会話と、書式を当てた文字列で確かめる。

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http/httptest"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/secondarykey/aibow/api"
	"github.com/secondarykey/aibow/engine"
	"github.com/secondarykey/aibow/format"
	"github.com/secondarykey/aibow/models"
	"github.com/secondarykey/aibow/tool"
)

// fakeGen は偽物の生成。受け取った会話を覚え、決まった返事を返す。
type fakeGen struct {
	mu      sync.Mutex
	id      string
	prompts []format.Prompt
	opts    []engine.GenerateOptions
	out     format.Output
	st      engine.GenerateStats
	block   chan struct{} // nil でなければ、生成の途中でこれが閉じられるか ctx が終わるまで待つ
	started chan struct{}
}

func (f *fakeGen) Info() *engine.ModelInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	return &engine.ModelInfo{ID: f.id, Name: f.id, NCtx: 32768}
}

func (f *fakeGen) CountTokens(p format.Prompt) (int, error) {
	return len(p.Messages)*10 + len(p.Tools), nil
}

func (f *fakeGen) ApplyTemplate(p format.Prompt) (string, error) { return "", nil }

func (f *fakeGen) Generate(ctx context.Context, p format.Prompt, opt engine.GenerateOptions,
	onPrompt func(string), onThinking, onContent func(string)) (format.Output, engine.GenerateStats, error) {
	f.mu.Lock()
	f.prompts = append(f.prompts, p)
	f.opts = append(f.opts, opt)
	f.mu.Unlock()
	onPrompt("<prompt>")
	if f.started != nil {
		f.started <- struct{}{}
	}
	onThinking("考え")
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return format.Output{Thinking: "考え"}, engine.GenerateStats{StopReason: "cancel"}, nil
		}
	}
	onContent(f.out.Content)
	return f.out, f.st, nil
}

// fakeModels は偽物のモデルの係。
type fakeModels struct {
	gen   *fakeGen
	loads []string
}

func (m *fakeModels) List() []models.Entry {
	return []models.Entry{{ID: "ollama:qwen3.5:4b", Name: "qwen3.5:4b"}, {ID: "ollama:qwen3.5:9b", Name: "qwen3.5:9b"}}
}

func (m *fakeModels) Find(name string) (models.Entry, error) {
	for _, e := range m.List() {
		if e.ID == name || e.Name == name {
			return e, nil
		}
	}
	return models.Entry{}, errors.New("見つかりません")
}

func (m *fakeModels) Load(e models.Entry) (*engine.ModelInfo, error) {
	m.loads = append(m.loads, e.ID)
	m.gen.mu.Lock()
	m.gen.id = e.ID
	m.gen.mu.Unlock()
	return m.gen.Info(), nil
}

func (m *fakeModels) GPULayers(string) int                                { return 999 }
func (m *fakeModels) SetGPULayers(string, int) (*engine.ModelInfo, error) { return nil, nil }
func (m *fakeModels) HasGPU() bool                                        { return true }

func newServer(t *testing.T, g *fakeGen) (*Client, *fakeModels) {
	t.Helper()
	g.id = "ollama:qwen3.5:4b"
	m := &fakeModels{gen: g}
	s := api.New(g, m, 32768, api.Defaults{Temperature: 0.7, MaxTokens: 100, PresencePenalty: 1.5, Think: true})
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return New(ts.URL + "/v1/"), m // /v1 が付いていてもよい
}

// tricky は、往復で変わりやすいもの(数・配列・True・前後の空白・< と &・崩れた呼び出し・思考)を入れた会話。
func tricky() format.Prompt {
	params := json.RawMessage(`{"type": "object", "properties": {"path": {"type": "string", "description": "a < b && c"}, "n": {"type": "integer"}, "flags": {"type": "array"}, "ok": {"type": "boolean"}}, "required": ["path"]}`)
	return format.Prompt{
		Think: true,
		Tools: []tool.Def{{Name: "read_file", Description: "Read <a> file & more", Parameters: params}},
		Messages: []format.Message{
			{Role: "system", Content: "あなたは aibow です。\n<rules>"},
			{Role: "user", Content: "読んで"},
			{Role: "assistant", Content: "", Thinking: "読もう", ToolCalls: []format.ToolCall{
				{Call: tool.Call{Name: "read_file", Args: []tool.Arg{{Name: "path", Value: "a.go"}, {Name: "n", Value: " 5"}, {Name: "flags", Value: `["x", "y"]`}, {Name: "ok", Value: "True"}}}},
				{Call: tool.Call{Name: "read_file"}, Raw: "<tool_call>\n<function=read_file>", Error: "崩れています"},
				{Call: tool.Call{Name: "read_file", Args: []tool.Arg{{Name: "path", Value: "b.go"}, {Name: "n", Value: "3"}}}},
			}},
			{Role: "tool", Content: "package a"},
			{Role: "tool", Content: "エラー: 崩れています"},
			{Role: "tool", Content: "package b"},
			{Role: "assistant", Content: "読みました"},
			{Role: "user", Content: "次"},
		},
	}
}

// 会話 → JSON → 会話 で1文字も変わらない(サーバーが受け取った会話が同じで、書式を当てた文字列も同じ)
func TestRoundTripPrompt(t *testing.T) {
	g := &fakeGen{st: engine.GenerateStats{StopReason: "eos"}}
	c, _ := newServer(t, g)
	p := tricky()
	if _, _, err := c.Generate(context.Background(), p, engine.GenerateOptions{Temperature: 0, MaxTokens: 50, PresencePenalty: 0},
		func(string) {}, func(string) {}, func(string) {}); err != nil {
		t.Fatal(err)
	}
	got := g.prompts[0]
	// 空のスライスと nil の違いは見ない(保存の形も JSON なので同じになる)
	if a, b := jsonOf(got.Messages), jsonOf(p.Messages); a != b || got.Think != p.Think {
		t.Errorf("会話が変わった:\n got  %s\n want %s", a, b)
	}
	f := format.For("qwen35")
	if a, b := f.BuildPrompt(got), f.BuildPrompt(p); a != b {
		t.Errorf("書式を当てた文字列が変わった:\n got  %q\n want %q", a, b)
	}
	// temperature 0 は greedy として渡る(書かないと既定の 0.7 になる)
	if o := g.opts[0]; o.Temperature != 0 || o.MaxTokens != 50 || o.PresencePenalty != 0 {
		t.Errorf("生成の設定: %+v", o)
	}
}

// サーバーの返事 → 会話 で、生成の結果(崩れた呼び出しの位置も)と記録が engine と同じ形に戻る
func TestRoundTripOutput(t *testing.T) {
	out := format.Output{Thinking: "考え", Content: "読みます", ToolCalls: []format.ToolCall{
		{Call: tool.Call{Name: "read_file", Args: []tool.Arg{{Name: "path", Value: "a.go"}, {Name: "n", Value: " 5"}, {Name: "ok", Value: "True"}, {Name: "flags", Value: `["x", "y"]`}}}},
		{Call: tool.Call{Name: "read_file"}, Raw: "<tool_call>\n<function=read_file>", Error: "崩れています"},
		{Call: tool.Call{Name: "read_file", Args: []tool.Arg{{Name: "n", Value: "3"}}}},
	}}
	st := engine.GenerateStats{PromptTokens: 100, ReusedTokens: 60, PromptSec: 0.5, GenTokens: 20, GenSec: 2, StopReason: "stop"}
	g := &fakeGen{out: out, st: st}
	c, _ := newServer(t, g)

	var prompt string
	var events []string
	gotOut, gotSt, err := c.Generate(context.Background(), tricky(), engine.GenerateOptions{MaxTokens: 50},
		func(s string) { prompt = s },
		func(s string) { events = append(events, "思考:"+s) },
		func(s string) { events = append(events, "答え:"+s) })
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotOut, out) {
		t.Errorf("生成の結果:\n got  %+v\n want %+v", gotOut, out)
	}
	if gotSt != st {
		t.Errorf("生成の記録:\n got  %+v\n want %+v", gotSt, st)
	}
	if prompt != "<prompt>" || !slices.Equal(events, []string{"思考:考え", "答え:読みます"}) {
		t.Errorf("途中経過: %q %q", prompt, events)
	}
}

// 停止(ctx を止める)で、届いた分を cancel として返す(エラーにしない。engine と同じ)
func TestCancel(t *testing.T) {
	g := &fakeGen{block: make(chan struct{}), started: make(chan struct{}, 1)}
	c, _ := newServer(t, g)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-g.started
		time.Sleep(50 * time.Millisecond) // 思考の塊が届くのを待つ
		cancel()
	}()
	out, st, err := c.Generate(ctx, format.Prompt{Messages: []format.Message{{Role: "user", Content: "hi"}}}, engine.GenerateOptions{MaxTokens: 5},
		func(string) {}, func(string) {}, func(string) {})
	if err != nil || st.StopReason != "cancel" || out.Thinking != "考え" {
		t.Errorf("%+v %+v %v", out, st, err)
	}
}

// LoadModel のあとは、そのモデルで生成を頼む(サーバーが違うモデルを読んでいれば読み直す)。Info は最後に知った値
func TestLoadModelAndInfo(t *testing.T) {
	g := &fakeGen{st: engine.GenerateStats{StopReason: "eos"}}
	c, m := newServer(t, g)
	if c.Info() != nil {
		t.Error("まだ聞いていないのに Info がある")
	}
	info, err := c.LoadModel("ollama:qwen3.5:9b")
	if err != nil || info.ID != "ollama:qwen3.5:9b" || c.Info().ID != "ollama:qwen3.5:9b" {
		t.Fatalf("%+v %v", info, err)
	}
	// ほかのクライアントが 4B に戻した
	m.Load(models.Entry{ID: "ollama:qwen3.5:4b"})
	if _, _, err := c.Generate(context.Background(), format.Prompt{Messages: []format.Message{{Role: "user", Content: "hi"}}},
		engine.GenerateOptions{MaxTokens: 5}, func(string) {}, func(string) {}, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"ollama:qwen3.5:9b", "ollama:qwen3.5:4b", "ollama:qwen3.5:9b"}; !slices.Equal(m.loads, want) {
		t.Errorf("読み込んだモデル %v, want %v", m.loads, want)
	}

	l, err := c.Models()
	if err != nil || len(l.Models) != 2 || l.NCtx != 32768 || l.GPULayers["ollama:qwen3.5:9b"] != 999 || l.Current.ID != "ollama:qwen3.5:9b" {
		t.Errorf("一覧: %+v %v", l, err)
	}
}

// 作業領域は作り直さず、入るかを確かめるだけ。トークン数は、発言の無い(ツールの説明だけの)会話でも数えられる
func TestContextAndCount(t *testing.T) {
	c, _ := newServer(t, &fakeGen{})
	if changed, err := c.EnsureContextSize(32768); changed || err != nil {
		t.Errorf("入るはず: %v %v", changed, err)
	}
	if _, err := c.EnsureContextSize(32769); !IsContextError(err) {
		t.Errorf("入らないはず: %v", err)
	}
	n, err := c.CountTokens(format.Prompt{Tools: tricky().Tools})
	if err != nil || n != 1 {
		t.Errorf("ツールだけ: %d %v", n, err)
	}
	if n, _ := c.CountTokens(tricky()); n != 81 {
		t.Errorf("会話: %d", n)
	}
}

// サーバーにつながらないときは ErrUnreachable(理由と対処とつなぎ先が分かる)
func TestUnreachable(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close() // 誰も待ち受けていないポート
	c := New("http://" + addr)
	_, err := c.Models()
	if !errors.Is(err, ErrUnreachable) {
		t.Errorf("%v", err)
	}
	_, _, err = c.Generate(context.Background(), format.Prompt{Messages: []format.Message{{Role: "user", Content: "hi"}}},
		engine.GenerateOptions{MaxTokens: 5}, func(string) {}, func(string) {}, func(string) {})
	if !errors.Is(err, ErrUnreachable) {
		t.Errorf("%v", err)
	}
}

// サーバーのエラーは APIError(code で見分けられる)
func TestAPIError(t *testing.T) {
	c, _ := newServer(t, &fakeGen{})
	_, err := c.LoadModel("none")
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != 404 || ae.Code != "model_not_found" {
		t.Errorf("%#v", err)
	}
}

func jsonOf(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
