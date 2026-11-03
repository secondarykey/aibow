// worktree の差分の画面(0039 の段2)。入力欄の上の「差分(n ファイル)」で開く。
//
// 左にファイルの一覧(足した / 変えた / 消した。取り込み済みの印)、右に選んだファイルの差分(+ は緑、- は赤)。
// 差分は、worktree を作ったときのコミットとの比較(Go の git.Changes / git.Diff)。
//
//   取り込む … チェックしたファイルの差分を、本体の作業ツリーに当てる(git apply)。コミットはユーザーがする。
//              全部当たるか、何も変えないか。当たらなければ、当たらないファイルをここに出す(本体は変わっていない)
//   捨てる   … worktree とブランチを消す。会話は本体のフォルダに戻る(切り替えも外れる)
//
// 見た目(ダイアログか横の枠か・色付け)は仕様の未決事項。今はダイアログにしている(ユーザーが見て決める)。
import { useCallback, useEffect, useState, type ReactNode } from "react";
import { LuCheck, LuFileMinus, LuFilePen, LuFilePlus, LuGitBranch, LuX } from "react-icons/lu";
import type { WorktreeChange, WorktreeState } from "../../bindings/github.com/secondarykey/aibow";
import { ChatService } from "../backend";
import { errorMessage } from "../errors";
import { baseText, STATUS_LABELS } from "../worktree";
import { CodeBlock } from "./CodeBlock";

interface Props {
  id: string; // 会話の ID
  state: WorktreeState;
  generating: boolean; // この会話の返事を生成中(捨てられない)
  onClose(): void;
  onChanged(): void;   // 取り込んだ(入力欄の上の数を取り直す)
  onDiscarded(): void; // 捨てた(会話の切り替えを外す)
  onNotice(text: string): void;
}

const ICONS: Record<string, ReactNode> = { added: <LuFilePlus />, modified: <LuFilePen />, deleted: <LuFileMinus /> };

export function WorktreeDiff({ id, state, generating, onClose, onChanged, onDiscarded, onNotice }: Props) {
  const [changes, setChanges] = useState<WorktreeChange[] | null>(null);
  const [checked, setChecked] = useState<Set<string>>(new Set());
  const [selected, setSelected] = useState<string>("");
  const [diff, setDiff] = useState<string>("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [confirmDiscard, setConfirmDiscard] = useState(false);

  // 一覧を取り直す。チェックは、まだ取り込んでいないものにだけ付ける(取り込み済みをもう一度当てると、当たらずに失敗するため)。
  // 取り込んだあとに取り直すときは、外していたチェックをそのままにする(取り込まなかったものに付け直さない)
  const load = useCallback(async (keep: boolean) => {
    try {
      const list = (await ChatService.WorktreeChanges(id)) ?? [];
      setChanges(list);
      const pending = list.filter(c => !c.applied).map(c => c.path);
      setChecked(cur => new Set(keep ? pending.filter(p => cur.has(p)) : pending));
      setSelected(cur => keep && list.some(c => c.path === cur) ? cur : list[0]?.path ?? "");
    } catch (err) {
      setError(errorMessage(err));
    }
  }, [id]);
  useEffect(() => { void load(false); }, [load]);

  // 選んだファイルの差分
  useEffect(() => {
    if (!selected) {
      setDiff("");
      return;
    }
    let stale = false;
    ChatService.WorktreeDiff(id, selected).then(
      d => { if (!stale) setDiff(d); },
      err => { if (!stale) setError(errorMessage(err)); },
    );
    return () => { stale = true; };
  }, [id, selected, changes]);

  // Esc で閉じる
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape" && !busy) onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose, busy]);

  const toggle = (path: string) => setChecked(cur => {
    const next = new Set(cur);
    if (next.has(path)) next.delete(path);
    else next.add(path);
    return next;
  });

  const apply = async () => {
    const paths = (changes ?? []).filter(c => checked.has(c.path)).map(c => c.path);
    setBusy(true);
    setError("");
    try {
      await ChatService.ApplyWorktree(id, paths);
      onNotice(`${paths.length} ファイルを本体に取り込みました(コミットはしていません)`);
      onChanged();
      await load(true);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  const discard = async () => {
    setBusy(true);
    setError("");
    try {
      await ChatService.DiscardWorktree(id);
      onNotice("worktree を捨てました");
      onDiscarded();
      onClose();
    } catch (err) {
      setError(errorMessage(err));
      setBusy(false);
    }
  };

  const pending = (changes ?? []).filter(c => !c.applied).length;
  const nChecked = (changes ?? []).filter(c => checked.has(c.path)).length;

  return (
    <div className="worktree-overlay" onMouseDown={e => { if (e.target === e.currentTarget && !busy) onClose(); }}>
      <div className="worktree-dialog" role="dialog" aria-label="worktree の差分">
        <header className="worktree-head">
          <LuGitBranch />
          <span className="worktree-title" title={state.path}>{state.branch}</span>
          <span className="worktree-base">{baseText(state)} との差分</span>
          <button type="button" className="icon-btn" title="閉じる" onClick={onClose} disabled={busy}><LuX /></button>
        </header>
        <div className="worktree-body">
          <ul className="worktree-files">
            {changes?.length === 0 && <li className="worktree-empty">変わったファイルはありません</li>}
            {changes?.map(c => (
              <li key={c.path} className={(c.path === selected ? "current " : "") + c.status + (c.applied ? " applied" : "")}>
                <input type="checkbox" checked={checked.has(c.path)} disabled={busy} onChange={() => toggle(c.path)}
                  title={c.applied ? "取り込み済み" : "取り込む"} />
                <button type="button" className="worktree-file" onClick={() => setSelected(c.path)} title={`${STATUS_LABELS[c.status] ?? c.status}: ${c.path}`}>
                  <span className={"worktree-status " + c.status}>{ICONS[c.status]}</span>
                  <span className="worktree-path">{c.path}</span>
                  {c.applied && <span className="worktree-applied" title="本体に同じ中身がある"><LuCheck /> 取り込み済み</span>}
                </button>
              </li>
            ))}
          </ul>
          <div className="worktree-diff-text">
            {selected && diff && <CodeBlock code={diff.replace(/\n$/, "")} lang="diff" closed />}
          </div>
        </div>
        {error && <p className="worktree-error">{error}</p>}
        <footer className="worktree-foot">
          {confirmDiscard ? (
            <>
              <span className="confirm-text">
                {pending > 0 ? `取り込んでいない ${pending} ファイルの変更も消えます。worktree を捨てますか?` : "worktree を捨てますか?"}
              </span>
              <button type="button" className="danger" disabled={busy || generating} onClick={discard}>捨てる</button>
              <button type="button" disabled={busy} onClick={() => setConfirmDiscard(false)}>やめる</button>
            </>
          ) : (
            <>
              <button type="button" className="danger" disabled={busy || generating}
                title={generating ? "返事を生成中は捨てられない" : "worktree とブランチを消して、本体のフォルダに戻す"}
                onClick={() => setConfirmDiscard(true)}>
                worktree を捨てる
              </button>
              <span className="worktree-spacer" />
              <button type="button" className="primary" disabled={busy || nChecked === 0} onClick={apply}
                title="選んだファイルの差分を本体の作業ツリーに当てる(コミットはしない)">
                取り込む({nChecked} ファイル)
              </button>
            </>
          )}
        </footer>
      </div>
    </div>
  );
}
