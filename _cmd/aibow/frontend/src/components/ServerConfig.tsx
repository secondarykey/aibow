// 管理の画面で、aibow-server の並列数・作業領域・資料のフォルダを見て変える(0061)。どれも server.json に書き戻す(Go の ui/serverconfig.go)。
//
//	ServerRunSettings … 並列数と作業領域(「モデル」のタブ)。確定式の保存(0053)。起動し直すと切り替わる(その場ではモデルを読み直さない。決定事項 2)
//	ServerFolders     … 資料のフォルダ(「資料のフォルダ」のタブ。未決事項 1 で、ユーザーが別のタブを選んだ)。追加・パスの変更・削除はその場で効く
//
// 起動の引数・環境変数で決めている値は、変えられないようにして錠の吹き出しに理由を出す(決定事項 6。理由の文は Go が作る)。
// 呼べるのは管理者だけ(Go が断る。画面は出し分けるだけ)。
import { useCallback, useEffect, useState, type KeyboardEvent, type ReactNode } from "react";
import { LuCheck, LuFolder, LuFolderX, LuInfo, LuLock, LuPencil, LuPlus } from "react-icons/lu";
import { ChatService } from "../backend";
import type { ServerFolder, ServerSettingsView } from "../../bindings/github.com/secondarykey/aibow/ui";
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

// ---- 資料のフォルダ --------------------------------------------------------------------

// ServerFolders は資料のフォルダの一覧。タブを開くたびに取り直す(配布プロファイルのタブで選んだものが「使用中」に出るように)
export function ServerFolders({ onError }: { onError(text: string): void }) {
  const [folders, setFolders] = useState<ServerFolder[] | null>(null);
  const [editing, setEditing] = useState<string | null>(null); // パスを変えている行の名前
  useEffect(() => {
    ChatService.ServerSettings().then(v => setFolders(v.folders ?? []), err => onError(errorMessage(err)));
  }, [onError]);

  // 変えたら一覧を差し替える。断られたら理由を出して false
  const change = async (f: () => Promise<ServerFolder[] | null>): Promise<boolean> => {
    try {
      setFolders((await f()) ?? []);
    } catch (err) {
      onError(errorMessage(err));
      return false;
    }
    return true;
  };
  if (!folders) return null;
  const how = "配布プロファイルで選ぶと、配布された人の会話のファイルのツールが、このフォルダを読み書きする。aibow-server の設定ファイル(server.json の folders)に書き、すぐ効く";
  return (
    <>
      <h2 className="section">
        資料のフォルダ
        <LuInfo className="settings-info" title={how} aria-label={how} />
      </h2>
      <div className="place-list">
        {folders.map(f => (
          editing === f.name
            ? <FolderPathForm key={f.name} folder={f} onDone={() => setEditing(null)}
                onApply={async p => { if (await change(() => ChatService.SetServerFolderPath(f.name, p))) setEditing(null); }} />
            : <FolderRow key={f.name} folder={f} onEdit={() => setEditing(f.name)}
                onRemove={() => { change(() => ChatService.RemoveServerFolder(f.name)); }} />
        ))}
      </div>
      <AddFolder onAdd={(name, path) => change(() => ChatService.AddServerFolder(name, path))} />
    </>
  );
}

function FolderRow({ folder: f, onEdit, onRemove }: { folder: ServerFolder; onEdit(): void; onRemove(): void }) {
  const users = f.usedBy ?? [];
  const Icon = f.missing ? LuFolderX : LuFolder;
  return (
    <div className={"place-row" + (f.missing ? " missing" : "")}>
      <Icon className="place-icon" aria-hidden="true" />
      <span className="folder-name">{f.name}</span>
      <span className="place-path" title={f.path}>{f.path}</span>
      <span className="place-note" title={users.length ? `配布プロファイル: ${users.join("・")}` : undefined}>
        {f.missing ? "フォルダがありません" : users.length ? `${users.length} 個のプロファイルで使用中` : ""}
      </span>
      <button type="button" className="place-remove" onClick={onEdit} title="パスを変更" aria-label="パスを変更"><LuPencil /></button>
      <DeleteButton
        iconOnly
        question={`資料のフォルダ ${f.name} を削除しますか?(フォルダの中身は消えません)`}
        disabled={users.length > 0}
        title={users.length
          ? `配布プロファイル「${users.join("」「")}」が使っています。プロファイルで別のフォルダを選んでから削除してください`
          : "資料のフォルダを削除(フォルダの中身は消えない)"}
        onConfirm={onRemove}
      />
    </div>
  );
}

function FolderPathForm({ folder: f, onApply, onDone }: { folder: ServerFolder; onApply(path: string): void; onDone(): void }) {
  const [path, setPath] = useState(f.path);
  return (
    <div className="place-row">
      <LuFolder className="place-icon" aria-hidden="true" />
      <span className="folder-name">{f.name}</span>
      <input type="text" className="folder-path-input" value={path} autoFocus title="aibow-server の PC のフォルダ(絶対パス)"
        onChange={e => setPath(e.target.value)}
        onKeyDown={e => {
          if (e.key === "Enter" && path.trim()) onApply(path);
          if (e.key === "Escape") onDone();
        }} />
      <button type="button" className="with-icon" disabled={!path.trim() || path === f.path} onClick={() => onApply(path)}><LuCheck /> 適用</button>
      <button type="button" onClick={onDone}>キャンセル</button>
    </div>
  );
}

function AddFolder({ onAdd }: { onAdd(name: string, path: string): Promise<boolean> }) {
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
      <input type="text" value={path} placeholder="D:\docs\rules" title="aibow-server の PC のフォルダ(絶対パス)"
        onChange={e => setPath(e.target.value)} onKeyDown={enter} />
      <button type="button" className="with-icon" disabled={!ready} onClick={add}><LuPlus /> 追加</button>
    </div>
  );
}
