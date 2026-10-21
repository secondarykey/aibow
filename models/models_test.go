package models

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

	c := Catalog{ModelsDir: filepath.Join(dir, "none"), OllamaDir: dir}
	if _, err := c.Find("ollama:qwen3.5:4b"); err != nil {
		t.Error(err)
	}
	if _, err := c.Find("ollama:qwen3.5:cloud"); err == nil {
		t.Error("本体のないモデルは見つからないはず")
	}
}

// 足した置き場所(フォルダ・GGUF のファイル)のモデルも一覧に出し、どこから来たかを付ける。
// 見つからない置き場所は一覧に出さず、Missing で分かる。同じファイルは2回出さない
func TestAddedPlaces(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) string {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write("models/a.gguf", "GGUF")
	write("extra/b.gguf", "GGUF")
	single := write("single/c.gguf", "GGUF")
	gone := filepath.Join(dir, "gone")

	c := Catalog{ModelsDir: filepath.Join(dir, "models"), OllamaDir: filepath.Join(dir, "ollama"),
		Added: []string{filepath.Join(dir, "extra"), single, gone, filepath.Join(dir, "extra", "b.gguf")}}
	got := map[string]Entry{}
	for _, e := range c.List() {
		got[e.Name] = e
	}
	if len(got) != 3 {
		t.Fatalf("3件のはず(同じファイルは1回): %+v", got)
	}
	if got["a.gguf"].Origin != OriginModels || got["b.gguf"].Origin != OriginAdded || got["c.gguf"].Place != single {
		t.Errorf("どこから来たか: %+v", got)
	}
	if m := c.Missing(); len(m) != 1 || m[0].Path != gone {
		t.Errorf("見つからない置き場所: %+v", m)
	}
	if pl := c.Places(); len(pl) != 6 || pl[1].Missing == "" || pl[3].Kind != "file" {
		t.Errorf("置き場所の一覧: %+v", pl)
	}

	// 足せるのは、フォルダか GGUF のファイルで、まだ入っていないもの
	write("other/x.gguf", "not gguf")
	write("other/y.txt", "GGUF")
	for _, bad := range []string{"", gone, filepath.Join(dir, "other", "x.gguf"), filepath.Join(dir, "other", "y.txt"),
		filepath.Join(dir, "models"), single} {
		if _, err := c.CheckPlace(bad); err == nil {
			t.Errorf("%q は足せないはず", bad)
		}
	}
	if p, err := c.CheckPlace(filepath.Join(dir, "other")); err != nil || p != filepath.Join(dir, "other") {
		t.Errorf("フォルダは足せるはず: %q %v", p, err)
	}
	if _, ok := c.IsAdded(single); !ok {
		t.Error("足した置き場所のはず")
	}
	if _, ok := c.IsAdded(c.ModelsDir); ok {
		t.Error("models フォルダは足した置き場所ではない")
	}
}

// 見つからなくなったときと、また見つかったときだけ知らせる
func TestMissingWatch(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "extra")
	c := Catalog{Added: []string{p}}
	var w MissingWatch
	if lost, _ := w.Check(c); len(lost) != 1 {
		t.Errorf("見つからないはず: %v", lost)
	}
	if lost, found := w.Check(c); len(lost)+len(found) != 0 {
		t.Errorf("2回目は知らせないはず: %v %v", lost, found)
	}
	os.MkdirAll(p, 0o755)
	if _, found := w.Check(c); len(found) != 1 {
		t.Errorf("また見つかったはず: %v", found)
	}
}
