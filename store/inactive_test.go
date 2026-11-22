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

// 0055 のテストで使う、利用者を作って ctx にする係
func newUsers(t *testing.T, s *SQLite) (func(name string, admin bool) context.Context, map[string]uuid.UUID) {
	t.Helper()
	ids := map[string]uuid.UUID{}
	now := time.Now()
	return func(name string, admin bool) context.Context {
		u := auth.User{ID: uuid.NewV7(), Name: name, CreatedAt: now, UpdatedAt: now, Admin: admin}
		if err := s.CreateUser(context.Background(), u); err != nil {
			t.Fatal(err)
		}
		ids[name] = u.ID
		return aibow.WithUser(context.Background(), u.ID)
	}, ids
}

// sharedRef は、ctx の人の共有された会話の一覧から id の1件を探す(無ければ ok=false)
func sharedRef(t *testing.T, s *SQLite, ctx context.Context, id uuid.UUID) (aibow.ConversationRef, bool) {
	t.Helper()
	list, err := s.SharedConversations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(list, func(r aibow.ConversationRef) bool { return r.ID == id })
	if i < 0 {
		return aibow.ConversationRef{}, false
	}
	return list[i], true
}

// 共有のされ方(0055 の決定事項 23):直接の相手・グループのメンバー・両方・全員・グループを抜けたあとで、ToMe と Groups が正しい。
// 一覧(SharedConversations)と読むだけの画面(ReadConversation)で同じ理由が返る。管理者として読むときは返さない
func TestShareReasons(t *testing.T) {
	s, _ := openTemp(t)
	user, _ := newUsers(t, s)
	admin := user("admin", true)
	bob, carol, dan, erin := user("bob", false), user("carol", false), user("dan", false), user("erin", false)

	sales, err := s.CreateGroup(admin, "営業")
	if err != nil {
		t.Fatal(err)
	}
	dev, err := s.CreateGroup(admin, "開発")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct {
		g    aibow.Group
		name string
	}{{sales, "carol"}, {sales, "dan"}, {dev, "dan"}, {dev, "erin"}} {
		if err := s.SetGroupMember(admin, m.g.ID, m.name, true); err != nil {
			t.Fatal(err)
		}
	}

	// bob の会話を carol(直接)と 営業・開発 グループに共有する
	now := time.Now()
	c := aibow.Conversation{ID: uuid.NewV7(), Title: "見積もり", CreatedAt: now, UpdatedAt: now}
	if err := s.CreateConversation(bob, c); err != nil {
		t.Fatal(err)
	}
	if err := s.SetConversationShare(bob, c.ID, aibow.ShareUsers, []string{"carol"}, []string{"営業", "開発"}); err != nil {
		t.Fatal(err)
	}

	check := func(who string, ctx context.Context, toMe bool, groups []string) {
		t.Helper()
		ref, ok := sharedRef(t, s, ctx, c.ID)
		if !ok {
			t.Fatalf("%s: 一覧に無い", who)
		}
		if ref.ToMe != toMe || !slices.Equal(ref.Groups, groups) {
			t.Errorf("%s の一覧: ToMe %v Groups %v(期待: %v %v)", who, ref.ToMe, ref.Groups, toMe, groups)
		}
		r, err := s.ReadConversation(ctx, c.ID)
		if err != nil {
			t.Fatal(err)
		}
		if r.ToMe != toMe || !slices.Equal(r.Groups, groups) {
			t.Errorf("%s の読む: ToMe %v Groups %v(期待: %v %v)", who, r.ToMe, r.Groups, toMe, groups)
		}
	}
	check("carol(直接と 営業)", carol, true, []string{"営業"})
	check("dan(営業と開発)", dan, false, []string{"営業", "開発"})
	check("erin(開発)", erin, false, []string{"開発"})

	// グループを抜けたら、そのグループは理由から消える。どの理由も無くなれば見えない
	if err := s.SetGroupMember(admin, sales.ID, "dan", false); err != nil {
		t.Fatal(err)
	}
	check("dan(開発だけ)", dan, false, []string{"開発"})
	if err := s.SetGroupMember(admin, dev.ID, "erin", false); err != nil {
		t.Fatal(err)
	}
	if _, ok := sharedRef(t, s, erin, c.ID); ok {
		t.Error("どのグループにもいなくなった erin に見える")
	}

	// 管理者として読むときは理由を返さない(共有されていないので)
	if r, err := s.ReadConversation(admin, c.ID); err != nil || !r.ByAdmin || r.ToMe || len(r.Groups) != 0 {
		t.Errorf("管理者として読む: %+v %v", r, err)
	}

	// 全員に共有したら、理由は Share で分かるので入れない(前の相手の表は空になる)
	if err := s.SetConversationShare(bob, c.ID, aibow.ShareAll, nil, nil); err != nil {
		t.Fatal(err)
	}
	for who, ctx := range map[string]context.Context{"carol": carol, "erin": erin} {
		ref, ok := sharedRef(t, s, ctx, c.ID)
		if !ok || ref.Share != aibow.ShareAll || ref.ToMe || len(ref.Groups) != 0 {
			t.Errorf("%s の全員に共有: %+v %v", who, ref, ok)
		}
	}
}

