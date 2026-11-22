package store

// share.go: 会話の共有と、ほかの人の会話を読む(0018 の 11-2)。誰に見えるかの条件 conversationShared はここの1か所。共有の相手を覚える setShareTargets はプロファイルを配るのにも使う。

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"uuid"

	"github.com/secondarykey/aibow"
)

// ---- 会話の共有(0018 の 11-2) --------------------------------------------------------------
//
// ほかの人の会話を読めるかは、ここの条件(conversationReadable)の1か所で決める(プロファイルの profileVisible と同じ考え方)。
// 読むのは ReadConversation だけで、書くメソッド(UpdateConversation・AddMessage など)は今までどおり持ち主の行だけを触る。
// なので、共有された人も管理者も書き込めない(決定事項 1・2)。

// conversationShared は、:me に共有されたほかの人の会話(全員か、:me が相手か、:me がメンバーのグループが相手。0031 の決定事項 27)。
// グループは「そのときのメンバー」に広げて覚えず、読むたびにメンバーの表を引く(あとから入った人にも見え、抜けた人には見えなくなる。決定事項 26)
const conversationShared = `(conversations.owner <> :me AND (conversations.share = 'all'
	OR (conversations.share = 'users' AND (
		EXISTS (SELECT 1 FROM conversation_shares s WHERE s.conversation_id = conversations.id AND s.user_id = :me)
		OR EXISTS (SELECT 1 FROM conversation_group_shares g JOIN user_group_members m ON m.group_id = g.group_id
			WHERE g.conversation_id = conversations.id AND m.user_id = :me)))))`

// shareNames は、共有の相手(指定した人とグループ)の名前。kind は conversation か profile(表の名前の頭)。範囲が users でなければ nil。
func (s *SQLite) shareNames(ctx context.Context, kind, share string, id uuid.UUID) (users, groups []string, err error) {
	if share != aibow.ShareUsers {
		return nil, nil, nil
	}
	if users, err = s.names(ctx, "SELECT u.name FROM "+kind+"_shares s JOIN users u ON u.id = s.user_id WHERE s."+kind+"_id = ? ORDER BY u.name", id.String()); err != nil {
		return nil, nil, err
	}
	groups, err = s.names(ctx, "SELECT g.name FROM "+kind+"_group_shares s JOIN user_groups g ON g.id = s.group_id WHERE s."+kind+"_id = ? ORDER BY g.name", id.String())
	return users, groups, err
}

// names は、名前の1列を読む(空でも nil ではなく [])。
func (s *SQLite) names(ctx context.Context, query string, args ...any) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// setShareTargets は、共有の相手(人とグループ)を入れ直す。kind は conversation か profile、what はエラーの文の「共有の相手」「配る相手」。
// 相手は名前で受け取り、いない名前ならエラー。me(自分)は相手にしない(自分のもの・管理者は相手でなくても見える)。
func setShareTargets(ctx context.Context, tx *sql.Tx, kind, what string, id uuid.UUID, me string, with, groups []string) error {
	for _, table := range []string{kind + "_shares", kind + "_group_shares"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE "+kind+"_id = ?", id.String()); err != nil {
			return err
		}
	}
	for _, n := range with {
		var uid string
		err := tx.QueryRowContext(ctx, "SELECT id FROM users WHERE name = ?", n).Scan(&uid)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%s %q という利用者はいません", what, n)
		}
		if err != nil {
			return err
		}
		if uid == me {
			continue
		}
		if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO "+kind+"_shares ("+kind+"_id, user_id) VALUES (?, ?)", id.String(), uid); err != nil {
			return err
		}
	}
	for _, n := range groups {
		var gid string
		err := tx.QueryRowContext(ctx, "SELECT id FROM user_groups WHERE name = ?", n).Scan(&gid)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%sのグループ %q はありません", what, n)
		}
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO "+kind+"_group_shares ("+kind+"_id, group_id) VALUES (?, ?)", id.String(), gid); err != nil {
			return err
		}
	}
	return nil
}

// SetConversationShare は、自分の会話の共有の範囲と相手(人とグループ)を変える。相手は名前で受け取り、いない名前ならエラー。自分は相手にしない。
func (s *SQLite) SetConversationShare(ctx context.Context, id uuid.UUID, share string, with, groups []string) error {
	owner, err := s.owner(ctx)
	if err != nil {
		return err
	}
	switch share {
	case aibow.ShareNone, aibow.ShareAll, aibow.ShareUsers:
	default:
		return fmt.Errorf("共有の範囲 %q は知らない値です(\"\"・all・users のどれか)", share)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, "UPDATE conversations SET share = ? WHERE id = ? AND owner = ?", share, id.String(), owner)
	if err := notFoundIfNone(res, err, aibow.ErrNotFound); err != nil {
		return err
	}
	if share != aibow.ShareUsers {
		with, groups = nil, nil // 相手の表は空にする
	}
	if err := setShareTargets(ctx, tx, "conversation", "共有の相手", id, owner, with, groups); err != nil {
		return err
	}
	return tx.Commit()
}

