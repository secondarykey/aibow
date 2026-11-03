package aibow

// worktree.go: worktree で作業し、最後に差分を見て取り込む / 捨てる(0039)。
//
// 会話ごとに Git の worktree を作り、モデルにはそこを書き換えさせる。最後にユーザーが本体との差分を見て、取り込む / 捨てる。
// 役割の分け方:
//
//	アプリ … Git の操作(作る・差分を取る・本体に当てる・消す)を決まった手順で行う(git パッケージ)
//	モデル … 普通の作業フォルダとして読み書きするだけ。worktree だとは伝えない(プロンプトは1文字も増えない)
//
// 流れ:
//
//  1. 会話の設定の切り替え(ConversationSettings.Worktree)を入れる。この時点では何も作らない(読むだけなら作らない)
//  2. 最初にメインのフォルダを書き換えるツール(write_file・edit_file・run_command)を実行する前に、
//     本体の .aibow/worktrees/<短い ID> に worktree を作り、結び付けを workspaces.json に書く(ensureWorktree)
//  3. それからは、その会話のメインのフォルダのパスが worktree になる(toolFolders・workDir)。名前は変えない
//  4. 差分の画面で、作ったときのコミット(base)との差分を見て、選んだファイルを本体の作業ツリーに当てる(ApplyWorktree)。コミットはユーザーがする
//  5. 捨てる(DiscardWorktree)・会話を消す(Delete)と、worktree とブランチを消す
//
// 作るのは画面のアプリだけ(aibow-server の資料のフォルダでは作らない。0039 の決定事項 7)。noWorkDir のときは何もしない。

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"uuid"

	"github.com/secondarykey/aibow/git"
	"github.com/secondarykey/aibow/log"
	"github.com/secondarykey/aibow/tool"
)

// worktreeDir は、worktree を置く本体の中のフォルダ(Claude Code の .claude/worktrees と同じ形)。
// 本体の .git/info/exclude に足して、本体の git status に出ないようにする。隠しフォルダなので list_files も入らない(0037)
const worktreeDir = ".aibow"

// worktreeExclude は、本体の .git/info/exclude に足す行(リポジトリの直下の .aibow だけ)
const worktreeExclude = "/" + worktreeDir + "/"

// worktreeWrites は、worktree で作業する会話で「確認しない」を既定にするツール(書き換えは worktree に入るため)。
var worktreeWrites = map[string]bool{"write_file": true, "edit_file": true}

// worktreeTriggers は、実行する前に worktree を作るツール(メインのフォルダを書き換えうるもの)。
// run_command も入れる(コマンドはファイルを書き換えうる。worktree を作る前に本体で動かすと、本体が変わってしまうため)。
var worktreeTriggers = map[string]bool{"write_file": true, "edit_file": true, "run_command": true}

// WorktreeState は、会話の worktree の状態(入力欄の上の切り替えに出す)。
type WorktreeState struct {
	// Usable は、切り替えを入れられるか(ワークスペースのメインのフォルダが Git のリポジトリで、git が使える)。
	// 入れられなければ Reason に理由(画面は切り替えを灰色にして、理由を出す)
	Usable bool   `json:"usable"`
	Reason string `json:"reason,omitempty"`
	// Active は、worktree を作って作業しているか(作ったあとは切り替えられない。捨てると外れる)
	Active bool   `json:"active"`
	Path   string `json:"path,omitempty"`   // worktree のフォルダ
	Branch string `json:"branch,omitempty"` // worktree のブランチ
	Base   string `json:"base,omitempty"`   // 作ったときのコミット
}

// WorktreeChange は、差分の画面に出すファイル1つ。
type WorktreeChange struct {
	git.Change
	Applied bool `json:"applied"` // 本体の作業ツリーに同じ中身がある(取り込み済み)
}

// worktreeOf は、会話の設定 set の会話の worktree(この PC で作っていれば)。
func (cs *Conversations) worktreeOf(set ConversationSettings) (WorkspaceWorktree, bool) {
	if cs.noWorkDir || set.conv == (uuid.UUID{}) {
		return WorkspaceWorktree{}, false
	}
	wt, _, ok := cs.workspaces.Worktree(set.conv.String())
	return wt, ok
}

// mainPath は、会話のワークスペースの本体のメインのフォルダ(worktree に置き換える前のパス)。無ければ fallback。
func (cs *Conversations) mainPath(set ConversationSettings, fallback string) string {
	if cs.noWorkDir {
		return fallback
	}
	if ws, ok := cs.workspaces.Get(set.Workspace); ok {
		return ws.MainFolder().Path
	}
	return fallback
}

