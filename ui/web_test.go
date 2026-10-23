package ui

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
	"uuid"

	"github.com/secondarykey/aibow"
	"github.com/secondarykey/aibow/api"
	"github.com/secondarykey/aibow/auth"
	"github.com/secondarykey/aibow/engine"
	"github.com/secondarykey/aibow/format"
	"github.com/secondarykey/aibow/models"
	"github.com/secondarykey/aibow/store"
)

// 画面用の口(web.go)のテスト。aibow-server の中と同じ組み立て(ServerHost・席・ツールなし)を、偽物の engine で作る(モデル不要)。

var testFormat = format.For("qwen35")

// fakeEngine は偽物の engine(ServerEngine)。読み込んでいるモデルは fakeModels が書き換える。
type fakeEngine struct {
	mu      sync.Mutex
	model   string
	wait    bool // ctx がキャンセルされるまで生成を終えない(停止のテスト)
	started chan struct{}
	tools   [][]string // 生成のたびに、モデルに知らせたツールの名前
	loading string     // 入れ替えている先のモデル(Loading が返す)
}

func (e *fakeEngine) Loading() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.loading
}

func (e *fakeEngine) Info() *engine.ModelInfo {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.model == "" {
		return nil
	}
	return &engine.ModelInfo{ID: e.model, Name: e.model, Arch: "qwen35", Format: testFormat.Name(), NCtx: 32768}
}

func (e *fakeEngine) CountTokens(p format.Prompt) (int, error) {
	return utf8.RuneCountInString(testFormat.BuildPrompt(p)), nil
}

func (e *fakeEngine) Generate(ctx context.Context, p format.Prompt, opt engine.GenerateOptions,
	onPrompt func(string), onThinking, onContent func(string)) (format.Output, engine.GenerateStats, error) {
	var names []string
	for _, t := range p.Tools {
		names = append(names, t.Name)
	}
	e.mu.Lock()
	e.tools = append(e.tools, names)
	wait, started := e.wait, e.started
	e.mu.Unlock()
	onPrompt(testFormat.BuildPrompt(p))
	if wait {
		onContent("途中")
		if started != nil {
			close(started)
		}
		<-ctx.Done()
		return format.Output{Content: "途中"}, engine.GenerateStats{GenTokens: 1, StopReason: "cancel"}, nil
	}
	onContent("こんにちは")
	return format.Output{Content: "こんにちは"}, engine.GenerateStats{GenTokens: 1, StopReason: "eos"}, nil
}

// fakeModels は偽物のモデルの係(ServerModels)。
type fakeModels struct {
	eng      *fakeEngine
	loads    []string
	layers   map[string]int  // SetGPULayers で覚えた層の数
	added    []string        // AddPlace で足した置き場所
	unloaded int             // Unload を呼んだ回数
	disabled map[string]bool // SetEnabled で使えなくしたモデル(ListAll の印だけ。List と Find は変えない)
}

func (m *fakeModels) List() []models.Entry {
	return []models.Entry{
		{ID: "ollama:a", Name: "a", Source: "ollama", Origin: models.OriginOllama, Place: `C:\Users\server\.ollama\models`},
		{ID: "ollama:b", Name: "b", Source: "ollama", Origin: models.OriginOllama, Place: `C:\Users\server\.ollama\models`},
	}
}

