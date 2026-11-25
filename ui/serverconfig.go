package ui

// serverconfig.go: aibow-server の並列数・作業領域・資料のフォルダを、管理の画面で見て変える(0061)。
//
// どれも server.json の値で、画面で変えると起動に使った server.json に書き戻す(config.SaveServerFile。ほかの鍵は残す)。
// 効き方が2つに分かれる:
//
//   - 並列数と作業領域は、モデルを読み込むときの作業領域(KV キャッシュ)の形・順番待ちの席の数・1本あたりの作業領域をまとめて決めるので、
//     起動し直すまで変わらない。画面は「今動いている値」と「次の起動の値」を並べて出す
//   - 資料のフォルダは、名前 → パスの表を引くだけなので、その場で差し替える(Conversations.SetFolders)
//
// 起動の引数・環境変数で決めている値は、server.json に書いても効かない(引数のほうが強い)か、黙って環境変数を上書きしてしまうので、
// 変えられないようにして理由を出す(決定事項 6。理由の文は aibow-server が作って渡す)。
// 見る・変えるのは管理者だけ(ログインなしで使うときはいつも。0046)。web.go の adminServerMethods でも断る。

import (
	"context"
	"errors"
	"fmt"
	"maps"
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
	// Folders は資料のフォルダ(名前の順)
	Folders []ServerFolder `json:"folders"`
}

// ServerValueView は、並列数か作業領域の、今動いている値と次の起動の値。
type ServerValueView struct {
	Running int `json:"running"`
	// Next は server.json に書いてある値(次に起動したときの値。書いていなければ環境変数か既定)
	Next int    `json:"next"`
	Lock string `json:"lock,omitempty"`
}

// ServerFolder は資料のフォルダ1つ。パスはサーバーの PC のもので、管理者にだけ返す(決定事項 10)。
type ServerFolder struct {
	Name string `json:"name"`
	Path string `json:"path"`
	// Missing は、フォルダが今は無いか(起動したあとに消された・動かされた)
	Missing bool `json:"missing"`
	// UsedBy は、このフォルダを選んでいるプロファイルの名前(あれば消せない)
	UsedBy []string `json:"usedBy"`
}

// folderUsers は、資料のフォルダを選んでいるプロファイルを探す係(store.SQLite)。
type folderUsers interface {
	ProfilesUsingFolder(ctx context.Context, folder string) ([]string, error)
}

// ServerSettings は、並列数・作業領域・資料のフォルダの今の様子を返す。server.json はそのつど読み直す(手で書き換えたときも、次の起動の値が分かるように)。
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
	v.Folders, err = s.serverFolders(ctx)
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

// AddServerFolder は、資料のフォルダを足す(その場で効き、配布プロファイルで選べるようになる)。
func (s *Service) AddServerFolder(ctx context.Context, name, path string) ([]ServerFolder, error) {
	name = strings.TrimSpace(name)
	return s.changeFolders(ctx, func(m map[string]string) (string, error) {
		if name == "" {
			return "", errors.New("資料のフォルダの名前を書いてください")
		}
		if _, ok := m[name]; ok {
			return "", fmt.Errorf("資料のフォルダ %s はもうあります。パスを変えるときは、一覧のその行で変えてください", name)
		}
		p, err := checkServerFolderPath(path)
		if err != nil {
			return "", err
		}
		m[name] = p
		return fmt.Sprintf("資料のフォルダを足しました: %s(%s", name, p), nil
	})
}

// SetServerFolderPath は、資料のフォルダのパスを変える(名前はそのまま。そのフォルダを使う会話は、次のツールの実行から新しいパス)。
func (s *Service) SetServerFolderPath(ctx context.Context, name, path string) ([]ServerFolder, error) {
	return s.changeFolders(ctx, func(m map[string]string) (string, error) {
		old, ok := m[name]
		if !ok {
			return "", fmt.Errorf("資料のフォルダ %s はありません", name)
		}
		p, err := checkServerFolderPath(path)
		if err != nil {
			return "", err
		}
		if p == old {
			return "", nil
		}
		m[name] = p
		return fmt.Sprintf("資料のフォルダのパスを変えました: %s(%s → %s", name, old, p), nil
	})
}

