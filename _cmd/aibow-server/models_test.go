package main

import (
	"os"
	"path/filepath"
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
