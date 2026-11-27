// 管理の画面で、aibow-server の並列数・作業領域・サーバーのワークスペース(0065)を見て変える(0061)。どれも server.json に書き戻す(Go の ui/serverconfig.go)。
//
//	ServerRunSettings … 並列数と作業領域(「モデル」のタブ)。確定式の保存(0053)。起動し直すと切り替わる(その場ではモデルを読み直さない。決定事項 2)
//	ServerWorkspaces  … サーバーのワークスペース(「サーバーのワークスペース」のタブ。0061 の未決事項 1 で、ユーザーが別のタブを選んだ)。追加・保存・削除はその場で効く
//
// 起動の引数・環境変数で決めている値は、変えられないようにして錠の吹き出しに理由を出す(決定事項 6。理由の文は Go が作る)。
// 呼べるのは管理者だけ(Go が断る。画面は出し分けるだけ)。
import { useCallback, useEffect, useState, type KeyboardEvent, type ReactNode } from "react";
import { LuInfo, LuLock, LuPlus, LuTriangleAlert, LuX } from "react-icons/lu";
import { ChatService } from "../backend";
import type { ServerSettingsView, ServerWorkspaceView } from "../../bindings/github.com/secondarykey/aibow/ui";
import type { ServerWorkspace } from "../../bindings/github.com/secondarykey/aibow/config";
import { errorMessage } from "../errors";
import { SaveBar, useUnsavedEntry } from "../unsaved";
import { NumberInput } from "./ConversationSettingsForm";
import { DeleteButton } from "./DeleteButton";

// ---- 並列数と作業領域 ------------------------------------------------------------------

export function ServerRunSettings({ onError }: { onError(text: string): void }) {
  const [v, setV] = useState<ServerSettingsView | null>(null);
  // 下書き(保存した値 = 次の起動の値から変えたもの)
  const [parallel, setParallel] = useState(0);
  const [ctx, setCtx] = useState(0);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  const apply = useCallback((s: ServerSettingsView) => {
    setV(s);
    setParallel(s.parallel.next);
    setCtx(s.ctx.next);
  }, []);
  useEffect(() => {
    ChatService.ServerSettings().then(apply, err => onError(errorMessage(err)));
  }, [apply, onError]);

  const dirty = !!v && (parallel !== v.parallel.next || ctx !== v.ctx.next);
  const save = async (): Promise<boolean> => {
    setError("");
    setSaving(true);
    try {
      apply(await ChatService.SaveServerSettings(parallel, ctx));
      return true;
    } catch (err) {
      setError(`保存できません: ${errorMessage(err)}`);
      return false;
    } finally {
      setSaving(false);
    }
  };
  const discard = () => { if (v) { setParallel(v.parallel.next); setCtx(v.ctx.next); } };
  useUnsavedEntry(dirty, save, discard);
  if (!v) return null;

  // 次の起動の値と今動いている値が違えば、起動し直すまで今の値のまま(決定事項 9)
  const pending = v.parallel.next !== v.parallel.running || v.ctx.next !== v.ctx.running;
  const perSeq = parallel > 0 ? Math.floor(ctx / parallel) : 0;
  const how = `aibow-server の設定ファイル(${v.file}${v.found ? "" : "。まだ無いので、保存すると作る"})に書く。aibow-server を起動し直すと切り替わる`;
  return (
    <>
      <h2 className="section">
        並列数と作業領域
        <LuInfo className="settings-info" title={how} aria-label={how} />
      </h2>
      {v.fileError && <div className="msg error">{v.fileError}。ファイルを直すまで保存できません</div>}
      {error && <div className="msg error">{error}</div>}
      <div className="server-run">
        <label htmlFor="server-parallel" title="同時に生成できる数(順番待ちの席の数)。作業領域はこの数で等分する">並列数</label>
        <RunValue lock={v.parallel.lock}>
          <NumberInput id="server-parallel" value={parallel} min={1} step={1} disabled={!!v.parallel.lock || saving} onChange={setParallel} />
        </RunValue>
        <label htmlFor="server-ctx" title="モデルを読み込むときに確保する作業領域(KV キャッシュ)の大きさ。全部の並列で分け合う">作業領域</label>
        <RunValue lock={v.ctx.lock}>
          <NumberInput id="server-ctx" value={ctx} min={1} step={1024} disabled={!!v.ctx.lock || saving} onChange={setCtx} />
          <span className="unit">トークン</span>
        </RunValue>
        <span className="server-run-key" title="1つの会話・リクエストが使える大きさ(作業領域 ÷ 並列数)。会話の上限 + 返事の上限はこの中に入れる">1本あたり</span>
        <span>{perSeq.toLocaleString()} トークン</span>
      </div>
      {pending && (
        <p className="hint server-run-pending">
          今動いているのは 並列数 {v.parallel.running}・作業領域 {v.ctx.running.toLocaleString()} トークン(1本 {v.perSeq.toLocaleString()} トークン)。
          aibow-server を起動し直すと切り替わります
        </p>
      )}
      <SaveBar dirty={dirty} saving={saving} blocked={v.fileError ? "設定ファイルを直すまで保存できません" : undefined} scope="全員に効きます"
        onSave={() => { save().catch(() => {}); }} onCancel={discard} />
    </>
  );
}

