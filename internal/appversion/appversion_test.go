package appversion

import (
	"strings"
	"testing"
)

func TestNextPatch(t *testing.T) {
	for in, want := range map[string]string{"0.3.1": "0.3.2", "1.9.9": "1.9.10", "1.2": "1.2", "1.2.x": "1.2.x"} {
		if got := nextPatch(in); got != want {
			t.Errorf("nextPatch(%q) = %q, want %q", in, got, want)
		}
	}
}

// go test も go-build の一時フォルダの下で動くので、go run と同じに見え、+dev が付く
func TestVersionUnderGoTest(t *testing.T) {
	if !IsGoRun() {
		t.Skip("テストの exe が go-build の下に無い(-o で作ったなど)")
	}
	v := Version()
	if !strings.HasSuffix(v, "+dev") || v == strings.TrimSpace(version)+"+dev" {
		t.Errorf("patch を1つ上げて +dev を付けるはず: %q(version は %q)", v, strings.TrimSpace(version))
	}
}
