package ui

// workspaces.go: 設定の画面の「ワークスペース」と、入力欄の上のワークスペースを選ぶ部品から呼ぶメソッド(0035)。
//
// 中身は config.Workspaces にあり、ここは画面との中継だけ。ワークスペースは PC のフォルダの組なので、デスクトップのアプリだけ。
// ブラウザ(aibow-server)の画面用の口には載せない(web.go の browserMethods に入れない)。口の作りを間違えても、
// サーバーの PC のフォルダを登録できないよう、ここでも断る(workspaces が nil)。

import (
	"context"
	"errors"
	"os"

	"github.com/secondarykey/aibow/config"
	"github.com/secondarykey/aibow/git"
	"github.com/secondarykey/aibow/log"
)

// errNoWorkspaces は、ワークスペースを使えないとき(ブラウザ・起動に失敗した)のエラー。
var errNoWorkspaces = errors.New("ワークスペースは、デスクトップのアプリでだけ使えます")

// WorkspaceList は、画面に出すワークスペースの一覧。
type WorkspaceList struct {
	Workspaces []WorkspaceView `json:"workspaces"`
	Path       string          `json:"path"`            // workspaces.json の場所(設定の画面に出す)
	Error      string          `json:"error,omitempty"` // 読めなかったときの理由(書き換えない)
}

// WorkspaceView は、画面に出すワークスペース1つ。
type WorkspaceView struct {
	config.Workspace
	Folders []FolderView `json:"folders"`
}

// FolderView は、画面に出すフォルダ1つ。この PC にあるか・リポジトリかを添える(どちらもそのつど見る)。
type FolderView struct {
	config.WorkspaceFolder
	Missing bool `json:"missing,omitempty"` // この PC に無い(消した・ドライブを外した)
	Git     bool `json:"git,omitempty"`     // .git がある
}

// workspaceStore は、画面からワークスペースを使う係(使えなければエラー)。
func (s *Service) workspaceStore() (*config.Workspaces, error) {
	if s.startupErr != nil {
		return nil, s.startupErr
	}
	if s.workspaces == nil || s.browser {
		return nil, errNoWorkspaces
	}
	return s.workspaces, nil
}

// Workspaces は、この PC のワークスペースの一覧。ブラウザでは空。
func (s *Service) Workspaces() WorkspaceList {
	w, err := s.workspaceStore()
	if err != nil {
		return WorkspaceList{Workspaces: []WorkspaceView{}}
	}
	r := WorkspaceList{Workspaces: []WorkspaceView{}, Path: w.Path()}
	if err := w.Err(); err != nil {
		r.Error = err.Error()
	}
	for _, ws := range w.List() {
		v := WorkspaceView{Workspace: ws, Folders: []FolderView{}}
		for _, f := range ws.Folders {
			v.Folders = append(v.Folders, FolderView{WorkspaceFolder: f, Missing: !isDir(f.Path), Git: git.IsRepo(f.Path)})
		}
		r.Workspaces = append(r.Workspaces, v)
	}
	return r
}

// CreatedWorkspace は CreateWorkspace の結果。Existing なら、新しく作らずに今あるワークスペースを返した
// (画面はトーストで「既にあるワークスペースを選んだ」と知らせる。0049 の決定事項 2)。
type CreatedWorkspace struct {
	Workspace config.Workspace `json:"workspace"`
	Existing  bool             `json:"existing"`
}

// CreateWorkspace は、フォルダ path 1つのワークスペースを作る。
// reuse なら、同じフォルダ1つだけのワークスペースがあればそれを返す(入力欄の上の「フォルダから新しく作る」)。
// reuse でなければ、同じフォルダでも新しく作る(設定の画面の「フォルダから作る」。0049)。
func (s *Service) CreateWorkspace(path string, reuse bool) (CreatedWorkspace, error) {
	w, err := s.workspaceStore()
	if err != nil {
		return CreatedWorkspace{}, err
	}
	ws, existing, err := w.CreateFromFolder(path, reuse)
	if err != nil {
		return CreatedWorkspace{}, err
	}
	if existing {
		log.Infof("同じフォルダのワークスペース %s(%s)を選びました", ws.Name, ws.MainFolder().Path)
	} else {
		log.Noticef("ワークスペース %s(%s)", ws.Name, ws.MainFolder().Path)
	}
	return CreatedWorkspace{Workspace: ws, Existing: existing}, nil
}

// UpdateWorkspace は、ワークスペース ws.ID の名前とフォルダの組を変える(設定の画面の「ワークスペース」)。
// 次の送信から効く(ツールは送信のたびにワークスペースから作るため)。
func (s *Service) UpdateWorkspace(ws config.Workspace) error {
	w, err := s.workspaceStore()
	if err != nil {
		return err
	}
	return w.Update(ws)
}

// DeleteWorkspace は、ワークスペースを消す。そのワークスペースを選んでいた会話は消さない(見つからないワークスペースになる)。
func (s *Service) DeleteWorkspace(id string) error {
	w, err := s.workspaceStore()
	if err != nil {
		return err
	}
	return w.Delete(id)
}

// migrateWorkDirs は、ワークスペースより前の会話の作業フォルダを、この PC にあるものだけワークスペースに移す(起動したとき)。
// aibow-server につないでいてつながらなければ、次の起動で移す(移さなかった会話は、その間フォルダのツールを出さない)。
func (s *Service) migrateWorkDirs(ctx context.Context) {
	if s.workspaces == nil || s.browser {
		return
	}
	moved, kept, err := s.convs.MigrateWorkDirs(ctx)
	if err != nil {
		log.Warnf("作業フォルダをワークスペースに移せません(次の起動でもう一度移します): %v", err)
	}
	if moved > 0 {
		log.Noticef("作業フォルダを決めていた %d 個の会話を、ワークスペースに移しました(%s)", moved, s.workspaces.Path())
	}
	if kept > 0 {
		log.Noticef("作業フォルダがこの PC に無い %d 個の会話は、移さずに残しました(この PC ではフォルダのツールを出しません)", kept)
	}
}

func isDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}