func (m *fakeModels) Find(name string) (models.Entry, error) {
	for _, e := range m.List() {
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
	m.eng.mu.Lock()
	m.eng.model = e.ID
	m.eng.mu.Unlock()
	return m.eng.Info(), nil
}

func (m *fakeModels) HasGPU() bool { return true }

func (m *fakeModels) GPULayers(id string) int {
	if n, ok := m.layers[id]; ok {
		return n
	}
	return 999
}

func (m *fakeModels) SetGPULayers(id string, n int) (*engine.ModelInfo, error) {
	if m.layers == nil {
		m.layers = map[string]int{}
	}
	m.layers[id] = n
	return nil, nil
}

func (m *fakeModels) Unload() (bool, error) {
	m.unloaded++
	m.eng.mu.Lock()
	m.eng.model = ""
	m.eng.mu.Unlock()
	return true, nil
}

func (m *fakeModels) Places() []models.Place {
	l := []models.Place{{Path: `C:\Users\server\.ollama\models`, Origin: models.OriginOllama, Kind: "dir", Models: 2}}
	for _, p := range m.added {
		l = append(l, models.Place{Path: p, Origin: models.OriginAdded, Kind: "dir"})
	}
	return l
}

func (m *fakeModels) AddPlace(path string) error {
	m.added = append(m.added, path)
	return nil
}

func (m *fakeModels) RemovePlace(path string) error {
	return errors.New("外せません: " + path)
}

func (m *fakeModels) ListAll() []models.Entry {
	l := m.List()
	for i := range l {
		l[i].Disabled = m.disabled[l[i].ID]
	}
	return l
}

func (m *fakeModels) SetEnabled(id string, enabled bool) (bool, error) {
	if m.disabled == nil {
		m.disabled = map[string]bool{}
	}
	m.disabled[id] = !enabled
	return false, nil
}

type testWeb struct {
	url  string
	eng  *fakeEngine
	mm   *fakeModels
	seat *api.Seat
	st   *store.SQLite
	// user は、画面用の口を呼んだ利用者(aibow-server の who の代わり)。nil ならログインなしの形の利用者。テストの途中で替える
	user atomic.Pointer[uuid.UUID]
	// admin は、呼んだ人が管理者か(0018 の 11-0)。テストの途中で替える
	admin atomic.Bool
}

// newTestWeb は、aibow-server の browser.go と同じ組み立てで、画面用の口を httptest で立てる。
func newTestWeb(t *testing.T) *testWeb {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	app, _, err := aibow.LoadAppSettings(aibow.WithUser(context.Background(), aibow.LocalUser), st, InitialAppSettings(InitialValues{GPULayers: 999, PresencePenalty: 1.5, HistoryTokens: 8192, ReplyTokens: 8192}))
	if err != nil {
		t.Fatal(err)
	}
	eng := &fakeEngine{model: "ollama:a"}
	mm := &fakeModels{eng: eng}
	seat := api.NewSeat()
	events := NewEvents()
	srv := httptest.NewUnstartedServer(nil)
	srv.Start()
	t.Cleanup(srv.Close)
	svc, life := New(Options{
		Host:  NewServerHost(eng, mm, 32768, srv.URL),
		Store: st, App: app, Version: "0.0.1-test",
		Notify: events, Seat: seat, WithoutTools: true,
	})
	t.Cleanup(life.Stop)
	w := &testWeb{url: srv.URL, eng: eng, mm: mm, seat: seat, st: st}
	// httptest は 127.0.0.1 で待ち受けるので、サーバーの PC の上からかは見ない(管理者かだけで、見せるものが変わることを確かめる)
	srv.Config.Handler = Handler(svc, events, func(*http.Request) (Caller, error) {
		u := w.user.Load()
		switch {
		case u == nil:
			return Caller{ID: aibow.LocalUser, Admin: w.admin.Load()}, nil
		case *u == (uuid.UUID{}):
			return Caller{}, errors.New("ログインしてください")
		}
		return Caller{ID: *u, Admin: w.admin.Load()}, nil
	})
	return w
}

// call はメソッドを呼び、状態と本文を返す。
func (w *testWeb) call(t *testing.T, ctx context.Context, method string, args ...any) (int, []byte) {
	t.Helper()
	if args == nil {
		args = []any{}
	}
	b, _ := json.Marshal(args)
	req, _ := http.NewRequestWithContext(ctx, "POST", w.url+"/aibow/ui/call/"+method, bytes.NewReader(b))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, nil
		}
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out bytes.Buffer
	out.ReadFrom(res.Body)
	return res.StatusCode, out.Bytes()
}

