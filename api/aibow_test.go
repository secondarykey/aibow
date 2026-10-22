package api

// aibow_test.go: ステップ8(0015)で足した、aibow 専用の口と欄のテスト(モデル不要)。

import (
	"bufio"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/secondarykey/aibow/engine"
	"github.com/secondarykey/aibow/format"
	"github.com/secondarykey/aibow/models"
	"github.com/secondarykey/aibow/tool"
)

// fakeModels は偽物のモデルの係。読み込むと fakeGen の読み込み中のモデルを書き換える。
type fakeModels struct {
	gen    *fakeGen
	list   []models.Entry
	layers map[string]int
	loads  []string // 読み込んだモデルの ID(順番)
	places []string // 足した置き場所
	// disabled は、管理者が使えないようにしたモデル(Find が DisabledError を返す。0018 の決定事項 13)
	disabled map[string]bool
}

func newFakeModels(g *fakeGen) *fakeModels {
	return &fakeModels{gen: g, layers: map[string]int{}, list: []models.Entry{
		{ID: "ollama:qwen3.5:4b", Name: "qwen3.5:4b", Source: "ollama"},
		{ID: "ollama:qwen3.5:9b", Name: "qwen3.5:9b", Source: "ollama"},
	}}
}

func (m *fakeModels) List() []models.Entry { return m.list }

func (m *fakeModels) Find(name string) (models.Entry, error) {
	for _, e := range m.list {
		if e.ID == name || e.Name == name {
			if m.disabled[e.ID] {
				return models.Entry{}, &models.DisabledError{ID: e.ID}
			}
			return e, nil
		}
	}
	return models.Entry{}, errors.New("見つかりません: " + name)
}

func (m *fakeModels) Load(e models.Entry) (*engine.ModelInfo, error) {
	m.loads = append(m.loads, e.ID)
	m.gen.mu.Lock()
	m.gen.id, m.gen.none = e.ID, false
	m.gen.mu.Unlock()
	return m.gen.Info(), nil
}

func (m *fakeModels) Unload() (bool, error) {
	m.gen.mu.Lock()
	defer m.gen.mu.Unlock()
	was := !m.gen.none
	m.gen.none = true
	return was, nil
}

func (m *fakeModels) Places() []models.Place {
	list := []models.Place{{Path: "models", Origin: models.OriginModels, Kind: "dir"}}
	for _, p := range m.places {
		list = append(list, models.Place{Path: p, Origin: models.OriginAdded, Kind: "dir"})
	}
	return list
}

func (m *fakeModels) AddPlace(path string) error {
	if path == "" || slices.Contains(m.places, path) {
		return errors.New("足せません: " + path)
	}
	m.places = append(m.places, path)
	return nil
}

func (m *fakeModels) RemovePlace(path string) error {
	i := slices.Index(m.places, path)
	if i < 0 {
		return errors.New("足した置き場所ではありません: " + path)
	}
	m.places = slices.Delete(m.places, i, i+1)
	return nil
}

func (m *fakeModels) GPULayers(id string) int {
	if n, ok := m.layers[id]; ok {
		return n
	}
	return 999
}

func (m *fakeModels) SetGPULayers(id string, n int) (*engine.ModelInfo, error) {
	m.layers[id] = n
	if info := m.gen.Info(); info.ID == id {
		return info, nil
	}
	return nil, nil
}

func (m *fakeModels) HasGPU() bool { return true }

func newModelsServer(g *fakeGen, m *fakeModels) *httptest.Server {
	s := New(g, m, 32768, Defaults{Temperature: 0.7, MaxTokens: 100, PresencePenalty: 1.5, Think: true})
	s.now = func() time.Time { return time.Unix(1700000000, 0) }
	return httptest.NewServer(s.Handler())
}

