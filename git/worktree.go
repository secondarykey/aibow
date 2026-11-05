package git

// worktree.go: worktree を作る・本体の状態を見る・差分を取る・本体に当てる・消す(0039)。
//
// worktree は、1つのリポジトリ(.git のオブジェクト)を分け合ったまま、別のフォルダに別のブランチを取り出す仕組み。
// 作るときのコミットから始まるので、本体のコミットしていない変更と、Git に入っていないもの(node_modules など)は入らない。
//
// 取り込みは「差分をパッチにして、本体の作業ツリーに当てるだけ」(git apply。--3way は付けない。0039 の決定事項 4):
//
//   - git apply は、全部当たるか、何も変えないかのどちらか。本体の作業ツリーだけを変え、索引(ステージ)には触れない
//   - 本体にコミットしていない変更があっても、別の行なら当たる。同じ行が変わっていれば、何も変えずに失敗する
//   - --3way は本体の索引を変え、コミットしていない変更があると一部だけ当たることがあった(仕様に書いた試し)。ユーザーの手に残す、と合わない

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// 変わり方(Change.Status)
const (
	Added    = "added"
	Modified = "modified"
	Deleted  = "deleted"
)

// Change は、worktree で変わったファイル1つ。
type Change struct {
	Path   string `json:"path"`   // リポジトリの中の相対パス(/ 区切り。git の書き方のまま)
	Status string `json:"status"` // added / modified / deleted
	// Blob は、worktree の中身のハッシュ(索引に入れたもの)。消したファイルなら空。取り込み済みかを比べる(Applied)
	Blob string `json:"-"`
}

// Add は、リポジトリ repo の HEAD から、フォルダ path に新しいブランチ branch の worktree を作る。
// 作ったときの元(コミット・そのとき本体がいたブランチ・コミットの件名)を返す。
// 差分はいつもこのコミット(base)と比べる(本体の HEAD があとで進んでも、worktree で変えたものだけを取り出すため)。
func Add(ctx context.Context, repo, path, branch string) (Ref, error) {
	if _, err := run(ctx, repo, nil, "rev-parse", "--verify", "--quiet", "HEAD^{commit}"); err != nil {
		return Ref{}, fmt.Errorf("まだコミットが無いリポジトリでは worktree を作れません(最初のコミットをしてから使ってください): %w", err)
	}
	base, err := Head(ctx, repo)
	if err != nil {
		return Ref{}, err
	}
	if _, err := run(ctx, repo, nil, "worktree", "add", "-b", branch, path, base.Commit); err != nil {
		return Ref{}, err
	}
	return base, nil
}

// Ref は、本体で今チェックアウトしているもの(worktree を作るときの元。画面に出す)。
type Ref struct {
	Branch  string // ブランチの名前。ブランチから外れている(detached HEAD)ときは空
	Commit  string // コミットのハッシュ
	Subject string // コミットのメッセージの1行目(件名)
}

// Head は、リポジトリ repo で今チェックアウトしているブランチとコミット。まだコミットが無ければエラー。
func Head(ctx context.Context, repo string) (Ref, error) {
	out, err := run(ctx, repo, nil, "log", "-1", "--format=%H%x00%s", "HEAD")
	if err != nil {
		return Ref{}, err
	}
	hash, subject, ok := strings.Cut(strings.TrimRight(out, "\r\n"), "\x00")
	if !ok || hash == "" {
		return Ref{}, fmt.Errorf("git log の出力を読めません: %q", out)
	}
	branch, err := run(ctx, repo, nil, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return Ref{}, err
	}
	r := Ref{Branch: strings.TrimSpace(branch), Commit: hash, Subject: subject}
	if r.Branch == "HEAD" {
		r.Branch = "" // ブランチから外れているときは、--abbrev-ref が HEAD と答える
	}
	return r, nil
}

// Exclude は、リポジトリ repo の .git/info/exclude に pattern の行を足す(すでにあれば何もしない)。
// .gitignore と違い Git に入っていないファイルなので、リポジトリのファイルを書き換えずに、本体の git status から外せる。
// 場所は git に聞く(rev-parse --git-path。repo 自身が worktree でも、本体の .git の中を指す)。
func Exclude(ctx context.Context, repo, pattern string) error {
	out, err := run(ctx, repo, nil, "rev-parse", "--git-path", "info/exclude")
	if err != nil {
		return err
	}
	path := strings.TrimSpace(out)
	if !filepath.IsAbs(path) {
		path = filepath.Join(repo, path)
	}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%s を読めません: %w", path, err)
	}
	for line := range strings.Lines(string(data)) {
		if strings.TrimSpace(line) == pattern {
			return nil
		}
	}
	if len(data) > 0 && !bytes.HasSuffix(data, []byte("\n")) {
		data = append(data, '\n')
	}
	data = append(data, []byte("# aibow の worktree(アプリが足した行)\n"+pattern+"\n")...)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("%s に書けません: %w", path, err)
	}
	return nil
}

