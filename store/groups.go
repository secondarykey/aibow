package store

// groups.go: グループとグループの長(0031 の段4)。
//
// グループは管理者が作り、名前を付け、利用者を入れる。共有の相手にグループを選ぶと、会話とプロファイルの「指定したグループ」の表
// (conversation_group_shares・profile_group_shares)に覚え、誰に見えるかは読むたびにメンバーの表を引く(sqlite.go の
// conversationShared・profileVisible)。メンバーを変えると、そのグループに共有されたものが見える人もすぐ変わる(決定事項 26・33)。
//
// 誰が変えられるかも、ここの条件の1か所で決める(プロファイルの profileEditable と同じ考え方。決定事項 34):
//   - 作る・名前を変える・消す・長を決める … 管理者だけ(:admin = 1)
//   - メンバーを足す・外す             … 管理者か、そのグループの長(groupEditable)

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
	"uuid"

	"github.com/secondarykey/aibow"
	"github.com/secondarykey/aibow/auth"
)

// groupEditable は、:me がメンバーを変えられるグループ(管理者か、そのグループの長)
const groupEditable = `(:admin = 1 OR EXISTS (SELECT 1 FROM user_group_members m
	WHERE m.group_id = user_groups.id AND m.user_id = :me AND m.leader = 1))`

// maxGroupName はグループの名前の長さの上限(文字数。表示名と同じ)
const maxGroupName = 64

// checkGroupName はグループの名前の決まり(前後の空白を削る・空は不可・64 文字まで・制御文字なし。表示名の決まりに合わせた)。
func checkGroupName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("グループの名前を入れてください")
	}
	if utf8.RuneCountInString(name) > maxGroupName {
		return "", fmt.Errorf("グループの名前は %d 文字までです", maxGroupName)
	}
	if strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return "", errors.New("グループの名前に改行やタブなどの制御文字は使えません")
	}
	return name, nil
}

// groups は、グループの一覧(名前の順)。メンバーと長、共有されている会話・配られているプロファイルの数も入れる。
func (s *SQLite) groups(ctx context.Context) ([]aibow.Group, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT g.id, g.name,
		(SELECT COUNT(*) FROM conversation_group_shares x JOIN conversations c ON c.id = x.conversation_id WHERE x.group_id = g.id AND c.share = 'users'),
		(SELECT COUNT(*) FROM profile_group_shares x JOIN profiles p ON p.id = x.profile_id WHERE x.group_id = g.id AND p.share = 'users')
		FROM user_groups g ORDER BY g.name`)
	if err != nil {
		return nil, err
	}
	out := []aibow.Group{}
	index := map[string]int{}
	for rows.Next() {
		g := aibow.Group{Members: []string{}, Leaders: []string{}}
		var id string
		if err := rows.Scan(&id, &g.Name, &g.Conversations, &g.Profiles); err != nil {
			rows.Close()
			return nil, err
		}
		if g.ID, err = parseID(id); err != nil {
			rows.Close()
			return nil, err
		}
		index[id] = len(out)
		out = append(out, g)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// 接続は1本なので、グループの rows を閉じてからメンバーを読む
	rows, err = s.db.QueryContext(ctx, `SELECT m.group_id, u.name, m.leader FROM user_group_members m JOIN users u ON u.id = m.user_id ORDER BY u.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var gid, name string
		var leader bool
		if err := rows.Scan(&gid, &name, &leader); err != nil {
			return nil, err
		}
		i, ok := index[gid]
		if !ok {
			continue
		}
		out[i].Members = append(out[i].Members, name)
		if leader {
			out[i].Leaders = append(out[i].Leaders, name)
		}
	}
	return out, rows.Err()
}

// onlyAdmin は、ctx の利用者が管理者でなければ ErrNotAdmin。
func (s *SQLite) onlyAdmin(ctx context.Context) error {
	me, err := s.owner(ctx)
	if err != nil {
		return err
	}
	admin, err := s.isAdmin(ctx, me)
	if err != nil {
		return err
	}
	if admin != 1 {
		return aibow.ErrNotAdmin
	}
	return nil
}

// groupExistsErr は、名前が UNIQUE で断られたときのエラー(SQLite のエラーの文で見分ける。CreateUser と同じ)。
func groupExistsErr(err error) error {
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed: user_groups.name") {
		return aibow.ErrGroupExists
	}
	return err
}

func (s *SQLite) CreateGroup(ctx context.Context, name string) (aibow.Group, error) {
	if err := s.onlyAdmin(ctx); err != nil {
		return aibow.Group{}, err
	}
	name, err := checkGroupName(name)
	if err != nil {
		return aibow.Group{}, err
	}
	g := aibow.Group{ID: uuid.NewV7(), Name: name, Members: []string{}, Leaders: []string{}}
	now := formatTime(time.Now())
	_, err = s.db.ExecContext(ctx, "INSERT INTO user_groups (id, name, created_at, updated_at) VALUES (?, ?, ?, ?)", g.ID.String(), name, now, now)
	return g, groupExistsErr(err)
}

