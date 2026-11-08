package ui

// share.go: 画面から呼ぶ、会話の共有と、ほかの人の会話を読む・写すメソッド(0018 の 11-2)。グループは groups.go。

import (
	"context"
	"errors"
	"slices"
	"strings"
	"uuid"

	"github.com/secondarykey/aibow"
	"github.com/secondarykey/aibow/log"
)

// ---- 会話の共有と管理者が読む(0018 の 11-2) ---------------------------------------------------
//
// ほかの人の会話は読むだけ(保存先の ReadConversation)。続けたいときは写して自分の会話にする(CopyShared)。

// SetConversationShare は、自分の会話の共有の範囲(""・all・users)と相手(利用者の名前とグループの名前。0031)を変える。
func (s *Service) SetConversationShare(ctx context.Context, id uuid.UUID, share string, with, groups []string) error {
	if s.startupErr != nil {
		return s.startupErr
	}
	if share == aibow.ShareUsers && len(with) == 0 && len(groups) == 0 {
		return errors.New("共有の相手を1人以上選んでください")
	}
	if err := s.store.SetConversationShare(ctx, id, share, with, groups); err != nil {
		return err
	}
	switch share {
	case aibow.ShareNone:
		log.Noticef("会話 %s を非公開にしました", id)
	case aibow.ShareAll:
		log.Noticef("会話 %s を全員に共有しました", id)
	default:
		log.Noticef("会話 %s を %s に共有しました", id, targets(with, groups))
	}
	return nil
}

// SharedConversations は、自分に共有されたほかの人の会話の一覧(会話の一覧の下の「共有された会話」)。
func (s *Service) SharedConversations(ctx context.Context) ([]aibow.ConversationRef, error) {
	if s.startupErr != nil {
		return nil, s.startupErr
	}
	return s.store.SharedConversations(ctx)
}

// UserConversations は、利用者 name の会話の一覧(管理者が「利用者」のタブから見る。決定事項 25)。
func (s *Service) UserConversations(ctx context.Context, name string) ([]aibow.ConversationRef, error) {
	if s.startupErr != nil {
		return nil, s.startupErr
	}
	return s.store.UserConversations(ctx, name)
}

// targets は、ログに書く共有の相手(人はログインの名前。表示名は変わるので使わない。0031 の決定事項 20)
func targets(with, groups []string) string {
	all := slices.Clone(with)
	for _, g := range groups {
		all = append(all, g+" グループ")
	}
	return strings.Join(all, "・")
}

// Directory は、共有の相手に選べる人(名前と表示名)とグループ(0031)。全員を一度に返し、画面で絞る。
func (s *Service) Directory(ctx context.Context) (aibow.Directory, error) {
	if s.startupErr != nil {
		return aibow.Directory{}, s.startupErr
	}
	return s.store.Directory(ctx)
}

// ReadConversation は、ほかの人の会話を読むだけで開く。管理者として読んだ(共有されていない)ときは、
// 誰が・誰の・どの会話を読んだかをログ(Notice)に残す(0018 の決定事項 1。組織の道具の監査のため)。
func (s *Service) ReadConversation(ctx context.Context, id uuid.UUID) (aibow.SharedRead, error) {
	if s.startupErr != nil {
		return aibow.SharedRead{}, s.startupErr
	}
	r, err := s.store.ReadConversation(ctx, id)
	if err != nil {
		return r, err
	}
	if r.ByAdmin {
		log.Noticef("管理者 %s が %s の会話「%s」(%s)を読みました", r.ReaderName, r.OwnerName, r.Conversation.Title, id)
	} else {
		log.Infof("%s が、共有された %s の会話 %s を読みました", r.ReaderName, r.OwnerName, id)
	}
	return r, nil
}

// CopyShared は、ほかの人の会話を写して自分の会話にする(写して続ける。決定事項 2)。
func (s *Service) CopyShared(ctx context.Context, id uuid.UUID) (aibow.Conversation, error) {
	if s.startupErr != nil {
		return aibow.Conversation{}, s.startupErr
	}
	c, err := s.convs.CopyShared(ctx, id)
	if err != nil {
		return c, err
	}
	log.Noticef("会話 %s を写して、自分の会話「%s」(%s)を作りました", id, c.Title, c.ID)
	return c, nil
}
