package ui

// profiles.go: 画面から呼ぶ、プロファイルのメソッド(0014)。

import (
	"cmp"
	"context"
	"fmt"
	"uuid"

	"github.com/secondarykey/aibow"
	"github.com/secondarykey/aibow/log"
)

// ---- プロファイル(0014) -------------------------------------------------------------------
//
// プロファイルは名前の付いた「新しい会話の既定」。値は会話を作るときに写し、決まり(固定・使えない)は会話からそのつど見に行く。
// 扱いを決めるのは aibow(Conversations)で、ここは画面との中継とログだけ。

// Profiles は、プロファイルの一覧(しまったものも。使っている会話の数・新しい会話で最初に選ばれるか)。
func (s *Service) Profiles(ctx context.Context) ([]aibow.ProfileInfo, error) {
	if s.startupErr != nil {
		return nil, s.startupErr
	}
	return s.convs.Profiles(ctx)
}

// CreateProfile は、新しいプロファイルを作る(設定の画面の「新しいプロファイル」)。
func (s *Service) CreateProfile(ctx context.Context, name string, values aibow.ProfileValues, rules aibow.ProfileRules) (aibow.Profile, error) {
	if s.startupErr != nil {
		return aibow.Profile{}, s.startupErr
	}
	if err := s.checkFolder(ctx, "", rules.Folder); err != nil {
		return aibow.Profile{}, err
	}
	p, err := s.convs.CreateProfile(ctx, name, values, rules)
	if err == nil {
		log.Noticef("プロファイル「%s」を作りました", p.Name)
	}
	return p, err
}

// UpdateProfile は、プロファイルの名前・値・決まりを変える。値は今ある会話に効かず、決まりは今ある会話にもすぐ効く。
// 配っているプロファイルなら、モデルは使えるモデルの中から(0018 の決定事項 13)。
func (s *Service) UpdateProfile(ctx context.Context, p aibow.Profile) error {
	if s.startupErr != nil {
		return s.startupErr
	}
	old, err := s.store.Profile(ctx, p.ID)
	if err != nil {
		return err
	}
	if old.Share != aibow.ShareNone {
		if err := s.usableModel(p.Values.Model); err != nil {
			return err
		}
	}
	if err := s.checkFolder(ctx, old.Rules.Folder, p.Rules.Folder); err != nil {
		return err
	}
	if p.Rules.Folder != old.Rules.Folder {
		log.Noticef("プロファイル「%s」の資料のフォルダ: %s(%s が変えた)", p.Name, cmp.Or(p.Rules.Folder, "なし"), whoOf(ctx))
	}
	if err := s.convs.UpdateProfile(ctx, p); err != nil {
		return err
	}
	log.Noticef("プロファイル「%s」を変えました(固定 %v・使えない %+v)", p.Name, p.Rules.Fixed, p.Rules.Off)
	return nil
}

// usableModel は、配るプロファイルのモデルが使えるか(管理者が使えないようにしたモデルでは配らない。0018 の決定事項 13)。
func (s *Service) usableModel(id string) error {
	if id == "" {
		return nil
	}
	if err := s.host.usable(id); err != nil {
		return fmt.Errorf("配るプロファイルのモデルは、使えるモデルから選んでください: %w", err)
	}
	return nil
}

// SetProfileShare は、プロファイルを配る範囲(""・all・users)と相手(利用者の名前とグループの名前。0031)を変える(0018 の 11-1)。管理者だけ。
func (s *Service) SetProfileShare(ctx context.Context, id uuid.UUID, share string, with, groups []string) error {
	if s.startupErr != nil {
		return s.startupErr
	}
	p, err := s.store.Profile(ctx, id)
	if err != nil {
		return err
	}
	if share != aibow.ShareNone {
		if err := s.usableModel(p.Values.Model); err != nil {
			return err
		}
	}
	if err := s.convs.SetProfileShare(ctx, id, share, with, groups); err != nil {
		return err
	}
	switch share {
	case aibow.ShareNone:
		log.Noticef("プロファイル「%s」を配るのをやめました", p.Name)
	case aibow.ShareAll:
		log.Noticef("プロファイル「%s」を全員に配りました", p.Name)
	default:
		log.Noticef("プロファイル「%s」を %s に配りました", p.Name, targets(with, groups))
	}
	return nil
}

