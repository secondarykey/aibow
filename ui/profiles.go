package ui

// profiles.go: 画面から呼ぶ、プロファイルのメソッド(0014)。

import (
	"context"
	"fmt"
	"strings"
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
		// 自分のプロファイル(配っていない)は自分にだけ効くので Info(0053 の決定事項 21)
		log.Infof("プロファイル「%s」を作成しました(%s)", p.Name, p.ID)
	}
	return p, err
}

// UpdateProfile は、プロファイルの名前・値・決まりを変える。値は今ある会話に効かず、決まりは今ある会話にもすぐ効く。
// 配っているプロファイルなら、モデルは使えるモデルの中から(0018 の決定事項 13)。
// 画面の「保存」は SaveProfile(この PC の確認しないも一緒に書く)。これはツールの扱いを変えない保存。
func (s *Service) UpdateProfile(ctx context.Context, p aibow.Profile) error {
	return s.SaveProfile(ctx, p, nil)
}

// SaveProfile は、プロファイルの編集の「保存」(0053 の決定事項 7)。プロファイルの名前・値・決まりと、
// 同じ画面で変えたツールの「確認しない」(この PC の tools.json。ブラウザでは出さないので空)をまとめて書く。
// 1つの画面の中で、保存されるものとされないものが混ざらないよう、画面は1回の「保存」でここを1回呼ぶ。
//
// ログは1回の保存で1行(決定事項 21・23)。配っているプロファイル(ほかの人の会話に効く)は Notice で、誰が変えたかも添える。
// 自分のプロファイルは Info。確認しないは先に書く(書けなければ、プロファイルも保存しない。画面は下書きを残してエラーを出す)。
func (s *Service) SaveProfile(ctx context.Context, p aibow.Profile, tools []ToolPolicyChange) error {
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
	toolItems, err := s.applyToolPolicies(ctx, tools)
	if err != nil {
		return err
	}
	if err := s.convs.UpdateProfile(ctx, p); err != nil {
		return err
	}
	items := aibow.ProfileChanges(old, p)
	if len(toolItems) > 0 {
		items = append(items, "この PC の確認の扱い("+strings.Join(toolItems, "・")+")")
	}
	if old.Share != aibow.ShareNone {
		log.Noticef("プロファイル「%s」を保存しました(%s%s)", p.Name, aibow.ChangedItems(items), byWhom(ctx))
	} else {
		log.Infof("プロファイル「%s」を保存しました(%s)", p.Name, aibow.ChangedItems(items))
	}
	return nil
}

// byWhom は、ログの括弧の後ろに添える「誰が変えたか」(aibow-server のときだけ。アプリは1人なので書かない)。
func byWhom(ctx context.Context) string {
	if w := whoOf(ctx); w != "" {
		return "・" + w + " が変えた"
	}
	return ""
}

// usableModel は、配るプロファイルのモデルが使えるか(管理者が使えないようにしたモデルでは配らない。0018 の決定事項 13)。
func (s *Service) usableModel(id string) error {
	if id == "" {
		return nil
	}
	if err := s.host.usable(id); err != nil {
		return fmt.Errorf("配布するプロファイルのモデルは、使えるモデルから選んでください: %w", err)
	}
	return nil
}

// SetProfileShare は、配布プロファイルを配る範囲(all・users)と相手(利用者の名前とグループの名前。0031)を変える(0018 の 11-1)。管理者だけ。
// 配布をやめる("")と、個人プロファイルを配るのは断る(aibow が断る。0059 の決定事項 10)。やめるときは停止(SetProfileArchived)か削除。
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
	log.Noticef("プロファイル「%s」の配布先を%sにしました(%s%s)", p.Name, shareTargets(share, with, groups), id, byWhom(ctx))
	return nil
}

// shareTargets は、ログに書く配る相手(「全員」か、人とグループの名前)。
func shareTargets(share string, with, groups []string) string {
	if share == aibow.ShareAll {
		return "全員"
	}
	return targets(with, groups)
}

