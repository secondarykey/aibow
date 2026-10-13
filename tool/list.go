package tool

// list.go: 作業フォルダの中のファイルを並べるツール(list_files)。
//
// モデルはフォルダの中を見られないので、まず「何があるか」を知る手段が要る。
// 下のフォルダまでたどって一度に返す(1階層ずつだと、呼び出しの回数がかさむため)。
// その代わり大きさに上限を付ける(ツールの結果はそのままプロンプトに入り、会話の上限を使うため)。
//
// 上限は件数と文字数の両方。件数だけだと、深い場所のファイルはパスが長く、500 件でも 4 万文字を超えることがある
// (aibow のリポジトリで試したら、.claude/ の worktree だけで 500 件・約 43,000 文字になった)。
//
// 並べる順は「浅い順」(作業フォルダの直下を全部 → その1つ下を全部 → …)。
// 上限で切ったときに、全体の形(直下に何があるか)が必ず残るようにするため。
// フォルダを1つずつ奥までたどる順だと、最初の大きなフォルダで上限に達し、その後ろが見えなくなる。
//
// 隠しフォルダ(名前が . で始まる。.claude・.vscode など)は、名前だけ出して中に入らない。
// 道具の設定や作業用のコピーが入っていることが多く、たどると一覧がそれで埋まってしまうため。
// 中を見たいときは path に指定すれば並べる。

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"unicode/utf8"
)

const (
	listMax      = 500  // list_files が返す件数の上限
	listMaxRunes = 8000 // list_files が返す文字数の上限(read_file と同じ)
)

// listSkip は、名前も出さずに飛ばすフォルダ。中身が多いうえ、モデルが読む必要がほぼ無い
var listSkip = map[string]bool{".git": true, "node_modules": true}

type listFiles struct {
	dir string // 作業フォルダ(絶対パス)
}

func (listFiles) Def() Def {
	return Def{
		Name: "list_files",
		Description: "List files and folders in the working directory recursively, shallower ones first. Folder names end with /. " +
			"Skips .git and node_modules, and does not go into hidden folders (names starting with .) unless you pass one as path.",
		Parameters: json.RawMessage(`{"type": "object", "properties": {"path": {"type": "string", ` +
			`"description": "Folder to list, relative to the working directory. Omit to list the whole working directory."}}, "required": []}`),
	}
}

func (t listFiles) Run(ctx context.Context, args Args) (string, error) {
	p, err := localPath(t.dir, args.String("path"))
	if err != nil {
		return "", err
	}
	root, err := openRoot(t.dir)
	if err != nil {
		return "", err
	}
	defer root.Close()

	if st, err := root.Stat(p); err != nil {
		return "", openError(p, err)
	} else if !st.IsDir() {
		return "", fmt.Errorf("%s はフォルダではありません。ファイルの中身は read_file で読めます", p)
	}

	l := lister{}
	// 浅い順にたどる(幅優先)。queue は次に中を並べるフォルダ
	queue := []string{p}
	for len(queue) > 0 && !l.full {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		dir := queue[0]
		queue = queue[1:]
		// fs.ReadDir は名前の順に返す。シンボリックリンクのフォルダはフォルダとして扱わない
		// (外を指していても一覧に名前が出るだけ。開くときに os.Root が止める)
		entries, err := fs.ReadDir(root.FS(), dir)
		if err != nil {
			if dir == p {
				return "", fmt.Errorf("%s の中を読めません: %w", p, err)
			}
			continue // 下のフォルダが読めない(権限など)ときは、名前だけ出ている状態で先へ進む
		}
		for _, d := range entries {
			if sub, ok := l.add(path.Join(dir, d.Name()), d); ok {
				queue = append(queue, sub)
			}
			if l.full {
				break
			}
		}
	}
	return l.result(p), nil
}

// lister は list_files の結果を、上限を数えながら組み立てる。
type lister struct {
	b      strings.Builder
	count  int  // 並べた件数
	runes  int  // 並べた文字数
	full   bool // 上限に達して切った
	hidden bool // 中を省略した隠しフォルダがある
}

// add は1件を並べる。中もたどるフォルダなら、そのパスと true を返す。
func (l *lister) add(name string, d fs.DirEntry) (string, bool) {
	if d.IsDir() && listSkip[d.Name()] {
		return "", false
	}
	hiddenDir := d.IsDir() && strings.HasPrefix(d.Name(), ".")
	line := name
	switch {
	case hiddenDir:
		line += "/ (中は省略)"
	case d.IsDir():
		line += "/"
	}
	n := utf8.RuneCountInString(line) + 1 // 改行の分
	if l.count == listMax || l.runes+n > listMaxRunes {
		l.full = true
		return "", false
	}
	l.b.WriteString(line + "\n")
	l.count, l.runes = l.count+1, l.runes+n
	if hiddenDir {
		l.hidden = true
		return "", false
	}
	return name, d.IsDir()
}

// result は並べた結果に、省略と切ったことの説明を添えて返す。
func (l *lister) result(p string) string {
	if l.count == 0 {
		if p == "." {
			return "(作業フォルダは空です)"
		}
		return fmt.Sprintf("(%s は空のフォルダです)", p)
	}
	if l.hidden {
		l.b.WriteString("(「中は省略」は隠しフォルダです。中を見るには path に指定してください)\n")
	}
	if l.full {
		fmt.Fprintf(&l.b, "(%d 件か %d 文字を超えるため、ここで切りました。path に下のフォルダを指定すると、その中だけを並べられます)\n", listMax, listMaxRunes)
	}
	return strings.TrimSuffix(l.b.String(), "\n")
}
