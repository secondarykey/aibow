package store

// profiles.go: プロファイルと、プロファイルを配る(0018 の 11-1)。見える・変えられるの条件 profileVisible・profileUsed・profileEditable はここの1か所。

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/secondarykey/aibow"
	"github.com/secondarykey/aibow/models"
)

// ---- プロファイル -------------------------------------------------------------------------

const profileColumns = "id, name, settings, rules, archived, created_at, updated_at"

// ---- プロファイルを配る(0018 の 11-1) ----------------------------------------------------
//
// 誰に何が見えて、誰が変えられるかを、ここの SQL の条件だけで決める(認可を1か所に置く)。
// 条件の ? には、順に ctx の利用者(me)と、管理者か(admin。0 / 1)を入れる(profileArgs)。

const (
	// profileVisible は、me に見える(新しい会話で選べる)プロファイル
	// (:me が指定した人に入っている、または指定したグループのメンバー。0031 の決定事項 27)
	profileVisible = `(profiles.owner = :me OR profiles.share = 'all'
		OR (profiles.share = 'users' AND (
			EXISTS (SELECT 1 FROM profile_shares s WHERE s.profile_id = profiles.id AND s.user_id = :me)
			OR EXISTS (SELECT 1 FROM profile_group_shares g JOIN user_group_members m ON m.group_id = g.group_id
				WHERE g.profile_id = profiles.id AND m.user_id = :me)))
		OR (profiles.share <> '' AND :admin = 1))`
	// profileUsed は、me がそのプロファイルから作った会話がある(配られなくなっても決まりを引くため。決定事項 21)
	profileUsed = `EXISTS (SELECT 1 FROM conversations c WHERE c.profile_id = profiles.id AND c.owner = :me)`
	// profileEditable は、me が変えられるプロファイル(自分の配っていないもの・管理者なら配られたもの。決定事項 9・20)
	profileEditable = `((profiles.share = '' AND profiles.owner = :me) OR (profiles.share <> '' AND :admin = 1))`
	// profileSelect は、読むときの列(保存している列 + 持ち主・配る範囲・作った人の名前・変えられないか・もう配られていないか)
	profileSelect = "SELECT " + profileColumns + ", owner, share, owner_name, NOT " + profileEditable + ", NOT " + profileVisible + " FROM profiles"
)

// profileArgs は、上の条件の名前付きの引数(me・admin)。
func (s *SQLite) profileArgs(ctx context.Context) ([]any, string, error) {
	me, err := s.owner(ctx)
	if err != nil {
		return nil, "", err
	}
	admin, err := s.isAdmin(ctx, me)
	if err != nil {
		return nil, "", err
	}
	return []any{sql.Named("me", me), sql.Named("admin", admin)}, me, nil
}

// fillShareWith は、変えられる人にだけ、配る相手の名前を入れる。
func (s *SQLite) fillShareWith(ctx context.Context, p *aibow.Profile) error {
	if p.ReadOnly || p.Share != aibow.ShareUsers {
		return nil
	}
	var err error
	p.ShareWith, p.ShareGroups, err = s.shareNames(ctx, "profile", p.Share, p.ID)
	return err
}

