// 設定の画面の「ワークスペース」(デスクトップだけ。0035)。その PC のワークスペース(名前の付いたフォルダの組。workspaces.json)を
// 作る・名前を変える・消す。会話はワークスペースの ID だけを持つので、ここで変えたものは、そのワークスペースを選んだ会話の次の送信から効く。
// 消しても会話は消さない(その会話では「この PC にはこのワークスペースがありません」になる)。
//
// 一覧の行を押すと下に詳しい欄が開く(MCP のタブと同じ形)。
import { useState } from "react";
import { LuFolderPlus, LuGitBranch, LuTriangleAlert, LuX } from "react-icons/lu";
import type { WorkspaceList, WorkspaceView } from "../../bindings/github.com/secondarykey/aibow/ui";
import type { Workspace } from "../../bindings/github.com/secondarykey/aibow";
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

// WorkspaceForm は、ワークスペース1つの詳しい欄(名前を変える・消す)。
function WorkspaceForm({ workspace, locked, onClose, onSaved, onDeleted }: {
  workspace: WorkspaceView;
  locked: boolean;
  onClose(): void;
  onSaved(): void;
  onDeleted(): void;
}) {
  const [name, setName] = useState(workspace.name);
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const folders = workspace.folders ?? [];
  const changed = name.trim() !== workspace.name;

  const run = (p: Promise<void>, done: () => void) => {
    setSaving(true);
    setError("");
    p.then(done, err => setError(errorMessage(err))).finally(() => setSaving(false));
  };
  const save = () => {
    const ws: Workspace = { id: workspace.id, name, folders: folders.map(({ path, name, main, writable }) => ({ path, name, main, writable })) };
    run(ChatService.UpdateWorkspace(ws), onSaved);
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
      <div className="field-label">フォルダ</div>
      <div className="workspace-folders">
        {folders.map(f => (
          <div key={f.path} className={"workspace-folder" + (f.missing ? " missing" : "")} title={f.missing ? "この PC に見つからない" : undefined}>
            <span className="mcp-summary">{f.path}</span>
            {f.git && <span className="origin-chip" title="リポジトリ(.git がある)"><LuGitBranch /></span>}
            {f.missing && <LuTriangleAlert className="workspace-warn" />}
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
