package main

import (
	"os"
	"path/filepath"
	"testing"
)

// Ollama の保存フォルダを模したものを作り、一覧と名前が正しく出ることを確かめる
func TestListOllamaModels(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manifest := func(digest string) string {
		return `{"layers":[
			{"mediaType":"application/vnd.ollama.image.template","digest":"sha256:tmpl","size":10},
			{"mediaType":"application/vnd.ollama.image.model","digest":"` + digest + `","size":1073741824}]}`
	}
	write("blobs/sha256-aaa", "gguf")
	write("blobs/sha256-bbb", "gguf")
	write("manifests/registry.ollama.ai/library/qwen3.5/4b", manifest("sha256:aaa"))
	write("manifests/hf.co/someone/repo/Q4_K_M", manifest("sha256:bbb"))
	write("manifests/registry.ollama.ai/library/qwen3.5/cloud", `{"layers":[]}`)       // 本体なし
	write("manifests/registry.ollama.ai/library/broken/x", manifest("sha256:missing")) // blob がない

	got := listOllamaModels(dir)
	if len(got) != 2 {
		t.Fatalf("2件のはずが %d 件: %+v", len(got), got)
	}
	want := map[string]string{
		"ollama:qwen3.5:4b":                filepath.Join(dir, "blobs", "sha256-aaa"),
		"ollama:hf.co/someone/repo:Q4_K_M": filepath.Join(dir, "blobs", "sha256-bbb"),
	}
	for _, m := range got {
		if want[m.ID] != m.Path {
			t.Errorf("%s: path %q", m.ID, m.Path)
		}
		if m.SizeGB != 1 {
			t.Errorf("%s: size %v", m.ID, m.SizeGB)
		}
	}

	c := ModelCatalog{ModelsDir: filepath.Join(dir, "none"), OllamaDir: dir}
	if _, err := c.Find("ollama:qwen3.5:4b"); err != nil {
		t.Error(err)
	}
	if _, err := c.Find("ollama:qwen3.5:cloud"); err == nil {
		t.Error("本体のないモデルは見つからないはず")
	}
}
