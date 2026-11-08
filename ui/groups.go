package ui

// groups.go: 画面から呼ぶ、グループのメソッド(0031 の段4)。

import (
	"context"
	"strings"
	"uuid"

	"github.com/secondarykey/aibow"
	"github.com/secondarykey/aibow/log"
)

// ---- グループ(0031 の段4) ---------------------------------------------------------------------
//
// 誰が何を変えられるかは保存先が1か所で決める(管理者だけ / 管理者かそのグループの長)。ここはログを残すだけ。
// メンバーを変えると、そのグループに共有された会話・配られたプロファイルが見える人も変わるので、
// 誰が・どのグループに・誰を足した / 外したかを Notice に残す(管理者が読んだ記録と同じ考え方。決定事項 33)。

// CreateGroup はグループを作る(管理者だけ)。
func (s *Service) CreateGroup(ctx context.Context, name string) (aibow.Group, error) {
	if s.startupErr != nil {
		return aibow.Group{}, s.startupErr
	}
	g, err := s.store.CreateGroup(ctx, name)
	if err != nil {
		return g, err
	}
	log.Noticef("グループ「%s」を作りました(%s)", g.Name, whoOf(ctx))
	return g, nil
}

// RenameGroup はグループの名前を変える(管理者だけ)。
func (s *Service) RenameGroup(ctx context.Context, id uuid.UUID, name string) error {
	if s.startupErr != nil {
		return s.startupErr
	}
	if err := s.store.RenameGroup(ctx, id, name); err != nil {
		return err
	}
	log.Noticef("グループ %s の名前を「%s」にしました(%s)", id, strings.TrimSpace(name), whoOf(ctx))
	return nil
}

// DeleteGroup はグループを消す(管理者だけ)。そのグループへの共有と配るも消える。
func (s *Service) DeleteGroup(ctx context.Context, id uuid.UUID) error {
	if s.startupErr != nil {
		return s.startupErr
	}
	name := s.groupName(ctx, id)
	if err := s.store.DeleteGroup(ctx, id); err != nil {
		return err
	}
	log.Noticef("グループ「%s」を消しました(%s。このグループへの共有と配るも消えました)", name, whoOf(ctx))
	return nil
}

// SetGroupMember は、グループに利用者を足す / 外す(管理者か、そのグループの長)。
func (s *Service) SetGroupMember(ctx context.Context, id uuid.UUID, user string, member bool) error {
	if s.startupErr != nil {
		return s.startupErr
	}
	if err := s.store.SetGroupMember(ctx, id, user, member); err != nil {
		return err
	}
	if member {
		log.Noticef("%s がグループ「%s」に %s を足しました", whoOf(ctx), s.groupName(ctx, id), user)
	} else {
		log.Noticef("%s がグループ「%s」から %s を外しました", whoOf(ctx), s.groupName(ctx, id), user)
	}
	return nil
}

// SetGroupLeader は、メンバーを長にする / 外す(管理者だけ)。
func (s *Service) SetGroupLeader(ctx context.Context, id uuid.UUID, user string, leader bool) error {
	if s.startupErr != nil {
		return s.startupErr
	}
	if err := s.store.SetGroupLeader(ctx, id, user, leader); err != nil {
		return err
	}
	if leader {
		log.Noticef("%s がグループ「%s」の長に %s を決めました", whoOf(ctx), s.groupName(ctx, id), user)
	} else {
		log.Noticef("%s がグループ「%s」の長から %s を外しました", whoOf(ctx), s.groupName(ctx, id), user)
	}
	return nil
}

// groupName は、ログに書くグループの名前(読めなければ ID)。
func (s *Service) groupName(ctx context.Context, id uuid.UUID) string {
	d, err := s.store.Directory(ctx)
	if err == nil {
		for _, g := range d.Groups {
			if g.ID == id {
				return g.Name
			}
		}
	}
	return id.String()
}
