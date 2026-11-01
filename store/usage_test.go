package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
	"uuid"

	"github.com/secondarykey/aibow/auth"
	"github.com/secondarykey/aibow/genlog"
)

// 生成の記録(0033):利用者ごと・モデルごと・日ごとにまとめる。利用者を消しても記録と名前は残る
func TestGenerationLog(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	jst := time.FixedZone("JST", 9*60*60)
	now := time.Date(2026, 11, 1, 10, 0, 0, 0, jst)

	alice := auth.User{ID: uuid.NewV7(), Name: "alice", DisplayName: "佐藤", CreatedAt: now}
	bob := auth.User{ID: uuid.NewV7(), Name: "bob", CreatedAt: now}
	for _, u := range []auth.User{alice, bob} {
		if err := s.CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	add := func(u auth.User, at time.Time, model, purpose string, promptSec, genSec float64, gen int, wait *float64) {
		t.Helper()
		r := genlog.Record{ID: uuid.NewV7(), At: at, User: u.ID, UserName: u.Name, Route: genlog.RouteBrowser, Purpose: purpose, Model: model,
			PromptTokens: 100, ReusedTokens: 40, GenTokens: gen, PromptSec: promptSec, GenSec: genSec, EncodeSec: 0.5, WaitSec: wait, StopReason: "eos"}
		if err := s.AddGeneration(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	w := 2.0
	add(alice, now, "file:a.gguf", genlog.PurposeReply, 1, 2, 10, &w)                    // 今日
	add(alice, now.Add(-time.Hour), "file:b.gguf", genlog.PurposeSummary, 1, 1, 20, nil) // 今日(要約)
	add(alice, now.Add(-3*24*time.Hour), "file:a.gguf", genlog.PurposeReply, 2, 2, 30, nil)
	add(bob, now.Add(-10*24*time.Hour), "file:a.gguf", genlog.PurposeReply, 9, 9, 40, nil) // 7日の外
	// だれでもない生成(作りの抜け)も記録には残る
	if err := s.AddGeneration(ctx, genlog.Record{ID: uuid.NewV7(), At: now, Route: genlog.RouteAPI, Purpose: genlog.PurposeReply, Model: "file:a.gguf", GenSec: 1, StopReason: "eos"}); err != nil {
		t.Fatal(err)
	}

	week := genlog.Since(now, 7)
	users, err := s.UsageByUser(ctx, week)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 || users[0].Key != alice.ID.String() || users[0].Name != "alice" || users[0].DisplayName != "佐藤" || users[0].Deleted {
		t.Fatalf("利用者ごと: %+v", users)
	}
	a := users[0]
	// GPU の秒数 = プロンプト + 生成 + エンコード(3回で 3.5 + 2.5 + 4.5)
	if a.Count != 3 || a.Summaries != 1 || a.GPUSec != 10.5 || a.GenTokens != 60 || a.PromptTokens != 300 || a.ReusedTokens != 120 || a.WaitSec != 2 {
		t.Errorf("alice の 7 日: %+v", a)
	}
	if users[1].Key != "" || users[1].Count != 1 {
		t.Errorf("だれでもない生成: %+v", users[1])
	}

	today, err := s.UsageOfUser(ctx, alice.ID.String(), genlog.Since(now, 1))
	if err != nil || today.Count != 2 || today.GenTokens != 30 {
		t.Errorf("alice の今日: %+v %v", today, err)
	}
	none, err := s.UsageOfUser(ctx, uuid.NewV7().String(), week)
	if err != nil || none.Count != 0 || none.GPUSec != 0 {
		t.Errorf("記録の無い人: %+v %v", none, err)
	}

	byModel, err := s.UsageByModel(ctx, alice.ID.String(), week)
	if err != nil || len(byModel) != 2 || byModel[0].Key != "file:a.gguf" || byModel[0].Count != 2 || byModel[1].Key != "file:b.gguf" {
		t.Errorf("モデルごと: %+v %v", byModel, err)
	}
	// 日の区切りは since の場所(JST)。09:00 の生成は UTC では前の日の 0 時台だが、JST の 11/1 に数える
	add(alice, time.Date(2026, 11, 1, 0, 30, 0, 0, jst), "file:a.gguf", genlog.PurposeReply, 0, 1, 1, nil)
	byDay, err := s.UsageByDay(ctx, alice.ID.String(), week)
	if err != nil || len(byDay) != 2 || byDay[0].Key != "2026-11-01" || byDay[0].Count != 3 || byDay[1].Key != "2026-10-29" {
		t.Errorf("日ごと: %+v %v", byDay, err)
	}

	// 利用者を消しても記録は残り、写した名前で読める(決定事項 4・8)
	if err := s.DeleteUser(ctx, alice.ID); err != nil {
		t.Fatal(err)
	}
	users, err = s.UsageByUser(ctx, week)
	if err != nil || users[0].Name != "alice" || !users[0].Deleted || users[0].DisplayName != "" {
		t.Errorf("消した人の分: %+v %v", users, err)
	}
}
