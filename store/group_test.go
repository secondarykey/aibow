package store

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
	"uuid"

	"github.com/secondarykey/aibow"
	"github.com/secondarykey/aibow/auth"
)

// グループ(0031 の段4)。グループに共有したものは、そのときのメンバーではなく「グループ」として覚え、
// あとから入った人にも見え、抜けた人には見えなくなる。メンバーを変えられるのは管理者とそのグループの長だけ。
func TestGroups(t *testing.T) {
	s, _ := openTemp(t)
	now := time.Now()
	user := func(name string, admin bool) context.Context {
		u := auth.User{ID: uuid.NewV7(), Name: name, CreatedAt: now, UpdatedAt: now, Admin: admin}
		if err := s.CreateUser(context.Background(), u); err != nil {
			t.Fatal(err)
		}
		return aibow.WithUser(context.Background(), u.ID)
	}
	alice := user("alice", true) // 管理者
	bob, carol, dan, erin := user("bob", false), user("carol", false), user("dan", false), user("erin", false)
	fred := user("fred", false) // どのグループにも入らない人

	// 作る・名前を変える・消す・長を決めるは管理者だけ
	if _, err := s.CreateGroup(bob, "営業"); !errors.Is(err, aibow.ErrNotAdmin) {
		t.Errorf("管理者でない人がグループを作れる: %v", err)
	}
	sales, err := s.CreateGroup(alice, " 営業 ")
	if err != nil || sales.Name != "営業" {
		t.Fatalf("作る: %+v %v", sales, err)
	}
	if _, err := s.CreateGroup(alice, "営業"); !errors.Is(err, aibow.ErrGroupExists) {
		t.Errorf("同じ名前: %v", err)
	}
	dev, err := s.CreateGroup(alice, "開発")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"carol", "dan"} {
		if err := s.SetGroupMember(alice, sales.ID, name, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetGroupMember(alice, sales.ID, "local", true); err == nil {
		t.Error("ログインなしの形の利用者はグループに入れない")
	}

	// bob の会話を「営業」グループに共有する
	c := aibow.Conversation{ID: uuid.NewV7(), Title: "見積もり", CreatedAt: now, UpdatedAt: now}
	if err := s.CreateConversation(bob, c); err != nil {
		t.Fatal(err)
	}
	if err := s.SetConversationShare(bob, c.ID, aibow.ShareUsers, nil, []string{"営業"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetConversationShare(bob, c.ID, aibow.ShareUsers, nil, []string{"ない"}); err == nil {
		t.Error("無いグループには共有できない")
	}
	if got, _, _ := s.Conversation(bob, c.ID); len(got.ShareGroups) != 1 || got.ShareGroups[0] != "営業" || len(got.ShareWith) != 0 {
		t.Errorf("持ち主に見える相手: %+v", got)
	}
	sees := func(ctx context.Context) bool {
		t.Helper()
		list, err := s.SharedConversations(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, readErr := s.ReadConversation(ctx, c.ID)
		in := slices.ContainsFunc(list, func(r aibow.ConversationRef) bool { return r.ID == c.ID })
		if in != (readErr == nil) {
			t.Errorf("一覧と読むが合わない: 一覧 %v 読む %v", in, readErr)
		}
		return in
	}
	if !sees(carol) || !sees(dan) || sees(erin) {
		t.Errorf("グループのメンバーだけに見える: carol %v dan %v erin %v", sees(carol), sees(dan), sees(erin))
	}
	// あとから入った人にも見え、抜けた人には見えなくなる(決定事項 26)
	if err := s.SetGroupMember(alice, sales.ID, "erin", true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetGroupMember(alice, sales.ID, "dan", false); err != nil {
		t.Fatal(err)
	}
	if !sees(erin) || sees(dan) {
		t.Errorf("メンバーを変えたあと: erin %v dan %v", sees(erin), sees(dan))
	}

	// 長はそのグループのメンバーだけを足す・外せる(決定事項 30・31)
	if err := s.SetGroupLeader(carol, sales.ID, "carol", true); !errors.Is(err, aibow.ErrNotAdmin) {
		t.Errorf("長を決めるのは管理者だけ: %v", err)
	}
	if err := s.SetGroupLeader(alice, sales.ID, "dan", true); err == nil {
		t.Error("メンバーでない人は長にできない")
	}
	if err := s.SetGroupLeader(alice, sales.ID, "carol", true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetGroupMember(carol, sales.ID, "dan", true); err != nil {
		t.Errorf("長がメンバーを足せない: %v", err)
	}
	if err := s.SetGroupMember(carol, dev.ID, "dan", true); !errors.Is(err, aibow.ErrNotGroupLeader) {
		t.Errorf("長でないグループのメンバーを変えられる: %v", err)
	}
	if err := s.SetGroupMember(dan, sales.ID, "bob", true); !errors.Is(err, aibow.ErrNotGroupLeader) {
		t.Errorf("長でないメンバーがメンバーを変えられる: %v", err)
	}
	if err := s.SetGroupMember(carol, uuid.NewV7(), "dan", true); !errors.Is(err, aibow.ErrNotFound) {
		t.Errorf("無いグループ: %v", err)
	}
	for _, f := range []func() error{
		func() error { return s.RenameGroup(carol, sales.ID, "営業部") },
		func() error { return s.DeleteGroup(carol, sales.ID) },
		func() error { _, err := s.CreateGroup(carol, "新しい"); return err },
	} {
		if err := f(); !errors.Is(err, aibow.ErrNotAdmin) {
			t.Errorf("長がグループそのものを変えられる: %v", err)
		}
	}
	d, err := s.Directory(erin)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Groups) != 2 || d.Groups[0].Name != "営業" || !slices.Equal(d.Groups[0].Members, []string{"carol", "dan", "erin"}) ||
		!slices.Equal(d.Groups[0].Leaders, []string{"carol"}) || d.Groups[0].Conversations != 1 {
		t.Errorf("グループの一覧(だれでも見える): %+v", d.Groups)
	}
	// 長がグループから抜けると、長でもなくなる(決定事項 30・31。自分を外すこともできる)
	if err := s.SetGroupMember(carol, sales.ID, "carol", false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetGroupMember(carol, sales.ID, "dan", false); !errors.Is(err, aibow.ErrNotGroupLeader) {
		t.Errorf("抜けた長がまだメンバーを変えられる: %v", err)
	}
	if err := s.SetGroupMember(alice, sales.ID, "carol", true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetGroupMember(carol, sales.ID, "dan", false); !errors.Is(err, aibow.ErrNotGroupLeader) {
		t.Errorf("入り直しても長には戻らない: %v", err)
	}

	// プロファイルをグループに配る
	p := aibow.Profile{ID: uuid.NewV7(), Name: "見積もり用", Values: aibow.ProfileValues{HistoryTokens: 1, ReplyTokens: 1}, CreatedAt: now, UpdatedAt: now}
	if err := s.CreateProfile(alice, p); err != nil {
		t.Fatal(err)
	}
	if err := s.SetProfileShare(alice, p.ID, aibow.ShareUsers, []string{"bob"}, []string{"営業"}); err != nil {
		t.Fatal(err)
	}
	visible := func(ctx context.Context) bool {
		t.Helper()
		list, err := s.ListProfiles(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return slices.ContainsFunc(list, func(q aibow.Profile) bool { return q.ID == p.ID })
	}
	if !visible(bob) || !visible(erin) || visible(fred) {
		t.Errorf("配ったプロファイル: bob %v erin %v fred %v", visible(bob), visible(erin), visible(fred))
	}
	if got, _ := s.Profile(alice, p.ID); !slices.Equal(got.ShareGroups, []string{"営業"}) || !slices.Equal(got.ShareWith, []string{"bob"}) {
		t.Errorf("管理者に見える配る相手: %+v", got)
	}

	// グループを消すと、そのグループへの共有と配るも消え、相手のいなくなった会話は非公開になる(決定事項 28)。
	// ほかの相手(bob)が残るプロファイルは配ったまま
	if err := s.DeleteGroup(alice, sales.ID); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := s.Conversation(bob, c.ID); got.Share != aibow.ShareNone || got.ShareGroups != nil {
		t.Errorf("相手のいなくなった会話: %+v", got)
	}
	if sees(erin) {
		t.Error("消したグループに共有した会話が見える")
	}
	if got, _ := s.Profile(alice, p.ID); got.Share != aibow.ShareUsers || len(got.ShareGroups) != 0 || !visible(bob) || visible(erin) {
		t.Errorf("ほかの相手が残るプロファイル: %+v", got)
	}
	if err := s.DeleteGroup(alice, sales.ID); !errors.Is(err, aibow.ErrNotFound) {
		t.Errorf("消したグループ: %v", err)
	}
}