// worktreeUsable は、会話の設定 set で worktree を使えるか。使えなければ理由を返す。
func (cs *Conversations) worktreeUsable(set ConversationSettings) error {
	if cs.noWorkDir {
		return errors.New("worktree は、デスクトップのアプリでだけ使えます")
	}
	if set.Workspace == "" {
		return errors.New("ワークスペースを選ぶと使えます")
	}
	ws, ok := cs.workspaces.Get(set.Workspace)
	if !ok {
		return errors.New("この PC にはこのワークスペースがありません")
	}
	if !IsGitRepo(ws.MainFolder().Path) {
		return fmt.Errorf("メインのフォルダ %s が Git のリポジトリではありません(直下に .git がありません)", ws.MainFolder().Name)
	}
	return git.Available()
}

// worktreeMode は、会話の設定 set で、メインのフォルダの書き換えが worktree に入るか(作った、または切り替えを入れていて作れる)。
// ファイルを書き換えるツールの確認の既定を変える(defaultPolicy)。作れないのに切り替えだけ入っていれば、本体を直接書き換えるので確認する。
func (cs *Conversations) worktreeMode(set ConversationSettings) bool {
	if _, ok := cs.worktreeOf(set); ok {
		return true
	}
	return set.Worktree && cs.worktreeUsable(set) == nil
}

// WorktreeStateOf は、会話 id(新しい会話の画面なら空)の、設定 set での worktree の状態。
func (cs *Conversations) WorktreeStateOf(id uuid.UUID, set ConversationSettings) WorktreeState {
	set.conv = id
	if wt, ok := cs.worktreeOf(set); ok {
		return WorktreeState{Usable: true, Active: true, Path: wt.Path, Branch: wt.Branch, Base: wt.Base}
	}
	if err := cs.worktreeUsable(set); err != nil {
		return WorktreeState{Reason: err.Error()}
	}
	return WorktreeState{Usable: true}
}

// needsWorktree は、ツール t を実行する前に worktree を作るか。
// 切り替えを入れた会話で、まだ作っておらず、組み込みの書き換えうるツールが、メインのフォルダ(引数 folder を省いたか、メインの名前)を扱うとき。
// MCP のサーバーのツールでは作らない(どこを書き換えるかが分からないため。${workdir} のサーバーは、作ったあとは worktree で起動する)。
func (cs *Conversations) needsWorktree(set ConversationSettings, t tool.Tool, args tool.Args) bool {
	if !set.Worktree || !worktreeTriggers[t.Def().Name] {
		return false
	}
	if _, remote := t.(tool.Remote); remote {
		return false
	}
	if _, ok := cs.worktreeOf(set); ok {
		return false
	}
	ws, ok := cs.workspaces.Get(set.Workspace)
	if !ok {
		return false
	}
	folder := args.String("folder")
	return folder == "" || strings.EqualFold(folder, ws.MainFolder().Name)
}

// ensureWorktree は、会話の worktree を作り、作ったことのお知らせ(画面のカードに出す)を返す。
// 作れない状態(リポジトリでない・git が無い)なら何もしない(本体を直接書き換える。確認の既定も「確認する」のまま)。
// 作ろうとして失敗したら、エラーを返す(ツールは実行しない。切り替えを入れたのに本体を書き換えてしまわないように)。
func (cs *Conversations) ensureWorktree(ctx context.Context, set ConversationSettings, emit func(ChatEvent)) (string, error) {
	if cs.worktreeUsable(set) != nil {
		return "", nil
	}
	ws, _ := cs.workspaces.Get(set.Workspace)
	repo := ws.MainFolder().Path
	short, err := shortWorktreeID(ctx, repo, set.conv)
	if err != nil {
		return "", fmt.Errorf("worktree を作れないため、実行していません: %w", err)
	}
	path := filepath.Join(repo, worktreeDir, "worktrees", short)
	branch := "aibow/" + short
	// 本体の git status に出ないよう、先に exclude に足す(数える前に足すので、作ったものは数えない)
	if err := git.Exclude(ctx, repo, worktreeExclude); err != nil {
		return "", fmt.Errorf("worktree を作れないため、実行していません: %w", err)
	}
	uncommitted, err := git.Uncommitted(ctx, repo)
	if err != nil {
		return "", fmt.Errorf("worktree を作れないため、実行していません: %w", err)
	}
	base, err := git.Add(ctx, repo, path, branch)
	if err != nil {
		return "", fmt.Errorf("worktree を作れないため、実行していません: %w", err)
	}
	wt := WorkspaceWorktree{Conversation: set.conv.String(), Repo: repo, Path: path, Branch: branch, Base: base}
	if err := cs.workspaces.addWorktree(ws.ID, wt); err != nil {
		// 結び付けを書けなければ、作った worktree を消しておく(どの会話のものか分からないまま残さない)
		if rerr := git.Remove(context.WithoutCancel(ctx), repo, path, branch); rerr != nil {
			log.Warnf("worktree %s を片付けられません: %v", path, rerr)
		}
		return "", fmt.Errorf("worktree を作りましたが、結び付けを保存できないため消しました(実行していません): %w", err)
	}
	log.Noticef("会話 %s の worktree を作りました: %s(ブランチ %s・コミット %s から)", set.conv, path, branch, shortHash(base))
	note := worktreeNote(path, base, uncommitted)
	emit(ChatEvent{ConversationID: set.conv, Type: "worktree", Text: note})
	return note, nil
}

