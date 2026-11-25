package ui

// serverconfig.go: aibow-server の並列数・作業領域・サーバーのワークスペース(0065。以前の資料のフォルダ)を、管理の画面で見て変える(0061)。
//
// どれも server.json の値で、画面で変えると起動に使った server.json に書き戻す(config.SaveServerFile。ほかの鍵は残す)。
// 効き方が2つに分かれる:
//
//   - 並列数と作業領域は、モデルを読み込むときの作業領域(KV キャッシュ)の形・順番待ちの席の数・1本あたりの作業領域をまとめて決めるので、
//     起動し直すまで変わらない。画面は「今動いている値」と「次の起動の値」を並べて出す
//   - サーバーのワークスペースは、名前 → フォルダの組の表を引くだけなので、その場で差し替える(Conversations.SetServerWorkspaces)
//
// 起動の引数・環境変数で決めている値は、server.json に書いても効かない(引数のほうが強い)か、黙って環境変数を上書きしてしまうので、
// 変えられないようにして理由を出す(決定事項 6。理由の文は aibow-server が作って渡す)。
// 見る・変えるのは管理者だけ(ログインなしで使うときはいつも。0046)。web.go の adminServerMethods でも断る。

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/secondarykey/aibow/config"
	"github.com/secondarykey/aibow/log"
)

// ServerConfig は、aibow-server が起動のときに渡す、管理の画面で変える設定の今の様子。
type ServerConfig struct {
	// Path は書き戻す server.json(起動に使ったファイル。無ければ探した場所。決定事項 3・7)
	Path          string
	Parallel, Ctx ServerValue
}

// ServerValue は、起動のときに決まる値1つ(並列数か作業領域)。
type ServerValue struct {
	// Running は今動いている値(起動のときに決めた値)
	Running int
	// Fallback は、server.json に書いていないときに使う値(環境変数か既定)
	Fallback int
	// Lock は変えられない理由(引数・環境変数で決めているとき)。空なら変えられる
	Lock string
}

// ServerSettingsView は、管理の画面に出すサーバーの設定(ServerSettings の戻り値)。
type ServerSettingsView struct {
	// File は書き戻す server.json の場所(サーバーの PC のパス)。Found はそのファイルが今あるか(無ければ保存したときに作る)
	File  string `json:"file"`
	Found bool   `json:"found"`
	// FileError は、server.json を読めないときの理由(手で書き間違えたなど)。そのあいだは保存できない
	FileError string `json:"fileError,omitempty"`
	// Parallel は並列数、Ctx は作業領域の大きさ
	Parallel ServerValueView `json:"parallel"`
	Ctx      ServerValueView `json:"ctx"`
	// PerSeq は、今動いている1本あたりの作業領域(1つの会話・リクエストが使えるのはここまで。0023 の決定事項 11)
	PerSeq int `json:"perSeq"`
	// Workspaces はサーバーのワークスペース(server.json の並び。0065)
	Workspaces []ServerWorkspaceView `json:"workspaces"`
}

// ServerValueView は、並列数か作業領域の、今動いている値と次の起動の値。
type ServerValueView struct {
	Running int `json:"running"`
	// Next は server.json に書いてある値(次に起動したときの値。書いていなければ環境変数か既定)
	Next int    `json:"next"`
	Lock string `json:"lock,omitempty"`
}

// ServerWorkspaceView は、管理の画面に出すサーバーのワークスペース1つ(0065 の決定事項 16)。パスはサーバーの PC のもので、管理者にだけ返す(0061 の決定事項 10)。
type ServerWorkspaceView struct {
	Name    string             `json:"name"`
	Folders []ServerFolderView `json:"folders"`
	// UsedBy は、このワークスペースを選んでいるプロファイルの名前(あれば消せない)
	UsedBy []string `json:"usedBy"`
}

// ServerFolderView は、サーバーのワークスペースのフォルダ1つ。
type ServerFolderView struct {
	config.WorkspaceFolder
	// Missing は、フォルダが今は無いか(起動したあとに消された・動かされた)
	Missing bool `json:"missing"`
}