// mustCall はメソッドを呼び、200 でなければ止める。戻り値を v に読む(nil なら読まない)。
func (w *testWeb) mustCall(t *testing.T, v any, method string, args ...any) {
	t.Helper()
	status, body := w.call(t, context.Background(), method, args...)
	if status != http.StatusOK {
		t.Fatalf("%s: %d %s", method, status, body)
	}
	if v != nil {
		if err := json.Unmarshal(body, v); err != nil {
			t.Fatalf("%s の返事が読めません: %v %s", method, err, body)
		}
	}
}

type sseMsg struct {
	name string
	chat aibow.ChatEvent
}

// openEvents は GET /aibow/ui/events につなぎ、届いた知らせを流す。つながった(最初のコメント行が届いた)あとに返る。
func (w *testWeb) openEvents(t *testing.T) <-chan sseMsg {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, "GET", w.url+"/aibow/ui/events", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type: %q", ct)
	}
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	if !sc.Scan() || sc.Text() != ": connected" {
		t.Fatalf("最初の行: %q", sc.Text())
	}
	ch := make(chan sseMsg, 100)
	go func() {
		defer res.Body.Close()
		defer close(ch)
		var name string
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				m := sseMsg{name: name}
				if name == ChatEventName {
					json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &m.chat)
				}
				ch <- m
			}
		}
	}()
	return ch
}

// until は、want の種類の chat イベントが届くまで読み、それまでに届いたものを返す。
func until(t *testing.T, ch <-chan sseMsg, want string) []aibow.ChatEvent {
	t.Helper()
	var got []aibow.ChatEvent
	timeout := time.After(5 * time.Second)
	for {
		select {
		case m, ok := <-ch:
			if !ok {
				t.Fatalf("%s の前に SSE が切れました(届いたもの: %v)", want, types(got))
			}
			got = append(got, m.chat)
			if m.chat.Type == want {
				return got
			}
		case <-timeout:
			t.Fatalf("%s が届きません(届いたもの: %v)", want, types(got))
		}
	}
}

func types(evs []aibow.ChatEvent) []string {
	var s []string
	for _, e := range evs {
		s = append(s, e.Type)
	}
	return s
}

func (w *testWeb) newConversation(t *testing.T) aibow.Conversation {
	t.Helper()
	var c aibow.Conversation
	w.mustCall(t, &c, "NewConversation", uuid.UUID{}, nil)
	return c
}