// worktreeNote は、worktree を作ったときのお知らせ(本体のコミットしていない変更と、Git に入っていないものは入らない。決定事項 9)。
func worktreeNote(path, base string, uncommitted int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "worktree を作りました(%s。コミット %s から)。", path, shortHash(base))
	if uncommitted > 0 {
		fmt.Fprintf(&b, "本体のコミットしていない変更 %d 件は worktree に入りません。", uncommitted)
	}
	b.WriteString("Git に入っていないもの(node_modules など)は worktree にはありません。")
	return b.String()
}

func shortHash(h string) string {
	if len(h) > 7 {
		return h[:7]
	}
	return h
}

// shortWorktreeID は、worktree のフォルダ名とブランチに使う短い ID(会話の ID の先頭 8 文字。Windows のパスの長さを抑えるため)。
// 会話の ID は UUIDv7 で、先頭は作った時刻なので、近い時刻に作った会話とは重なりうる。重なれば(フォルダかブランチがあれば)4 文字ずつ延ばす。
func shortWorktreeID(ctx context.Context, repo string, conv uuid.UUID) (string, error) {
	hex := strings.ReplaceAll(conv.String(), "-", "")
	for n := 8; n <= len(hex); n += 4 {
		id := hex[:n]
		if _, err := os.Stat(filepath.Join(repo, worktreeDir, "worktrees", id)); err == nil {
			continue
		}
		exists, err := git.BranchExists(ctx, repo, "aibow/"+id)
		if err != nil {
			return "", err
		}
		if !exists {
			return id, nil
		}
	}
	return "", errors.New("worktree の名前が、今あるフォルダかブランチと重なります")
}

// keepWorktree は、会話の設定を変えるときに、worktree の結び付けを守る(UpdateSettings)。
// 作ったあとは切り替えを外せない(捨てるのは差分の画面)・ワークスペースを変えられない(worktree はそのワークスペースのリポジトリのもの)。
func (cs *Conversations) keepWorktree(id uuid.UUID, stored ConversationSettings, s *ConversationSettings) error {
	stored.conv = id
	if _, ok := cs.worktreeOf(stored); !ok {
		return nil
	}
	if s.Workspace != stored.Workspace {
		return errors.New("この会話は worktree で作業しているので、ワークスペースを変えられません。差分の画面で取り込むか捨ててから変えてください")
	}
	s.Worktree = true
	return nil
}

// worktreeFor は、会話 id の worktree(作っていなければエラー)。
func (cs *Conversations) worktreeFor(ctx context.Context, id uuid.UUID) (WorkspaceWorktree, error) {
	if _, _, err := cs.store.Conversation(ctx, id); err != nil {
		return WorkspaceWorktree{}, err // ほかの人の会話は扱わない(保存先で確かめる)
	}
	wt, _, ok := cs.workspaces.Worktree(id.String())
	if !ok || cs.noWorkDir {
		return WorkspaceWorktree{}, errors.New("この会話は、この PC で worktree を作っていません")
	}
	return wt, nil
}