// RunValue は入力欄と、変えられないときの錠(理由は吹き出し)。
function RunValue({ lock, children }: { lock?: string; children: ReactNode }) {
  return (
    <span className="server-run-value">
      {children}
      {lock && <LuLock className="place-lock" title={lock} aria-label={lock} />}
    </span>
  );
}

// ---- サーバーのワークスペース ------------------------------------------------------------
//
// 一覧の行を押すと下にフォルダの組の欄が開く(アプリの設定の画面の「ワークスペース」と同じ形。0065 の未決事項 1 は、これを見せて決める)。
// フォルダの組は下書きで変え、「保存」で1回に書く(その場で効く)。ワークスペースの名前は変えられない(配布プロファイルが名前で選ぶため)。

// ServerWorkspaces はサーバーのワークスペースの一覧。タブを開くたびに取り直す(配布プロファイルのタブで選んだものが「使用中」に出るように)
export function ServerWorkspaces({ onError }: { onError(text: string): void }) {
  const [list, setList] = useState<ServerWorkspaceView[] | null>(null);
  const [selected, setSelected] = useState("");
  useEffect(() => {
    ChatService.ServerSettings().then(v => setList(v.workspaces ?? []), err => onError(errorMessage(err)));
  }, [onError]);

  // 変えたら一覧を差し替える。断られたら理由を出して false
  const change = async (f: () => Promise<ServerWorkspaceView[] | null>): Promise<boolean> => {
    try {
      setList((await f()) ?? []);
    } catch (err) {
      onError(errorMessage(err));
      return false;
    }
    return true;
  };
  if (!list) return null;
  const current = list.find(w => w.name === selected);
  const how = "配布プロファイルで選ぶと、配布された人の会話のファイルのツールが、このワークスペースのフォルダを読み書きする(読むだけのフォルダには書かない)。aibow-server の設定ファイル(server.json の workspaces)に書き、すぐ効く";
  return (
    <div className="workspace-settings">
      <h2 className="section">
        サーバーのワークスペース
        <LuInfo className="settings-info" title={how} aria-label={how} />
      </h2>
      {list.length === 0 ? (
        <p className="tool-none">(サーバーのワークスペースはまだありません)</p>
      ) : (
        <div className="mcp-table server-workspace-table">
          {list.map(w => {
            const folders = w.folders ?? [];
            const users = w.usedBy ?? [];
            return (
              <button
                key={w.name}
                type="button"
                className={"user-row model-row mcp-row" + (w.name === selected ? " current" : "")}
                onClick={() => setSelected(s => (s === w.name ? "" : w.name))}
                title={folders.map(f => `${f.name}: ${f.path}`).join("\n")}
              >
                <span className="mcp-name">{w.name}</span>
                <span className="mcp-summary">{folders.map(f => f.name).join("・")}</span>
                <span className="place-note" title={users.length ? `配布プロファイル: ${users.join("・")}` : undefined}>
                  {users.length ? `${users.length} 個のプロファイルで使用中` : ""}
                </span>
                <span>
                  {folders.some(f => f.missing) && (
                    <span className="origin-chip warn" title="サーバーの PC に見つからないフォルダがある"><LuTriangleAlert /></span>
                  )}
                </span>
              </button>
            );
          })}
        </div>
      )}

      {current && (
        <ServerWorkspaceForm
          key={current.name + JSON.stringify(current.folders)}
          workspace={current}
          onClose={() => setSelected("")}
          onSave={ws => change(() => ChatService.SaveServerWorkspace(ws))}
          onDelete={() => change(() => ChatService.RemoveServerWorkspace(current.name)).then(ok => ok && setSelected(""))}
        />
      )}

      <AddWorkspace onAdd={async (name, path) => {
        const ok = await change(() => ChatService.AddServerWorkspace(name, path));
        if (ok) setSelected(name.trim());
        return ok;
      }} />
    </div>
  );
}