// 呼べるメソッドは JSON の配列を引数にして呼べ、ブラウザで使わないメソッドとサーバーの PC の事情は出さない
func TestWebCall(t *testing.T) {
	w := newTestWeb(t)
	var v string
	w.mustCall(t, &v, "Version")
	if v != "0.0.1-test" {
		t.Errorf("Version: %q", v)
	}

	var m ModelsResult
	w.mustCall(t, &m, "Models")
	if len(m.Models) != 2 || m.Current == nil || m.Current.ID != "ollama:a" {
		t.Fatalf("Models: %+v", m)
	}
	// 出どころとサーバーの PC のパスは見せない。つなぎ先(サーバーのアドレス)は出す(画面はサーバー越しと同じ見せ方になる)
	if m.Models[0].Origin != "" || m.Models[0].Place != "" || m.Server != w.url {
		t.Errorf("サーバーの PC の事情が見えている: %+v / server %q", m.Models[0], m.Server)
	}
	var ms ModelSettingsResult
	w.mustCall(t, &ms, "ModelSettings")
	if ms.Settings.NCtx != 32768 || ms.Settings.Lib != "" || ms.Settings.Port != 0 {
		t.Errorf("表示だけの設定は作業領域だけのはず: %+v", ms.Settings)
	}

	for _, name := range []string{"SetGPULayers", "ToolPrefs", "ConfirmTool", "BuiltinTools", "ChooseWorkDir", "Nothing"} {
		if status, body := w.call(t, context.Background(), name); status != http.StatusNotFound {
			t.Errorf("%s は 404 のはず: %d %s", name, status, body)
		}
	}
	// サーバーの PC の事情を変えるものは、管理者でなければ 403(0018 の 11-0)
	for _, c := range []struct {
		name string
		args []any
	}{
		{"UnloadModel", nil}, {"SetModelGPULayers", []any{"ollama:a", 10}}, {"ModelPlaces", nil},
		{"AddModelPlace", []any{"/data"}}, {"RemoveModelPlace", []any{"/data"}}, {"SetModelEnabled", []any{"ollama:a", false}},
	} {
		if status, body := w.call(t, context.Background(), c.name, c.args...); status != http.StatusForbidden {
			t.Errorf("%s は 403 のはず: %d %s", c.name, status, body)
		}
	}
	if w.mm.unloaded != 0 || len(w.mm.added) != 0 || len(w.mm.layers) != 0 {
		t.Errorf("断ったのに変わっている: %+v", w.mm)
	}
	if status, body := w.call(t, context.Background(), "TryLimits", 1); status != http.StatusBadRequest {
		t.Errorf("引数の数が違えば 400 のはず: %d %s", status, body)
	}
	if status, body := w.call(t, context.Background(), "TryLimits", "x", 1); status != http.StatusBadRequest {
		t.Errorf("引数の型が違えば 400 のはず: %d %s", status, body)
	}
	// 作業領域は固定。入らなければメソッドのエラー(500)で、理由が分かる
	status, body := w.call(t, context.Background(), "TryLimits", 30000, 8192)
	if status != http.StatusInternalServerError || !strings.Contains(string(body), "aibow-server の作業領域(32768 トークン)に入りません") {
		t.Errorf("TryLimits: %d %s", status, body)
	}
	var changed bool
	w.mustCall(t, &changed, "TryLimits", 8192, 8192)
	if changed {
		t.Error("作業領域は作り直さないはず")
	}
}

// 管理者には、モデルのタブを全部見せて変えさせる(0018 の決定事項 8)
func TestWebAdminModels(t *testing.T) {
	w := newTestWeb(t)
	w.admin.Store(true)

	var m ModelsResult
	w.mustCall(t, &m, "Models")
	if !m.Manage || m.Models[0].Origin != models.OriginOllama || m.Models[0].Place == "" || m.GPULayers["ollama:a"] != 999 {
		t.Fatalf("管理者には出どころ・置き場所・層の数を見せるはず: %+v", m)
	}
	var places []models.Place
	w.mustCall(t, &places, "AddModelPlace", "/data")
	if len(places) != 2 || places[1].Path != "/data" {
		t.Errorf("足したあとの置き場所: %+v", places)
	}
	if status, body := w.call(t, context.Background(), "RemoveModelPlace", "/nothing"); status != http.StatusInternalServerError || !strings.Contains(string(body), "外せません") {
		t.Errorf("外せない置き場所: %d %s", status, body)
	}
	w.mustCall(t, nil, "SetModelGPULayers", "ollama:b", 12)
	if w.mm.layers["ollama:b"] != 12 {
		t.Errorf("層の数: %+v", w.mm.layers)
	}
	w.mustCall(t, nil, "UnloadModel")
	if w.mm.unloaded != 1 {
		t.Error("降ろしていない")
	}
	w.mustCall(t, nil, "SetModelEnabled", "ollama:b", false)
	m = ModelsResult{}
	w.mustCall(t, &m, "Models")
	if !m.Models[1].Disabled || m.Models[0].Disabled {
		t.Errorf("使えないようにしたモデルに印が付いていない: %+v", m.Models)
	}

	// 管理者でなくなれば、見せない
	w.admin.Store(false)
	m = ModelsResult{}
	w.mustCall(t, &m, "Models")
	if m.Manage || m.Models[0].Origin != "" || m.Models[0].Place != "" || len(m.GPULayers) != 0 {
		t.Errorf("管理者でない人に、サーバーの PC の事情が見えている: %+v", m)
	}
}