// WorktreeChanges は、会話 id の worktree で変わったファイル(作ったときのコミットとの差分。パスの順)と、取り込み済みかの印。
func (cs *Conversations) WorktreeChanges(ctx context.Context, id uuid.UUID) ([]WorktreeChange, error) {
	wt, err := cs.worktreeFor(ctx, id)
	if err != nil {
		return nil, err
	}
	changes, err := git.Changes(ctx, wt.Path, wt.Base)
	if err != nil {
		return nil, err
	}
	applied, err := git.Applied(ctx, wt.Repo, changes)
	if err != nil {
		return nil, err
	}
	out := make([]WorktreeChange, len(changes))
	for i, c := range changes {
		out[i] = WorktreeChange{Change: c, Applied: applied[i]}
	}
	return out, nil
}

// WorktreeDiff は、会話 id の worktree の、ファイル path の差分の文(画面に出す)。
func (cs *Conversations) WorktreeDiff(ctx context.Context, id uuid.UUID, path string) (string, error) {
	wt, err := cs.worktreeFor(ctx, id)
	if err != nil {
		return "", err
	}
	return git.Diff(ctx, wt.Path, wt.Base, false, path)
}

// ApplyWorktree は、会話 id の worktree の、選んだファイル paths の差分を、本体の作業ツリーに当てる(コミットはユーザーがする)。
// 全部当たるか、何も変えないか(git apply)。当たらなければ、当たらないファイルを書いたエラーを返す。worktree は残す(会話を続けられるように)。
func (cs *Conversations) ApplyWorktree(ctx context.Context, id uuid.UUID, paths []string) error {
	if len(paths) == 0 {
		return errors.New("取り込むファイルを選んでください")
	}
	wt, err := cs.worktreeFor(ctx, id)
	if err != nil {
		return err
	}
	patch, err := git.Diff(ctx, wt.Path, wt.Base, true, paths...)
	if err != nil {
		return err
	}
	if err := git.Apply(ctx, wt.Repo, []byte(patch)); err != nil {
		return err
	}
	log.Noticef("会話 %s の worktree から、%d 個のファイルを本体 %s に取り込みました: %s", id, len(paths), wt.Repo, strings.Join(paths, ", "))
	return nil
}

// DiscardWorktree は、会話 id の worktree とブランチを消し、会話を本体のフォルダに戻す(切り替えも外す)。
// 生成中は消さない(モデルが書いている途中のフォルダを消さないため)。
func (cs *Conversations) DiscardWorktree(ctx context.Context, id uuid.UUID) error {
	if g := cs.generating.Load(); g != nil && *g == id {
		return errors.New("返事を生成中の会話の worktree は捨てられません。停止してから捨ててください")
	}
	wt, err := cs.worktreeFor(ctx, id)
	if err != nil {
		return err
	}
	if err := git.Remove(ctx, wt.Repo, wt.Path, wt.Branch); err != nil {
		return err
	}
	if err := cs.workspaces.removeWorktree(id.String()); err != nil {
		return fmt.Errorf("worktree は消しましたが、結び付けを消せません: %w", err)
	}
	log.Noticef("会話 %s の worktree を捨てました: %s(ブランチ %s)", id, wt.Path, wt.Branch)
	c, _, err := cs.store.Conversation(ctx, id)
	if err != nil {
		return err
	}
	c.Settings.Worktree = false
	return cs.store.UpdateConversation(ctx, c)
}

// removeWorktreeOf は、消した会話 id の worktree とブランチを消す(会話を消したとき)。
// この PC に結び付けが無ければ何もしない(会話をサーバーに置いていて、ほかの PC で作った会話など)。
// 取り込んでいない差分があるかは、画面が消す前に確かめる。会話はもう消えているので、失敗はログに残すだけ。
func (cs *Conversations) removeWorktreeOf(ctx context.Context, id uuid.UUID) {
	if cs.noWorkDir {
		return
	}
	wt, _, ok := cs.workspaces.Worktree(id.String())
	if !ok {
		return
	}
	if err := git.Remove(ctx, wt.Repo, wt.Path, wt.Branch); err != nil {
		log.Warnf("消した会話 %s の worktree %s を消せません(手で git worktree remove してください): %v", id, wt.Path, err)
		return
	}
	if err := cs.workspaces.removeWorktree(id.String()); err != nil {
		log.Warnf("消した会話 %s の worktree の結び付けを消せません: %v", id, err)
		return
	}
	log.Noticef("消した会話 %s の worktree を消しました: %s(ブランチ %s)", id, wt.Path, wt.Branch)
}