// SetProfileShare は、配る範囲と相手を変える(aibow.Store の約束の説明を参照)。相手は名前で受け取り、いない名前ならエラー。
func (s *SQLite) SetProfileShare(ctx context.Context, id uuid.UUID, share string, with, groups []string) error {
	args, me, err := s.profileArgs(ctx)
	if err != nil {
		return err
	}
	if args[1].(sql.NamedArg).Value.(int) != 1 {
		return aibow.ErrNotAdmin
	}
	switch share {
	case aibow.ShareNone, aibow.ShareAll, aibow.ShareUsers:
	default:
		return fmt.Errorf("配る範囲 %q は知らない値です(\"\"・all・users のどれか)", share)
	}
	p, err := s.Profile(ctx, id)
	if err != nil {
		return err
	}
	if p.ReadOnly || p.Withdrawn {
		return aibow.ErrProfileReadOnly
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// 初めて配るときに、作った人の名前を覚える(作った人を消しても、受け取った人に誰が配ったかを見せるため)
	name := p.OwnerName
	if name == "" {
		if err := tx.QueryRowContext(ctx, "SELECT name FROM users WHERE id = ?", p.Owner.String()).Scan(&name); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE profiles SET share = ?, owner_name = ?, updated_at = ? WHERE id = ?",
		share, name, formatTime(time.Now()), id.String()); err != nil {
		return err
	}
	if share != aibow.ShareUsers {
		with, groups = nil, nil // 相手の表は空にする
	}
	// 自分は相手にしない(管理者なので、配らなくても見える)
	if err := setShareTargets(ctx, tx, "profile", "配る相手", id, me, with, groups); err != nil {
		return err
	}
	return tx.Commit()
}

// editableErr は、変えようとして1行も変わらなかったときのエラー。見えるなら変えられないもの、見えなければ無いもの。
func (s *SQLite) editableErr(ctx context.Context, id uuid.UUID) error {
	if _, err := s.Profile(ctx, id); err != nil {
		return err
	}
	return aibow.ErrProfileReadOnly
}

func scanProfile(r rowScanner) (aibow.Profile, error) {
	var p aibow.Profile
	var id, values, rules, created, updated, owner string
	if err := r.Scan(&id, &p.Name, &values, &rules, &p.Archived, &created, &updated, &owner, &p.Share, &p.OwnerName, &p.ReadOnly, &p.Withdrawn); err != nil {
		return p, err
	}
	var err error
	if p.ID, err = parseID(id); err != nil {
		return p, err
	}
	if p.Owner, err = parseID(owner); err != nil {
		return p, err
	}
	if err := json.Unmarshal([]byte(values), &p.Values); err != nil {
		return p, fmt.Errorf("プロファイル %s の値が読めません: %w", id, err)
	}
	p.Values.Model = models.IDOf(p.Values.Model) // 会話と同じく、以前の形のモデルの ID を読み替える
	if err := json.Unmarshal([]byte(rules), &p.Rules); err != nil {
		return p, fmt.Errorf("プロファイル %s の決まりが読めません: %w", id, err)
	}
	if p.CreatedAt, err = parseTime(created); err != nil {
		return p, err
	}
	if p.UpdatedAt, err = parseTime(updated); err != nil {
		return p, err
	}
	return p, nil
}

// ListProfiles は、ctx の利用者に見えるプロファイル(自分のもの・配られたもの。profileVisible)。
func (s *SQLite) ListProfiles(ctx context.Context) ([]aibow.Profile, error) {
	args, _, err := s.profileArgs(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, profileSelect+" WHERE "+profileVisible+" ORDER BY id", args...)
	if err != nil {
		return nil, err
	}
	out := []aibow.Profile{}
	for rows.Next() {
		p, err := scanProfile(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if err := s.fillShareWith(ctx, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Profile は、見えるプロファイルか、ctx の利用者がそのプロファイルから作った会話があるプロファイル(配られなくなっても決まりを引くため)。
func (s *SQLite) Profile(ctx context.Context, id uuid.UUID) (aibow.Profile, error) {
	args, _, err := s.profileArgs(ctx)
	if err != nil {
		return aibow.Profile{}, err
	}
	args = append(args, sql.Named("id", id.String()))
	p, err := scanProfile(s.db.QueryRowContext(ctx, profileSelect+" WHERE ("+profileVisible+" OR "+profileUsed+") AND profiles.id = :id", args...))
	if errors.Is(err, sql.ErrNoRows) {
		return p, aibow.ErrProfileNotFound
	}
	if err != nil {
		return p, err
	}
	return p, s.fillShareWith(ctx, &p)
}

// profileJSON は、プロファイルの値と決まりを保存する形(JSON)にする。
func profileJSON(p aibow.Profile) (values, rules string, err error) {
	v, err := json.Marshal(p.Values)
	if err != nil {
		return "", "", err
	}
	r, err := json.Marshal(p.Rules)
	if err != nil {
		return "", "", err
	}
	return string(v), string(r), nil
}

func (s *SQLite) CreateProfile(ctx context.Context, p aibow.Profile) error {
	owner, err := s.owner(ctx)
	if err != nil {
		return err
	}
	values, rules, err := profileJSON(p)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "INSERT INTO profiles ("+profileColumns+", owner) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		p.ID.String(), p.Name, values, rules, p.Archived, formatTime(p.CreatedAt), formatTime(p.UpdatedAt), owner)
	return err
}

// UpdateProfile は、変えられるプロファイル(profileEditable)だけを書き換える。配る範囲は変えない(SetProfileShare)。
func (s *SQLite) UpdateProfile(ctx context.Context, p aibow.Profile) error {
	args, _, err := s.profileArgs(ctx)
	if err != nil {
		return err
	}
	values, rules, err := profileJSON(p)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, "UPDATE profiles SET name = :name, settings = :values, rules = :rules, archived = :archived, updated_at = :at WHERE id = :id AND "+profileEditable,
		append(args, sql.Named("name", p.Name), sql.Named("values", values), sql.Named("rules", rules), sql.Named("archived", p.Archived),
			sql.Named("at", formatTime(p.UpdatedAt)), sql.Named("id", p.ID.String()))...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return s.editableErr(ctx, p.ID)
	}
	return nil
}

// DeleteProfile は、使っている会話が無いときだけプロファイルを消す。
// 外部キーでも消せないようになっているが、SQLite の制約のエラーは何が理由か読みにくいので、先に数えて ErrProfileInUse を返す。
// 数えてから消すまでの間に会話が増えないよう、1つのトランザクションで行う。
func (s *SQLite) DeleteProfile(ctx context.Context, id uuid.UUID) error {
	args, _, err := s.profileArgs(ctx)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var used int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM conversations WHERE profile_id = ?", id.String()).Scan(&used); err != nil {
		return err
	}
	if used > 0 {
		return aibow.ErrProfileInUse
	}
	res, err := tx.ExecContext(ctx, "DELETE FROM profiles WHERE id = :id AND "+profileEditable, append(args, sql.Named("id", id.String()))...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		tx.Rollback() // 接続は1本なので、トランザクションを閉じてから読む(閉じないと、読む側が待ち続ける)
		return s.editableErr(ctx, id)
	}
	return tx.Commit()
}
