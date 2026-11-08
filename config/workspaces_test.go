package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ワークスペースを作る・名前を変える・消すと workspaces.json に書き、読み直すと同じものが戻る(0035 の完了の条件 1)
func TestWorkspacesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspaces.json")
	w, err := LoadWorkspaces(path)
	if err != nil || len(w.List()) != 0 {
		t.Fatalf("ファイルが無いときは空: %v, %v", w.List(), err)
	}
	dir := filepath.Join(t.TempDir(), "example")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ws, err := w.CreateFromFolder(dir)
	if err != nil {
		t.Fatal(err)
	}
	if ws.Name != "example" || len(ws.Folders) != 1 || ws.MainFolder() != (WorkspaceFolder{Path: dir, Name: "example", Main: true, Writable: true}) {
		t.Errorf("フォルダから作ったワークスペース: %+v", ws)
	}
	// 同じフォルダ1つだけのワークスペースは、新しく作らずに今あるものを返す
	if again, err := w.CreateFromFolder(dir); err != nil || again.ID != ws.ID || len(w.List()) != 1 {
		t.Errorf("同じフォルダ: %+v(一覧 %d), %v", again, len(w.List()), err)
	}
	renamed := ws
	renamed.Name = "  サンプル  "
	if err := w.Update(renamed); err != nil {
		t.Fatal(err)
	}
	r, err := LoadWorkspaces(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := r.Get(ws.ID); !ok || got.Name != "サンプル" || got.MainFolder().Path != dir {
		t.Errorf("読み直したワークスペース: %+v, %v", got, ok)
	}
	renamed.Name = " "
	if err := w.Update(renamed); err == nil {
		t.Error("空の名前はエラーのはず")
	}
	// フォルダの説明は上限(folderDescriptionMax 文字)まで。日本語も1文字と数える
	renamed.Name = "サンプル"
	renamed.Folders = []WorkspaceFolder{{Path: dir, Name: "example", Main: true, Writable: true, Description: strings.Repeat("あ", folderDescriptionMax)}}
	if err := w.Update(renamed); err != nil {
		t.Errorf("上限ちょうどの説明: %v", err)
	}
	renamed.Folders[0].Description += "い"
	if err := w.Update(renamed); err == nil || !strings.Contains(err.Error(), "説明が長すぎます") {
		t.Errorf("長すぎる説明: %v", err)
	}
	if err := w.Delete(ws.ID); err != nil {
		t.Fatal(err)
	}
	if r, _ = LoadWorkspaces(path); len(r.List()) != 0 {
		t.Errorf("消したあと: %+v", r.List())
	}
	// 無いフォルダ・相対パスからは作らない
	for _, p := range []string{filepath.Join(dir, "none"), "relative"} {
		if _, err := w.CreateFromFolder(p); err == nil {
			t.Errorf("%s から作れてしまった", p)
		}
	}
}

// 壊れた workspaces.json は書き換えない(ワークスペースが無いものとして動く)
func TestWorkspacesBrokenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspaces.json")
	for _, body := range []string{"{", `{"workspaces":[{"id":"a","name":"x","folders":[{"path":"/a","name":"a"}]}]}`} {
		os.WriteFile(path, []byte(body), 0o644)
		w, err := LoadWorkspaces(path)
		if err == nil || w == nil || len(w.List()) != 0 {
			t.Fatalf("%s: 壊れたファイルはエラーのはず: %v", body, err)
		}
		if _, err := w.CreateFromFolder(t.TempDir()); err == nil {
			t.Errorf("%s: 読めなかったファイルに書いてしまう", body)
		}
		if b, _ := os.ReadFile(path); string(b) != body {
			t.Errorf("%s: ファイルが書き換わった: %s", body, b)
		}
	}
}

// フォルダの組の決まり(メインはちょうど1つ・名前は重ならない・パスに見える名前は使わない)
func TestWorkspaceValidate(t *testing.T) {
	abs := t.TempDir()
	ok := WorkspaceFolder{Path: abs, Name: "main", Main: true, Writable: true}
	cases := []struct {
		name    string
		folders []WorkspaceFolder
		want    string // エラーに含まれる文字(空なら通る)
	}{
		{"メイン1つ", []WorkspaceFolder{ok, {Path: abs, Name: "notes"}}, ""},
		{"メインなし", []WorkspaceFolder{{Path: abs, Name: "a"}}, "メイン"},
		{"メイン2つ", []WorkspaceFolder{ok, {Path: abs, Name: "b", Main: true}}, "メイン"},
		{"名前が重なる", []WorkspaceFolder{ok, {Path: abs, Name: "MAIN"}}, "2つ"},
		{"パスに見える名前", []WorkspaceFolder{ok, {Path: abs, Name: "a/b"}}, "使えません"},
		{"相対パス", []WorkspaceFolder{ok, {Path: "rel", Name: "rel"}}, "絶対パス"},
	}
	for _, c := range cases {
		err := Workspace{ID: "x", Name: "w", Folders: c.folders}.validate()
		if c.want == "" && err != nil || c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}