func postPath(t *testing.T, url, path, body string) *http.Response {
	t.Helper()
	res, err := http.Post(url+path, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func decode[T any](t *testing.T, res *http.Response) T {
	t.Helper()
	defer res.Body.Close()
	var v T
	if err := json.NewDecoder(res.Body).Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// /v1/models は使えるモデルを全部、/models は状態と層の数つきで返す
func TestModelLists(t *testing.T) {
	g := &fakeGen{nCtx: 32768}
	m := newFakeModels(g)
	m.layers["ollama:qwen3.5:9b"] = 28
	ts := newModelsServer(g, m)
	defer ts.Close()

	res, _ := http.Get(ts.URL + "/v1/models")
	v1 := decode[modelList](t, res)
	if len(v1.Data) != 2 || v1.Data[1].ID != "ollama:qwen3.5:9b" {
		t.Errorf("/v1/models: %+v", v1)
	}

	res, _ = http.Get(ts.URL + "/models")
	got := decode[serverModels](t, res)
	if got.NCtx != 32768 || !got.GPU || got.Current == nil || got.Current.ID != "ollama:qwen3.5:4b" || len(got.Data) != 2 {
		t.Fatalf("/models: %+v", got)
	}
	if got.Data[0].Status.Value != "loaded" || got.Data[1].Status.Value != "unloaded" ||
		got.Data[0].NGPULayers != 999 || got.Data[1].NGPULayers != 28 || got.Data[1].Name != "qwen3.5:9b" {
		t.Errorf("/models の data: %+v", got.Data)
	}
}

// リクエストの model が違えば読み直す。名前(qwen3.5:9b)でも ID でもよい。知らない名前なら読み込み中のモデルのまま
func TestChatSwitchesModel(t *testing.T) {
	g := &fakeGen{nCtx: 100, tokens: 1, content: []string{"x"}, stop: "eos"}
	m := newFakeModels(g)
	ts := newModelsServer(g, m)
	defer ts.Close()

	for _, model := range []string{"qwen3.5:9b", "ollama:qwen3.5:9b", "gpt-4o", "ollama:qwen3.5:4b"} {
		res := post(t, ts.URL, `{"model":"`+model+`","messages":[{"role":"user","content":"hi"}]}`)
		got := decode[completion](t, res)
		if got.Model != g.Info().ID {
			t.Errorf("%s: 返事のモデル %s", model, got.Model)
		}
	}
	if want := []string{"ollama:qwen3.5:9b", "ollama:qwen3.5:4b"}; !slices.Equal(m.loads, want) {
		t.Errorf("読み込んだモデル %v, want %v", m.loads, want)
	}
}

// 管理者が使えないようにしたモデルは、読み込み中のモデルで代わりに答えず 404 で断る。
// OpenAI 互換の口は知らないモデルと同じ model_not_found、aibow 専用の /models/load は model_disabled(0018 の決定事項 13)
func TestDisabledModel(t *testing.T) {
	g := &fakeGen{nCtx: 100, tokens: 1, content: []string{"x"}, stop: "eos"}
	m := newFakeModels(g)
	m.disabled = map[string]bool{"ollama:qwen3.5:9b": true}
	ts := newModelsServer(g, m)
	defer ts.Close()

	res := post(t, ts.URL, `{"model":"qwen3.5:9b","messages":[{"role":"user","content":"hi"}]}`)
	got := decode[errorBody](t, res)
	if res.StatusCode != http.StatusNotFound || got.Error.Code != "model_not_found" || !strings.Contains(got.Error.Message, "管理者") {
		t.Errorf("チャット: %d %+v", res.StatusCode, got)
	}
	res = postPath(t, ts.URL, "/models/load", `{"model":"qwen3.5:9b"}`)
	got = decode[errorBody](t, res)
	if res.StatusCode != http.StatusNotFound || got.Error.Code != "model_disabled" {
		t.Errorf("/models/load: %d %+v", res.StatusCode, got)
	}
	if len(m.loads) != 0 {
		t.Errorf("読み込んでしまった: %v", m.loads)
	}
}

// /models/load は明示的に読み込み、/aibow/gpu-layers は層の数を覚える(読み込み中のモデルなら読み直した情報を返す)
func TestLoadAndGPULayers(t *testing.T) {
	g := &fakeGen{nCtx: 100}
	m := newFakeModels(g)
	ts := newModelsServer(g, m)
	defer ts.Close()

	got := decode[loadResult](t, postPath(t, ts.URL, "/models/load", `{"model":"qwen3.5:9b"}`))
	if !got.Success || got.Model == nil || got.Model.ID != "ollama:qwen3.5:9b" {
		t.Errorf("load: %+v", got)
	}
	res := postPath(t, ts.URL, "/models/load", `{"model":"none"}`)
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Errorf("知らないモデルの load: %d", res.StatusCode)
	}

	// model を書かなければ読み込み中のモデル
	got = decode[loadResult](t, postPath(t, ts.URL, "/aibow/gpu-layers", `{"n_gpu_layers":20}`))
	if !got.Success || got.Model == nil || m.layers["ollama:qwen3.5:9b"] != 20 {
		t.Errorf("gpu-layers(読み込み中): %+v %v", got, m.layers)
	}
	// 読み込んでいないモデルは覚えるだけ
	got = decode[loadResult](t, postPath(t, ts.URL, "/aibow/gpu-layers", `{"model":"qwen3.5:4b","n_gpu_layers":30}`))
	if !got.Success || got.Model != nil || m.layers["ollama:qwen3.5:4b"] != 30 {
		t.Errorf("gpu-layers(読み込んでいない): %+v %v", got, m.layers)
	}
	res = postPath(t, ts.URL, "/aibow/gpu-layers", `{"model":"qwen3.5:4b"}`)
	res.Body.Close()
	if res.StatusCode != 400 {
		t.Errorf("層の数が無い: %d", res.StatusCode)
	}
}

// /apply-template と /aibow/count-tokens は、チャットと同じ本文を読む
func TestTemplateAndCount(t *testing.T) {
	g := &fakeGen{nCtx: 100, tokens: 42}
	ts := newTestServer(g)
	defer ts.Close()
	body := `{"messages":[{"role":"system","content":"s"},{"role":"user","content":"hi"}]}`
	if got := decode[templateResult](t, postPath(t, ts.URL, "/apply-template", body)); got.Prompt != "<2 件の発言>" {
		t.Errorf("apply-template: %+v", got)
	}
	if got := decode[countResult](t, postPath(t, ts.URL, "/aibow/count-tokens", body)); got.NTokens != 42 {
		t.Errorf("count-tokens: %+v", got)
	}
}

// aibow のクライアントには、モデルに渡した文字列・細かい終了の理由・崩れた呼び出しを aibow 欄で分けて返す。
// 引数は往復で変わらない形でだけ型の値にする(True・前後の空白は文字列のまま)
func TestAibowFieldsNonStream(t *testing.T) {
	g := toolGen()
	g.calls = append(g.calls, format.ToolCall{Call: tool.Call{Name: "read_file", Args: []tool.Arg{{Name: "n", Value: " 5"}}}})
	ts := newTestServer(g)
	defer ts.Close()

	res := post(t, ts.URL, `{"messages":[{"role":"user","content":"読んで"}],"aibow":{"return_prompt":true},`+toolsJSON+`}`)
	got := decode[completion](t, res)
	c := got.Choices[0]
	if c.Message.Content != "読みます" || len(c.Message.ToolCalls) != 2 || *c.FinishReason != "tool_calls" {
		t.Fatalf("返事が違う: %+v", c.Message)
	}
	if a := c.Message.ToolCalls[0].Function.Arguments; a != `{"path":"a.go","n":5,"flags":["x", "y"],"ok":"True","memo":"7"}` {
		t.Errorf("引数(True は文字列のまま): %s", a)
	}
	if a := c.Message.ToolCalls[1].Function.Arguments; a != `{"n":" 5"}` {
		t.Errorf("引数(前後の空白は文字列のまま): %s", a)
	}
	x := got.Aibow
	if x == nil || x.Prompt != "fake prompt" || x.StopReason != "stop" || len(x.BrokenToolCalls) != 1 ||
		x.BrokenToolCalls[0].Index != 1 || x.BrokenToolCalls[0].Raw != "<tool_call>\n<function=read_file>" {
		t.Errorf("aibow 欄が違う: %+v", x)
	}
	if tm := got.Timings; tm == nil || tm.CacheN != 3 || tm.PromptN != 7 || tm.PredictedN != 7 || tm.PredictedMS != 1000 {
		t.Errorf("timings が違う: %+v", got.Timings)
	}
}

func TestAibowFieldsStream(t *testing.T) {
	g := toolGen()
	ts := newTestServer(g)
	defer ts.Close()

	res := post(t, ts.URL, `{"messages":[{"role":"user","content":"読んで"}],"stream":true,"aibow":{"return_prompt":true},`+toolsJSON+`}`)
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
	// 役 → モデルに渡した文字列 → 答え → 呼び出し → 終了の理由(timings と aibow 欄つき)。崩れた呼び出しは答えに足さない
	if len(chunks) != 5 {
		t.Fatalf("塊の数 %d: %+v", len(chunks), chunks)
	}
	if p := chunks[1]; p.Aibow == nil || p.Aibow.Prompt != "fake prompt" || len(p.Choices) != 0 {
		t.Errorf("プロンプトの塊: %+v", p)
	}
	if d := chunks[2].Choices[0].Delta; d.Content != "読みます" {
		t.Errorf("答えの塊: %+v", d)
	}
	last := chunks[4]
	if *last.Choices[0].FinishReason != "tool_calls" || last.Timings == nil || last.Aibow == nil ||
		last.Aibow.StopReason != "stop" || len(last.Aibow.BrokenToolCalls) != 1 || last.Aibow.BrokenToolCalls[0].Index != 1 {
		t.Errorf("終了の塊: %+v %+v", last, last.Aibow)
	}
}

// 履歴の aibow 欄の崩れた呼び出しは、元の並びに戻して書式に渡す(次のプロンプトにモデルが書いたままを入れるため)
func TestAibowBrokenHistory(t *testing.T) {
	g := &fakeGen{nCtx: 100, tokens: 1, stop: "eos"}
	ts := newTestServer(g)
	defer ts.Close()

	res := post(t, ts.URL, `{"messages":[
		{"role":"user","content":"読んで"},
		{"role":"assistant","content":"","tool_calls":[
			{"id":"call_0","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"a\"}"}},
			{"id":"call_2","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"b\"}"}}],
			"aibow":{"broken_tool_calls":[{"index":1,"name":"read_file","raw":"<tool_call>\n<function=read_file>","error":"崩れ"}]}},
		{"role":"tool","content":"A"},{"role":"tool","content":"エラー: 崩れ"},{"role":"tool","content":"B"}]}`)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("ステータス %d", res.StatusCode)
	}
	calls := g.prompts[0].Messages[1].ToolCalls
	if len(calls) != 3 || calls[0].Args[0].Value != "a" || calls[1].Raw != "<tool_call>\n<function=read_file>" ||
		calls[1].Name != "read_file" || calls[2].Args[0].Value != "b" {
		t.Errorf("呼び出しの並び: %+v", calls)
	}

	// index が並びに合わなければ 400
	res = post(t, ts.URL, `{"messages":[{"role":"assistant","content":"","aibow":{"broken_tool_calls":[{"index":3,"raw":"x"}]}}]}`)
	res.Body.Close()
	if res.StatusCode != 400 {
		t.Errorf("合わない index: %d", res.StatusCode)
	}
}

// POST /models/unload は読み込み中のモデルを降ろす。別のモデルを名指ししたら何もしない。降ろしたあとのチャットは model のモデルを読み込む
func TestUnload(t *testing.T) {
	g := &fakeGen{nCtx: 100, tokens: 1, content: []string{"x"}, stop: "eos"}
	m := newFakeModels(g)
	ts := newModelsServer(g, m)
	defer ts.Close()

	res := postPath(t, ts.URL, "/models/unload", `{"model":"qwen3.5:9b"}`)
	if got := decode[loadResult](t, res); !got.Success || g.Info() == nil {
		t.Fatalf("読み込んでいないモデルは降ろさないはず: %+v", got)
	}
	res = postPath(t, ts.URL, "/models/unload", `{"model":""}`)
	if got := decode[loadResult](t, res); !got.Success || g.Info() != nil {
		t.Fatalf("降ろしたはず: %+v", got)
	}
	res, _ = http.Get(ts.URL + "/models")
	if got := decode[serverModels](t, res); got.Current != nil || got.Data[0].Status.Value != "unloaded" {
		t.Errorf("降ろしたあとの /models: %+v", got)
	}
	res = post(t, ts.URL, `{"model":"ollama:qwen3.5:4b","messages":[{"role":"user","content":"hi"}]}`)
	if got := decode[completion](t, res); got.Model != "ollama:qwen3.5:4b" || g.Info() == nil {
		t.Errorf("降ろしたあとのチャットは読み込むはず: %+v", got)
	}
}

// 置き場所を足す・外す。返事はそのあとの一覧。足せないときは理由を 400 で返す。表示だけの設定は作業領域の大きさつき
func TestPlaces(t *testing.T) {
	g := &fakeGen{nCtx: 100}
	m := newFakeModels(g)
	s := New(g, m, 32768, Defaults{MaxTokens: 100})
	s.SetSettings(Settings{Port: 8089, Lib: "lib", ConfigFile: "server.json"})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	res := postPath(t, ts.URL, "/aibow/places/add", `{"path":"/data/models2"}`)
	if got := decode[placeList](t, res); len(got.Places) != 2 || got.Places[1].Path != `/data/models2` || got.Places[1].Origin != models.OriginAdded {
		t.Errorf("足したあとの一覧: %+v", got)
	}
	res = postPath(t, ts.URL, "/aibow/places/add", `{"path":"/data/models2"}`)
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("同じ置き場所は足せないはず: %d", res.StatusCode)
	}
	res.Body.Close()
	res = postPath(t, ts.URL, "/aibow/places/remove", `{"path":"/data/models2"}`)
	if got := decode[placeList](t, res); len(got.Places) != 1 {
		t.Errorf("外したあとの一覧: %+v", got)
	}
	res, _ = http.Get(ts.URL + "/aibow/places")
	if got := decode[placeList](t, res); len(got.Places) != 1 {
		t.Errorf("一覧: %+v", got)
	}
	res, _ = http.Get(ts.URL + "/aibow/settings")
	if got := decode[Settings](t, res); got.NCtx != 32768 || got.Port != 8089 || got.ConfigFile != "server.json" {
		t.Errorf("表示だけの設定: %+v", got)
	}
}

