package config

// serverworkspaces.go: aibow-server のサーバーのワークスペース(server.json の workspaces。0065)。
//
// 以前の資料のフォルダ(folders。名前 → 絶対パス。0024 の 12-2)は、配布プロファイルで1つだけ選べるフォルダだった。
// アプリのワークスペース(0035)はフォルダを複数持ち(メイン・書ける / 読むだけ・説明)、ファイルのツールは引数 folder でフォルダを選べる。
// 資料のフォルダも同じ形にして「サーバーのワークスペース」にした。フォルダの欄はアプリと同じ WorkspaceFolder を使う
// (形をそろえると、ツールの中は何も変えずに済む。ツールはもともと複数のフォルダを扱える)。
//
// アプリのワークスペースとは別のもの(決定事項 4)。置き場所は server.json だけで、ID も worktree も持たない。
// 配布プロファイルは名前で選ぶ(ProfileRules.Workspace)ので、名前は変えられない(0061 と同じ)。
//
// 前の形(folders)からの移し方(決定事項 10):
//
//   - folders だけが書いてあれば、資料のフォルダ1つを「同じ名前のワークスペース・フォルダ1つ(メイン・書ける)」として読む(今と同じ動き)
//   - 起動のときには書き換えない(手で書いたファイルを、起動しただけで変えない)。管理の画面で最初に保存したときに workspaces を書き、folders を消す
//   - folders と workspaces の両方があれば、どちらが正しいか分からないので起動のエラーにする

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"
)

// ServerWorkspace は、サーバーのワークスペース1つ(名前とフォルダの組)。
type ServerWorkspace struct {
	// Name は配布プロファイルで選ぶときの名前(ProfileRules.Workspace)。変えられない
	Name string `json:"name"`
	// Folders はフォルダの組。メイン(Main)はちょうど1つ。パスはサーバーの PC のもので、モデルにもブラウザにも見せない(管理者の画面だけ)
	Folders []WorkspaceFolder `json:"folders"`
}

// MainFolder はメインのフォルダ(無ければ空)。
func (w ServerWorkspace) MainFolder() WorkspaceFolder {
	for _, f := range w.Folders {
		if f.Main {
			return f
		}
	}
	return WorkspaceFolder{}
}

// Clone は写し(フォルダの組も写す。画面から変えるときに、今の表を書き換えないため)。
func (w ServerWorkspace) Clone() ServerWorkspace {
	w.Folders = slices.Clone(w.Folders)
	return w
}

// CloneServerWorkspaces は、ワークスペースの一覧の写し。
func CloneServerWorkspaces(list []ServerWorkspace) []ServerWorkspace {
	out := make([]ServerWorkspace, len(list))
	for i, w := range list {
		out[i] = w.Clone()
	}
	return out
}

// ServerWorkspaces は、server.json に書いてあるサーバーのワークスペース。
// workspaces があればそれ、無くて前の形の folders があれば、それを移したもの(migrated が true)。
// 両方あるときは LoadServerFile がエラーにしているので、ここでは workspaces を返す。
func (f ServerFile) ServerWorkspaces() (list []ServerWorkspace, migrated bool) {
	if len(f.Workspaces) > 0 || len(f.Folders) == 0 {
		return CloneServerWorkspaces(f.Workspaces), false
	}
	return foldersToWorkspaces(f.Folders), true
}

// foldersToWorkspaces は、前の形の資料のフォルダ(名前 → 絶対パス)を、ワークスペースの形にする(名前の順)。
// フォルダは1つで、名前は資料のフォルダの名前・メイン・書ける(0024 の決定事項 5 で書けたため。今と同じ動き)。
// 資料のフォルダの名前がモデルの呼ぶ名前に使えない形(/ を含むなど)なら、フォルダの名前だけはフォルダ名にする。
func foldersToWorkspaces(folders map[string]string) []ServerWorkspace {
	var out []ServerWorkspace
	for _, name := range slices.Sorted(maps.Keys(folders)) {
		path := folders[name]
		fname := name
		if checkFolderName(fname) != nil {
			fname = folderName(path)
		}
		out = append(out, ServerWorkspace{Name: name, Folders: []WorkspaceFolder{{Path: path, Name: fname, Main: true, Writable: true}}})
	}
	return out
}