// workspaceUsers は、サーバーのワークスペースを選んでいるプロファイルを探す係(store.SQLite)。
type workspaceUsers interface {
	ProfilesUsingWorkspace(ctx context.Context, workspace string) ([]string, error)
}

// ServerSettings は、並列数・作業領域・サーバーのワークスペースの今の様子を返す。server.json はそのつど読み直す(手で書き換えたときも、次の起動の値が分かるように)。
func (s *Service) ServerSettings(ctx context.Context) (ServerSettingsView, error) {
	if err := s.checkServerConfig(ctx); err != nil {
		return ServerSettingsView{}, err
	}
	return s.serverSettings(ctx)
}

func (s *Service) serverSettings(ctx context.Context) (ServerSettingsView, error) {
	sc := s.server
	v := ServerSettingsView{File: sc.Path}
	file, found, err := config.ReadServerFile(sc.Path)
	v.Found = found
	if err != nil {
		v.FileError = err.Error()
	}
	v.Parallel = nextValue(sc.Parallel, file.Parallel)
	v.Ctx = nextValue(sc.Ctx, file.Ctx)
	if st, err := s.host.settings(); err == nil {
		v.PerSeq = st.NCtx
	}
	v.Workspaces, err = s.serverWorkspaceViews(ctx)
	return v, err
}

// nextValue は、server.json の値 inFile(書いていなければ 0)から、次の起動の値を決める。
// 引数・環境変数で決めているときは、同じ起動の仕方なら今と同じ値になる。
func nextValue(v ServerValue, inFile int) ServerValueView {
	next := v.Running
	if v.Lock == "" {
		next = v.Fallback
		if inFile > 0 {
			next = inFile
		}
	}
	return ServerValueView{Running: v.Running, Next: next, Lock: v.Lock}
}

// SaveServerSettings は、並列数と作業領域を server.json に書く(管理の画面の「保存」。次の起動から効く。決定事項 2・9)。
// 次の起動の値と同じ項目は書かない。変えた項目は Notice のログに1行(0053)。
func (s *Service) SaveServerSettings(ctx context.Context, parallel, nCtx int) (ServerSettingsView, error) {
	if err := s.checkServerConfig(ctx); err != nil {
		return ServerSettingsView{}, err
	}
	cur, err := s.serverSettings(ctx)
	if err != nil {
		return cur, err
	}
	if cur.FileError != "" {
		return cur, fmt.Errorf("%s。ファイルを直してから、もう一度保存してください", cur.FileError)
	}
	var edit config.ServerEdit
	var items []string
	for _, f := range []struct {
		label string
		cur   ServerValueView
		value int
		set   **int
	}{
		{"並列数", cur.Parallel, parallel, &edit.Parallel},
		{"作業領域", cur.Ctx, nCtx, &edit.Ctx},
	} {
		if f.value == f.cur.Next {
			continue
		}
		if f.cur.Lock != "" {
			return cur, fmt.Errorf("%sは%s", f.label, f.cur.Lock)
		}
		if f.value < 1 {
			return cur, fmt.Errorf("%sは 1 以上にしてください: %d", f.label, f.value)
		}
		*f.set = &f.value
		items = append(items, fmt.Sprintf("%s %d → %d", f.label, f.cur.Next, f.value))
	}
	if len(items) == 0 {
		return cur, nil
	}
	if err := config.SaveServerFile(s.server.Path, edit); err != nil {
		return cur, err
	}
	log.Noticef("サーバーの設定を保存しました(%s。次の起動から%s)", strings.Join(items, "・"), byWhom(ctx))
	return s.serverSettings(ctx)
}

