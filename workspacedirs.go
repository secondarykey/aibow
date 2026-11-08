package aibow

// workspacedirs.go: 会話の仕組みとワークスペース(workspaces.json。config.Workspaces。0035)のつなぎ。
// ワークスペースのファイルの読み書きは config パッケージにあり、ここは会話の仕組みに渡すことと、前の作業フォルダ(work_dir)を移すことだけ。

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/secondarykey/aibow/config"
)

// SetWorkspaces は、その PC のワークスペース(workspaces.json)を決める(アプリの起動時に1回)。
// 決めなければ、ワークスペースを選んだ会話でもフォルダのツールを出さない(見つからないワークスペースと同じ扱い)。
func (cs *Conversations) SetWorkspaces(w *config.Workspaces) {
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
