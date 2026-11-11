package ui

// conversations.go: 画面から呼ぶ、会話のメソッド(一覧・開く・メーター・作る・消す・設定を変える)。送るのは send.go、共有は share.go。

import (
	"context"
	"errors"
	"fmt"
	"uuid"

	"github.com/secondarykey/aibow"
	"github.com/secondarykey/aibow/config"
	"github.com/secondarykey/aibow/engine"
	"github.com/secondarykey/aibow/format"
	"github.com/secondarykey/aibow/log"
)

// ---- 会話 -------------------------------------------------------------------------------

// Conversations は会話の一覧を、更新の新しい順に返す。
func (s *Service) Conversations(ctx context.Context) ([]aibow.Conversation, error) {
	if s.startupErr != nil {
		return nil, s.startupErr
	}
	return s.convs.List(ctx)
}

// ConversationDetail は会話1つと、その発言。
type ConversationDetail struct {
	Conversation aibow.Conversation    `json:"conversation"`
	Messages     []aibow.StoredMessage `json:"messages"`
	Compactions  []aibow.Compaction    `json:"compactions"` // 要約(古い順)。画面の区切りに使う
	Generating   bool                  `json:"generating"`  // この会話で返事を生成中か
	Usage        *aibow.Usage          `json:"usage"`       // メーター。モデルが読み込まれていなければ null
	// Profile は、この会話のプロファイル(名前・値・決まり。0014)。画面は、固定した項目を押せなくして理由を添えるのに使う。
	// プロファイルを持たない会話なら null
	Profile *aibow.Profile `json:"profile"`
	// Folder は、この会話で使える aibow-server の資料のフォルダの名前(パスは出さない。無ければ空。0024 の決定事項 26)
	Folder string `json:"folder,omitempty"`
}

// Conversation は会話1つと、その発言を古い順に返す。
func (s *Service) Conversation(ctx context.Context, id uuid.UUID) (ConversationDetail, error) {
	if s.startupErr != nil {
		return ConversationDetail{}, s.startupErr
	}
	c, ms, err := s.convs.Get(ctx, id)
	if err != nil {
		return ConversationDetail{}, err
	}
	comps, err := s.convs.Compactions(ctx, id)
	if err != nil {
		return ConversationDetail{}, err
	}
	g := s.convs.Generating(ctx)
	u, err := s.Usage(ctx, id)
	if err != nil {
		return ConversationDetail{}, err
	}
	p, err := s.convs.ProfileOf(ctx, c)
	if err != nil {
		return ConversationDetail{}, err
	}
	return ConversationDetail{Conversation: c, Messages: ms, Compactions: comps, Generating: g != nil && *g == id, Usage: u, Profile: p,
		Folder: s.convs.FolderName(c.Settings)}, nil
}