// AddServerWorkspace は、サーバーのワークスペースを足す(名前と最初のフォルダのパス。その場で効き、配布プロファイルで選べるようになる)。
// 最初のフォルダはメイン・書ける・名前はフォルダ名(以前の資料のフォルダと同じ動き。0065 の決定事項 16・17)。
func (s *Service) AddServerWorkspace(ctx context.Context, name, path string) ([]ServerWorkspaceView, error) {
	name = strings.TrimSpace(name)
	return s.changeServerWorkspaces(ctx, func(list []config.ServerWorkspace) ([]config.ServerWorkspace, string, error) {
		if name == "" {
			return nil, "", errors.New("サーバーのワークスペースの名前を書いてください")
		}
		if i := serverWorkspaceIndex(list, name); i >= 0 {
			return nil, "", fmt.Errorf("サーバーのワークスペース %s はもうあります。フォルダを足すときは、一覧のそのワークスペースを開いてください", list[i].Name)
		}
		p, err := checkServerFolderPath(path)
		if err != nil {
			return nil, "", err
		}
		f := config.WorkspaceFolder{Path: p, Name: config.FolderName(p, nil), Main: true, Writable: true}
		list = append(list, config.ServerWorkspace{Name: name, Folders: []config.WorkspaceFolder{f}})
		return list, fmt.Sprintf("サーバーのワークスペースを足しました: %s(%s %s", name, f.Name, folderLabel(f)), nil
	})
}

// SaveServerWorkspace は、サーバーのワークスペース ws.Name のフォルダの組を ws のものにする(管理の画面の「保存」。その場で効く)。
// フォルダを足す・外す・パスや名前を変える・メインにする・書ける / 読むだけ・説明を、まとめて1回で書く(アプリのワークスペースの設定の画面と同じ形)。
// ワークスペースの名前は変えられない(配布プロファイルが名前で選ぶため。0061 と同じ)。変えた項目は Notice のログに1行(0053)。
func (s *Service) SaveServerWorkspace(ctx context.Context, ws config.ServerWorkspace) ([]ServerWorkspaceView, error) {
	ws = config.NormalizeServerWorkspace(ws)
	return s.changeServerWorkspaces(ctx, func(list []config.ServerWorkspace) ([]config.ServerWorkspace, string, error) {
		i := serverWorkspaceIndex(list, ws.Name)
		if i < 0 {
			return nil, "", fmt.Errorf("サーバーのワークスペース %s はありません", ws.Name)
		}
		for _, f := range ws.Folders {
			if _, err := checkServerFolderPath(f.Path); err != nil {
				return nil, "", fmt.Errorf("フォルダ %s: %w", f.Name, err)
			}
		}
		ws.Name = list[i].Name
		items := folderChanges(list[i].Folders, ws.Folders)
		if len(items) == 0 {
			return list, "", nil
		}
		list[i] = ws
		return list, fmt.Sprintf("サーバーのワークスペース %s を保存しました(%s", ws.Name, strings.Join(items, "・")), nil
	})
}

// RemoveServerWorkspace は、サーバーのワークスペースを消す。選んでいるプロファイルがあれば断る(0061 の決定事項 10)。フォルダの中身は消さない。
func (s *Service) RemoveServerWorkspace(ctx context.Context, name string) ([]ServerWorkspaceView, error) {
	return s.changeServerWorkspaces(ctx, func(list []config.ServerWorkspace) ([]config.ServerWorkspace, string, error) {
		i := serverWorkspaceIndex(list, name)
		if i < 0 {
			return nil, "", fmt.Errorf("サーバーのワークスペース %s はありません", name)
		}
		users, err := s.workspaceUsedBy(ctx, list[i].Name)
		if err != nil {
			return nil, "", err
		}
		if len(users) > 0 {
			return nil, "", fmt.Errorf("配布プロファイル「%s」がサーバーのワークスペース %s を使っています。プロファイルで別のワークスペースを選んでから削除してください",
				strings.Join(users, "」「"), list[i].Name)
		}
		var folders []string
		for _, f := range list[i].Folders {
			folders = append(folders, f.Name+" "+folderLabel(f))
		}
		msg := fmt.Sprintf("サーバーのワークスペースを削除しました: %s(%s", list[i].Name, strings.Join(folders, "・"))
		return slices.Delete(list, i, i+1), msg, nil
	})
}

