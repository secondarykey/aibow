package ui

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/secondarykey/aibow/log"
)

// aibow-server のログのレベルは、管理者だけが見て変えられる(0052)。管理者でなければ 403、知らない名前はエラー
func TestWebServerLogLevel(t *testing.T) {
	old := slog.Default()
	c, _ := log.Init(log.Options{Root: log.LevelNotice}) // 出口は標準エラー出力だけ(ファイルには書かない)
	t.Cleanup(func() {
		log.SetLevel(log.LevelInfo, "")
		c.Close()
		slog.SetDefault(old)
		slog.SetLogLoggerLevel(slog.LevelInfo)
	})
	w := newTestWeb(t)

	for _, m := range [][]any{{"ServerLogLevel"}, {"SetServerLogLevel", "debug"}, {"ServerLogFile"}} {
		if status, body := w.call(t, context.Background(), m[0].(string), m[1:]...); status != http.StatusForbidden || !strings.Contains(string(body), "管理者だけ") {
			t.Errorf("%s は管理者でなければ 403: %d %s", m[0], status, body)
		}
	}
	if l, _ := log.Level(); l != log.LevelNotice {
		t.Errorf("断ったのに変わっている: %v", l)
	}

	w.admin.Store(true)
	var name string
	w.mustCall(t, &name, "ServerLogLevel")
	if name != "notice" {
		t.Errorf("今のレベル: %q", name)
	}
	w.mustCall(t, &name, "SetServerLogLevel", "trace")
	if l, _ := log.Level(); name != "trace" || l != log.LevelTrace {
		t.Errorf("trace にしたあと: %q %v", name, l)
	}
	// この Init はファイルに書かないので、ログファイルの場所は空(画面は「ログはファイルに残っていません」)
	var file string
	w.mustCall(t, &file, "ServerLogFile")
	if file != "" {
		t.Errorf("ファイルに書いていないのに場所がある: %q", file)
	}
	if status, body := w.call(t, context.Background(), "SetServerLogLevel", "verbose"); status != http.StatusInternalServerError || !strings.Contains(string(body), "trace・debug・info・notice・warn・error") {
		t.Errorf("知らない名前: %d %s", status, body)
	}
}

// デスクトップのアプリ(ブラウザでない Service)からは、aibow-server のログのレベルを変えない(アプリのログが変わってしまうため)
func TestServerLogLevelDesktop(t *testing.T) {
	s := &Service{}
	if _, err := s.SetServerLogLevel(context.Background(), "debug"); err == nil {
		t.Error("デスクトップのアプリでは断るはず")
	}
}
