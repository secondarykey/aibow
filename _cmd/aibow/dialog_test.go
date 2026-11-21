package main

import (
	"errors"
	"testing"
)

// 取り消し(Windows で返るエラー)は空文字・nil になる
func TestDialogPathCancelled(t *testing.T) {
	p, err := dialogPath("", errors.New("cancelled by user"))
	if p != "" || err != nil {
		t.Fatalf("取り消しは (\"\", nil) のはず: (%q, %v)", p, err)
	}
}

// 選んだときと、取り消し以外のエラーはそのまま返す
func TestDialogPathPassThrough(t *testing.T) {
	p, err := dialogPath(`C:\models`, nil)
	if p != `C:\models` || err != nil {
		t.Fatalf("選んだパスがそのまま返るはず: (%q, %v)", p, err)
	}
	other := errors.New("unable to create dialog")
	if _, err := dialogPath("", other); !errors.Is(err, other) {
		t.Fatalf("取り消し以外のエラーはそのまま返るはず: %v", err)
	}
	// macOS・Linux の取り消し(もともと空文字・nil)もそのまま
	if p, err := dialogPath("", nil); p != "" || err != nil {
		t.Fatalf("(\"\", nil) はそのまま: (%q, %v)", p, err)
	}
}