// ShareNew は、配布プロファイルを最初から作って、そのまま配る(管理の画面の「新規」。0059 の決定事項 9)。管理者だけ(保存先が確かめる)。
// 配るプロファイルのモデルは、使えるモデルから(0018 の決定事項 13。SetProfileShare と同じ)。
func (s *Service) ShareNew(ctx context.Context, name, model, share string, with, groups []string) (aibow.Profile, error) {
	if s.startupErr != nil {
		return aibow.Profile{}, s.startupErr
	}
	if err := s.usableModel(model); err != nil {
		return aibow.Profile{}, err
	}
	p, err := s.convs.ShareNew(ctx, name, model, share, with, groups)
	if err != nil {
		return p, err
	}
	log.Noticef("プロファイル「%s」を作成して、%sに配布しました(%s%s)", p.Name, shareTargets(share, with, groups), p.ID, byWhom(ctx))
	return p, nil
}

// CopyProfile は、プロファイル(配られたものなど)を写して自分のプロファイルを作る(値と決まりも。0018 の決定事項 19)。
func (s *Service) CopyProfile(ctx context.Context, id uuid.UUID) (aibow.Profile, error) {
	if s.startupErr != nil {
		return aibow.Profile{}, s.startupErr
	}
	p, err := s.convs.CopyProfile(ctx, id)
	if err == nil {
		log.Infof("プロファイルを複製して「%s」を作成しました(%s)", p.Name, p.ID)
	}
	return p, err
}

// SetProfileArchived は、プロファイルをしまう / 戻す。配布プロファイルなら、配布の停止 / 再開(0059 の決定事項 12)。
func (s *Service) SetProfileArchived(ctx context.Context, id uuid.UUID, archived bool) error {
	if s.startupErr != nil {
		return s.startupErr
	}
	p, err := s.store.Profile(ctx, id)
	if err != nil {
		return err
	}
	if err := s.convs.SetProfileArchived(ctx, id, archived); err != nil {
		return err
	}
	done := map[bool]string{true: "をアーカイブしました", false: "をアーカイブ解除しました"}[archived]
	if p.Share != aibow.ShareNone {
		done = map[bool]string{true: "の配布を停止しました", false: "の配布を再開しました"}[archived]
	}
	log.Noticef("プロファイル「%s」%s(%s%s)", p.Name, done, id, byWhom(ctx))
	return nil
}

// DeleteProfile は、使っている会話が無いプロファイルを消す。
func (s *Service) DeleteProfile(ctx context.Context, id uuid.UUID) error {
	if s.startupErr != nil {
		return s.startupErr
	}
	name := s.profileName(ctx, id)
	if err := s.convs.DeleteProfile(ctx, id); err != nil {
		return err
	}
	log.Noticef("プロファイル「%s」を削除しました(%s%s)", name, id, byWhom(ctx))
	return nil
}

// profileName は、ログに書くプロファイルの名前(ID だけでは何のことか分からないため。0053 の決定事項 24)。読めなければ空。
func (s *Service) profileName(ctx context.Context, id uuid.UUID) string {
	p, err := s.store.Profile(ctx, id)
	if err != nil {
		return ""
	}
	return p.Name
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
		log.Infof("既定のプロファイルを「%s」にしました", p.Name)
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
	log.Infof("会話「%s」の設定を、プロファイル「%s」に保存しました(%s)", s.conversationTitle(ctx, conv), s.profileName(ctx, profile), conv)
	return nil
}

// SaveAsProfile は、会話の今の設定で新しいプロファイルを作る(会話の設定の画面の「新しいプロファイルとして保存」)。
func (s *Service) SaveAsProfile(ctx context.Context, conv uuid.UUID, name string) (aibow.Profile, error) {
	if s.startupErr != nil {
		return aibow.Profile{}, s.startupErr
	}
	p, err := s.convs.SaveAsProfile(ctx, conv, name)
	if err == nil {
		log.Infof("会話「%s」の設定から、プロファイル「%s」を作成しました(%s)", s.conversationTitle(ctx, conv), p.Name, conv)
	}
	return p, err
}