// RemoveServerFolder は、資料のフォルダを消す。選んでいるプロファイルがあれば断る(決定事項 10)。
func (s *Service) RemoveServerFolder(ctx context.Context, name string) ([]ServerFolder, error) {
	return s.changeFolders(ctx, func(m map[string]string) (string, error) {
		old, ok := m[name]
		if !ok {
			return "", fmt.Errorf("資料のフォルダ %s はありません", name)
		}
		users, err := s.folderUsedBy(ctx, name)
		if err != nil {
			return "", err
		}
		if len(users) > 0 {
			return "", fmt.Errorf("配布プロファイル「%s」が資料のフォルダ %s を使っています。プロファイルで別のフォルダを選んでから消してください",
				strings.Join(users, "」「"), name)
		}
		delete(m, name)
		return fmt.Sprintf("資料のフォルダを削除しました: %s(%s", name, old), nil
	})
}

// changeFolders は、資料のフォルダの表を写して edit で変え、server.json に書いてから、今の表と会話の仕組みに差し替える。
// edit はログの文(括弧の閉じる前まで)を返す。空なら何も変えていない。書けなかったら、今の表は変えない。
func (s *Service) changeFolders(ctx context.Context, edit func(m map[string]string) (string, error)) ([]ServerFolder, error) {
	if err := s.checkServerConfig(ctx); err != nil {
		return nil, err
	}
	// 読んでから書くまでを1つにする(2人が同時に足しても、片方が消えないように)
	s.folderEditMu.Lock()
	defer s.folderEditMu.Unlock()
	m := s.folderMap()
	msg, err := edit(m)
	if err != nil {
		return nil, err
	}
	if msg != "" {
		if err := config.SaveServerFile(s.server.Path, config.ServerEdit{SetFolders: true, Folders: m}); err != nil {
			return nil, err
		}
		s.setFolders(m)
		log.Noticef("%s%s)", msg, byWhom(ctx))
	}
	return s.serverFolders(ctx)
}

// checkServerFolderPath は、資料のフォルダのパスがサーバーの PC の絶対パスで、フォルダとしてあるかを確かめ、整えたパスを返す。
func checkServerFolderPath(path string) (string, error) {
	p := strings.TrimSpace(path)
	if p == "" {
		return "", errors.New("資料のフォルダのパスを書いてください")
	}
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("資料のフォルダのパスは、サーバーの PC の絶対パスで書いてください: %s", p)
	}
	p = filepath.Clean(p)
	if st, err := os.Stat(p); err != nil || !st.IsDir() {
		return "", fmt.Errorf("サーバーの PC にフォルダがありません: %s", p)
	}
	return p, nil
}

// serverFolders は、資料のフォルダの一覧(名前の順・パスと、選んでいるプロファイル付き)。
func (s *Service) serverFolders(ctx context.Context) ([]ServerFolder, error) {
	m := s.folderMap()
	out := []ServerFolder{}
	for _, name := range slices.Sorted(maps.Keys(m)) {
		users, err := s.folderUsedBy(ctx, name)
		if err != nil {
			return nil, err
		}
		out = append(out, ServerFolder{Name: name, Path: m[name], Missing: !isDir(m[name]), UsedBy: users})
	}
	return out, nil
}

// folderUsedBy は、資料のフォルダ name を選んでいるプロファイルの名前。
func (s *Service) folderUsedBy(ctx context.Context, name string) ([]string, error) {
	fu, ok := s.store.(folderUsers)
	if !ok {
		return nil, errors.New("この保存先では、資料のフォルダを使っているプロファイルを調べられません")
	}
	users, err := fu.ProfilesUsingFolder(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("資料のフォルダ %s を使っているプロファイルを調べられません: %w", name, err)
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
const serverConfigOnly = "aibow-server の並列数・作業領域・資料のフォルダは、管理者だけが変えられます"

// ---- 資料のフォルダの表 -----------------------------------------------------------------

// folderMap は、今の資料のフォルダの表の写し。
func (s *Service) folderMap() map[string]string {
	s.folderMu.RLock()
	defer s.folderMu.RUnlock()
	m := maps.Clone(s.folders)
	if m == nil {
		m = map[string]string{}
	}
	return m
}

// folderNames は、資料のフォルダの名前(名前の順)。
func (s *Service) folderNames() []string {
	s.folderMu.RLock()
	defer s.folderMu.RUnlock()
	return slices.Sorted(maps.Keys(s.folders))
}

// hasFolders は、資料のフォルダが1つでもあるか。
func (s *Service) hasFolders() bool {
	s.folderMu.RLock()
	defer s.folderMu.RUnlock()
	return len(s.folders) > 0
}

// setFolders は、資料のフォルダの表を差し替え、会話の仕組みにも渡す(次のツールの実行から効く)。
func (s *Service) setFolders(m map[string]string) {
	s.folderMu.Lock()
	s.folders = maps.Clone(m)
	s.folderMu.Unlock()
	if s.convs != nil {
		s.convs.SetFolders(m)
	}
}