// 送信は生成が終わるまで返らず、経過は SSE の chat で届く。会話は DB に残り、ツールはモデルに知らせない
func TestWebSend(t *testing.T) {
	w := newTestWeb(t)
	events := w.openEvents(t)
	c := w.newConversation(t)
	if c.Settings.Model != "ollama:a" {
		t.Errorf("新しい会話のモデルは読み込み中のモデルのはず: %q", c.Settings.Model)
	}
	w.mustCall(t, nil, "Send", c.ID, "こんにちは?")
	got := until(t, events, "done")
	var content string
	for _, e := range got {
		if e.ConversationID != c.ID {
			t.Errorf("違う会話の知らせ: %+v", e)
		}
		if e.Type == "content" {
			content += e.Text
		}
	}
	if content != "こんにちは" {
		t.Errorf("答えの断片: %q(届いたもの: %v)", content, types(got))
	}

	var d ConversationDetail
	w.mustCall(t, &d, "Conversation", c.ID)
	if len(d.Messages) != 2 || d.Messages[1].Content != "こんにちは" || d.Generating {
		t.Errorf("保存された発言: %+v", d.Messages)
	}
	if len(w.eng.tools) != 1 || len(w.eng.tools[0]) != 0 {
		t.Errorf("ブラウザの会話ではツールを知らせないはず: %v", w.eng.tools)
	}
}

// 送信の前に、会話のモデルが読み込み中のモデルと違えば(Cline などが入れ替えた)、席の中で読み直す
func TestWebSendReloadsModel(t *testing.T) {
	w := newTestWeb(t)
	events := w.openEvents(t)
	c := w.newConversation(t)
	w.mm.Load(models.Entry{ID: "ollama:b"}) // ほかの人がモデルを入れ替えた
	w.mustCall(t, nil, "Send", c.ID, "こんにちは?")
	got := until(t, events, "done")
	if w.eng.Info().ID != "ollama:a" || !strings.Contains(strings.Join(types(got), ","), "model") {
		t.Errorf("会話のモデルに読み直すはず: %s(届いたもの: %v)", w.eng.Info().ID, types(got))
	}

	// 入力欄の下で選んだときも、席を取ってから読み込む
	var info engine.ModelInfo
	w.mustCall(t, &info, "SwitchModel", "ollama:b")
	if info.ID != "ollama:b" {
		t.Errorf("SwitchModel: %+v", info)
	}
}

// 停止は fetch を止めること(つながりが切れて、リクエストの context が終わる)。そこまでの返事を保存して done が届く
func TestWebStop(t *testing.T) {
	w := newTestWeb(t)
	w.eng.wait, w.eng.started = true, make(chan struct{})
	events := w.openEvents(t)
	c := w.newConversation(t)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-w.eng.started
		cancel()
	}()
	w.call(t, ctx, "Send", c.ID, "長く書いて")
	until(t, events, "done")

	var d ConversationDetail
	w.mustCall(t, &d, "Conversation", c.ID)
	if len(d.Messages) != 2 || d.Messages[1].Content != "途中" || d.Messages[1].Stats == nil || d.Messages[1].Stats.StopReason != "cancel" {
		t.Errorf("止めたところまでの返事が残るはず: %+v", d.Messages)
	}
}

// 席がふさがっていれば(Cline などが生成中)、「待っています」を知らせてから待ち、空いたら生成する
func TestWebSeat(t *testing.T) {
	w := newTestWeb(t)
	events := w.openEvents(t)
	c := w.newConversation(t)
	if !w.seat.TryTake() {
		t.Fatal("席が空いていない")
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.mustCall(t, nil, "Send", c.ID, "こんにちは?")
	}()
	got := until(t, events, "notice")
	if n := got[len(got)-1]; !strings.Contains(n.Text, "待っています") {
		t.Errorf("お知らせ: %+v", n)
	}
	select {
	case <-done:
		t.Fatal("席がふさがっている間に生成した")
	case <-time.After(100 * time.Millisecond):
	}
	w.seat.Release()
	until(t, events, "done")
	<-done
	// 終わったら席を返している
	if !w.seat.TryTake() {
		t.Error("送信が終わったのに席が返っていない")
	}
}