// Uncommitted は、リポジトリ repo のコミットしていない変更の数(Git に入っていないファイルも数える。無視しているものは数えない)。
// worktree を作るときに「入りません」と知らせるため。
func Uncommitted(ctx context.Context, repo string) (int, error) {
	out, err := run(ctx, repo, nil, "status", "--porcelain=v1", "-z", "--no-renames")
	if err != nil {
		return 0, err
	}
	n := 0
	for rec := range strings.SplitSeq(out, "\x00") {
		if rec != "" {
			n++
		}
	}
	return n, nil
}

// BranchExists は、リポジトリ repo にブランチ branch があるか。
func BranchExists(ctx context.Context, repo, branch string) (bool, error) {
	_, err := run(ctx, repo, nil, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	var ge *Error
	if errors.As(err, &ge) && ge.Stderr == "" {
		return false, nil // --quiet で無いときは、何も言わずに 1 で終わる
	}
	return err == nil, err
}

// Changes は、worktree wt の今の中身と base との違いを、ファイルごとに返す(パスの順)。
//
// 新しいファイル・消したファイルも含めるため、worktree の中のものを全部ステージしてから(add -A)、索引と base を比べる。
// worktree の索引はアプリのもの(モデルには Git の操作をさせない)なので、ステージしてかまわない。
// 名前を変えたファイルは、消したものと足したものの2つにする(--no-renames。ファイルごとに選んで取り込めるように)。
func Changes(ctx context.Context, wt, base string) ([]Change, error) {
	if _, err := run(ctx, wt, nil, "add", "-A"); err != nil {
		return nil, err
	}
	out, err := run(ctx, wt, nil, "diff", "--cached", "--raw", "-z", "--no-renames", "--no-abbrev", base)
	if err != nil {
		return nil, err
	}
	return parseRaw(out)
}

// parseRaw は、git diff --raw -z の出力を読む。1件は「:元のモード 先のモード 元のハッシュ 先のハッシュ 変わり方\0パス\0」。
func parseRaw(out string) ([]Change, error) {
	var list []Change
	fields := strings.Split(out, "\x00")
	for i := 0; i+1 < len(fields); i += 2 {
		meta, path := fields[i], fields[i+1]
		if meta == "" {
			break
		}
		f := strings.Fields(strings.TrimPrefix(meta, ":"))
		if len(f) < 5 {
			return nil, fmt.Errorf("git diff --raw の出力を読めません: %q", meta)
		}
		c := Change{Path: path, Blob: f[3]}
		switch f[4][:1] {
		case "A":
			c.Status = Added
		case "D":
			c.Status, c.Blob = Deleted, ""
		default: // M(中身)・T(種類。ファイル ⇄ リンク)
			c.Status = Modified
		}
		list = append(list, c)
	}
	slices.SortFunc(list, func(a, b Change) int { return strings.Compare(a.Path, b.Path) })
	return list, nil
}

// Diff は、worktree wt の base との差分の文。paths を渡せばそのファイルだけ(空なら全部)。
// binary なら、バイナリのファイルも当てられる形(--binary)にする(本体に当てるとき)。画面に出すときは付けない
// (付けると、画像などが読めない文字の塊になるため。付けなければ「Binary files … differ」の1行)。
func Diff(ctx context.Context, wt, base string, binary bool, paths ...string) (string, error) {
	if _, err := run(ctx, wt, nil, "add", "-A"); err != nil {
		return "", err
	}
	args := []string{"diff", "--cached", "--no-renames"}
	if binary {
		args = append(args, "--binary")
	}
	args = append(args, base, "--")
	args = append(args, paths...)
	return run(ctx, wt, nil, args...)
}

// Applied は、changes のそれぞれが、リポジトリ repo の作業ツリーにすでに入っているか(取り込み済みか)。wt・base は worktree とそのコミット。
//
// 覚えておくのではなく、そのつど本体を見て決める(取り込んだあとに本体で書き換えれば、取り込み済みでなくなる):
//
//   - 消したファイルは、本体に無いか
//   - 足した・変えたファイルは、まず本体のファイルのハッシュが worktree のもの(Blob)と同じか。ハッシュは git に計算させる
//     (hash-object。改行の変換などの決まりを当ててから計算するので、Windows の改行(core.autocrlf)でも、同じ中身なら同じになる)
//   - 変えたファイルで中身が違えば、そのファイルの差分を本体に逆向きに当てられるか(apply --check -R。何も変えずに確かめるだけ)。
//     本体の同じファイルの別の行にコミットしていない変更があっても、取り込んだ変更が入っていれば当てられる
//     (中身の比べ方だけだと、そのとき取り込み済みにならなかった。2026-11-04、実際の画面で試して分かった)
func Applied(ctx context.Context, repo, wt, base string, changes []Change) ([]bool, error) {
	done := make([]bool, len(changes))
	var paths []string
	var idx []int
	for i, c := range changes {
		_, err := os.Stat(filepath.Join(repo, filepath.FromSlash(c.Path)))
		exists := err == nil
		if c.Status == Deleted {
			done[i] = !exists
			continue
		}
		if exists {
			paths = append(paths, c.Path)
			idx = append(idx, i)
		}
	}
	if len(paths) == 0 {
		return done, nil
	}
	out, err := run(ctx, repo, []byte(strings.Join(paths, "\n")+"\n"), "hash-object", "--stdin-paths")
	if err != nil {
		return nil, err
	}
	hashes := strings.Fields(out)
	if len(hashes) != len(paths) {
		return nil, fmt.Errorf("git hash-object の出力の数が合いません(%d 件に %d 行)", len(paths), len(hashes))
	}
	var differ []string // 中身の違う、変えたファイル(逆向きに当てて確かめる)
	for k, i := range idx {
		done[i] = hashes[k] == changes[i].Blob
		if !done[i] && changes[i].Status == Modified {
			differ = append(differ, changes[i].Path)
		}
	}
	if len(differ) == 0 {
		return done, nil
	}
	patch, err := Diff(ctx, wt, base, true, differ...)
	if err != nil {
		return nil, err
	}
	sections := splitPatch(patch)
	for i, c := range changes {
		sec, ok := sections[c.Path]
		if !ok || done[i] || c.Status != Modified {
			continue
		}
		if _, err := run(ctx, repo, []byte(sec), "apply", "--check", "-R", "-"); err == nil {
			done[i] = true
		}
	}
	return done, nil
}

// splitPatch は、git diff の出力をファイルごとに分ける(パス → そのファイルの差分)。
// 名前を変えたものは無い(--no-renames)ので、見出し「diff --git a/X b/X」の X は前と後ろで同じ。
// 引用符で囲まれたパス(改行などを含む)は分けない(取り込み済みかを、中身の比べ方だけで決める)。
func splitPatch(patch string) map[string]string {
	out := map[string]string{}
	const head = "diff --git a/"
	var path string
	var b strings.Builder
	flush := func() {
		if path != "" {
			out[path] = b.String()
		}
		path = ""
		b.Reset()
	}
	for line := range strings.Lines(patch) {
		if strings.HasPrefix(line, head) {
			flush()
			rest := strings.TrimRight(strings.TrimPrefix(line, head), "\r\n")
			if n := len(rest) - len(" b/"); n > 0 && n%2 == 0 && rest[n/2:n/2+3] == " b/" {
				path = rest[:n/2]
			}
		}
		if path != "" {
			b.WriteString(line)
		}
	}
	flush()
	return out
}

// ApplyError は、パッチが当たらなかったときのエラー。何も変えていない(git apply は全部当たるか、何も変えないか)。
type ApplyError struct {
	Files  []string // 当たらなかったファイル(git のエラーの文から読み取ったもの。読み取れなければ空)
	Output string   // git のエラーの文
}

func (e *ApplyError) Error() string {
	if len(e.Files) == 0 {
		return "差分を本体に当てられませんでした(何も変えていません): " + strings.TrimSpace(e.Output)
	}
	return "次のファイルの差分が本体に当たらないため、何も変えていません(本体の同じところが変わっています): " + strings.Join(e.Files, "、")
}

// failedFile は、git apply のエラーの文から、当たらなかったファイルを読み取る。
//
//	error: patch failed: main.go:12
//	error: main.go: patch does not apply
//	error: new.txt: already exists in working directory
//	error: gone.txt: No such file or directory
var failedFile = []*regexp.Regexp{
	regexp.MustCompile(`^error: patch failed: (.+):\d+$`),
	regexp.MustCompile(`^error: (.+): (?:patch does not apply|already exists in working directory|No such file or directory|does not exist in index|does not match index)$`),
}

// Apply は、パッチ patch を、リポジトリ repo の作業ツリーに当てる(索引には触れない)。
// 当たらなければ何も変えずに *ApplyError を返す。
func Apply(ctx context.Context, repo string, patch []byte) error {
	if len(bytes.TrimSpace(patch)) == 0 {
		return nil
	}
	_, err := run(ctx, repo, patch, "apply", "-")
	var ge *Error
	if !errors.As(err, &ge) {
		return err
	}
	ae := &ApplyError{Output: ge.Stderr}
	for line := range strings.Lines(ge.Stderr) {
		line = strings.TrimSpace(line)
		for _, re := range failedFile {
			if m := re.FindStringSubmatch(line); m != nil && !slices.Contains(ae.Files, m[1]) {
				ae.Files = append(ae.Files, m[1])
				break
			}
		}
	}
	return ae
}

// Remove は、リポジトリ repo の worktree(フォルダ path)と、そのブランチ branch を消す。
// worktree の中の変更は捨てる(--force)。フォルダがもう無い(手で消した)ときは、Git の記録だけを片付ける(worktree prune)。
// ブランチがもう無ければ、そのまま。
func Remove(ctx context.Context, repo, path, branch string) error {
	if _, err := run(ctx, repo, nil, "worktree", "remove", "--force", path); err != nil {
		if _, serr := os.Stat(path); !errors.Is(serr, os.ErrNotExist) {
			return err
		}
		if _, err := run(ctx, repo, nil, "worktree", "prune"); err != nil {
			return err
		}
	}
	if ok, err := BranchExists(ctx, repo, branch); err != nil || !ok {
		return err
	}
	_, err := run(ctx, repo, nil, "branch", "-D", branch)
	return err
}
