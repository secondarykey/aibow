package store

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/secondarykey/aibow"
	"github.com/secondarykey/aibow/auth"
)

// 配ったプロファイルが、誰に見えて、誰が変えられるか(0018 の 11-1。認可は SQLite の条件の1か所で決める)
func TestProfileShare(t *testing.T) {
	s, _ := openTemp(t)
	now := time.Now()
	user := func(name string, admin bool) context.Context {
		u := auth.User{ID: uuid.NewV7(), Name: name, CreatedAt: now, UpdatedAt: now, Admin: admin}
		if err := s.CreateUser(context.Background(), u); err != nil {
			t.Fatal(err)
		}
		return aibow.WithUser(context.Background(), u.ID)
	}
	alice, dave := user("alice", true), user("dave", true) // 管理者
	bob, carol := user("bob", false), user("carol", false)

	p := aibow.Profile{ID: uuid.NewV7(), Name: "調べもの", Values: aibow.ProfileValues{HistoryTokens: 1, ReplyTokens: 1}, CreatedAt: now, UpdatedAt: now}
	if err := s.CreateProfile(alice, p); err != nil {
		t.Fatal(err)
	}
	visible := func(ctx context.Context) bool {
		list, err := s.ListProfiles(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, q := range list {
			if q.ID == p.ID {
				return true
			}
		}
		return false
	}
	if visible(bob) || visible(dave) {
		t.Fatal("配る前は、ほかの人(管理者も)には見えない")
	}
	if err := s.SetProfileShare(bob, p.ID, aibow.ShareAll, nil); !errors.Is(err, aibow.ErrNotAdmin) {
		t.Errorf("管理者でない人は配れない: %v", err)
	}

	// bob にだけ配る
	if err := s.SetProfileShare(alice, p.ID, aibow.ShareUsers, []string{"bob"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetProfileShare(alice, p.ID, aibow.ShareUsers, []string{"nobody"}); err == nil {
		t.Error("いない名前には配れない")
	}
	if !visible(bob) || visible(carol) || !visible(dave) {
		t.Errorf("見える人: bob %v carol %v dave %v", visible(bob), visible(carol), visible(dave))
	}
	got, err := s.Profile(bob, p.ID)
	if err != nil || !got.ReadOnly || got.OwnerName != "alice" || got.ShareWith != nil || got.Withdrawn {
		t.Errorf("受け取った人に見えるもの: %+v %v", got, err)
	}
	if got, _ := s.Profile(alice, p.ID); got.ReadOnly || len(got.ShareWith) != 1 || got.ShareWith[0] != "bob" {
		t.Errorf("管理者に見えるもの: %+v", got)
	}
	got.Name = "書き換え"
	if err := s.UpdateProfile(bob, got); !errors.Is(err, aibow.ErrProfileReadOnly) {
		t.Errorf("受け取った人は変えられない: %v", err)
	}
	if err := s.DeleteProfile(bob, p.ID); !errors.Is(err, aibow.ErrProfileReadOnly) {
		t.Errorf("受け取った人は消せない: %v", err)
	}
	if err := s.UpdateProfile(carol, got); !errors.Is(err, aibow.ErrProfileNotFound) {
		t.Errorf("受け取っていない人には無いもの: %v", err)
	}
	// 配ったものは、ほかの管理者も変えられる(決定事項 20)
	if err := s.UpdateProfile(dave, got); err != nil {
		t.Errorf("ほかの管理者が変えられない: %v", err)
	}

	// bob がこのプロファイルから会話を作ってから、配るのをやめる。新しい会話の一覧には出ないが、会話の決まりのために読める(決定事項 21)
	c := aibow.Conversation{ID: uuid.NewV7(), ProfileID: p.ID, CreatedAt: now, UpdatedAt: now}
	if err := s.CreateConversation(bob, c); err != nil {
		t.Fatal(err)
	}
	if err := s.SetProfileShare(alice, p.ID, aibow.ShareNone, nil); err != nil {
		t.Fatal(err)
	}
	if visible(bob) {
		t.Error("配るのをやめたのに一覧に出る")
	}
	if got, err := s.Profile(bob, p.ID); err != nil || !got.Withdrawn {
		t.Errorf("作った会話のためには読めるはず: %+v %v", got, err)
	}
	if _, err := s.Profile(carol, p.ID); !errors.Is(err, aibow.ErrProfileNotFound) {
		t.Errorf("会話の無い人には見えない: %v", err)
	}
	if visible(dave) {
		t.Error("配っていないものは、ほかの管理者にも見えない")
	}

	// 全員に配ってから作った人を消しても、配ったものは残る(決定事項 20)
	if err := s.SetProfileShare(alice, p.ID, aibow.ShareAll, nil); err != nil {
		t.Fatal(err)
	}
	a, _ := s.UserByName(context.Background(), "alice")
	if err := s.DeleteUser(context.Background(), a.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Profile(carol, p.ID); err != nil || got.OwnerName != "alice" {
		t.Errorf("作った人を消しても配り続ける: %+v %v", got, err)
	}
	// 管理者から外れた人は、配ったものを変えられない
	d, _ := s.UserByName(context.Background(), "dave")
	s.SetAdmin(context.Background(), d.ID, false, now)
	if got, _ := s.Profile(dave, p.ID); !got.ReadOnly {
		t.Error("管理者から外れたのに変えられる")
	}
}

// 会話の共有(0018 の 11-2):共有された人と管理者は読めるが書き込めない。共有していない人には、あることも見せない
func TestConversationShare(t *testing.T) {
	s, _ := openTemp(t)
	now := time.Now()
	user := func(name string, admin bool) context.Context {
		u := auth.User{ID: uuid.NewV7(), Name: name, CreatedAt: now, UpdatedAt: now, Admin: admin}
		if err := s.CreateUser(context.Background(), u); err != nil {
			t.Fatal(err)
		}
		return aibow.WithUser(context.Background(), u.ID)
	}
	alice := user("alice", true)
	bob, carol, dan := user("bob", false), user("carol", false), user("dan", false)

	c := aibow.Conversation{ID: uuid.NewV7(), Title: "調べもの", CreatedAt: now, UpdatedAt: now}
	if err := s.CreateConversation(bob, c); err != nil {
		t.Fatal(err)
	}
	m := aibow.StoredMessage{ID: uuid.NewV7(), ConversationID: c.ID, Role: "user", Content: "質問", CreatedAt: now}
	if err := s.AddMessage(bob, m); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadConversation(carol, c.ID); !errors.Is(err, aibow.ErrNotFound) {
		t.Errorf("共有していない人には見えない: %v", err)
	}
	if err := s.SetConversationShare(carol, c.ID, aibow.ShareAll, nil); !errors.Is(err, aibow.ErrNotFound) {
		t.Errorf("持ち主でない人は共有を変えられない: %v", err)
	}
	if err := s.SetConversationShare(bob, c.ID, aibow.ShareUsers, []string{"carol"}); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.SharedConversations(carol); len(list) != 1 || list[0].ID != c.ID || list[0].OwnerName != "bob" {
		t.Errorf("共有された会話の一覧: %+v", list)
	}
	if list, _ := s.SharedConversations(dan); len(list) != 0 {
		t.Errorf("相手でない人の一覧に出る: %+v", list)
	}
	r, err := s.ReadConversation(carol, c.ID)
	if err != nil || r.ByAdmin || r.OwnerName != "bob" || len(r.Messages) != 1 || r.ReaderName != "carol" {
		t.Errorf("共有された人が読む: %+v %v", r, err)
	}
	// 書くメソッドは持ち主の行だけを触るので、共有された人も管理者も書き込めない
	for _, ctx := range []context.Context{carol, alice} {
		if err := s.AddMessage(ctx, aibow.StoredMessage{ID: uuid.NewV7(), ConversationID: c.ID, Role: "user", CreatedAt: now}); !errors.Is(err, aibow.ErrNotFound) {
			t.Errorf("ほかの人の会話に書き込めてしまう: %v", err)
		}
		if err := s.UpdateConversation(ctx, c); !errors.Is(err, aibow.ErrNotFound) {
			t.Errorf("ほかの人の会話を変えられてしまう: %v", err)
		}
	}
	// 管理者は共有されていなくても読め、読んだのが管理者としてだと分かる
	if r, err := s.ReadConversation(alice, c.ID); err != nil || !r.ByAdmin {
		t.Errorf("管理者が読む: %+v %v", r.ByAdmin, err)
	}
	if list, err := s.UserConversations(alice, "bob"); err != nil || len(list) != 1 {
		t.Errorf("管理者が見る bob の会話: %+v %v", list, err)
	}
	if _, err := s.UserConversations(carol, "bob"); !errors.Is(err, aibow.ErrNotAdmin) {
		t.Errorf("管理者でない人は人の会話の一覧を見られない: %v", err)
	}
	if _, err := s.ReadConversation(bob, c.ID); !errors.Is(err, aibow.ErrNotFound) {
		t.Error("自分の会話は ReadConversation では読まない")
	}
	if got, _, _ := s.Conversation(bob, c.ID); got.Share != aibow.ShareUsers || len(got.ShareWith) != 1 || got.ShareWith[0] != "carol" {
		t.Errorf("持ち主に見える共有: %+v", got)
	}
	if names, _ := s.UserNames(carol); len(names) != 4 {
		t.Errorf("利用者の名前(local を除く): %v", names)
	}
	// 持ち主を消すと、共有した会話も消える(決定事項 26)
	b, _ := s.UserByName(context.Background(), "bob")
	if err := s.DeleteUser(context.Background(), b.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.SharedConversations(carol); len(list) != 0 {
		t.Errorf("消した人の共有した会話が残る: %+v", list)
	}
}
