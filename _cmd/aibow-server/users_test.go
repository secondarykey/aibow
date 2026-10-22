package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/secondarykey/aibow"
	"github.com/secondarykey/aibow/auth"
	"github.com/secondarykey/aibow/store"
)

// 利用者を作ると、その人のプロファイル「既定」もできる(0017 の決定事項 5)。消すと、その人のものも消える
func TestUserCommands(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	var env envReader
	if err := addUser(ctx, st, initialSettings(&env, 0), "bob"); err != nil {
		t.Fatal(err)
	}
	if err := addUser(ctx, st, initialSettings(&env, 0), "bob"); !errors.Is(err, auth.ErrUserExists) {
		t.Errorf("同じ名前: %v", err)
	}
	u, err := st.UserByName(ctx, "bob")
	if err != nil || u.Password == "" {
		t.Fatalf("作った利用者: %+v %v", u, err)
	}
	bob := aibow.WithUser(ctx, u.ID)
	list, err := st.ListProfiles(bob)
	if err != nil || len(list) != 1 || list[0].Name != aibow.DefaultProfileName || list[0].Values.HistoryTokens != 8192 {
		t.Fatalf("プロファイル: %+v %v", list, err)
	}
	if v, ok, _ := st.UserSetting(bob, aibow.SettingDefaultProfile); !ok || v != `"`+list[0].ID.String()+`"` {
		t.Errorf("新しい会話で最初に選ばれるプロファイル: %q %v", v, ok)
	}
	if mine, _ := st.ListProfiles(aibow.WithUser(ctx, aibow.LocalUser)); len(mine) != 0 {
		t.Errorf("ほかの人のプロファイルは作らない: %+v", mine)
	}

	before := u.Password
	if err := resetPassword(ctx, st, "bob"); err != nil {
		t.Fatal(err)
	}
	if u, _ := st.UserByName(ctx, "bob"); u.Password == before {
		t.Error("パスワードが変わっていない")
	}

	// 管理者の印(0018 の 11-0)。外して一人もいなくなっても止めない(決定事項 16)
	if u.Admin {
		t.Error("作ったばかりの人が管理者になっている")
	}
	if err := setAdmin(ctx, st, "bob", true); err != nil {
		t.Fatal(err)
	}
	if u, _ := st.UserByName(ctx, "bob"); !u.Admin {
		t.Error("管理者になっていない")
	}
	if err := setAdmin(ctx, st, "bob", false); err != nil {
		t.Fatal(err)
	}
	if u, _ := st.UserByName(ctx, "bob"); u.Admin {
		t.Error("管理者から外れていない")
	}
	if err := setAdmin(ctx, st, "nobody", true); !errors.Is(err, auth.ErrUserNotFound) {
		t.Errorf("いない人: %v", err)
	}

	if err := deleteUser(ctx, st, "local", ""); err == nil {
		t.Error("ログインなしの形の利用者は消せない")
	}
	if err := deleteUser(ctx, st, "bob", ""); err != nil {
		t.Fatal(err)
	}
	if list, _ := st.ListProfiles(bob); len(list) != 0 {
		t.Errorf("消した人のプロファイルが残っている: %+v", list)
	}
	for _, bad := range []string{"", "a b", "x\ty", "1234567890123456789012345678901234"} {
		if checkName(bad) == nil {
			t.Errorf("使えない名前を受け付けた: %q", bad)
		}
	}
}