// ほかの人のためにモデルを入れ替えている間は、前のモデルを見せ続ける(画面のモデルが空にならない)。
// 新しい会話もそのモデルで作り、送信すると席の中で読み直す。降ろしたなら「なし」(2026-10-22)
func TestWebModelsWhileLoading(t *testing.T) {
	w := newTestWeb(t)
	var m ModelsResult
	w.mustCall(t, &m, "Models")

	w.eng.mu.Lock()
	w.eng.model, w.eng.loading = "", "ollama:b" // 4B → 9B の入れ替えの途中(前のモデルは解放済み)
	w.eng.mu.Unlock()
	w.mustCall(t, &m, "Models")
	if m.Current == nil || m.Current.ID != "ollama:a" {
		t.Fatalf("入れ替えの途中は、前のモデルを見せるはず: %+v", m.Current)
	}
	// 画面と同じく、プロファイルの値(モデルは決めていない)を写した設定で作る
	var d aibow.ConversationSettings
	w.mustCall(t, &d, "ProfileDefaults", uuid.UUID{})
	var c aibow.Conversation
	w.mustCall(t, &c, "NewConversation", uuid.UUID{}, d)
	if d.Model != "" || c.Settings.Model != "ollama:a" {
		t.Errorf("新しい会話は前のモデルで作るはず: %q(プロファイルのモデル %q)", c.Settings.Model, d.Model)
	}

	// 入れ替えが終わって b になった。送信すると、会話のモデル a に読み直す
	w.eng.mu.Lock()
	w.eng.model, w.eng.loading = "ollama:b", ""
	w.eng.mu.Unlock()
	events := w.openEvents(t)
	w.mustCall(t, nil, "Send", c.ID, "こんにちは?")
	until(t, events, "done")
	if w.eng.Info().ID != "ollama:a" {
		t.Errorf("会話のモデルに読み直すはず: %s", w.eng.Info().ID)
	}

	// 降ろした(入れ替え中でもない)なら「なし」
	w.eng.mu.Lock()
	w.eng.model = ""
	w.eng.mu.Unlock()
	w.mustCall(t, &m, "Models")
	if m.Current != nil {
		t.Errorf("降ろしたら「なし」のはず: %+v", m.Current)
	}
}

