package ui

// worktree.go: 入力欄の上の「worktree で作業する」の切り替えと、差分の画面から呼ぶメソッド(0039)。
//
// 中身は aibow の Conversations(worktree.go)にあり、ここは画面との中継だけ。worktree は PC のフォルダなので、デスクトップのアプリだけ。
// ブラウザ(aibow-server)の画面用の口には載せない(web.go の browserMethods に入れない)。口の作りを間違えても使えないよう、ここでも断る。

import (
	"context"
	"uuid"

	"github.com/secondarykey/aibow"
)

// worktreeReady は、worktree を使えるか(使えなければエラー)。
func (s *Service) worktreeReady() error {
	if s.startupErr != nil {
		return s.startupErr
	}
	if s.browser {
		return errNoWorkspaces
	}
	return nil
}

// WorktreeState は、会話 id(新しい会話の画面なら空の ID)の、設定 settings での worktree の状態。
// 画面がまだ保存していない設定(選んだばかりのワークスペース)でも見られるよう、設定を受け取る。
func (s *Service) WorktreeState(ctx context.Context, id uuid.UUID, settings aibow.ConversationSettings) aibow.WorktreeState {
	if err := s.worktreeReady(); err != nil {
		return aibow.WorktreeState{Reason: err.Error()}
	}
	return s.convs.WorktreeStateOf(ctx, id, settings)
}

// WorktreeChanges は、会話 id の worktree で変わったファイル(差分の画面の一覧)。
func (s *Service) WorktreeChanges(ctx context.Context, id uuid.UUID) ([]aibow.WorktreeChange, error) {
	if err := s.worktreeReady(); err != nil {
		return nil, err
	}
	list, err := s.convs.WorktreeChanges(ctx, id)
	if list == nil {
		list = []aibow.WorktreeChange{} // 画面の型を「配列」だけにする(null にしない)
	}
	return list, err
}

// WorktreeDiff は、会話 id の worktree の、ファイル path の差分の文(差分の画面で選んだファイル)。
func (s *Service) WorktreeDiff(ctx context.Context, id uuid.UUID, path string) (string, error) {
	if err := s.worktreeReady(); err != nil {
		return "", err
	}
	return s.convs.WorktreeDiff(ctx, id, path)
}

// ApplyWorktree は、会話 id の worktree の、選んだファイル paths の差分を本体の作業ツリーに当てる。
func (s *Service) ApplyWorktree(ctx context.Context, id uuid.UUID, paths []string) error {
	if err := s.worktreeReady(); err != nil {
		return err
	}
	return s.convs.ApplyWorktree(ctx, id, paths)
}

// DiscardWorktree は、会話 id の worktree とブランチを消し、会話を本体のフォルダに戻す。
func (s *Service) DiscardWorktree(ctx context.Context, id uuid.UUID) error {
	if err := s.worktreeReady(); err != nil {
		return err
	}
	return s.convs.DiscardWorktree(ctx, id)
}