// changeServerWorkspaces は、サーバーのワークスペースの表を写して edit で変え、確かめて server.json に書いてから、今の表と会話の仕組みに差し替える。
// edit はログの文(括弧の閉じる前まで)を返す。空なら何も変えていない。書けなかったら、今の表は変えない。
// server.json が前の形(folders)なら、ここで workspaces に書き直して folders を消す(0065 の決定事項 10。SaveServerFile)。
func (s *Service) changeServerWorkspaces(ctx context.Context, edit func(list []config.ServerWorkspace) ([]config.ServerWorkspace, string, error)) ([]ServerWorkspaceView, error) {
	if err := s.checkServerConfig(ctx); err != nil {
		return nil, err
	}
	// 読んでから書くまでを1つにする(2人が同時に足しても、片方が消えないように)
	s.wsEditMu.Lock()
	defer s.wsEditMu.Unlock()
	list, msg, err := edit(s.serverWorkspaceList())
	if err != nil {
		return nil, err
	}
	if msg != "" {
		if err := config.CheckServerWorkspaces(list); err != nil {
			return nil, err
		}
		if err := config.SaveServerFile(s.server.Path, config.ServerEdit{SetWorkspaces: true, Workspaces: list}); err != nil {
			return nil, err
		}
		s.setServerWorkspaces(list)
		log.Noticef("%s%s)", msg, byWhom(ctx))
	}
	return s.serverWorkspaceViews(ctx)
}

// folderChanges は、フォルダの組を old から next にしたときに変えた項目を、画面の言葉で返す(ログの1行に並べる。0053)。
// 同じフォルダかは、名前か、名前を変えたならパスで見る。
func folderChanges(old, next []config.WorkspaceFolder) []string {
	var items []string
	matched := make([]bool, len(old))
	find := func(same func(o config.WorkspaceFolder) bool) int {
		for i, o := range old {
			if !matched[i] && same(o) {
				return i
			}
		}
		return -1
	}
	for _, n := range next {
		i := find(func(o config.WorkspaceFolder) bool { return o.Name == n.Name })
		if i < 0 {
			i = find(func(o config.WorkspaceFolder) bool { return o.Path == n.Path })
		}
		if i < 0 {
			items = append(items, fmt.Sprintf("フォルダを追加 %s(%s)", n.Name, folderLabel(n)))
			continue
		}
		matched[i] = true
		o := old[i]
		if o.Name != n.Name {
			items = append(items, fmt.Sprintf("フォルダの名前(%s → %s)", o.Name, n.Name))
		}
		if o.Path != n.Path {
			items = append(items, fmt.Sprintf("%s のパス(%s → %s)", n.Name, o.Path, n.Path))
		}
		if !o.Main && n.Main {
			items = append(items, fmt.Sprintf("メイン(%s)", n.Name))
		}
		if o.Writable != n.Writable {
			items = append(items, fmt.Sprintf("%s を%s", n.Name, writableLabel(n.Writable)))
		}
		if o.Description != n.Description {
			items = append(items, fmt.Sprintf("%s の説明", n.Name))
		}
	}
	for i, o := range old {
		if !matched[i] {
			items = append(items, fmt.Sprintf("フォルダを外す %s(%s)", o.Name, o.Path))
		}
	}
	return items
}

// folderLabel は、ログに書くフォルダ1つの中身(「(パス, 書ける / 読むだけ)」の括弧の中)。
func folderLabel(f config.WorkspaceFolder) string {
	return f.Path + ", " + writableLabel(f.Writable)
}

func writableLabel(writable bool) string {
	if writable {
		return "書ける"
	}
	return "読むだけ"
}

