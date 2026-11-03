package aibow

// worktreename.go: worktree の名前(ブランチとフォルダの名前に使う英語の短い名前。0039)。
//
// ブランチ名は aibow/<名前>-<会話の ID の末尾 4 文字>(名前が無ければ aibow/<末尾 8 文字>)。
// 名前は、その会話の最初の送信で、返事を書かせる前にモデルに1回だけ短く書かせ、会話の設定に残す(2026-11-04、ユーザーと決めた)。
//
//   - なぜ最初の送信か:作業領域(KV キャッシュ)は1つなので、会話の途中で別の頼みを挟むと、その会話のキャッシュが崩れ、
//     次の生成でプロンプトを丸ごと読み直すことになる。最初の送信なら、その会話のキャッシュはまだほとんど無い
//   - なぜ英語か:ブランチ名にマルチバイトを使わない(ターミナルで打ちにくい)。会話のタイトル(日本語)は使わない
//   - 作るのは、ワークスペースのメインのフォルダが Git のリポジトリの会話だけ(切り替えを入れていなくても作る。あとで入れたときのため)
//   - 会話の設定の画面で見られ、worktree を作る前なら直せる(SetWorktreeName)

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
	"uuid"

	"github.com/secondarykey/aibow/engine"
	"github.com/secondarykey/aibow/format"
	"github.com/secondarykey/aibow/genlog"
	"github.com/secondarykey/aibow/log"
)

// worktreeNameMax は、worktree の名前の長さの上限(文字数)。Windows のパスの長さを抑えるため
const worktreeNameMax = 32

// worktreeNamePattern は、worktree の名前に使える形(英小文字・数字を - でつなぐ)。Git のブランチ名にもフォルダ名にもそのまま使える
var worktreeNamePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// checkWorktreeName は、worktree の名前を確かめる(空はよい。名前なしで末尾の ID だけにする)。
func checkWorktreeName(name string) error {
	if name == "" {
		return nil
	}
	if utf8.RuneCountInString(name) > worktreeNameMax || !worktreeNamePattern.MatchString(name) {
		return fmt.Errorf("worktree の名前 %q は使えません(英小文字・数字を - でつないだ %d 文字までの名前にしてください。例: fix-login-error)", name, worktreeNameMax)
	}
	return nil
}

// slugify は、モデルが書いた名前を、worktree の名前の形に整える(英小文字・数字のほかは - にし、続く - を1つにする)。
// 整えて何も残らなければ空。長すぎれば語の切れ目で切る。
func slugify(s string) string {
	s = strings.ToLower(firstLine(strings.TrimSpace(s)))
	var b strings.Builder
	dash := false
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	for len(out) > worktreeNameMax {
		i := strings.LastIndexByte(out, '-')
		if i < 0 {
			out = out[:worktreeNameMax]
			break
		}
		out = out[:i]
	}
	return out
}

// worktreeNamePrompt は、名前を書かせる頼み。返事は名前だけにさせる(読み取りやすいように)。
// 頼みの文は英語(ツールの説明・環境の説明と同じ。小さいモデルが従いやすい)。
func worktreeNamePrompt(task string) []format.Message {
	if r := []rune(task); len(r) > 500 {
		task = string(r[:500]) // 名前を決めるには頭だけで足りる。プロンプトを短く保つ
	}
	return []format.Message{{Role: "user", Content: "Name the following task as a short English git branch name: " +
		"2 to 4 lowercase words joined by hyphens (for example: fix-login-error). Reply with the name only.\n\nTask:\n" + task}}
}

// nameWorktree は、会話の最初の送信で、worktree の名前をモデルに書かせて会話の設定に残す(作れる会話で、まだ名前が無いときだけ)。
// 失敗しても送信は止めない(名前なしで、末尾の ID だけのブランチ名になる)。ログに残すだけ。
func (cs *Conversations) nameWorktree(ctx context.Context, conv Conversation, saved []StoredMessage, text string) {
	if text == "" || conv.Settings.WorktreeName != "" || slices.ContainsFunc(saved, func(m StoredMessage) bool { return m.Role == "user" }) {
		return
	}
	if cs.worktreeUsable(conv.Settings) != nil {
		return
	}
	// 使用量の記録(aibow-server につないでいるとき)では、返事に数える(短い生成なので、用途を増やさない)
	gctx := genlog.WithPurpose(ctx, genlog.PurposeReply, conv.ID)
	out, st, err := cs.gen.Generate(gctx, format.Prompt{Messages: worktreeNamePrompt(text), Think: false},
		engine.GenerateOptions{Temperature: 0, MaxTokens: 24}, func(string) {}, func(string) {}, func(string) {})
	if err != nil {
		if ctx.Err() == nil {
			log.Warnf("会話 %s の worktree の名前を作れません(名前なしで続けます): %v", conv.ID, err)
		}
		return
	}
	name := slugify(out.Content)
	log.Infof("会話 %s の worktree の名前: %q(モデルが書いたもの %q・%d トークン・%.2f 秒)", conv.ID, name, strings.TrimSpace(out.Content), st.GenTokens, st.PromptSec+st.GenSec)
	if name == "" {
		return
	}
	if err := cs.saveWorktreeName(context.WithoutCancel(ctx), conv.ID, name, false); err != nil {
		log.Warnf("会話 %s の worktree の名前を保存できません: %v", conv.ID, err)
	}
}

// SetWorktreeName は、会話 id の worktree の名前を変える(会話の設定の画面)。空にすると名前なし(末尾の ID だけ)。
// 作ったあとは変えられない(ブランチとフォルダはもうできている)。
// 会話の設定を丸ごと書き戻す UpdateSettings では変えない(画面が持っている古い設定で、最初の送信で作った名前を消さないように)。
func (cs *Conversations) SetWorktreeName(ctx context.Context, id uuid.UUID, name string) error {
	name = strings.TrimSpace(name)
	if err := checkWorktreeName(name); err != nil {
		return err
	}
	if _, _, ok := cs.workspaces.Worktree(id.String()); ok {
		return errors.New("この会話の worktree はもう作ってあるので、名前は変えられません(捨ててから変えてください)")
	}
	return cs.saveWorktreeName(ctx, id, name, true)
}

// saveWorktreeName は、会話の設定の worktree の名前だけを書き換えて保存する。overwrite でなければ、すでに名前があるときは変えない。
func (cs *Conversations) saveWorktreeName(ctx context.Context, id uuid.UUID, name string, overwrite bool) error {
	c, _, err := cs.store.Conversation(ctx, id)
	if err != nil {
		return err
	}
	if !overwrite && c.Settings.WorktreeName != "" {
		return nil
	}
	c.Settings.WorktreeName = name
	return cs.store.UpdateConversation(ctx, c) // 更新日時は変えない(名前を変えただけで一覧の並びが変わらないように)
}
