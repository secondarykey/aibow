package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/secondarykey/aibow/models"
)

// 覚えた GPU の層数は server-models.json に書き、次の起動で読める。ファイルが無ければ何も覚えていない
func TestServerState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", stateFileName)
	s, err := loadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.gpuLayers("ollama:qwen3.5:9b"); ok {
		t.Error("まだ覚えていないはず")
	}
	if err := s.setGPULayers("ollama:qwen3.5:9b", 28); err != nil {
		t.Fatal(err)
	}
	s2, err := loadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if n, ok := s2.gpuLayers("ollama:qwen3.5:9b"); !ok || n != 28 {
		t.Errorf("読み直した値: %d %v", n, ok)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Error("一時ファイルが残っている")
	}
}

// 足した置き場所は server-models.json に覚え、起動し直しても残る。server.json の modelsDir は外せない
func TestModelPlaces(t *testing.T) {
	dir := t.TempDir()
	extra := filepath.Join(dir, "extra")
	os.MkdirAll(extra, 0o755)
	if err := os.WriteFile(filepath.Join(extra, "a.gguf"), []byte("GGUF"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, stateFileName)
	s, _ := loadState(path)
	m := &modelManager{catalog: models.Catalog{ModelsDir: filepath.Join(dir, "models"), OllamaDir: filepath.Join(dir, "ollama")}, state: s}
	if err := m.AddPlace(extra); err != nil {
		t.Fatal(err)
	}
	if err := m.AddPlace(filepath.Join(dir, "none")); err == nil {
		t.Error("無い置き場所は足せないはず")
	}
	if err := m.RemovePlace(m.catalog.ModelsDir); err == nil {
		t.Error("models フォルダは外せないはず")
	}
	if err := s.setGPULayers("x", 1); err != nil { // 層の数を書いても置き場所は消えない
		t.Fatal(err)
	}

	s2, _ := loadState(path)
	m2 := &modelManager{catalog: m.catalog, state: s2}
	if l := m2.List(); len(l) != 1 || l[0].Origin != models.OriginAdded {
		t.Fatalf("起動し直したあとの一覧: %+v", l)
	}
	if err := m2.RemovePlace(extra); err != nil {
		t.Fatal(err)
	}
	if l := m2.List(); len(l) != 0 {
		t.Errorf("外したあとの一覧: %+v", l)
	}
}

// 管理者が使えないようにしたモデルは一覧に出さず、探しても DisabledError。パスでは読み込ませない。
// 以前の形の ID で覚えた層の数は、読み込むときに今の形へ書き換える(0018 の決定事項 13〜15・17)
func TestDisabledModels(t *testing.T) {
	dir := t.TempDir()
	mdir := filepath.Join(dir, "models")
	os.MkdirAll(mdir, 0o755)
	for _, name := range []string{"a.gguf", "b.gguf"} {
		if err := os.WriteFile(filepath.Join(mdir, name), []byte("GGUF"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	outside := filepath.Join(dir, "outside.gguf")
	os.WriteFile(outside, []byte("GGUF"), 0o644)

	path := filepath.Join(dir, stateFileName)
	old, _ := json.Marshal(map[string]any{"gpuLayers": map[string]int{"file:" + filepath.Join(mdir, "a.gguf"): 20, "ollama:x": 5}})
	if err := os.WriteFile(path, old, 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := loadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if n, ok := s.gpuLayers("file:a.gguf"); !ok || n != 20 {
		t.Errorf("以前の形の ID の層の数を読み替えていない: %d %v", n, ok)
	}
	if b, _ := os.ReadFile(path); strings.Contains(string(b), mdir) {
		t.Errorf("ファイルを書き換えていない: %s", b)
	}

	m := &modelManager{catalog: models.Catalog{ModelsDir: mdir, OllamaDir: filepath.Join(dir, "ollama")}, state: s}
	if err := s.setDisabled("file:b.gguf", true); err != nil {
		t.Fatal(err)
	}
	if l := m.List(); len(l) != 1 || l[0].ID != "file:a.gguf" {
		t.Errorf("使えるモデルの一覧: %+v", l)
	}
	if l := m.ListAll(); len(l) != 2 || !l[1].Disabled {
		t.Errorf("管理者に見せる一覧: %+v", l)
	}
	if _, err := m.Find("file:b.gguf"); err == nil {
		t.Error("使えないモデルが見つかった")
	} else if _, ok := errors.AsType[*models.DisabledError](err); !ok {
		t.Errorf("DisabledError のはず: %v", err)
	}
	if e, err := m.Find("file:" + filepath.Join(mdir, "a.gguf")); err != nil || e.ID != "file:a.gguf" {
		t.Errorf("以前の形の ID で探せない: %+v %v", e, err)
	}
	if _, err := m.Find(outside); err == nil {
		t.Error("一覧の外のファイルをパスで探せてしまう(つないでくる人に読み込ませない)")
	}
	if e, err := m.findArg(outside); err != nil || e.ID != "file:outside.gguf" {
		t.Errorf("起動のときはパスで書ける: %+v %v", e, err)
	}
	if _, err := m.findArg(filepath.Join(mdir, "b.gguf")); err == nil {
		t.Error("起動のときも、使えないモデルは断る")
	}
	s2, _ := loadState(path)
	if !s2.disabled("file:b.gguf") {
		t.Error("使えないモデルを覚えていない")
	}
}
