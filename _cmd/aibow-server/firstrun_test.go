package main

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/secondarykey/aibow"
	"github.com/secondarykey/aibow/auth"
	"github.com/secondarykey/aibow/log"
	"github.com/secondarykey/aibow/store"
)

// ログインが要る形で起動したときの知らせ(0020 の決定事項 2・4):管理者がいない・パスワードの無い local に会話がある
func TestNoteFirstRun(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var buf bytes.Buffer
	prev := log.Logger()
	log.SetLogger(slog.New(slog.NewTextHandler(&buf, nil)))
	defer log.SetLogger(prev)
	note := func() string {
		buf.Reset()
		noteFirstRun(st)
		return buf.String()
	}

	if got := note(); !strings.Contains(got, "管理者がいません") || strings.Contains(got, "local の会話") {
		t.Errorf("初めての起動: %s", got)
	}

	ctx := context.Background()
	now := time.Now()
	c := aibow.Conversation{ID: uuid.NewV7(), Title: "前の会話", CreatedAt: now, UpdatedAt: now}
	if err := st.CreateConversation(aibow.WithUser(ctx, aibow.LocalUser), c); err != nil {
		t.Fatal(err)
	}
	hash, _ := auth.HashPassword("pw")
	admin := auth.User{ID: uuid.NewV7(), Name: "alice", Password: hash, Admin: true, CreatedAt: now, UpdatedAt: now}
	if err := st.CreateUser(ctx, admin); err != nil {
		t.Fatal(err)
	}
	if got := note(); strings.Contains(got, "管理者がいません") || !strings.Contains(got, "local の会話が 1 件") {
		t.Errorf("管理者がいて、local に会話がある: %s", got)
	}

	st.SetPassword(ctx, aibow.LocalUser, hash, now)
	if got := note(); got != "" {
		t.Errorf("local にパスワードがあれば知らせない: %s", got)
	}
}
