package aibow

// workspaces.go: ワークスペース(名前の付いた、PC のフォルダの組。0035)と、その置き場所(workspaces.json)。
//
// 以前の作業フォルダは「会話ごとのパス1つ」で、会話の設定(会話の DB)に入っていた。aibow-server につなぐと会話は server.db に入るので、
// 別の PC で開くと前の PC のパスが残ってしまう。パスは「その PC のもの」なので、PC の手元のファイルに名前を付けて登録し、
// 会話はその ID だけを持つ(決定事項 1)。別の PC にはその ID が無いので、フォルダのツールを出さない。
//
// コーディングエージェントは「どのソースに対して働くか」が先にあり、会話はその上の1回の作業。
// 対象(ワークスペース)を会話から切り離しておくと、同じ対象で何度も会話を始められ、会話を消しても対象の登録は残る。
//
// 置き場所の分け方(settingsfile.go・connectionfile.go)では、tools.json と同じ「画面で変えるが、その PC の人が決めること」:
//
//	ワークスペース = PC のフォルダの組 … workspaces.json(パスは PC ごと。人と共有しない)
//	会話           = 1回の作業        … 会話の DB(ワークスペースの ID だけ)
//
// ワークスペースはフォルダの組。フォルダごとに「メインか」と「読むだけ / 書く」を持ち、メインは1つだけ(決定事項 2)。
// リポジトリかどうかは種類として分けず、.git があるかをそのつど見る(どのフォルダもリポジトリでありうる)。
//
// 「作業領域」(WorkspaceSettings・ensureWorkspace。モデルの KV キャッシュの置き場)とは別のもの。名前が似ているので気をつける。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"uuid"
)

// Workspace はワークスペース1つ。
type Workspace struct {
	ID   string `json:"id"`   // 会話の設定(ConversationSettings.Workspace)が持つ ID(UUID)
	Name string `json:"name"` // 画面に出す名前
	// Folders はフォルダの組。メイン(Main)はちょうど1つ
	Folders []WorkspaceFolder `json:"folders"`
}

// WorkspaceFolder はワークスペースのフォルダ1つ。
type WorkspaceFolder struct {
	Path string `json:"path"` // 絶対パス
	// Name はモデルが呼ぶ短い名前(ファイルのツールの引数 folder の値。既定はフォルダ名)。ワークスペースの中で重ならない
	Name string `json:"name"`
	// Main はメインのフォルダか。run_command のカレント・${workdir} の MCP のサーバーに渡すフォルダ。folder を省いたときもこれ
	Main bool `json:"main,omitempty"`
	// Writable は、ファイルのツールで書けるか(false なら読むだけ。write_file・edit_file を受け付けない)
	Writable bool `json:"writable,omitempty"`
}

// MainFolder はメインのフォルダ(無ければ空)。
func (w Workspace) MainFolder() WorkspaceFolder {
	for _, f := range w.Folders {
		if f.Main {
			return f
		}
	}
	return WorkspaceFolder{}
}

// workspacesFile は workspaces.json の中身。
type workspacesFile struct {
	Workspaces []Workspace `json:"workspaces"`
}

// Workspaces は、その PC のワークスペースの一覧(workspaces.json)。nil でも使える(何も登録していない扱い)。
type Workspaces struct {
	path     string
	mu       sync.Mutex
	list     []Workspace
	readOnly bool // 読めなかったファイルを上書きして消さないよう、書かない
	err      error
}

// LoadWorkspaces は workspaces.json を読む。ファイルが無ければ空。
// 読めない・壊れているときはエラーを返すが、Workspaces も返す(そのときは何も登録していない扱いで、ファイルは書き換えない)。
// path が空なら、ファイルを使わない(テストなど。変えたものはメモリの中だけ)。
func LoadWorkspaces(path string) (*Workspaces, error) {
	w := &Workspaces{path: path}
	if path == "" {
		return w, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return w, nil
	}
	var f workspacesFile
	if err == nil {
		if err = json.Unmarshal(data, &f); err != nil {
			err = fmt.Errorf("%s: %w", jsonErrorLine(data, err), err)
		}
	}
	if err == nil {
		for _, ws := range f.Workspaces {
			if err = ws.validate(); err != nil {
				break
			}
		}
	}
	if err != nil {
		w.readOnly = true
		w.err = fmt.Errorf("ワークスペースのファイル %s を読めません(ワークスペースが無いものとして動き、ファイルは書き換えません): %w", path, err)
		return w, w.err
	}
	w.list = f.Workspaces
	return w, nil
}

