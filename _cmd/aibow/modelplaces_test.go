package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// 足した置き場所は model-places.json に書き、次の起動で読める。壊れたファイルは書き換えない
func TestPlacesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "model-places.json")
	f, err := loadPlacesFile(path)
	if err != nil || len(f.list()) != 0 {
		t.Fatalf("無いファイルは空のはず: %v %v", f.list(), err)
	}
	if err := f.set([]string{`D:\models2`}); err != nil {
		t.Fatal(err)
	}
	f2, err := loadPlacesFile(path)
	if err != nil || !slices.Equal(f2.list(), []string{`D:\models2`}) {
		t.Errorf("読み直した置き場所: %v %v", f2.list(), err)
	}

	os.WriteFile(path, []byte("{"), 0o644)
	f3, err := loadPlacesFile(path)
	if err == nil {
		t.Fatal("壊れたファイルはエラーのはず")
	}
	if err := f3.set([]string{"x"}); err == nil {
		t.Error("壊れたファイルは書き換えないはず")
	}
	if b, _ := os.ReadFile(path); string(b) != "{" {
		t.Errorf("ファイルが変わった: %s", b)
	}
}
