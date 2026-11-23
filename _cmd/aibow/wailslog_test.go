package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// aibow-server につながらない・証明書が違う・401 の「Binding call failed」だけ Debug に落ち、ほかは ERROR のまま(0057 の決定事項 13)。
// 文は Wails v3.0.0 が書く形(「Binding call failed: Bound method returned an error: <エラーの文>」)
func TestWailsLoggerQuietsServerErrors(t *testing.T) {
	var buf bytes.Buffer
	l := wailsLogger(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	const head = "Binding call failed: Bound method returned an error: "
	l.Error(head + "aibow-server につながりません(つなぎ先: http://127.0.0.1:8089)。aibow-server を起動してください: dial tcp: refused")
	l.Error(head + "aibow-server の証明書が、信頼したときのものと違います(覚えたフィンガープリント: a・サーバーが出したフィンガープリント: b)")
	l.Error(head + "アプリのログインの期限(10 日)が切れました。もう一度ログインしてください")
	if buf.Len() != 0 {
		t.Fatalf("Debug に落ちて、Info の出口には出ないはず: %s", buf.String())
	}
	l.Error(head + "会話が見つかりません")
	l.Error("aibow-server につながりません") // Binding call failed でない行はそのまま
	if out := buf.String(); strings.Count(out, "level=ERROR") != 2 {
		t.Errorf("ほかのエラーは ERROR のまま: %s", out)
	}
}
