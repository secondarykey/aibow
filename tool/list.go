package tool

// list.go: 作業フォルダの中のファイルを並べるツール(list_files)。
//
// モデルはフォルダの中を見られないので、まず「何があるか」を知る手段が要る。
// 下のフォルダまでたどって一度に返す(1階層ずつだと、呼び出しの回数がかさむため)。
// その代わり件数に上限を付ける(ツールの結果はそのままプロンプトに入り、会話の上限を使うため)。

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
)

const (
	// listMax は list_files が返す件数の上限
	listMax = 500
)

// listSkip は、たどらないフォルダ。中身が多いうえ、モデルが読む必要がほぼ無い
var listSkip = map[string]bool{".git": true, "node_modules": true}

type listFiles struct {
	dir string // 作業フォルダ(絶対パス)
}

func (listFiles) Def() Def {
	return Def{
		Name:        "list_files",
		Description: "List files and folders in the working directory recursively. Folder names end with /. Skips .git and node_modules.",
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

	// fs.WalkDir はフォルダごとに名前の順でたどるので、結果も名前の順になる。
	// シンボリックリンクのフォルダはたどらない(外を指していても一覧に名前が出るだけ。開くときに os.Root が止める)
	var lines []string
	full := false
	err = fs.WalkDir(root.FS(), p, func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if name == p {
			return nil // 指定したフォルダそのものは並べない
		}
		if d.IsDir() && listSkip[d.Name()] {
			return fs.SkipDir
		}
		if len(lines) == listMax {
			full = true
			return fs.SkipAll
		}
		if d.IsDir() {
			name += "/"
		}
		lines = append(lines, name)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("%s の中をたどれません: %w", p, err)
	}

	if len(lines) == 0 {
		if p == "." {
			return "(作業フォルダは空です)", nil
		}
		return fmt.Sprintf("(%s は空のフォルダです)", p), nil
	}
	out := strings.Join(lines, "\n")
	if full {
		out += fmt.Sprintf("\n(%d 件を超えたため、ここで切りました。path に下のフォルダを指定すると、その中だけを並べられます)", listMax)
	}
	return out, nil
}
