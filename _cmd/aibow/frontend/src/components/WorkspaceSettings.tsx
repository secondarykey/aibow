// 設定の画面の「ワークスペース」(デスクトップだけ。0035)。その PC のワークスペース(名前の付いたフォルダの組。workspaces.json)を
// 作る・名前とフォルダの組を変える・消す。会話はワークスペースの ID だけを持つので、ここで変えたものは、そのワークスペースを選んだ会話の次の送信から効く。
// 消しても会話は消さない(その会話では「この PC にはこのワークスペースがありません」になる)。
//
// 一覧の行を押すと下に詳しい欄が開く(MCP のタブと同じ形)。
import { useState } from "react";
import { LuFolderPlus, LuGitBranch, LuPlus, LuTriangleAlert, LuX } from "react-icons/lu";
import type { FolderView, WorkspaceList, WorkspaceView } from "../../bindings/github.com/secondarykey/aibow/ui";
import type { Workspace } from "../../bindings/github.com/secondarykey/aibow/config";
import { ChatService } from "../backend";
import { errorMessage } from "../errors";
import { createFromFolder, mainFolder } from "../workspaces";

interface Props {
  list: WorkspaceList;
  reload(): void;
}

export function WorkspaceSettings({ list, reload }: Props) {
  const [selected, setSelected] = useState("");
  const [error, setError] = useState("");
  const workspaces = list.workspaces ?? [];
  const locked = !!list.error; // workspaces.json が読めなければ、画面からは保存しない(壊れたファイルを上書きしない)
  const current = workspaces.find(w => w.id === selected);

  const create = () => {
    setError("");
    createFromFolder().then(
      id => {
        if (!id) return;
        reload();
        setSelected(id);
      },
      err => setError(errorMessage(err)),
    );
  };

  return (
    <div className="workspace-settings">
      {(error || list.error) && <div className="msg error">{error || list.error}</div>}
      <div className="user-toolbar">
        <button type="button" className="user-create" disabled={locked} onClick={create} title="フォルダを選んで、そのフォルダ1つのワークスペースを作る">
          <LuFolderPlus /> フォルダから作る
        </button>
      </div>

      {workspaces.length === 0 ? (
        <p className="tool-none">(ワークスペースはまだありません)</p>
      ) : (
        <div className="mcp-table workspace-table">
          {workspaces.map(w => {
            const main = mainFolder(w);
            return (
              <button
                key={w.id}
                type="button"
                className={"user-row model-row mcp-row" + (w.id === selected ? " current" : "")}
                onClick={() => setSelected(s => (s === w.id ? "" : w.id))}
                title={(w.folders ?? []).map(f => f.path).join("\n")}
              >
                <span className="mcp-name">{w.name}</span>
                <span className="mcp-summary">{main?.path}</span>
                <span>
                  {main?.git && <span className="origin-chip" title="メインのフォルダはリポジトリ(.git がある)"><LuGitBranch /></span>}
                  {(w.folders ?? []).some(f => f.missing) && (
                    <span className="origin-chip warn" title="この PC に見つからないフォルダがある"><LuTriangleAlert /></span>
                  )}
                </span>
              </button>
            );
          })}
        </div>
      )}

      {current && (
        <WorkspaceForm
          key={current.id}
          workspace={current}
          locked={locked}
          onClose={() => setSelected("")}
          onSaved={() => reload()}
          onDeleted={() => {
            setSelected("");
            reload();
          }}
        />
      )}

      {list.path && <p className="hint tool-path" title="ワークスペースの保存先(この PC だけのもの。aibow-server につないでいても手元に置く)">{list.path}</p>}
    </div>
  );
}

// FOLDER_DESCRIPTION_MAX は、フォルダの説明の長さの上限(Go の folderDescriptionMax と同じ。送信のたびにモデルに渡すので短く保つ)
const FOLDER_DESCRIPTION_MAX = 100;