// 入れ替えの途中は current が null になるので、入れ替えている先を loading と status で知らせる(降ろしたのと見分けるため)
func TestServerModelsLoading(t *testing.T) {
	g := &fakeGen{}
	s := New(g, newFakeModels(g), 32768, Defaults{MaxTokens: 100})
	g.mu.Lock()
	g.none, g.loading = true, "ollama:qwen3.5:9b"
	g.mu.Unlock()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/models", nil))
	var out struct {
		Current *engine.ModelInfo `json:"current"`
		Loading string            `json:"loading"`
		Data    []struct {
			ID     string `json:"id"`
			Status struct {
				Value string `json:"value"`
			} `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Current != nil || out.Loading != "ollama:qwen3.5:9b" {
		t.Errorf("current %+v / loading %q", out.Current, out.Loading)
	}
	for _, m := range out.Data {
		want := "unloaded"
		if m.ID == "ollama:qwen3.5:9b" {
			want = "loading"
		}
		if m.Status.Value != want {
			t.Errorf("%s の status: %s, want %s", m.ID, m.Status.Value, want)
		}
	}
}

// サーバーの PC の事情を変える口は、ほかの PC からは 403(0017 の 10-2)。サーバーの PC からと、管理者は通る(0018 の 11-0)
func TestPCSide(t *testing.T) {
	s := &Server{}
	ok := s.pcSide(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	for _, c := range []struct {
		addr  string
		admin bool
		want  int
	}{
		{"127.0.0.1:50000", false, http.StatusNoContent},
		{"[::1]:50000", false, http.StatusNoContent},
		{"192.168.1.20:50000", false, http.StatusForbidden},
		{"[fe80::1]:50000", false, http.StatusForbidden},
		{"192.168.1.20:50000", true, http.StatusNoContent},
	} {
		s.SetAdmin(func(*http.Request) bool { return c.admin })
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/models/unload", nil)
		req.RemoteAddr = c.addr
		ok(rec, req)
		if rec.Code != c.want {
			t.Errorf("%s(管理者 %v): %d(期待 %d)", c.addr, c.admin, rec.Code, c.want)
		}
	}
}
