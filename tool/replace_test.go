package tool

import (
	"errors"
	"testing"
)

// Replace は、ファイルによらない置き換え(0045 の決定事項 4)。edit_file の文は write_test.go で確かめている。
func TestReplace(t *testing.T) {
	r, err := Replace("a\nb\nc\n", "b\n", "B\n")
	if err != nil || r.Text != "a\nB\nc\n" || r.Line != 2 || r.Reindented {
		t.Errorf("1か所: %+v, %v", r, err)
	}

	if _, err := Replace("a\nb\n", "x", "y"); !errors.Is(err, ErrNotFound) {
		t.Errorf("見つからない: %v", err)
	}
	if _, err := Replace("a\nb\n", "", "y"); !errors.Is(err, ErrEmptyOld) {
		t.Errorf("空: %v", err)
	}
	if _, err := Replace("a\nb\n", "a", "a"); !errors.Is(err, ErrSameText) {
		t.Errorf("同じ: %v", err)
	}
}

// 何か所あったかは errors.As で取り出せ、errors.Is(err, ErrAmbiguous) も真。
func TestReplaceAmbiguous(t *testing.T) {
	_, err := Replace("x\nx\nx\n", "x", "y")
	if !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("ErrAmbiguous にならない: %v", err)
	}
	var amb *AmbiguousError
	if !errors.As(err, &amb) || amb.Count != 3 {
		t.Errorf("数が取り出せない: %v", err)
	}
}

// 字下げがどの行も同じだけ違うときは、中身の字下げに合わせる。
func TestReplaceReindent(t *testing.T) {
	text := "func f() {\n\tx := 1\n\ty := 2\n}\n"
	r, err := Replace(text, "\t\tx := 1\n\t\ty := 2\n", "\t\tx := 10\n\t\ty := 20\n")
	if err != nil {
		t.Fatal(err)
	}
	if r.Text != "func f() {\n\tx := 10\n\ty := 20\n}\n" || r.Line != 2 || !r.Reindented {
		t.Errorf("字下げを合わせていない: %+v", r)
	}
}

// 中身が CRLF で old が LF なら、CRLF に直して探し、new も CRLF にする。
func TestReplaceCRLF(t *testing.T) {
	r, err := Replace("a\r\nb\r\nc\r\n", "a\nb\n", "A\nB\n")
	if err != nil {
		t.Fatal(err)
	}
	if r.Text != "A\r\nB\r\nc\r\n" || r.Line != 1 {
		t.Errorf("CRLF に合わせていない: %q", r.Text)
	}
}