// ShareCopy は、プロファイルを写して、そのまま配る(管理の画面の「写して配る」。0032 の段3)。管理者だけ(保存先が確かめる)。
// 配るプロファイルのモデルは、使えるモデルから(0018 の決定事項 13。SetProfileShare と同じ)。
func (s *Service) ShareCopy(ctx context.Context, id uuid.UUID, share string, with, groups []string) (aibow.Profile, error) {
	if s.startupErr != nil {
		return aibow.Profile{}, s.startupErr
	}
	src, err := s.store.Profile(ctx, id)
	if err != nil {
		return aibow.Profile{}, err
	}
	if err := s.usableModel(src.Values.Model); err != nil {
		return aibow.Profile{}, err
	}
	p, err := s.convs.ShareCopy(ctx, id, share, with, groups)
	if err != nil {
		return p, err
	}
	switch share {
	case aibow.ShareAll:
		log.Noticef("プロファイル「%s」を写して、全員に配りました(%s)", p.Name, whoOf(ctx))
	default:
		log.Noticef("プロファイル「%s」を写して、%s に配りました(%s)", p.Name, targets(with, groups), whoOf(ctx))
	}
	return p, nil
}

// CopyProfile は、プロファイル(配られたものなど)を写して自分のプロファイルを作る(値と決まりも。0018 の決定事項 19)。
func (s *Service) CopyProfile(ctx context.Context, id uuid.UUID) (aibow.Profile, error) {
	if s.startupErr != nil {
		return aibow.Profile{}, s.startupErr
	}
	p, err := s.convs.CopyProfile(ctx, id)
	if err == nil {
		log.Noticef("プロファイルを写して「%s」を作りました", p.Name)
	}
	return p, err
}

// SetProfileArchived は、プロファイルをしまう / 戻す。
func (s *Service) SetProfileArchived(ctx context.Context, id uuid.UUID, archived bool) error {
	if s.startupErr != nil {
		return s.startupErr
	}
	if err := s.convs.SetProfileArchived(ctx, id, archived); err != nil {
		return err
	}
	log.Noticef("プロファイル %s をしまう: %v", id, archived)
	return nil
}

// DeleteProfile は、使っている会話が無いプロファイルを消す。
func (s *Service) DeleteProfile(ctx context.Context, id uuid.UUID) error {
	if s.startupErr != nil {
		return s.startupErr
	}
	if err := s.convs.DeleteProfile(ctx, id); err != nil {
		return err
	}
	log.Noticef("プロファイル %s を消しました", id)
	return nil
}

// SetDefaultProfile は、新しい会話で最初に選ばれるプロファイルを変える。
func (s *Service) SetDefaultProfile(ctx context.Context, id uuid.UUID) error {
	if s.startupErr != nil {
		return s.startupErr
	}
	if err := s.convs.SetDefaultProfile(ctx, id); err != nil {
		return err
	}
	if p, err := s.convs.DefaultProfile(ctx); err == nil {
		log.Noticef("新しい会話で最初に選ばれるプロファイル: %s", p.Name)
	}
	return nil
}

// ProfileDefaults は、プロファイル id から新しい会話を作るときの設定(新しい会話の画面でプロファイルを選んだとき)。
func (s *Service) ProfileDefaults(ctx context.Context, id uuid.UUID) (aibow.ConversationSettings, error) {
	if s.startupErr != nil {
		return aibow.ConversationSettings{}, s.startupErr
	}
	return s.convs.DefaultsOf(ctx, id)
}

// SaveToProfile は、会話の今の設定でプロファイルの値を上書きする(会話の設定の画面の「プロファイルに保存」)。
func (s *Service) SaveToProfile(ctx context.Context, conv, profile uuid.UUID) error {
	if s.startupErr != nil {
		return s.startupErr
	}
	if err := s.convs.SaveToProfile(ctx, conv, profile); err != nil {
		return err
	}
	log.Noticef("会話 %s の設定を、プロファイル %s に保存しました", conv, profile)
	return nil
}

// SaveAsProfile は、会話の今の設定で新しいプロファイルを作る(会話の設定の画面の「新しいプロファイルとして保存」)。
func (s *Service) SaveAsProfile(ctx context.Context, conv uuid.UUID, name string) (aibow.Profile, error) {
	if s.startupErr != nil {
		return aibow.Profile{}, s.startupErr
	}
	p, err := s.convs.SaveAsProfile(ctx, conv, name)
	if err == nil {
		log.Noticef("会話 %s の設定から、プロファイル「%s」を作りました", conv, p.Name)
	}
	return p, err
}