// 誰の呼び出しかは aibow-server の who で決まり、会話・プロファイル・表示の設定・生成の途中経過はその人のものだけが見える(0017)。
// 決められなければ 401
// 管理者がプロファイルを配り、受け取った人は読むだけで、写す・新しい会話で選べる。配るのをやめたら選べない(0018 の 11-1)
func TestWebSharedProfile(t *testing.T) {
	w := newTestWeb(t)
	now := time.Now()
	add := func(name string, admin bool) *uuid.UUID {
		u := auth.User{ID: uuid.NewV7(), Name: name, Admin: admin, CreatedAt: now, UpdatedAt: now}
		if err := w.st.CreateUser(context.Background(), u); err != nil {
			t.Fatal(err)
		}
		return &u.ID
	}
	alice, bob := add("alice", true), add("bob", false)

	w.user.Store(alice)
	var p aibow.Profile
	w.mustCall(t, &p, "CreateProfile", "調べもの",
		aibow.ProfileValues{Model: "ollama:a", HistoryTokens: 4096, ReplyTokens: 1024}, aibow.ProfileRules{Fixed: []string{"system"}})
	w.mm.disabled = map[string]bool{"ollama:a": true}
	if status, body := w.call(t, context.Background(), "SetProfileShare", p.ID, aibow.ShareAll, []string{}); status == http.StatusOK || !strings.Contains(string(body), "使えるモデル") {
		t.Errorf("使えないモデルのプロファイルは配れない: %d %s", status, body)
	}
	w.mm.disabled = nil
	w.mustCall(t, nil, "SetProfileShare", p.ID, aibow.ShareUsers, []string{"bob"})

	w.user.Store(bob)
	var list []aibow.ProfileInfo
	w.mustCall(t, &list, "Profiles")
	if len(list) != 1 || list[0].Profile.ID != p.ID || !list[0].Profile.ReadOnly || list[0].Profile.OwnerName != "alice" {
		t.Fatalf("受け取った人の一覧: %+v", list)
	}
	if status, body := w.call(t, context.Background(), "UpdateProfile", list[0].Profile); status == http.StatusOK || !strings.Contains(string(body), "写して") {
		t.Errorf("受け取った人は変えられない: %d %s", status, body)
	}
	if status, _ := w.call(t, context.Background(), "SetProfileShare", p.ID, aibow.ShareAll, []string{}); status == http.StatusOK {
		t.Error("管理者でない人は配れない")
	}
	var cp aibow.Profile
	w.mustCall(t, &cp, "CopyProfile", p.ID)
	if cp.Name != "調べもの(写し)" || cp.ReadOnly || len(cp.Rules.Fixed) != 1 {
		t.Errorf("写し: %+v", cp)
	}
	var c aibow.Conversation
	w.mustCall(t, &c, "NewConversation", p.ID, nil)
	if c.ProfileID != p.ID || c.Settings.HistoryTokens != 4096 {
		t.Errorf("配られたプロファイルの会話: %+v", c)
	}

	w.user.Store(alice)
	w.mustCall(t, nil, "SetProfileShare", p.ID, aibow.ShareNone, []string{})
	w.user.Store(bob)
	w.mustCall(t, &list, "Profiles")
	if len(list) != 1 || list[0].Profile.ID != cp.ID {
		t.Errorf("配るのをやめたら一覧から消える: %+v", list)
	}
	if status, _ := w.call(t, context.Background(), "ProfileDefaults", p.ID); status == http.StatusOK {
		t.Error("配るのをやめたものは新しい会話で選べない")
	}
	if status, body := w.call(t, context.Background(), "Conversation", c.ID); status != http.StatusOK {
		t.Errorf("もう作った会話は開ける: %d %s", status, body)
	}
}

// 会話を共有すると相手は読むだけで開け、写して続けられる。管理者は共有されていなくても読める(0018 の 11-2)
func TestWebSharedConversation(t *testing.T) {
	w := newTestWeb(t)
	now := time.Now()
	add := func(name string, admin bool) *uuid.UUID {
		u := auth.User{ID: uuid.NewV7(), Name: name, Admin: admin, CreatedAt: now, UpdatedAt: now}
		if err := w.st.CreateUser(context.Background(), u); err != nil {
			t.Fatal(err)
		}
		uctx := aibow.WithUser(context.Background(), u.ID)
		app, _, _ := aibow.LoadAppSettings(uctx, w.st, InitialAppSettings(InitialValues{GPULayers: 999, PresencePenalty: 1.5, HistoryTokens: 8192, ReplyTokens: 8192}))
		if _, err := aibow.NewConversations(w.st, nil, app).PrepareProfiles(uctx); err != nil {
			t.Fatal(err)
		}
		return &u.ID
	}
	alice, bob, carol := add("alice", true), add("bob", false), add("carol", false)

	w.user.Store(bob)
	c := w.newConversation(t)
	w.mustCall(t, nil, "Send", c.ID, "こんにちは?")
	if status, _ := w.call(t, context.Background(), "SetConversationShare", c.ID, aibow.ShareUsers, []string{}); status == http.StatusOK {
		t.Error("相手を選ばずに指定した人にはできない")
	}
	w.mustCall(t, nil, "SetConversationShare", c.ID, aibow.ShareUsers, []string{"carol"})

	w.user.Store(carol)
	var refs []aibow.ConversationRef
	w.mustCall(t, &refs, "SharedConversations")
	if len(refs) != 1 || refs[0].ID != c.ID || refs[0].OwnerName != "bob" {
		t.Fatalf("共有された会話: %+v", refs)
	}
	var r aibow.SharedRead
	w.mustCall(t, &r, "ReadConversation", c.ID)
	if r.ByAdmin || len(r.Messages) != 2 {
		t.Errorf("読む: %+v", r)
	}
	// ほかの人の会話に送ってみる。Send は結果を SSE で返すので呼び出しは 200 で終わる。書き込めていないことは、最後に持ち主の会話で確かめる
	w.call(t, context.Background(), "Send", c.ID, "書き込み")
	var cp aibow.Conversation
	w.mustCall(t, &cp, "CopyShared", c.ID)
	var mine []aibow.Conversation
	w.mustCall(t, &mine, "Conversations")
	if len(mine) != 1 || mine[0].ID != cp.ID {
		t.Errorf("写した会話は自分の一覧に出る: %+v", mine)
	}
	if status, _ := w.call(t, context.Background(), "UserConversations", "bob"); status == http.StatusOK {
		t.Error("管理者でない人は人の会話の一覧を見られない")
	}

	w.user.Store(alice)
	w.mustCall(t, &refs, "UserConversations", "bob")
	if len(refs) != 1 {
		t.Errorf("管理者が見る bob の会話: %+v", refs)
	}
	w.mustCall(t, &r, "ReadConversation", c.ID)
	if !r.ByAdmin {
		t.Error("共有されていない会話を管理者として読んだ印が無い")
	}

	w.user.Store(bob)
	var detail ConversationDetail
	w.mustCall(t, &detail, "Conversation", c.ID)
	if len(detail.Messages) != 2 || detail.Conversation.Share != aibow.ShareUsers {
		t.Errorf("ほかの人が書き込めてしまった・共有の印が無い: %d %+v", len(detail.Messages), detail.Conversation)
	}
}