// 非アクティブ(0055 の決定事項 16):印は付けた人の一覧にだけ効き、会話か利用者を消すと一緒に消える。
// 付けられるのは自分の会話と自分に共有された会話だけ。送信(TouchConversation)しても外れない
func TestConversationInactive(t *testing.T) {
	s, _ := openTemp(t)
	user, ids := newUsers(t, s)
	bob, carol, dan := user("bob", false), user("carol", false), user("dan", false)

	now := time.Now()
	mine := aibow.Conversation{ID: uuid.NewV7(), Title: "自分の", CreatedAt: now, UpdatedAt: now}
	shared := aibow.Conversation{ID: uuid.NewV7(), Title: "共有した", CreatedAt: now, UpdatedAt: now}
	private := aibow.Conversation{ID: uuid.NewV7(), Title: "非公開", CreatedAt: now, UpdatedAt: now}
	for _, c := range []aibow.Conversation{mine, shared, private} {
		if err := s.CreateConversation(bob, c); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetConversationShare(bob, shared.ID, aibow.ShareAll, nil, nil); err != nil {
		t.Fatal(err)
	}

	ownInactive := func(ctx context.Context, id uuid.UUID) bool {
		t.Helper()
		list, err := s.ListConversations(ctx)
		if err != nil {
			t.Fatal(err)
		}
		i := slices.IndexFunc(list, func(c aibow.Conversation) bool { return c.ID == id })
		if i < 0 {
			t.Fatalf("一覧に %s が無い", id)
		}
		return list[i].Inactive
	}
	sharedInactive := func(ctx context.Context, id uuid.UUID) bool {
		t.Helper()
		ref, ok := sharedRef(t, s, ctx, id)
		if !ok {
			t.Fatalf("共有された会話の一覧に %s が無い", id)
		}
		return ref.Inactive
	}

	// 自分の会話に付ける・送信しても外れない・外す
	if err := s.SetConversationInactive(bob, mine.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetConversationInactive(bob, mine.ID, true); err != nil {
		t.Errorf("2回付けてもエラーにしない: %v", err)
	}
	if err := s.TouchConversation(bob, mine.ID, now.Add(time.Minute), ""); err != nil {
		t.Fatal(err)
	}
	if !ownInactive(bob, mine.ID) || ownInactive(bob, shared.ID) {
		t.Error("付けた会話だけが非アクティブ")
	}
	if err := s.SetConversationInactive(bob, mine.ID, false); err != nil || ownInactive(bob, mine.ID) {
		t.Errorf("外す: %v", err)
	}

	// 共有された会話に付けても、持ち主とほかの相手の一覧は変わらない
	if err := s.SetConversationInactive(carol, shared.ID, true); err != nil {
		t.Fatal(err)
	}
	if !sharedInactive(carol, shared.ID) || sharedInactive(dan, shared.ID) || ownInactive(bob, shared.ID) {
		t.Error("共有された会話の印は付けた人の一覧だけに効く")
	}

	// 見えない会話(ほかの人の非公開の会話・無い会話)には付けられない(あることも見せない)
	if err := s.SetConversationInactive(carol, private.ID, true); !errors.Is(err, aibow.ErrNotFound) {
		t.Errorf("見えない会話に付けられる: %v", err)
	}
	if err := s.SetConversationInactive(carol, uuid.NewV7(), true); !errors.Is(err, aibow.ErrNotFound) {
		t.Errorf("無い会話に付けられる: %v", err)
	}

	// 共有をやめると一覧から消え、共有し直されたら印は残っている(決定事項 21)
	if err := s.SetConversationShare(bob, shared.ID, aibow.ShareNone, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := sharedRef(t, s, carol, shared.ID); ok {
		t.Error("共有をやめた会話が一覧に残る")
	}
	if err := s.SetConversationShare(bob, shared.ID, aibow.ShareAll, nil, nil); err != nil {
		t.Fatal(err)
	}
	if !sharedInactive(carol, shared.ID) {
		t.Error("共有し直したら印が消えた")
	}

	count := func() int {
		t.Helper()
		var n int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM conversation_inactive").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	// 会話を消すと印も消える
	if err := s.SetConversationInactive(dan, shared.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetConversationInactive(bob, private.ID, true); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 3 {
		t.Fatalf("印の数 %d(期待: 3)", n)
	}
	if err := s.DeleteConversation(bob, private.ID); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 2 {
		t.Errorf("会話を消したあとの印の数 %d(期待: 2)", n)
	}
	// 利用者を消すと、その人が付けた印も消える
	if err := s.DeleteUser(context.Background(), ids["carol"]); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 1 {
		t.Errorf("利用者を消したあとの印の数 %d(期待: 1)", n)
	}
}

// アプリモード(OneUser の aibow.db。利用者 local)でも非アクティブを付けられる(決定事項 22)
func TestConversationInactiveOneUser(t *testing.T) {
	s, _ := openTemp(t)
	s.OneUser()
	ctx := context.Background()
	now := time.Now()
	c := aibow.Conversation{ID: uuid.NewV7(), Title: "手元", CreatedAt: now, UpdatedAt: now}
	if err := s.CreateConversation(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := s.SetConversationInactive(ctx, c.ID, true); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListConversations(ctx)
	if err != nil || len(list) != 1 || !list[0].Inactive {
		t.Errorf("一覧: %+v %v", list, err)
	}
}