// Usage は、会話がモデルに渡す文字列で、会話の上限をどれだけ使っているか(メーター)を返す。
// トークン数は今のモデルで数えるので、モデルが読み込まれていなければ null を返す。
// 画面は、モデル・上限・会話の設定を変えたあとに呼び直す。
func (s *Service) Usage(ctx context.Context, id uuid.UUID) (*aibow.Usage, error) {
	if s.startupErr != nil {
		return nil, s.startupErr
	}
	u, err := s.convs.Usage(ctx, id)
	// モデルが無い(降ろした)・書式に未対応のモデルなら、数えられないのでメーターを出さない。
	// エラーにすると会話を開くこと(Conversation)まで失敗し、開くたびにエラーのカードが積み重なるため(未対応なことは読み込んだときと送信したときに伝える)
	// 画像の付いた会話を、画像を読めないモデルで開いたときも同じ(0030。送ったときに理由を伝える)
	if errors.Is(err, engine.ErrNoModel) || errors.Is(err, format.ErrUnsupported) || errors.Is(err, engine.ErrNoVision) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// NewConversation は、プロファイル profile から新しい会話を作る(空なら新しい会話で最初に選ばれるプロファイル)。
// settings が null ならプロファイルの値のコピーで始まる。
// 画面は、新しい会話の画面で最初に送信するときに呼ぶ(空の会話を作らないため)。
func (s *Service) NewConversation(ctx context.Context, profile uuid.UUID, settings *aibow.ConversationSettings) (aibow.Conversation, error) {
	if s.startupErr != nil {
		return aibow.Conversation{}, s.startupErr
	}
	// プロファイルにモデルが無い(初めての起動)と、会話の仕組みは「読み込んでいるモデル」を会話のモデルにする。
	// ここでは、画面が見せている読み込み中のモデルにする。aibow-server がほかの人のためにモデルを入れ替えている間は、
	// 読み込んでいるモデルが無い(前のモデルは解放済み)が、画面には前のモデルを見せているため(2026-10-22)。
	// モデルを降ろしている(0015 の 8-4)なら、最後に使ったモデルにし、送信の前に読み込む(0015 の 29)
	if settings != nil {
		if err := s.noNewAllow(nil, settings.ToolPolicies); err != nil {
			return aibow.Conversation{}, err
		}
	}
	if settings != nil && settings.Model == "" {
		model := s.lastModel()
		if i := s.host.info(); i != nil {
			model = i.ID
		}
		if model != "" {
			cp := *settings
			cp.Model = model
			settings = &cp
		}
	}
	return s.convs.CreateIn(ctx, profile, settings)
}

// lastModel は最後に読み込んだモデル(次の起動で読み込むモデル)。
func (s *Service) lastModel() string {
	s.appMu.Lock()
	defer s.appMu.Unlock()
	return s.app.Workspace.Model
}

// DeleteConversation は会話を消す。返事を生成中の会話は消せない。
func (s *Service) DeleteConversation(ctx context.Context, id uuid.UUID) error {
	if s.startupErr != nil {
		return s.startupErr
	}
	return s.convs.Delete(ctx, id)
}

// RenameConversation は会話のタイトルを変える。空にはできない。
func (s *Service) RenameConversation(ctx context.Context, id uuid.UUID, title string) error {
	if s.startupErr != nil {
		return s.startupErr
	}
	return s.convs.Rename(ctx, id, title)
}

// SetExcluded は、吹き出し1つ分の発言を「モデルに渡さない」にする / 戻す(0011)。msgID は吹き出しの中の発言のどれか。
// 返事なら、ツールの呼び出しと結果も一緒に変わる。生成中の会話は変えられない。次の送信から効く。
func (s *Service) SetExcluded(ctx context.Context, id, msgID uuid.UUID, excluded bool) error {
	if s.startupErr != nil {
		return s.startupErr
	}
	if err := s.convs.SetExcluded(ctx, id, msgID, excluded); err != nil {
		return err
	}
	log.Infof("会話 %s の発言 %s を、モデルに%s", id, msgID, map[bool]string{true: "渡さないにしました", false: "渡すに戻しました"}[excluded])
	return nil
}

// UpdateConversationSettings は会話の設定を変える。次の送信から効く。
func (s *Service) UpdateConversationSettings(ctx context.Context, id uuid.UUID, settings aibow.ConversationSettings) error {
	if s.startupErr != nil {
		return s.startupErr
	}
	if s.browser {
		c, _, err := s.convs.Get(ctx, id)
		if err != nil {
			return err
		}
		if err := s.noNewAllow(c.Settings.ToolPolicies, settings.ToolPolicies); err != nil {
			return err
		}
	}
	return s.convs.UpdateSettings(ctx, id, settings)
}

// noNewAllow は、ブラウザの利用者が、会話の設定でツールを「確認しない」にしていないかを確かめる(0024 の決定事項 3・18)。
// 確認しないで実行するかの既定は管理者が決める。利用者が「確認しない」にできるのは、確認のカードの「この会話では今後聞かない」だけ
// (そのツールの呼び出しを一度見てから決めることになる)。もう「確認しない」にしてあるツールは、そのまま残せる。
func (s *Service) noNewAllow(old, next map[string]config.ToolPolicy) error {
	if !s.browser {
		return nil
	}
	for name, p := range next {
		if p == config.ToolAllow && old[name] != config.ToolAllow {
			return fmt.Errorf("ツール %s を「確認しない」にできるのは、確認のカードの「この会話では今後聞かない」からだけです", name)
		}
	}
	return nil
}
