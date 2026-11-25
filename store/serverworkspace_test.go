package store

// 版 24(配布プロファイルの決まりの資料のフォルダを、サーバーのワークスペースに移す。0065)のテスト。

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"testing"
	"time"
	"uuid"
)

// 版 23 の DB を開くと、決まりの folder が workspace に移り、ほかの決まりは残る。folder の無い決まりは変えない。
// 移したあとは ProfilesUsingWorkspace で、ワークスペースを選んでいるプロファイルが分かる
func TestMigrationFromVersion23(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v23.db")
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	all := ""
	for _, m := range migrations[:23] {
		all += m
	}
	if _, err := db.Exec(all + "PRAGMA user_version = 23;"); err != nil {
		t.Fatal(err)
	}
	now := formatTime(time.Now())
	ids := map[string]string{}
	for name, rules := range map[string]string{
		"仕様の相談": `{"fixed":["model"],"folder":"社内規程"}`,
		"議事録係":  `{"folder":"社内規程"}`,
		"自分用":   `{"fixed":["model"]}`,
	} {
		id := uuid.NewV7().String()
		ids[name] = id
		if _, err := db.Exec(`INSERT INTO profiles (id, name, settings, rules, created_at, updated_at) VALUES (?, ?, '{}', ?, ?, ?)`, id, name, rules, now, now); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rulesOf := func(name string) string {
		var r string
		if err := s.db.QueryRow("SELECT rules FROM profiles WHERE id = ?", ids[name]).Scan(&r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	if r := rulesOf("仕様の相談"); r != `{"fixed":["model"],"workspace":"社内規程"}` {
		t.Errorf("移した決まり: %s", r)
	}
	if r := rulesOf("自分用"); r != `{"fixed":["model"]}` {
		t.Errorf("folder の無い決まりは変えない: %s", r)
	}
	c := context.Background()
	if names, err := s.ProfilesUsingWorkspace(c, "社内規程"); err != nil || !slices.Equal(names, []string{"仕様の相談", "議事録係"}) {
		t.Errorf("ProfilesUsingWorkspace: %v %v", names, err)
	}
	if names, err := s.ProfilesUsingWorkspace(c, "無い"); err != nil || len(names) != 0 {
		t.Errorf("選んでいないワークスペース: %v %v", names, err)
	}
}
