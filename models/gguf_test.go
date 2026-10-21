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