// conversationRefs は、ほかの人の会話の一覧(持ち主の名前付き)を where の条件で読む。
func (s *SQLite) conversationRefs(ctx context.Context, where string, args ...any) ([]aibow.ConversationRef, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT conversations.id, conversations.title, COALESCE(u.name, ''), COALESCE(u.display_name, ''), conversations.updated_at, conversations.share
		FROM conversations LEFT JOIN users u ON u.id = conversations.owner WHERE `+where+` ORDER BY conversations.updated_at DESC, conversations.id DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []aibow.ConversationRef{}
	for rows.Next() {
		var r aibow.ConversationRef
		var id, updated string
		if err := rows.Scan(&id, &r.Title, &r.OwnerName, &r.OwnerDisplay, &updated, &r.Share); err != nil {
			return nil, err
		}
		if r.ID, err = parseID(id); err != nil {
			return nil, err
		}
		if r.UpdatedAt, err = parseTime(updated); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *SQLite) SharedConversations(ctx context.Context) ([]aibow.ConversationRef, error) {
	me, err := s.owner(ctx)
	if err != nil {
		return nil, err
	}
	refs, err := s.conversationRefs(ctx, conversationShared, sql.Named("me", me))
	if err != nil {
		return nil, err
	}
	// 見えている理由と非アクティブの印は、一覧を読み終えてから別に読む(接続が1本なので、rows を閉じてから)。
	// 見えるかの判定(conversationShared)はそのまま使い、理由は同じ条件を分けて読む(判定の1か所を崩さない。0055 の決定事項 23)
	toMe, groups, err := s.shareReasons(ctx, me, "")
	if err != nil {
		return nil, err
	}
	inactive, err := s.inactiveSet(ctx, me)
	if err != nil {
		return nil, err
	}
	for i := range refs {
		id := refs[i].ID.String()
		if refs[i].Share == aibow.ShareUsers {
			refs[i].ToMe, refs[i].Groups = toMe[id], groups[id]
		}
		refs[i].Inactive = inactive[id]
	}
	return refs, nil
}

// shareReasons は、範囲が users の会話について、:me が直接の相手か(toMe)と、:me がメンバーで相手に入っているグループの名前(groups。名前の順)を読む。
// conv が空でなければ、その会話だけを読む。conversationShared の users の2つの EXISTS を、分けて読んだもの
func (s *SQLite) shareReasons(ctx context.Context, me, conv string) (toMe map[string]bool, groups map[string][]string, err error) {
	only := ""
	args := []any{sql.Named("me", me)}
	if conv != "" {
		only = " AND c.id = :conv"
		args = append(args, sql.Named("conv", conv))
	}
	toMe = map[string]bool{}
	ids, err := s.names(ctx, `SELECT c.id FROM conversation_shares s JOIN conversations c ON c.id = s.conversation_id
		WHERE s.user_id = :me AND c.share = 'users' AND c.owner <> :me`+only, args...)
	if err != nil {
		return nil, nil, err
	}
	for _, id := range ids {
		toMe[id] = true
	}
	rows, err := s.db.QueryContext(ctx, `SELECT c.id, ug.name FROM conversation_group_shares g
		JOIN conversations c ON c.id = g.conversation_id
		JOIN user_group_members m ON m.group_id = g.group_id
		JOIN user_groups ug ON ug.id = g.group_id
		WHERE m.user_id = :me AND c.share = 'users' AND c.owner <> :me`+only+` ORDER BY ug.name`, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	groups = map[string][]string{}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, nil, err
		}
		groups[id] = append(groups[id], name)
	}
	return toMe, groups, rows.Err()
}

// ---- 会話の一覧の非アクティブ(0055) ----------------------------------------------------------
//
// 非アクティブは会話の中身ではなく、一覧での見せ方の属性。一覧を見る人ごとに表 conversation_inactive に覚える
// (共有された会話に付けても、持ち主やほかの相手の一覧は変わらない。決定事項 16)。

// inactiveSet は、:me が非アクティブにした会話の ID。
func (s *SQLite) inactiveSet(ctx context.Context, me string) (map[string]bool, error) {
	ids, err := s.names(ctx, "SELECT conversation_id FROM conversation_inactive WHERE user_id = ?", me)
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set, nil
}