// Draft は、編集中のフォルダ1つ(見つからない印は、保存してある間だけ持つ)。
type Draft = { path: string; name: string; main: boolean; writable: boolean; description: string; missing?: boolean };

// FOLDER_DESCRIPTION_MAX は、フォルダの説明の長さの上限(Go の folderDescriptionMax と同じ。送信のたびにモデルに渡すので短く保つ)
const FOLDER_DESCRIPTION_MAX = 100;

// ServerWorkspaceForm は、サーバーのワークスペース1つのフォルダの組(足す・外す・パス・名前・メイン・書ける / 読むだけ・説明)と削除。
function ServerWorkspaceForm({ workspace, onClose, onSave, onDelete }: {
  workspace: ServerWorkspaceView;
  onClose(): void;
  onSave(ws: ServerWorkspace): Promise<boolean>;
  onDelete(): void;
}) {
  const saved: Draft[] = (workspace.folders ?? []).map(f => ({
    path: f.path, name: f.name, main: !!f.main, writable: !!f.writable, description: f.description ?? "", missing: f.missing,
  }));
  const [folders, setFolders] = useState<Draft[]>(saved);
  const [newPath, setNewPath] = useState("");
  const [saving, setSaving] = useState(false);
  const plain = (fs: Draft[]) => fs.map(({ path, name, main, writable, description }) => ({ path: path.trim(), name: name.trim(), main, writable, description: description.trim() }));
  const changed = JSON.stringify(plain(folders)) !== JSON.stringify(plain(saved));
  const users = workspace.usedBy ?? [];
  const multi = folders.length > 1;

  const set = (i: number, patch: Partial<Draft>) => setFolders(fs => fs.map((f, j) => (j === i ? { ...f, ...patch } : f)));
  const setMain = (i: number) => setFolders(fs => fs.map((f, j) => ({ ...f, main: j === i })));
  const remove = (i: number) => setFolders(fs => fs.filter((_, j) => j !== i));
  // 足すフォルダは読むだけから始める(サーバーの PC のフォルダを何人もが書くので、書くのは管理者が選んだときだけ。0065 の決定事項 17)
  const add = () => {
    const path = newPath.trim();
    if (!path) return;
    setFolders(fs => [...fs, { path, name: uniqueName(baseName(path), fs), main: false, writable: false, description: "" }]);
    setNewPath("");
  };
  const save = async () => {
    setSaving(true);
    await onSave({ name: workspace.name, folders: plain(folders) });
    setSaving(false);
  };

  return (
    <section className="user-detail workspace-form">
      <div className="user-detail-head">
        <h2 className="section">{workspace.name}</h2>
        <span className="spacer" />
        <button type="button" className="place-remove" onClick={onClose} title="閉じる"><LuX /></button>
      </div>
      <div className="workspace-folders">
        <span className="model-table-head" title="モデルが folder を省いたときに使うフォルダ">メイン</span>
        <span className="model-table-head" title="モデルがツールの引数 folder で呼ぶ名前(フォルダが複数のときだけ使う)">モデルが呼ぶ名前</span>
        <span className="model-table-head" title="aibow-server の PC のフォルダ(絶対パス)">パス</span>
        <span className="model-table-head" title="読むだけのフォルダには、ファイルを書き換えるツールを使わせない">書く</span>
        <span className="model-table-head" />
        {folders.map((f, i) => (
          <div key={i} className={"workspace-folder" + (f.missing ? " missing" : "")}>
            <span><input type="radio" name="server-workspace-main" checked={f.main} onChange={() => setMain(i)} title="メインにする" /></span>
            <input
              type="text"
              className="workspace-folder-name"
              value={f.name}
              disabled={!multi}
              onChange={e => set(i, { name: e.target.value })}
              title={multi ? "モデルが folder で呼ぶ名前" : "フォルダが1つのときは使わない"}
            />
            <span className="workspace-folder-path" title={f.missing ? "サーバーの PC に見つからない" : undefined}>
              <input type="text" className="workspace-folder-path-input" value={f.path} onChange={e => set(i, { path: e.target.value })} title="aibow-server の PC のフォルダ(絶対パス)" />
              {f.missing && <LuTriangleAlert className="workspace-warn" />}
            </span>
            <span><input type="checkbox" checked={f.writable} onChange={e => set(i, { writable: e.target.checked })} title={f.writable ? "書ける" : "読むだけ"} /></span>
            <button
              type="button"
              className="place-remove"
              disabled={f.main}
              onClick={() => remove(i)}
              title={f.main ? "メインのフォルダは外せません。先に別のフォルダをメインにしてください" : "このフォルダを外す(フォルダそのものは削除しない)"}
            >
              <LuX />
            </button>
            {/* 説明は名前の下の行に置く(名前・パスと同じ行だと、パスの欄が狭くなる) */}
            <span />
            <input
              type="text"
              className="workspace-folder-description"
              value={f.description}
              maxLength={FOLDER_DESCRIPTION_MAX}
              placeholder="説明"
              disabled={!multi}
              onChange={e => set(i, { description: e.target.value })}
              title={multi ? "フォルダの中身の短い説明。モデルに渡す環境の説明で、このフォルダの行の末尾に付ける(名前と違う言葉で呼んでも、どのフォルダかを当てやすくなる)" : "フォルダが1つのときは使わない"}
            />
          </div>
        ))}
      </div>
      <div className="place-add">
        <input type="text" value={newPath} placeholder="D:\docs\minutes" title="足すフォルダ(aibow-server の PC の絶対パス。読むだけで足す)"
          onChange={e => setNewPath(e.target.value)} onKeyDown={e => { if (e.key === "Enter") add(); }} />
        <button type="button" className="with-icon" disabled={!newPath.trim()} onClick={add}><LuPlus /> フォルダを追加</button>
      </div>
      <div className="mcp-actions">
        <button type="button" className="apply" onClick={save} disabled={saving || !changed}>保存</button>
        <span className="spacer" />
        <DeleteButton
          question={`サーバーのワークスペース「${workspace.name}」を削除しますか?(フォルダの中身は消えません)`}
          disabled={saving || users.length > 0}
          title={users.length
            ? `配布プロファイル「${users.join("」「")}」が使っています。プロファイルで別のワークスペースを選んでから削除してください`
            : "server.json から削除する(フォルダの中身は削除しない)"}
          onConfirm={onDelete}
        />
      </div>
    </section>
  );
}