// Draft は、編集中のフォルダ1つ(画面に出す印は、保存してある間だけ持つ)。
type Draft = Omit<FolderView, "missing" | "git"> & { missing?: boolean; git?: boolean };

// WorkspaceForm は、ワークスペース1つの詳しい欄(名前・フォルダを足す / 外す・メイン・読むだけ / 書く・モデルが呼ぶ名前・消す。0035 の段2)。
function WorkspaceForm({ workspace, locked, onClose, onSaved, onDeleted }: {
  workspace: WorkspaceView;
  locked: boolean;
  onClose(): void;
  onSaved(): void;
  onDeleted(): void;
}) {
  const [name, setName] = useState(workspace.name);
  const [folders, setFolders] = useState<Draft[]>(workspace.folders ?? []);
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const plain = (fs: Draft[]) =>
    fs.map(({ path, name, main, writable, description }) => ({ path, name, main: !!main, writable: !!writable, description: description ?? "" }));
  const changed = name.trim() !== workspace.name || JSON.stringify(plain(folders)) !== JSON.stringify(plain(workspace.folders ?? []));
  const multi = folders.length > 1;

  const run = (p: Promise<void>, done: () => void) => {
    setSaving(true);
    setError("");
    p.then(done, err => setError(errorMessage(err))).finally(() => setSaving(false));
  };
  const save = () => {
    const ws: Workspace = { id: workspace.id, name, folders: plain(folders) };
    run(ChatService.UpdateWorkspace(ws), onSaved);
  };
  const set = (i: number, patch: Partial<Draft>) => setFolders(fs => fs.map((f, j) => (j === i ? { ...f, ...patch } : f)));
  const setMain = (i: number) => setFolders(fs => fs.map((f, j) => ({ ...f, main: j === i })));
  const remove = (i: number) =>
    setFolders(fs => {
      const rest = fs.filter((_, j) => j !== i);
      // メインを外したら、残りの先頭をメインにする(メインはちょうど1つ)
      return fs[i].main && rest.length > 0 ? rest.map((f, j) => ({ ...f, main: j === 0 })) : rest;
    });
  // 足すフォルダは読むだけから始める(メインのソースの横に置く資料。書くかは選んで決める)
  const add = () => {
    setError("");
    ChatService.ChooseWorkDir("").then(
      dir => {
        if (!dir) return;
        if (folders.some(f => samePath(f.path, dir))) {
          setError(`${dir} はもう入っています`);
          return;
        }
        setFolders(fs => [...fs, { path: dir, name: uniqueName(baseName(dir), fs), main: false, writable: false }]);
      },
      err => setError(errorMessage(err)),
    );
  };

  return (
    <section className="user-detail workspace-form">
      <div className="user-detail-head">
        <h2 className="section">{workspace.name}</h2>
        <span className="spacer" />
        <button type="button" className="place-remove" onClick={onClose} title="閉じる"><LuX /></button>
      </div>
      {error && <div className="msg error">{error}</div>}
      <label htmlFor="workspace-name" title="入力欄の上と、ワークスペースの一覧に出す名前">名前</label>
      <input
        id="workspace-name"
        type="text"
        value={name}
        disabled={locked}
        onChange={e => setName(e.target.value)}
        onKeyDown={e => e.key === "Enter" && changed && save()}
      />
      <div className="field-label">
        フォルダ
        <button type="button" className="place-remove" disabled={locked} onClick={add} title="フォルダを足す"><LuPlus /></button>
      </div>
      <div className="workspace-folders">
        <span className="model-table-head" title="run_command のカレント・${workdir} の MCP のサーバーに渡すフォルダ。モデルが folder を省いたときもこのフォルダ">メイン</span>
        <span className="model-table-head" title="モデルがツールの引数 folder で呼ぶ名前(フォルダが複数のときだけ使う)">モデルが呼ぶ名前</span>
        <span className="model-table-head">パス</span>
        <span className="model-table-head" title="読むだけのフォルダには、ファイルを書き換えるツールを使わせない(コマンドからは書けるので、確認が守り)">書く</span>
        <span className="model-table-head" />
        {folders.map((f, i) => (
          <div key={f.path} className={"workspace-folder" + (f.missing ? " missing" : "")}>
            <span><input type="radio" name="workspace-main" checked={!!f.main} disabled={locked} onChange={() => setMain(i)} title="メインにする" /></span>
            <input
              type="text"
              className="workspace-folder-name"
              value={f.name}
              disabled={locked || !multi}
              onChange={e => set(i, { name: e.target.value })}
              title={multi ? "モデルが folder で呼ぶ名前" : "フォルダが1つのときは使わない"}
            />
            <span className="workspace-folder-path" title={f.missing ? "この PC に見つからない" : f.path}>
              <span className="mcp-summary">{f.path}</span>
              {f.git && <span className="origin-chip" title="リポジトリ(.git がある)"><LuGitBranch /></span>}
              {f.missing && <LuTriangleAlert className="workspace-warn" />}
            </span>
            <span><input type="checkbox" checked={!!f.writable} disabled={locked} onChange={e => set(i, { writable: e.target.checked })} title={f.writable ? "書ける" : "読むだけ"} /></span>
            <button type="button" className="place-remove" disabled={locked || folders.length === 1} onClick={() => remove(i)} title="このフォルダを外す(フォルダそのものは消さない)"><LuX /></button>
            {/* 説明は名前の下の行に置く(名前・パスと同じ行だと、パスの欄が狭くなる) */}
            <span />
            <input
              type="text"
              className="workspace-folder-description"
              value={f.description ?? ""}
              maxLength={FOLDER_DESCRIPTION_MAX}
              placeholder="説明"
              disabled={locked || !multi}
              onChange={e => set(i, { description: e.target.value })}
              title={multi ? "フォルダの中身の短い説明。モデルに渡す環境の説明で、このフォルダの行の末尾に付ける(名前と違う言葉で呼んでも、どのフォルダかを当てやすくなる)" : "フォルダが1つのときは使わない"}
            />
          </div>
        ))}
      </div>
      <div className="mcp-actions">
        <button type="button" className="apply" onClick={save} disabled={saving || locked || !changed}>保存</button>
        <span className="spacer" />
        {confirmDelete ? (
          <>
            <span className="confirm-text">消しますか?</span>
            <button type="button" className="danger" disabled={saving || locked} onClick={() => run(ChatService.DeleteWorkspace(workspace.id), onDeleted)}>消す</button>
            <button type="button" disabled={saving} onClick={() => setConfirmDelete(false)}>やめる</button>
          </>
        ) : (
          <button type="button" className="danger-outline" disabled={saving || locked} onClick={() => setConfirmDelete(true)} title="workspaces.json から消す(フォルダと会話は消さない)">
            消す
          </button>
        )}
      </div>
    </section>
  );
}

// baseName はパスの最後の名前(D:\Work\example → example)。ドライブそのもの(D:\)ならドライブの文字
function baseName(path: string): string {
  const parts = path.split(/[\\/]+/).filter(Boolean);
  const last = parts[parts.length - 1] ?? "folder";
  return parts.length > 1 ? last : last.replace(/:$/, "") || "folder";
}

// uniqueName は、ワークスペースの中で重ならない名前(重なれば -2・-3 … を付ける。大文字と小文字は区別しない。Go の validate と同じ)
function uniqueName(name: string, folders: Draft[]): string {
  const used = new Set(folders.map(f => f.name.toLowerCase()));
  let n = name;
  for (let i = 2; used.has(n.toLowerCase()); i++) n = `${name}-${i}`;
  return n;
}

// samePath は同じフォルダか(Windows のパスは大文字と小文字を区別しない。末尾の区切りは無視する)
function samePath(a: string, b: string): boolean {
  const norm = (p: string) => p.replace(/[\\/]+$/, "").toLowerCase();
  return norm(a) === norm(b);
}
