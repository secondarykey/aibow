package main

import (
	"os"
	"path/filepath"
	"testing"
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
