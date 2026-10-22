package main

// models.go: モデルの一覧と読み込み、モデルごとの GPU の層数(api.Models の本物。0015)。
//
// GPU の層数は「その PC の人が決めること」なので、サーバーの PC の server-models.json に覚える(0015 の 9)。
// aibow-server は DB を読まない(DB はアプリのもの。0012)。まだ覚えていないモデルは AIBOW_GPU_LAYERS(既定 999 = 全部)。
// 画面のアプリの DB の model_gpu_layers とは別の値(アプリが直接動かす形に戻したときは、アプリの値を使う)。
//
// 画面で足したモデルの置き場所も server-models.json に覚え、server.json の modelsDir(と Ollama の保存先)に足す形で一覧に出す
// (手で書く server.json は書き換えない。0015 の 22)。
//
// 管理者が使えないようにしたモデルも server-models.json に覚える(0018 の決定事項 13・17)。覚えるのは「使えないモデル」の ID で、
// 置き場所に新しく増えたモデルは、管理者が止めるまで使える。使えないモデルは一覧(List)に出さず、頼まれても読み込まない(Find が断る)。
// 管理者に見せる一覧(ListAll)にだけ、印を付けて出す。

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/secondarykey/aibow"
	"github.com/secondarykey/aibow/engine"
	"github.com/secondarykey/aibow/log"
	"github.com/secondarykey/aibow/models"
)

// modelManager は api.Models の本物。
type modelManager struct {
	eng           *engine.Engine
	catalog       models.Catalog // 手で決めた置き場所(足した置き場所は state にあり、catalogNow で合わせる)
	state         *serverState
	defaultLayers int // まだ覚えていないモデルの GPU の層数

	missing models.MissingWatch
	hidden  models.HiddenWatch
}

// catalogNow は、手で決めた置き場所に、画面で足した置き場所を合わせた一覧の係。
func (m *modelManager) catalogNow() models.Catalog {
	c := m.catalog
	c.Added = m.state.places()
	return c
}

// List は使えるモデルの一覧(管理者が使えないようにしたモデルは出さない)。
// 見つからない足した置き場所は一覧に出さず、見つからなくなったときに1回だけ Warn のログを出す(0015 の 28)。
// 同じ名前のファイルが2か所にあれば先のほうだけを出し、後ろのほうは隠したときに1回だけ Warn のログを出す(0018 の決定事項 15)。
func (m *modelManager) List() []models.Entry {
	return slices.DeleteFunc(m.ListAll(), func(e models.Entry) bool { return e.Disabled })
}

// ListAll は、管理者が使えないようにしたモデルも含めた一覧(管理者に見せる。使えないモデルには Disabled の印)。
func (m *modelManager) ListAll() []models.Entry {
	c := m.catalogNow()
	lost, found := m.missing.Check(c)
	logPlaces(lost, found)
	for _, e := range m.hidden.Check(c) {
		log.Warnf("ほかの置き場所に同じ名前のファイルがあるので、このモデルを一覧に出しません: %s(モデルの ID はファイル名なので、名前を変えると出ます)", e.Path)
	}
	list := c.List()
	for i := range list {
		list[i].Disabled = m.state.disabled(list[i].ID)
	}
	return list
}

// logPlaces は、見つからなくなった置き場所と、また見つかった置き場所をログに出す。
func logPlaces(lost, found []string) {
	for _, p := range lost {
		log.Warnf("モデルの置き場所が見つからないので、そのモデルを一覧に出しません: %s(覚えたまま残します。つなぎ直せば一覧に戻ります)", p)
	}
	for _, p := range found {
		log.Noticef("モデルの置き場所がまた見つかりました: %s", p)
	}
}

// Find は、ID(ollama:qwen3.5:4b・file:x.gguf。以前の形 file:<パス> も読み替える)か、一覧の名前(qwen3.5:4b。ollama list と同じ表示)で探す。
// 探すのは一覧にあるモデルだけ(つないでくる人に、サーバーの PC の好きな GGUF をパスで読み込ませない)。
// 管理者が使えないようにしたモデルなら *models.DisabledError(0018 の決定事項 13)。
func (m *modelManager) Find(name string) (models.Entry, error) {
	id := models.IDOf(name)
	all := m.ListAll()
	names := make([]string, 0, len(all))
	for _, e := range all {
		if e.ID == id || e.Name == name {
			if e.Disabled {
				return models.Entry{}, &models.DisabledError{ID: e.ID}
			}
			return e, nil
		}
		if !e.Disabled {
			names = append(names, e.Name)
		}
	}
	return models.Entry{}, fmt.Errorf("モデル %q が見つかりません。使えるモデル: %s", name, strings.Join(names, "・"))
}