func (s *SQLite) RenameGroup(ctx context.Context, id uuid.UUID, name string) error {
	if err := s.onlyAdmin(ctx); err != nil {
		return err
	}
	name, err := checkGroupName(name)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, "UPDATE user_groups SET name = ?, updated_at = ? WHERE id = ?", name, formatTime(time.Now()), id.String())
	return notFoundIfNone(res, groupExistsErr(err), aibow.ErrNotFound)
}

// DeleteGroup は、グループと、そのグループへの共有・配るを消す(外部キーの ON DELETE CASCADE)。
// 消したせいで相手がだれもいなくなった会話・プロファイルは、非公開(配らない)にする(決定事項 28)。
// 「指定した人」のまま相手がいないと、持ち主の画面では共有中に見えるのに、だれにも見えない、になるため。
func (s *SQLite) DeleteGroup(ctx context.Context, id uuid.UUID) error {
	if err := s.onlyAdmin(ctx); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	g := id.String()
	now := formatTime(time.Now())
	// グループを消す前に、このグループだけが相手のものを非公開にする(消したあとでは、どれがこのグループに共有されていたか分からない)
	for _, q := range []string{
		`UPDATE conversations SET share = '' WHERE share = 'users'
			AND id IN (SELECT conversation_id FROM conversation_group_shares WHERE group_id = :g)
			AND NOT EXISTS (SELECT 1 FROM conversation_shares s WHERE s.conversation_id = conversations.id)
			AND NOT EXISTS (SELECT 1 FROM conversation_group_shares x WHERE x.conversation_id = conversations.id AND x.group_id <> :g)`,
		`UPDATE profiles SET share = '', updated_at = :now WHERE share = 'users'
			AND id IN (SELECT profile_id FROM profile_group_shares WHERE group_id = :g)
			AND NOT EXISTS (SELECT 1 FROM profile_shares s WHERE s.profile_id = profiles.id)
			AND NOT EXISTS (SELECT 1 FROM profile_group_shares x WHERE x.profile_id = profiles.id AND x.group_id <> :g)`,
	} {
		if _, err := tx.ExecContext(ctx, q, sql.Named("g", g), sql.Named("now", now)); err != nil {
			return err
		}
	}
	res, err := tx.ExecContext(ctx, "DELETE FROM user_groups WHERE id = ?", g)
	if err := notFoundIfNone(res, err, aibow.ErrNotFound); err != nil {
		return err
	}
	return tx.Commit()
}

// SetGroupMember は、管理者か、そのグループの長だけができる(groupEditable の条件で行を選ぶ。決定事項 31・34)。
// 選べなかったら、グループがあれば ErrNotGroupLeader、無ければ ErrNotFound。
func (s *SQLite) SetGroupMember(ctx context.Context, id uuid.UUID, user string, member bool) error {
	args, _, err := s.profileArgs(ctx)
	if err != nil {
		return err
	}
	var gid string
	err = s.db.QueryRowContext(ctx, "SELECT id FROM user_groups WHERE id = :id AND "+groupEditable, append(args, sql.Named("id", id.String()))...).Scan(&gid)
	if errors.Is(err, sql.ErrNoRows) {
		var n int
		if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM user_groups WHERE id = ?", id.String()).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return aibow.ErrNotFound
		}
		return aibow.ErrNotGroupLeader
	}
	if err != nil {
		return err
	}
	uid, err := s.groupUser(ctx, user)
	if err != nil {
		return err
	}
	if member {
		_, err = s.db.ExecContext(ctx, "INSERT OR IGNORE INTO user_group_members (group_id, user_id) VALUES (?, ?)", gid, uid)
		return err
	}
	// 外すと行ごと消えるので、長でもなくなる(決定事項 30)
	_, err = s.db.ExecContext(ctx, "DELETE FROM user_group_members WHERE group_id = ? AND user_id = ?", gid, uid)
	return err
}

// SetGroupLeader は、メンバーを長にする / 外す(管理者だけ。決定事項 30・31)。
func (s *SQLite) SetGroupLeader(ctx context.Context, id uuid.UUID, user string, leader bool) error {
	if err := s.onlyAdmin(ctx); err != nil {
		return err
	}
	uid, err := s.groupUser(ctx, user)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, "UPDATE user_group_members SET leader = ? WHERE group_id = ? AND user_id = ?", leader, id.String(), uid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%s はこのグループのメンバーではありません(長はメンバーの中から決めます)", user)
	}
	return nil
}

// groupUser は、グループに入れる利用者の ID。ログインなしの形の利用者は入れない(共有の相手にもならないため)。
func (s *SQLite) groupUser(ctx context.Context, name string) (string, error) {
	var uid string
	err := s.db.QueryRowContext(ctx, "SELECT id FROM users WHERE name = ? AND id <> ?", name, aibow.LocalUser.String()).Scan(&uid)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("利用者 %s: %w", name, auth.ErrUserNotFound)
	}
	return uid, err
}