// NormalizeServerWorkspace は、画面から来たワークスペースを整える(名前の前後の空白・パスの Clean・フォルダの名前の既定・説明の空白)。
// 確かめるのは CheckServerWorkspaces。
func NormalizeServerWorkspace(w ServerWorkspace) ServerWorkspace {
	w = w.Clone()
	w.Name = strings.TrimSpace(w.Name)
	for i, f := range w.Folders {
		if f.Path = strings.TrimSpace(f.Path); filepath.IsAbs(f.Path) {
			f.Path = filepath.Clean(f.Path)
		}
		if f.Name = strings.TrimSpace(f.Name); f.Name == "" && f.Path != "" {
			f.Name = folderName(f.Path)
		}
		// 説明は環境の説明の1行に入るので、改行や続く空白を1つの空白にする(アプリの Workspaces.Update と同じ)
		f.Description = strings.Join(strings.Fields(f.Description), " ")
		w.Folders[i] = f
	}
	return w
}

// FolderName は、フォルダのパスから、モデルが呼ぶ名前の既定(フォルダ名)を決める。used にある名前とは重ならないよう -2・-3 … を付ける
// (大文字と小文字は区別しない。validateFolders と同じ)。管理の画面でフォルダを足したときの名前に使う。
func FolderName(path string, used []string) string {
	base := folderName(path)
	name := base
	for i := 2; slices.ContainsFunc(used, func(u string) bool { return strings.EqualFold(u, name) }); i++ {
		name = fmt.Sprintf("%s-%d", base, i)
	}
	return name
}

// CheckServerWorkspaces は、サーバーのワークスペースの一覧を確かめる(起動のときと、管理の画面から書く前。決定事項 9)。
//   - ワークスペースの名前が空でなく、重ならない(配布プロファイルが名前で選ぶため。大文字と小文字だけの違いも重なりとみなす)
//   - フォルダはアプリのワークスペースと同じ決まり(1つ以上・メインはちょうど1つ・名前は使える形で重ならない。validateFolders)
//   - パスは絶対パスでフォルダがある(書き間違えたフォルダを、会話で読もうとしたときに初めて知るのでは遅いため)
//   - 説明は folderDescriptionMax 文字まで(送信のたびにモデルに渡すため)
func CheckServerWorkspaces(list []ServerWorkspace) error {
	var names []string
	for _, w := range list {
		if strings.TrimSpace(w.Name) == "" {
			return errors.New("名前が空のサーバーのワークスペースがあります")
		}
		if slices.ContainsFunc(names, func(n string) bool { return strings.EqualFold(n, w.Name) }) {
			return fmt.Errorf("サーバーのワークスペース %s が2つあります(名前は重ならないようにしてください)", w.Name)
		}
		names = append(names, w.Name)
		if err := checkServerWorkspace(w); err != nil {
			return err
		}
	}
	return nil
}

// checkServerWorkspace は、サーバーのワークスペース1つのフォルダを確かめる。
func checkServerWorkspace(w ServerWorkspace) error {
	if err := validateFolders(w.Name, w.Folders); err != nil {
		return err
	}
	for _, f := range w.Folders {
		if st, err := os.Stat(f.Path); err != nil || !st.IsDir() {
			return fmt.Errorf("サーバーのワークスペース %s のフォルダ %s が、サーバーの PC にありません: %s", w.Name, f.Name, f.Path)
		}
		if n := utf8.RuneCountInString(f.Description); n > folderDescriptionMax {
			return fmt.Errorf("サーバーのワークスペース %s のフォルダ %s の説明が長すぎます(%d 文字。%d 文字までにしてください。説明は送信のたびにモデルに渡すため)",
				w.Name, f.Name, n, folderDescriptionMax)
		}
	}
	return nil
}