// findArg は、起動のときに読み込むモデル(-model・設定ファイルの model・AIBOW_MODEL)を探す。一覧の外の GGUF もパスで書けば使える
// (サーバーの PC の人が決めることなので)。ただし管理者が使えないようにしたモデルは *models.DisabledError。
func (m *modelManager) findArg(arg string) (models.Entry, error) {
	e, err := m.Find(arg)
	if err == nil {
		return e, nil
	}
	if _, ok := errors.AsType[*models.DisabledError](err); ok {
		return models.Entry{}, err
	}
	e, cerr := m.catalogNow().Find(arg)
	if cerr != nil {
		return models.Entry{}, err
	}
	if m.state.disabled(e.ID) {
		return models.Entry{}, &models.DisabledError{ID: e.ID}
	}
	log.Warnf("起動のときのモデル %s は一覧の置き場所の外にあるので、ほかのモデルに替えたあとは ID では戻れません(置き場所に足すと戻れます)", e.Path)
	return e, nil
}

// SetEnabled は、モデルを使えるか(管理者がモデルのタブで決める。0018 の決定事項 13)を変えて覚える。
// 一覧に無いモデルは決められない。使えなくしたのが読み込み中のモデルなら降ろす(読み込んだままだと、model を書かない
// リクエストがそのモデルで答えてしまうため)。降ろしたら true。順番待ちの席は、呼ぶ側が持つ。
func (m *modelManager) SetEnabled(id string, enabled bool) (bool, error) {
	id = models.IDOf(id)
	if !slices.ContainsFunc(m.ListAll(), func(e models.Entry) bool { return e.ID == id }) {
		return false, fmt.Errorf("モデル %q が一覧にありません", id)
	}
	if err := m.state.setDisabled(id, !enabled); err != nil {
		return false, err
	}
	if enabled {
		log.Noticef("モデル %s を使えるようにしました", id)
		return false, nil
	}
	log.Noticef("モデル %s を使えないようにしました(一覧に出さず、頼まれても読み込みません)", id)
	if cur := m.eng.Info(); cur != nil && cur.ID == id {
		if _, err := m.eng.UnloadModel(); err != nil {
			return false, err
		}
		log.Noticef("使えないようにしたモデル %s を降ろしました", id)
		return true, nil
	}
	return false, nil
}

func (m *modelManager) Load(e models.Entry) (*engine.ModelInfo, error) {
	info, err := m.eng.LoadModelWithLayers(e, m.GPULayers(e.ID))
	if err != nil {
		return nil, err
	}
	if info.Format == "" {
		log.Warnf("モデル %s の書式(種類: %s)には未対応です。生成するとエラーになります", info.ID, info.Arch)
	}
	return info, nil
}

func (m *modelManager) GPULayers(id string) int {
	if n, ok := m.state.gpuLayers(id); ok {
		return n
	}
	return m.defaultLayers
}

// SetGPULayers は層の数を変えて覚える。読み込み中のモデルなら読み直し、読み直せたときだけ覚える
// (読み直せなければ、エンジンが元の層の数で読み直している)。
func (m *modelManager) SetGPULayers(id string, n int) (*engine.ModelInfo, error) {
	var info *engine.ModelInfo
	if cur := m.eng.Info(); cur != nil && cur.ID == id {
		var err error
		if info, err = m.eng.SetGPULayers(n); err != nil {
			return nil, err
		}
	}
	if err := m.state.setGPULayers(id, n); err != nil {
		// 覚えられなくても、今の起動中は効いている。次の起動で戻ることをログに残す
		log.Warn(err.Error())
	}
	return info, nil
}

func (m *modelManager) HasGPU() bool { return m.eng.HasGPU }

func (m *modelManager) Unload() (bool, error) { return m.eng.UnloadModel() }

func (m *modelManager) Places() []models.Place { return m.catalogNow().Places() }

// AddPlace は置き場所を確かめて(サーバーの PC にあるか・フォルダか GGUF か・まだ入っていないか)覚える。
func (m *modelManager) AddPlace(path string) error {
	abs, err := m.catalogNow().CheckPlace(path)
	if err != nil {
		return err
	}
	if err := m.state.setPlaces(append(m.state.places(), abs)); err != nil {
		return err
	}
	log.Noticef("モデルの置き場所を足しました: %s", abs)
	return nil
}

