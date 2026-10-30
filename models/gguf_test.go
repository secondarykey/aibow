package models

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// GGUF のメタデータを読み飛ばして、general.architecture だけを取り出せる
func TestArch(t *testing.T) {
	var b bytes.Buffer
	w := func(v any) { binary.Write(&b, binary.LittleEndian, v) }
	str := func(s string) { w(uint64(len(s))); b.WriteString(s) }
	b.WriteString("GGUF")
	w(uint32(3)) // 版
	w(uint64(0)) // テンソルの数
	w(uint64(4)) // メタデータの数
	// 前に別の値(数・文字列の配列・数の配列)を置いて、読み飛ばしを確かめる
	str("general.alignment")
	w(uint32(ggufUint32))
	w(uint32(32))
	str("tokenizer.ggml.tokens")
	w(uint32(ggufArray))
	w(uint32(ggufString))
	w(uint64(2))
	str("<s>")
	str("こんにちは")
	str("tokenizer.ggml.scores")
	w(uint32(ggufArray))
	w(uint32(ggufFloat32))
	w(uint64(3))
	w([]float32{1, 2, 3})
	str("general.architecture")
	w(uint32(ggufString))
	str("gemma4")

	path := filepath.Join(t.TempDir(), "m.gguf")
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if a, err := Arch(path); err != nil || a != "gemma4" {
		t.Errorf("種類: %q %v", a, err)
	}

	// 途中で切れたファイル・GGUF でないファイルはエラー
	os.WriteFile(path, b.Bytes()[:b.Len()-3], 0o644)
	if _, err := Arch(path); err == nil {
		t.Error("切れたファイルはエラーのはず")
	}
	os.WriteFile(path, []byte("not gguf at all"), 0o644)
	if _, err := Arch(path); err == nil {
		t.Error("GGUF でないファイルはエラーのはず")
	}
}

// GGUF が勧めるサンプリングの値(general.sampling.*)も読む。general.* の後ろ(語彙など)は読まない(0026 の決定事項 37)
func TestReadHeaderSampling(t *testing.T) {
	var b bytes.Buffer
	w := func(v any) { binary.Write(&b, binary.LittleEndian, v) }
	str := func(s string) { w(uint64(len(s))); b.WriteString(s) }
	b.WriteString("GGUF")
	w(uint32(3))
	w(uint64(0))
	w(uint64(5))
	str("general.architecture")
	w(uint32(ggufString))
	str("gemma4")
	str("general.sampling.temp")
	w(uint32(ggufFloat32))
	w(float32(1))
	str("general.sampling.top_k")
	w(uint32(ggufInt32))
	w(int32(64))
	str("tokenizer.ggml.model")
	w(uint32(ggufString))
	str("gemma4")
	str("general.sampling.top_p") // general.* の後ろにあるものは読まない
	w(uint32(ggufFloat32))
	w(float32(0.95))

	path := filepath.Join(t.TempDir(), "m.gguf")
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	h, err := ReadHeader(path)
	if err != nil || h.Arch != "gemma4" || h.Sampling == nil {
		t.Fatalf("%+v %v", h, err)
	}
	if s := h.Sampling; s.Temp == nil || *s.Temp != 1 || s.TopK == nil || *s.TopK != 64 || s.TopP != nil {
		t.Errorf("サンプリング: %+v", *s)
	}
}

// チャットテンプレートは語彙の配列の後ろにあるので、配列を読み飛ばして読む(0027 の決定事項 2。書式を選ぶのに使う)
func TestReadHeaderChatTemplate(t *testing.T) {
	var b bytes.Buffer
	w := func(v any) { binary.Write(&b, binary.LittleEndian, v) }
	str := func(s string) { w(uint64(len(s))); b.WriteString(s) }
	b.WriteString("GGUF")
	w(uint32(3))
	w(uint64(0))
	w(uint64(3))
	str("general.architecture")
	w(uint32(ggufString))
	str("llama")
	str("tokenizer.ggml.tokens")
	w(uint32(ggufArray))
	w(uint32(ggufString))
	w(uint64(3))
	str("<|begin_of_text|>")
	str("a")
	str("b")
	str("tokenizer.chat_template")
	w(uint32(ggufString))
	str("{{- '<|start_header_id|>' + role }}")

	path := filepath.Join(t.TempDir(), "m.gguf")
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	h, err := ReadHeader(path)
	if err != nil || h.Arch != "llama" || h.ChatTemplate != "{{- '<|start_header_id|>' + role }}" {
		t.Errorf("%+v %v", h, err)
	}
}