// Path は workspaces.json の場所(画面に出す)。
func (w *Workspaces) Path() string {
	if w == nil {
		return ""
	}
	return w.path
}

// Err は、読めなかったときの理由(読めていれば nil)。
func (w *Workspaces) Err() error {
	if w == nil {
		return nil
	}
	return w.err
}

// List はワークスペースの一覧(登録した順)。写しを返す。
func (w *Workspaces) List() []Workspace {
	if w == nil {
		return []Workspace{}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]Workspace, 0, len(w.list))
	for _, ws := range w.list {
		out = append(out, ws.clone())
	}
	return out
}

// Get は ID のワークスペース。この PC に無ければ ok=false。
func (w *Workspaces) Get(id string) (Workspace, bool) {
	if w == nil || id == "" {
		return Workspace{}, false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	i := w.index(id)
	if i < 0 {
		return Workspace{}, false
	}
	return w.list[i].clone(), true
}

// CreateFromFolder は、フォルダ path 1つ(メイン・書く)のワークスペースを作る。名前はフォルダ名。
// 同じフォルダ1つだけのワークスペースがすでにあれば、新しく作らずにそれを返す(同じものが一覧に並ばないように。
// 今ある会話の作業フォルダを移すときも、パスごとに1つにする。MigrateWorkDirs)。
func (w *Workspaces) CreateFromFolder(path string) (Workspace, error) {
	if w == nil {
		return Workspace{}, errors.New("ワークスペースを使っていません")
	}
	if !filepath.IsAbs(path) {
		return Workspace{}, fmt.Errorf("フォルダは絶対パスで指定してください(%s)", path)
	}
	path = filepath.Clean(path)
	if st, err := os.Stat(path); err != nil || !st.IsDir() {
		return Workspace{}, fmt.Errorf("フォルダ %s が見つかりません", path)
	}
	w.mu.Lock()
	for _, ws := range w.list {
		if len(ws.Folders) == 1 && samePath(ws.Folders[0].Path, path) {
			w.mu.Unlock()
			return ws.clone(), nil
		}
	}
	w.mu.Unlock()
	name := folderName(path)
	ws := Workspace{
		ID:      uuid.NewV7().String(),
		Name:    name,
		Folders: []WorkspaceFolder{{Path: path, Name: name, Main: true, Writable: true}},
	}
	err := w.update(func(list *[]Workspace) error {
		*list = append(*list, ws)
		return nil
	})
	return ws, err
}

// Update は、ワークスペース ws.ID の名前とフォルダの組を ws のものにする(設定の画面の「ワークスペース」)。
// フォルダのパスは整え(Clean)、モデルが呼ぶ名前が空ならフォルダ名にする。メインはちょうど1つ・名前は重ならない(validate)。
func (w *Workspaces) Update(ws Workspace) error {
	ws.Name = strings.TrimSpace(ws.Name)
	if ws.Name == "" {
		return errors.New("ワークスペースの名前を空にはできません")
	}
	ws.Folders = slices.Clone(ws.Folders)
	for i, f := range ws.Folders {
		if !filepath.IsAbs(f.Path) {
			return fmt.Errorf("フォルダは絶対パスで指定してください(%s)", f.Path)
		}
		f.Path = filepath.Clean(f.Path)
		if f.Name = strings.TrimSpace(f.Name); f.Name == "" {
			f.Name = folderName(f.Path)
		}
		ws.Folders[i] = f
	}
	return w.update(func(list *[]Workspace) error {
		i := slices.IndexFunc(*list, func(x Workspace) bool { return x.ID == ws.ID })
		if i < 0 {
			return errWorkspaceNotFound
		}
		(*list)[i] = ws
		return nil
	})
}

// Delete はワークスペースを消す。会話は消さない(その会話では、見つからないワークスペースになる)。
func (w *Workspaces) Delete(id string) error {
	return w.update(func(list *[]Workspace) error {
		i := slices.IndexFunc(*list, func(ws Workspace) bool { return ws.ID == id })
		if i < 0 {
			return errWorkspaceNotFound
		}
		*list = slices.Delete(*list, i, i+1)
		return nil
	})
}

var errWorkspaceNotFound = errors.New("ワークスペースが見つかりません(ほかの画面で消したかもしれません)")

func (w *Workspaces) index(id string) int {
	return slices.IndexFunc(w.list, func(ws Workspace) bool { return ws.ID == id })
}

// update は一覧を変えて workspaces.json に書く。書けなければ元に戻す。
func (w *Workspaces) update(change func(*[]Workspace) error) error {
	if w == nil {
		return errors.New("ワークスペースを使っていません")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.readOnly {
		return fmt.Errorf("ワークスペースのファイル %s を読めなかったので、書き換えません(直すか消してから、アプリを起動し直してください)", w.path)
	}
	list := make([]Workspace, 0, len(w.list)+1)
	for _, ws := range w.list {
		list = append(list, ws.clone())
	}
	if err := change(&list); err != nil {
		return err
	}
	for _, ws := range list {
		if err := ws.validate(); err != nil {
			return err
		}
	}
	if err := w.save(list); err != nil {
		return err
	}
	w.list = list
	return nil
}

// save は workspaces.json に書く。書きかけのファイルが残らないよう、一時ファイルに書いてから名前を変える。
func (w *Workspaces) save(list []Workspace) error {
	if w.path == "" {
		return nil // テストなど、ファイルを使わないとき
	}
	b, err := json.MarshalIndent(workspacesFile{Workspaces: list}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(w.path), 0o755); err != nil {
		return fmt.Errorf("ワークスペースのファイルの置き場所を作れません: %w", err)
	}
	tmp := w.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return fmt.Errorf("ワークスペースのファイルを書けません: %w", err)
	}
	if err := os.Rename(tmp, w.path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("ワークスペースのファイルを書けません: %w", err)
	}
	return nil
}

func (ws Workspace) clone() Workspace {
	ws.Folders = slices.Clone(ws.Folders)
	return ws
}

// validate は、ワークスペース1つを確かめる(読んだとき・変えるとき)。
func (ws Workspace) validate() error {
	if ws.ID == "" {
		return errors.New("ID の無いワークスペースがあります")
	}
	if strings.TrimSpace(ws.Name) == "" {
		return fmt.Errorf("ワークスペース %s の名前が空です", ws.ID)
	}
	if len(ws.Folders) == 0 {
		return fmt.Errorf("ワークスペース %s にフォルダがありません", ws.Name)
	}
	mains := 0
	var names []string
	for _, f := range ws.Folders {
		if !filepath.IsAbs(f.Path) {
			return fmt.Errorf("ワークスペース %s のフォルダは絶対パスで指定してください(%s)", ws.Name, f.Path)
		}
		if err := checkFolderName(f.Name); err != nil {
			return fmt.Errorf("ワークスペース %s: %w", ws.Name, err)
		}
		// モデルは名前でフォルダを選ぶので、大文字と小文字だけの違いも重なりとみなす(4B が書き間違えても別のフォルダに行かないように)
		if slices.ContainsFunc(names, func(n string) bool { return strings.EqualFold(n, f.Name) }) {
			return fmt.Errorf("ワークスペース %s に、名前 %s のフォルダが2つあります(モデルが呼ぶ名前は重ならないようにしてください)", ws.Name, f.Name)
		}
		names = append(names, f.Name)
		if f.Main {
			mains++
		}
	}
	if mains != 1 {
		return fmt.Errorf("ワークスペース %s のメインのフォルダは1つにしてください(今は %d 個)", ws.Name, mains)
	}
	return nil
}

// checkFolderName は、モデルが呼ぶフォルダの名前を確かめる。ツールの引数の値になるので、パスに見えるものは断る。
func checkFolderName(name string) error {
	if strings.TrimSpace(name) != name || name == "" {
		return fmt.Errorf("フォルダの名前 %q は使えません(空や、前後の空白は使えません)", name)
	}
	if strings.ContainsAny(name, `/\:`) || name == "." || name == ".." {
		return fmt.Errorf("フォルダの名前 %q は使えません(/・\\・: を含む名前は、パスと見分けられないため)", name)
	}
	return nil
}

// folderName は、フォルダのパスから、モデルが呼ぶ名前の既定を決める(フォルダ名)。
// ドライブそのもの(D:\)なら、ドライブの文字にする。
func folderName(path string) string {
	name := filepath.Base(path)
	if checkFolderName(name) == nil {
		return name
	}
	if v := strings.TrimSuffix(filepath.VolumeName(path), ":"); v != "" && checkFolderName(v) == nil {
		return v
	}
	return "folder"
}

// samePath は、2つのパスが同じフォルダか。Windows は大文字と小文字を区別しない。
func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// IsGitRepo は、フォルダがリポジトリ(.git がある)か。worktree では .git がファイルなので、ある・なしだけを見る(決定事項 2)。
func IsGitRepo(path string) bool {
	_, err := os.Stat(filepath.Join(path, ".git"))
	return err == nil
}

// SetWorkspaces は、その PC のワークスペース(workspaces.json)を決める(アプリの起動時に1回)。
// 決めなければ、ワークスペースを選んだ会話でもフォルダのツールを出さない(見つからないワークスペースと同じ扱い)。
func (cs *Conversations) SetWorkspaces(w *Workspaces) {
	cs.workspaces = w
}

// MigrateWorkDirs は、ワークスペースより前の会話の作業フォルダ(WorkDir)を、ワークスペースに移す(0035 の段1。起動したときに1回)。
//
// 移すのは、そのパスがこの PC にある会話だけ。パスごとに1つのワークスペース(フォルダ1つ・メイン・書く。名前はフォルダ名)を作り、
// 会話の設定をその ID に置き換えて、WorkDir を空にする。パスが無いもの(別の PC で選んだフォルダ)はそのまま残し、
// この PC では使わない(workDir は WorkDir を見ない)。そのパスのある PC で起動したときに、そちらで移せるように。
// aibow-server につないでいるときの会話(server.db)も、つないでいる PC で同じように移す(保存の口が違うだけ)。
//
// 移した数と、この PC に無いので残した数を返す。
func (cs *Conversations) MigrateWorkDirs(ctx context.Context) (moved, kept int, err error) {
	if cs.workspaces == nil || cs.noWorkDir {
		return 0, 0, nil
	}
	list, err := cs.store.ListConversations(ctx)
	if err != nil {
		return 0, 0, err
	}
	for _, c := range list {
		dir := c.Settings.WorkDir
		if dir == "" {
			continue
		}
		if st, err := os.Stat(dir); err != nil || !st.IsDir() || !filepath.IsAbs(dir) {
			kept++
			continue
		}
		// 両方ある(移したあとに古い版で開いて作業フォルダを決めた、など)ときは、今のワークスペースを残して古いほうを捨てる
		if c.Settings.Workspace == "" {
			ws, err := cs.workspaces.CreateFromFolder(dir)
			if err != nil {
				return moved, kept, fmt.Errorf("会話 %s の作業フォルダ %s をワークスペースに移せません: %w", c.ID, dir, err)
			}
			c.Settings.Workspace = ws.ID
		}
		c.Settings.WorkDir = ""
		if err := cs.store.UpdateConversation(ctx, c); err != nil {
			return moved, kept, fmt.Errorf("会話 %s の作業フォルダをワークスペースに移せません: %w", c.ID, err)
		}
		moved++
	}
	return moved, kept, nil
}
