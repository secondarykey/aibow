package secret

import (
	"bytes"
	"testing"
)

// 包んで戻すと元に戻る。Windows では、包んだものに元の値がそのまま入っていない
func TestProtect(t *testing.T) {
	plain := []byte("aibowapp_トークン")
	sealed, err := Protect(plain)
	if err != nil {
		t.Fatal(err)
	}
	if Supported && bytes.Contains(sealed, plain) {
		t.Error("包んだものに元の値がそのまま入っている")
	}
	back, err := Unprotect(sealed)
	if err != nil || !bytes.Equal(back, plain) {
		t.Errorf("戻した値: %q %v", back, err)
	}
	if Supported {
		sealed[len(sealed)-1] ^= 0xFF
		if _, err := Unprotect(sealed); err == nil {
			t.Error("壊れたものは戻せないはず")
		}
	}
}