func TestWebUsers(t *testing.T) {
	w := newTestWeb(t)
	mine := w.newConversation(t)
	w.mustCall(t, nil, "SetDisplay", aibow.DisplaySettings{PinQuestion: false, Theme: aibow.ThemeDark})

	other := uuid.NewV7()
	w.user.Store(&other)
	var list []aibow.Conversation
	w.mustCall(t, &list, "Conversations")
	if len(list) != 0 {
		t.Errorf("ほかの人の会話が一覧に出ている: %+v", list)
	}
	if status, body := w.call(t, context.Background(), "Conversation", mine.ID); status != http.StatusInternalServerError || !strings.Contains(string(body), "見つかりません") {
		t.Errorf("ほかの人の会話を開けてしまう: %d %s", status, body)
	}
	var a aibow.AppSettings
	w.mustCall(t, &a, "AppSettings")
	if a.Display != InitialDisplay() {
		t.Errorf("ほかの人には、変えていない表示の設定: %+v", a.Display)
	}

	w.user.Store(nil)
	w.mustCall(t, &list, "Conversations")
	if len(list) != 1 || list[0].ID != mine.ID {
		t.Errorf("自分の会話: %+v", list)
	}
	w.mustCall(t, &a, "AppSettings")
	if a.Display.PinQuestion || a.Display.Theme != aibow.ThemeDark {
		t.Errorf("自分の表示の設定: %+v", a.Display)
	}

	// 生成の途中経過(SSE)は、その会話の持ち主の画面にだけ届く(0017 の 7)
	w.user.Store(&other)
	theirs := w.openEvents(t)
	w.user.Store(nil)
	ours := w.openEvents(t)
	w.mustCall(t, nil, "Send", mine.ID, "こんにちは?")
	until(t, ours, "done")
	select {
	case m := <-theirs:
		t.Errorf("ほかの人の画面に知らせが届いた: %+v", m)
	case <-time.After(200 * time.Millisecond):
	}

	w.user.Store(&uuid.UUID{})
	if status, body := w.call(t, context.Background(), "Conversations"); status != http.StatusUnauthorized || !strings.Contains(string(body), "ログインしてください") {
		t.Errorf("利用者を決められなければ 401: %d %s", status, body)
	}
}
