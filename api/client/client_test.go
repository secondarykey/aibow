package client

// client_test.go: 本物のサーバーの口(api パッケージ)を httptest で立て、その下の生成だけを偽物にしてテストする(モデル不要)。
// 会話 → JSON → 会話 の往復で1文字も変わらないこと(KV キャッシュを今と同じだけ使い回すため。0015 の 8)を、
// サーバーが受け取った会話と、書式を当てた文字列で確かめる。

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
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
	err     error  // nil でなければ、数えるときにこのエラーを返す
	loading string // 入れ替えている先のモデル(Loading が返す)
}

func (f *fakeGen) Loading() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.loading
}

func (f *fakeGen) Info() *engine.ModelInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.id == "" { // 降ろした
		return nil
	}
	return &engine.ModelInfo{ID: f.id, Name: f.id, NCtx: 32768}
}

func (f *fakeGen) CountTokens(p format.Prompt) (int, error) {
	if f.err != nil {
		return 0, f.err
	}
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
	gen    *fakeGen
	loads  []string
	places []string
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

func (m *fakeModels) Unload() (bool, error) {
	m.gen.mu.Lock()
	defer m.gen.mu.Unlock()
	m.gen.id = ""
	return true, nil
}

func (m *fakeModels) Places() []models.Place {
	list := []models.Place{{Path: "models", Origin: models.OriginModels, Kind: "dir"}}
	for _, p := range m.places {
		list = append(list, models.Place{Path: p, Origin: models.OriginAdded, Kind: "dir"})
	}
	return list
}

func (m *fakeModels) AddPlace(path string) error {
	if path == "" {
		return errors.New("置き場所のパスを書いてください")
	}
	m.places = append(m.places, path)
	return nil
}

func (m *fakeModels) RemovePlace(path string) error {
	m.places = slices.DeleteFunc(m.places, func(p string) bool { return p == path })
	return nil
}

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
// 型のある引数に型に合わない文字(" 5"・True)を書いた値は、tool.Arg の形(JSON の文字列)で持つ(0026)。
func tricky() format.Prompt {
	params := json.RawMessage(`{"type": "object", "properties": {"path": {"type": "string", "description": "a < b && c"}, "n": {"type": "integer"}, "flags": {"type": "array"}, "ok": {"type": "boolean"}}, "required": ["path"]}`)
	return format.Prompt{
		Think: true,
		Tools: []tool.Def{{Name: "read_file", Description: "Read <a> file & more", Parameters: params}},
		Messages: []format.Message{
			{Role: "system", Content: "あなたは aibow です。\n<rules>"},
			{Role: "user", Content: "読んで"},
			{Role: "assistant", Content: "", Thinking: "読もう", ToolCalls: []format.ToolCall{
				{Call: tool.Call{Name: "read_file", Args: []tool.Arg{{Name: "path", Value: "a.go"}, {Name: "n", Value: `" 5"`}, {Name: "flags", Value: `["x", "y"]`}, {Name: "ok", Value: `"True"`}}}},
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

	// 0026 より前に保存した会話(型のある引数でも、モデルが書いたままの文字 " 5"・True を持つ)も、書式を当てた文字列は変わらない
	old := tricky()
	old.Messages[2].ToolCalls[0].Args[1].Value, old.Messages[2].ToolCalls[0].Args[3].Value = " 5", "True"
	if _, _, err := c.Generate(context.Background(), old, engine.GenerateOptions{},
		func(string) {}, func(string) {}, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if a, b := f.BuildPrompt(g.prompts[1]), f.BuildPrompt(old); a != b {
		t.Errorf("前の形の会話で、書式を当てた文字列が変わった:\n got  %q\n want %q", a, b)
	}
}

// サーバーの返事 → 会話 で、生成の結果(崩れた呼び出しの位置も)と記録が engine と同じ形に戻る
func TestRoundTripOutput(t *testing.T) {
	out := format.Output{Thinking: "考え", Content: "読みます", ToolCalls: []format.ToolCall{
		{Call: tool.Call{Name: "read_file", Args: []tool.Arg{{Name: "path", Value: "a.go"}, {Name: "n", Value: `" 5"`}, {Name: "ok", Value: `"True"`}, {Name: "flags", Value: `["x", "y"]`}}}},
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

// 降ろすと Info が nil になる。置き場所を足す・外すと、返事はそのあとの一覧。足せないときはサーバーの理由がエラーになる
func TestUnloadAndPlaces(t *testing.T) {
	c, _ := newServer(t, &fakeGen{})
	if _, err := c.Models(); err != nil || c.Info() == nil {
		t.Fatalf("読み込み中のはず: %v", err)
	}
	if err := c.UnloadModel(); err != nil || c.Info() != nil {
		t.Fatalf("降ろしたはず: %v %+v", err, c.Info())
	}
	if l, err := c.Models(); err != nil || l.Current != nil {
		t.Errorf("降ろしたあとの一覧: %+v %v", l, err)
	}

	pl, err := c.AddPlace("/data/models2")
	if err != nil || len(pl) != 2 || pl[1].Origin != models.OriginAdded {
		t.Errorf("足したあとの一覧: %+v %v", pl, err)
	}
	var ae *APIError
	if _, err := c.AddPlace(""); !errors.As(err, &ae) || ae.Message != "置き場所のパスを書いてください" {
		t.Errorf("足せない理由: %v", err)
	}
	if pl, err := c.RemovePlace("/data/models2"); err != nil || len(pl) != 1 {
		t.Errorf("外したあとの一覧: %+v %v", pl, err)
	}
	if st, err := c.Settings(); err != nil || st.NCtx != 32768 {
		t.Errorf("表示だけの設定: %+v %v", st, err)
	}
}

// モデルが無い・書式に未対応のときは、HTTP 越しでも手元と同じエラーとして見分けられる(画面はメーターを出さないだけにする)
func TestModelErrors(t *testing.T) {
	for _, tc := range []struct{ err, is error }{
		{engine.ErrNoModel, engine.ErrNoModel},
		{format.Unsupported("gemma4"), format.ErrUnsupported},
	} {
		c, _ := newServer(t, &fakeGen{err: tc.err})
		_, err := c.CountTokens(format.Prompt{Messages: []format.Message{{Role: "user", Content: "hi"}}})
		if !errors.Is(err, tc.is) || err.Error() != tc.err.Error() {
			t.Errorf("%v: 返ってきたエラー %v", tc.err, err)
		}
	}
}

// サーバーがほかの人のためにモデルを入れ替えている間(current が null・loading あり)は、画面には前に知ったモデルを見せ続ける(Shown)。
// 本当のこと(Info)は null にしておき、会話の仕組みが送信の前に会話のモデルを読み込ませる。降ろしたなら「なし」(2026-10-22)
func TestModelsWhileServerLoading(t *testing.T) {
	g := &fakeGen{st: engine.GenerateStats{StopReason: "eos"}}
	c, m := newServer(t, g)
	if l, err := c.Models(); err != nil || l.Current == nil || l.Current.ID != "ollama:qwen3.5:4b" {
		t.Fatalf("最初の一覧: %+v %v", l.Current, err)
	}

	// Cline などのために 9B に入れ替えている途中(前のモデルは解放済み)
	g.mu.Lock()
	g.id, g.loading = "", "ollama:qwen3.5:9b"
	g.mu.Unlock()
	l, err := c.Models()
	if err != nil || l.Current == nil || l.Current.ID != "ollama:qwen3.5:4b" || c.Shown() == nil {
		t.Fatalf("入れ替えの途中は、前に知ったモデルを見せるはず: %+v %v", l.Current, err)
	}
	if c.Info() != nil {
		t.Errorf("本当のこと(Info)は null のはず(送信の前に読み込ませるため): %+v", c.Info())
	}

	// 降ろした(入れ替え中でもない)なら「なし」
	g.mu.Lock()
	g.loading = ""
	g.mu.Unlock()
	m.Unload()
	if l, err := c.Models(); err != nil || l.Current != nil || c.Shown() != nil {
		t.Errorf("降ろしたら「なし」のはず: %+v %v", l.Current, err)
	}
}

// LoadModel を呼ばずに(会話のモデルが最後に知った読み込み中のモデルと同じ)生成するときは、そのモデルを頼む。
// そのあとでほかの人が入れ替えていても、サーバーが読み直すので、違うモデルで返事をしない(2026-10-22)
func TestGenerateAsksKnownModel(t *testing.T) {
	g := &fakeGen{st: engine.GenerateStats{StopReason: "eos"}}
	c, m := newServer(t, g)
	if _, err := c.Models(); err != nil {
		t.Fatal(err)
	}
	m.Load(models.Entry{ID: "ollama:qwen3.5:9b"}) // ほかの人が 9B に入れ替えた(こちらはまだ知らない)
	if _, _, err := c.Generate(context.Background(), format.Prompt{Messages: []format.Message{{Role: "user", Content: "hi"}}},
		engine.GenerateOptions{MaxTokens: 5}, func(string) {}, func(string) {}, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"ollama:qwen3.5:9b", "ollama:qwen3.5:4b"}; !slices.Equal(m.loads, want) || g.Info().ID != "ollama:qwen3.5:4b" {
		t.Errorf("前に知った 4B で生成するはず: 読み込んだモデル %v / 読み込み中 %s", m.loads, g.Info().ID)
	}
}

// API キーを決めると、毎回のリクエストに Authorization: Bearer <キー> を付ける(0017 の 10-2)。サーバーが断れば、その理由の文がエラーになる
func TestKey(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		if got != "Bearer aibow_test" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":{"message":"API キーが無いか、違います","type":"invalid_request_error","code":"invalid_api_key"}}`))
			return
		}
		w.Write([]byte(`{"data":[],"ctx":32768,"gpu":true}`))
	}))
	defer srv.Close()
	c := New(srv.URL)
	if _, err := c.Models(); err == nil || !strings.Contains(err.Error(), "API キー") {
		t.Errorf("キーなし: %v", err)
	}
	c.SetKey(" aibow_test ")
	if _, err := c.Models(); err != nil || got != "Bearer aibow_test" {
		t.Errorf("キーあり: %q %v", got, err)
	}
}