// RemovePlace は足した置き場所を外す。手で決めた置き場所(modelsDir・Ollama)は外せない(server.json か環境変数で変える)。
func (m *modelManager) RemovePlace(path string) error {
	c := m.catalogNow()
	p, ok := c.IsAdded(path)
	if !ok {
		return fmt.Errorf("%s は画面で足した置き場所ではないので外せません(models フォルダと Ollama の保存先は server.json か環境変数で変えます)", path)
	}
	if err := m.state.setPlaces(slices.DeleteFunc(slices.Clone(c.Added), func(s string) bool { return s == p })); err != nil {
		return err
	}
	log.Noticef("モデルの置き場所を外しました: %s", p)
	return nil
}

// ---- server-models.json ----------------------------------------------------------------

// serverState は aibow-server が書くファイル(server-models.json)。手で書く server.json とは分ける(0015 の 19)。
type serverState struct {
	path string
	mu   sync.Mutex
	data stateData
}

// stateData はファイルの中身。
type stateData struct {
	// GPULayers はモデルの ID → GPU に載せる層の数
	GPULayers map[string]int `json:"gpuLayers"`
	// Places は画面で足したモデルの置き場所(フォルダか GGUF のファイル。サーバーの PC の絶対パス。8-4)
	Places []string `json:"places,omitempty"`
	// Disabled は、管理者が使えないようにしたモデルの ID(0018 の決定事項 17。置き場所に新しく増えたモデルは使える)
	Disabled []string `json:"disabled,omitempty"`
}

// loadState はファイルを読む。無ければ空(まだ何も覚えていない)。
func loadState(path string) (*serverState, error) {
	s := &serverState{path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s を読めません: %w", path, err)
	}
	if err := json.Unmarshal(data, &s.data); err != nil {
		return nil, fmt.Errorf("%s を読めません(aibow-server が書くファイルです。壊れているなら消すと、覚えた GPU の層数を忘れて起動します): %w", path, err)
	}
	// 以前の形のモデルの ID(file:<パス>)で覚えた層の数を、今の形(file:<ファイル名>)に書き換える(0018 の決定事項 14・15)
	if layers := aibow.LayersByID(s.data.GPULayers); !maps.Equal(layers, s.data.GPULayers) {
		next := s.data
		next.GPULayers = layers
		if err := writeJSONFile(path, next); err != nil {
			return nil, fmt.Errorf("%s の層の数のモデルの ID を今の形に書き換えられません: %w", path, err)
		}
		s.data = next
		log.Noticef("%s の層の数のモデルの ID を、今の形(file:<ファイル名>)に書き換えました", path)
	}
	return s, nil
}

func (s *serverState) gpuLayers(id string) (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.data.GPULayers[id]
	return n, ok
}

func (s *serverState) setGPULayers(id string, n int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	layers := maps.Clone(s.data.GPULayers)
	if layers == nil {
		layers = map[string]int{}
	}
	layers[id] = n
	next := s.data
	next.GPULayers = layers
	if err := writeJSONFile(s.path, next); err != nil {
		return fmt.Errorf("GPU の層数を %s に書けません(次の起動では覚えていません): %w", s.path, err)
	}
	s.data = next
	return nil
}

func (s *serverState) disabled(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Contains(s.data.Disabled, id)
}

func (s *serverState) setDisabled(id string, disabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := slices.DeleteFunc(slices.Clone(s.data.Disabled), func(d string) bool { return d == id })
	if disabled {
		list = append(list, id)
	}
	next := s.data
	next.Disabled = list
	if err := writeJSONFile(s.path, next); err != nil {
		return fmt.Errorf("使えないモデルを %s に書けません: %w", s.path, err)
	}
	s.data = next
	return nil
}

func (s *serverState) places() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.data.Places)
}

func (s *serverState) setPlaces(places []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.data
	next.Places = places
	if err := writeJSONFile(s.path, next); err != nil {
		return fmt.Errorf("モデルの置き場所を %s に書けません: %w", s.path, err)
	}
	s.data = next
	return nil
}

// writeJSONFile は、一時ファイルに書いてから名前を変える(書いている途中で落ちても、前のファイルが壊れないように)。
func writeJSONFile(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