// checkServerFolderPath は、フォルダのパスがサーバーの PC の絶対パスで、フォルダとしてあるかを確かめ、整えたパスを返す。
func checkServerFolderPath(path string) (string, error) {
	p := strings.TrimSpace(path)
	if p == "" {
		return "", errors.New("フォルダのパスを書いてください")
	}
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("フォルダのパスは、サーバーの PC の絶対パスで書いてください: %s", p)
	}
	p = filepath.Clean(p)
	if st, err := os.Stat(p); err != nil || !st.IsDir() {
		return "", fmt.Errorf("サーバーの PC にフォルダがありません: %s", p)
	}
	return p, nil
}

// serverWorkspaceViews は、サーバーのワークスペースの一覧(server.json の並び・パスと、選んでいるプロファイル付き)。
func (s *Service) serverWorkspaceViews(ctx context.Context) ([]ServerWorkspaceView, error) {
	out := []ServerWorkspaceView{}
	for _, w := range s.serverWorkspaceList() {
		users, err := s.workspaceUsedBy(ctx, w.Name)
		if err != nil {
			return nil, err
		}
		v := ServerWorkspaceView{Name: w.Name, Folders: []ServerFolderView{}, UsedBy: users}
		for _, f := range w.Folders {
			v.Folders = append(v.Folders, ServerFolderView{WorkspaceFolder: f, Missing: !isDir(f.Path)})
		}
		out = append(out, v)
	}
	return out, nil
}

// workspaceUsedBy は、サーバーのワークスペース name を選んでいるプロファイルの名前。
func (s *Service) workspaceUsedBy(ctx context.Context, name string) ([]string, error) {
	wu, ok := s.store.(workspaceUsers)
	if !ok {
		return nil, errors.New("この保存先では、サーバーのワークスペースを使っているプロファイルを調べられません")
	}
	users, err := wu.ProfilesUsingWorkspace(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("サーバーのワークスペース %s を使っているプロファイルを調べられません: %w", name, err)
	}
	if users == nil {
		users = []string{}
	}
	return users, nil
}

// checkServerConfig は、サーバーの設定を見たり変えたりしてよいか。aibow-server のブラウザの、管理者だけ。
func (s *Service) checkServerConfig(ctx context.Context) error {
	if !s.browser || s.server == nil || s.startupErr != nil {
		return errors.New("aibow-server の設定は、ブラウザの管理の画面で変えてください")
	}
	if !managesPC(ctx) {
		return errors.New(serverConfigOnly)
	}
	return nil
}

// serverConfigOnly は、管理者でない人に断るときの文(web.go の adminOnly と同じもの)。
const serverConfigOnly = "aibow-server の並列数・作業領域・サーバーのワークスペースは、管理者だけが変えられます"

// ---- サーバーのワークスペースの表 ---------------------------------------------------------

// serverWorkspaceList は、今のサーバーのワークスペースの表の写し。
func (s *Service) serverWorkspaceList() []config.ServerWorkspace {
	s.wsMu.RLock()
	defer s.wsMu.RUnlock()
	return config.CloneServerWorkspaces(s.serverWs)
}

// serverWorkspaceIndex は、名前 name のワークスペースの位置(無ければ -1)。
func serverWorkspaceIndex(list []config.ServerWorkspace, name string) int {
	name = strings.TrimSpace(name)
	return slices.IndexFunc(list, func(w config.ServerWorkspace) bool { return strings.EqualFold(w.Name, name) })
}

// hasServerWorkspaces は、サーバーのワークスペースが1つでもあるか。
func (s *Service) hasServerWorkspaces() bool {
	s.wsMu.RLock()
	defer s.wsMu.RUnlock()
	return len(s.serverWs) > 0
}

// setServerWorkspaces は、サーバーのワークスペースの表を差し替え、会話の仕組みにも渡す(次のツールの実行から効く)。
func (s *Service) setServerWorkspaces(list []config.ServerWorkspace) {
	s.wsMu.Lock()
	s.serverWs = config.CloneServerWorkspaces(list)
	s.wsMu.Unlock()
	if s.convs != nil {
		s.convs.SetServerWorkspaces(list)
	}
}