function AddWorkspace({ onAdd }: { onAdd(name: string, path: string): Promise<boolean> }) {
  const [name, setName] = useState("");
  const [path, setPath] = useState("");
  const ready = !!name.trim() && !!path.trim();
  const add = async () => {
    if (await onAdd(name, path)) { setName(""); setPath(""); }
  };
  const enter = (e: KeyboardEvent) => { if (e.key === "Enter" && ready) add(); };
  return (
    <div className="place-add">
      <input type="text" className="folder-name-input" value={name} placeholder="社内規程" title="名前(配布プロファイルで選ぶときの名前。あとから変えられない)"
        onChange={e => setName(e.target.value)} onKeyDown={enter} />
      <input type="text" value={path} placeholder="D:\docs\rules" title="最初のフォルダ(aibow-server の PC の絶対パス。メイン・書けるで足す)"
        onChange={e => setPath(e.target.value)} onKeyDown={enter} />
      <button type="button" className="with-icon" disabled={!ready} onClick={add}><LuPlus /> 追加</button>
    </div>
  );
}

// baseName はパスの最後の名前(D:\docs\minutes → minutes)。ドライブそのもの(D:\)ならドライブの文字(Go の folderName と同じ)
function baseName(path: string): string {
  const parts = path.split(/[\\/]+/).filter(Boolean);
  const last = parts[parts.length - 1] ?? "folder";
  return parts.length > 1 ? last : last.replace(/:$/, "") || "folder";
}

// uniqueName は、ワークスペースの中で重ならない名前(重なれば -2・-3 … を付ける。大文字と小文字は区別しない。Go の config.FolderName と同じ)
function uniqueName(name: string, folders: Draft[]): string {
  const used = new Set(folders.map(f => f.name.toLowerCase()));
  let n = name;
  for (let i = 2; used.has(n.toLowerCase()); i++) n = `${name}-${i}`;
  return n;
}
