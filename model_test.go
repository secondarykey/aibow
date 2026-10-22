package aibow

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	apiclient "github.com/secondarykey/aibow/api/client"
	"github.com/secondarykey/aibow/engine"
)

// HTTP 越しの生成(api/client。0015)は、手元の engine と同じ口で会話の仕組みに渡せる。
// 会話の仕組みのコードを変えずに、相手がネットワークの先に変わるだけ、を型で確かめる
var (
	_ Generator   = (*apiclient.Client)(nil)
	_ ModelLoader = (*apiclient.Client)(nil)
)

// fakeLoader は、会話のモデルを読み込む係の偽物。読み込んだモデルの ID を覚え、fakeGen が読み込んでいることにする。
type fakeLoader struct {
	g      *fakeGen
	loaded []string
	err    error
}

func (l *fakeLoader) LoadModel(id string) (*engine.ModelInfo, error) {
	if l.err != nil {
		return nil, l.err
	}
	l.loaded = append(l.loaded, id)
	l.g.model = id
	return &engine.ModelInfo{ID: id, Name: id, LoadSec: 1.5}, nil
}

// eventTexts は、イベントのうち typ のものの Text を並べる。
func eventTexts(evs []ChatEvent, typ string) []string {
	var out []string
	for _, ev := range evs {
		if ev.Type == typ {
			out = append(out, ev.Text)
		}
	}
	return out
}

// 会話ごとにモデルを持ち、読み込んでいるモデルと違う会話に送信すると読み直す。同じなら読み直さない(0014 の 3)
func TestSendLoadsConversationModel(t *testing.T) {
	g := &fakeGen{content: "ok"}
	cs, _ := newTestConversations(t, g)
	l := &fakeLoader{g: g}
	cs.SetModelLoader(l)

	a := mustCreate(t, cs)
	if a.Settings.Model != "file:fake" {
		t.Fatalf("新しい会話はプロファイルのモデル: %q", a.Settings.Model)
	}
	send(t, cs, a, "x")
	if len(l.loaded) != 0 {
		t.Errorf("同じモデルなら読み直さない: %v", l.loaded)
	}

	// 入力欄の下で 9B を選んだ会話(会話のモデルを変える)
	a.Settings.Model = "ollama:qwen3.5:9b"
	if err := cs.UpdateSettings(ctx, a.ID, a.Settings); err != nil {
		t.Fatal(err)
	}
	evs := send(t, cs, a, "y")
	if !slices.Equal(l.loaded, []string{"ollama:qwen3.5:9b"}) {
		t.Errorf("違うモデルなら送信の前に読み直す: %v", l.loaded)
	}
	if len(eventTexts(evs, "model_loading")) != 1 || len(eventTexts(evs, "model")) != 1 || len(eventTexts(evs, "error")) != 0 {
		t.Errorf("読み込み中と読み込んだことを画面に知らせる: %+v", evs)
	}

	// 元の会話に戻って送信すると、その会話のモデルに読み直す。作り直す・今すぐ要約も同じ
	b := mustCreate(t, cs)
	send(t, cs, b, "z")
	var none []ChatEvent
	cs.Regenerate(context.Background(), a.ID, func(ev ChatEvent) { none = append(none, ev) })
	if !slices.Equal(l.loaded, []string{"ollama:qwen3.5:9b", "file:fake", "ollama:qwen3.5:9b"}) {
		t.Errorf("会話を切り替えて送信するたびに、その会話のモデルにする: %v", l.loaded)
	}
	cs.Compact(context.Background(), b.ID, func(ChatEvent) {})
	if l.loaded[len(l.loaded)-1] != "file:fake" {
		t.Errorf("今すぐ要約もその会話のモデルで: %v", l.loaded)
	}
}

// 会話のモデルを読み込めなければ、生成せずにエラーを返す
func TestSendFailsWhenModelCannotLoad(t *testing.T) {
	g := &fakeGen{content: "ok"}
	cs, _ := newTestConversations(t, g)
	cs.SetModelLoader(&fakeLoader{g: g, err: errors.New("見つかりません")})
	c := mustCreate(t, cs)
	c.Settings.Model = "ollama:gone"
	cs.UpdateSettings(ctx, c.ID, c.Settings)
	var errs []string
	cs.Send(context.Background(), c.ID, "x", func(ev ChatEvent) {
		if ev.Type == "error" {
			errs = append(errs, ev.Error)
		}
	})
	if len(errs) != 1 || len(g.calls) != 0 {
		t.Errorf("生成せずにエラー: %v %d", errs, len(g.calls))
	}
	if _, ms, _ := cs.Get(ctx, c.ID); len(ms) != 0 {
		t.Errorf("発言も保存しない: %+v", ms)
	}
	if !strings.Contains(errs[0], "選び直して") {
		t.Errorf("手元のモデルの対処が無い: %s", errs[0])
	}

	// 対処まで書いてあるエラー(aibow-server につながらない、など)には、会話の仕組みの対処を書き足さない(食い違うため。0015)
	cs.SetModelLoader(&fakeLoader{g: g, err: remedied{}})
	errs = nil
	cs.Send(context.Background(), c.ID, "x", func(ev ChatEvent) {
		if ev.Type == "error" {
			errs = append(errs, ev.Error)
		}
	})
	if len(errs) != 1 || strings.Contains(errs[0], "選び直して") || !strings.Contains(errs[0], "起動してください") {
		t.Errorf("対処が食い違う: %v", errs)
	}
}