func (s *SQLite) SetConversationInactive(ctx context.Context, id uuid.UUID, inactive bool) error {
	me, err := s.owner(ctx)
	if err != nil {
		return err
	}
	// 付けられるのは、一覧に出る会話(自分の会話か、自分に共有された会話)だけ。ほかの人の会話はあることも見せない
	var visible bool
	err = s.db.QueryRowContext(ctx, "SELECT conversations.owner = :me OR "+conversationShared+" FROM conversations WHERE conversations.id = :id",
		sql.Named("me", me), sql.Named("id", id.String())).Scan(&visible)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !visible) {
		return aibow.ErrNotFound
	}
	if err != nil {
		return err
	}
	if inactive {
		_, err = s.db.ExecContext(ctx, "INSERT OR IGNORE INTO conversation_inactive (conversation_id, user_id) VALUES (?, ?)", id.String(), me)
	} else {
		_, err = s.db.ExecContext(ctx, "DELETE FROM conversation_inactive WHERE conversation_id = ? AND user_id = ?", id.String(), me)
	}
	return err
}

func (s *SQLite) UserConversations(ctx context.Context, name string) ([]aibow.ConversationRef, error) {
	args, _, err := s.profileArgs(ctx)
	if err != nil {
		return nil, err
	}
	if args[1].(sql.NamedArg).Value.(int) != 1 {
		return nil, aibow.ErrNotAdmin
	}
	return s.conversationRefs(ctx, "u.name = :name", sql.Named("name", name))
}

func (s *SQLite) ReadConversation(ctx context.Context, id uuid.UUID) (aibow.SharedRead, error) {
	args, me, err := s.profileArgs(ctx)
	if err != nil {
		return aibow.SharedRead{}, err
	}
	admin := args[1].(sql.NamedArg).Value.(int) == 1
	var r aibow.SharedRead
	var owner string
	var shared bool
	err = s.db.QueryRowContext(ctx, "SELECT conversations.owner, COALESCE(u.name, ''), COALESCE(u.display_name, ''), "+conversationShared+
		" FROM conversations LEFT JOIN users u ON u.id = conversations.owner WHERE conversations.id = :id",
		sql.Named("me", me), sql.Named("id", id.String())).Scan(&owner, &r.OwnerName, &r.OwnerDisplay, &shared)
	if errors.Is(err, sql.ErrNoRows) {
		return r, aibow.ErrNotFound
	}
	if err != nil {
		return r, err
	}
	// 自分の会話は Conversation で読む。共有されていない人には(管理者でなければ)、あることも見せない
	if owner == me || (!shared && !admin) {
		return r, aibow.ErrNotFound
	}
	if r.Conversation, err = scanConversation(s.db.QueryRowContext(ctx, conversationSelect+" WHERE id = ?", id.String())); err != nil {
		return r, err
	}
	r.Conversation.Settings.Workspace, r.Conversation.Settings.WorkDir, r.Conversation.Settings.Worktree = "", "", false // 持ち主の PC のフォルダは見せない(0035・0039)
	if r.Messages, err = s.messages(ctx, id); err != nil {
		return r, err
	}
	if r.Compactions, err = s.compactions(ctx, id, "conversation_id = ?", id.String()); err != nil {
		return r, err
	}
	if err := s.db.QueryRowContext(ctx, "SELECT name FROM users WHERE id = ?", me).Scan(&r.ReaderName); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return r, err
	}
	r.ByAdmin = !shared
	if shared && r.Conversation.Share == aibow.ShareUsers {
		toMe, groups, err := s.shareReasons(ctx, me, id.String())
		if err != nil {
			return r, err
		}
		r.ToMe, r.Groups = toMe[id.String()], groups[id.String()]
	}
	return r, nil
}

func (s *SQLite) Directory(ctx context.Context) (aibow.Directory, error) {
	d := aibow.Directory{Users: []aibow.UserRef{}}
	if _, err := s.owner(ctx); err != nil {
		return d, err
	}
	if err := s.userRefs(ctx, &d); err != nil {
		return d, err
	}
	// 接続は1本なので、人の rows を閉じてからグループを読む
	var err error
	d.Groups, err = s.groups(ctx)
	return d, err
}

func (s *SQLite) userRefs(ctx context.Context, d *aibow.Directory) error {
	rows, err := s.db.QueryContext(ctx, "SELECT name, display_name FROM users WHERE id <> ? ORDER BY name", aibow.LocalUser.String())
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var u aibow.UserRef
		if err := rows.Scan(&u.Name, &u.DisplayName); err != nil {
			return err
		}
		d.Users = append(d.Users, u)
	}
	return rows.Err()
}