// remedied は対処まで書いてあるエラー(HasRemedy の約束を満たす)。
type remedied struct{}

func (remedied) Error() string {
	return "aibow-server につながりません。aibow-server を起動してください"
}
func (remedied) HasRemedy() bool { return true }

// モデルを持たない会話(この機能より前)には、起動したときに最後に使ったモデルを写す。
// プロファイルにモデルが無ければ(初めての起動)、新しい会話は読み込んでいるモデルにする
func TestFillMissingModels(t *testing.T) {
	g := &fakeGen{content: "ok"}
	cs, st := newTestConversations(t, g)
	c := mustCreate(t, cs)
	c.Settings.Model = ""
	st.UpdateConversation(ctx, c)
	if n, err := cs.FillMissingModels(ctx, ""); n != 0 || err != nil {
		t.Errorf("最後に使ったモデルが無ければ何もしない: %d %v", n, err)
	}
	if n, err := cs.FillMissingModels(ctx, "ollama:qwen3.5:4b"); n != 1 || err != nil {
		t.Fatalf("写した数: %d %v", n, err)
	}
	if got, _, _ := cs.Get(ctx, c.ID); got.Settings.Model != "ollama:qwen3.5:4b" {
		t.Errorf("写したモデル: %q", got.Settings.Model)
	}
	if n, _ := cs.FillMissingModels(ctx, "ollama:qwen3.5:9b"); n != 0 {
		t.Error("持っている会話は変えない")
	}

	empty := testDefaults
	empty.Model = ""
	if c, _ := cs.Create(ctx, &empty); c.Settings.Model != "file:fake" {
		t.Errorf("モデルの無い設定で作ると、読み込んでいるモデル: %q", c.Settings.Model)
	}
}

// プロファイルのモデルは、既定を保存したとき(「この設定を新しい会話の既定にする」)だけ変わる。モデルを持たない設定では変えない
func TestDefaultsKeepModel(t *testing.T) {
	cs, _ := newTestConversations(t, &fakeGen{})
	d := mustDefaults(t, cs)
	d.Model = "ollama:qwen3.5:9b"
	if err := cs.SetDefaults(ctx, d); err != nil {
		t.Fatal(err)
	}
	if got := mustDefaultProfile(t, cs).Values.Model; got != "ollama:qwen3.5:9b" {
		t.Errorf("既定にするとモデルも写す: %q", got)
	}
	d.Model = ""
	cs.SetDefaults(ctx, d)
	if got := mustDefaults(t, cs).Model; got != "ollama:qwen3.5:9b" {
		t.Errorf("モデルを持たない設定では変えない: %q", got)
	}
}

// GPU の層数はモデルごとに覚える。前の版の DB は、今の層の数を最後に使ったモデルの値にする。覚えていないモデルは全体の値(0014 の 4)
func TestModelGPULayers(t *testing.T) {
	st := newMemStore()
	SaveAppSetting(ctx, st, SettingGPULayers, 20)
	SaveAppSetting(ctx, st, SettingLastModel, "ollama:qwen3.5:9b")
	initial := AppSettings{Workspace: WorkspaceSettings{GPULayers: 999}, Defaults: testDefaults}
	a, notes, err := LoadAppSettings(ctx, st, initial)
	if err != nil || len(notes) != 1 {
		t.Fatalf("引き継ぎを知らせる: %q %v", notes, err)
	}
	if a.Workspace.LayersFor("ollama:qwen3.5:9b") != 20 || a.Workspace.LayersFor("ollama:qwen3.5:4b") != 20 {
		t.Errorf("今の値を最後に使ったモデルの値にし、覚えていないモデルは全体の値: %+v", a.Workspace)
	}
	SaveAppSetting(ctx, st, SettingModelGPULayers, map[string]int{"ollama:qwen3.5:9b": 20, "ollama:qwen3.5:4b": 999})
	SaveAppSetting(ctx, st, SettingGPULayers, 30)
	a, notes, _ = LoadAppSettings(ctx, st, initial)
	if len(notes) != 0 || a.Workspace.LayersFor("ollama:qwen3.5:4b") != 999 || a.Workspace.LayersFor("file:other") != 30 {
		t.Errorf("2回目は引き継がず、覚えた値を使う: %q %+v", notes, a.Workspace)
	}
	// まだ層の数を変えたことが無い(環境変数の値のまま)なら、覚えない
	b, notes, _ := LoadAppSettings(ctx, newMemStore(), initial)
	if len(notes) != 0 || len(b.Workspace.ModelGPULayers) != 0 {
		t.Errorf("層の数が保存されていなければ何もしない: %q %+v", notes, b.Workspace)
	}
}
