// worktree.tsx: 会話ごとの worktree(0039)。入力欄の上の切り替え(WorktreeBar)と差分の画面(WorktreeDiff)が使う。
//
// worktree を作る・差分を取る・本体に当てる・消すのは、すべて Go(aibow の worktree.go と git パッケージ)。
// 画面は状態を取って出すだけ。作るのは送信の途中(最初に書き換えるツールの前)なので、Go の知らせ(chat の worktree)で取り直す。
// 送信が終わったとき(done)も取り直す(モデルが書き換えたファイルの数が変わるため)。
// ウィンドウに戻ったときも取り直す(ターミナルで本体のブランチを変えると、作る前の元の表示が変わるため)。
// ブラウザ(aibow-server)の会話では使わない(worktree は PC のフォルダ。0039 の決定事項 7)。
import { useCallback, useEffect, useRef, useState } from "react";
import { ChatService, isBrowser, onChat, onWindowFocus } from "./backend";
import { errorMessage } from "./errors";
import type { ConversationSettings, WorktreeChange, WorktreeState } from "../bindings/github.com/secondarykey/aibow";

// NIL は空の会話の ID(新しい会話の画面。Go では uuid.UUID{})
const NIL = "00000000-0000-0000-0000-000000000000";

export interface Worktree {
  state: WorktreeState | null;  // null: ワークスペースを選んでいない・ブラウザ(切り替えを出さない)
  changes: WorktreeChange[];    // 作ったあとの、変わったファイル(作る前は空)
  reload(): void;
}

// useWorktree は、会話 id(新しい会話の画面なら null)の、設定 settings での worktree の状態と、変わったファイル。
export function useWorktree(id: string | null, settings: ConversationSettings | null, onError: (text: string) => void): Worktree {
  const [state, setState] = useState<WorktreeState | null>(null);
  const [changes, setChanges] = useState<WorktreeChange[]>([]);
  const seq = useRef(0); // 会話を切り替えたあとに、前の会話の答えが届いても使わない
  const workspace = settings?.workspace ?? "";
  const on = !!settings?.worktree;
  // 状態を取るときの設定(ワークスペースと切り替えだけを見る。ほかの設定が変わるたびに取り直さない)
  const latest = useRef(settings);
  latest.current = settings;

  const reload = useCallback(() => {
    const n = ++seq.current;
    const set = latest.current;
    if (isBrowser || !set || !workspace) {
      setState(null);
      setChanges([]);
      return;
    }
    ChatService.WorktreeState(id ?? NIL, set).then(
      st => {
        if (n !== seq.current) return;
        setState(st);
        if (!st.active || !id) {
          setChanges([]);
          return;
        }
        ChatService.WorktreeChanges(id).then(
          c => { if (n === seq.current) setChanges(c ?? []); },
          err => onError(`worktree の差分を取得できません: ${errorMessage(err)}`),
        );
      },
      err => onError(`worktree の状態を取得できません: ${errorMessage(err)}`),
    );
  }, [id, workspace, on, onError]);

  useEffect(reload, [reload]);
  useEffect(() => onChat(ev => {
    if (id && ev.conversation_id === id && (ev.type === "worktree" || ev.type === "done")) reload();
  }), [id, reload]);
  useEffect(() => onWindowFocus(reload), [reload]);

  return { state, changes, reload };
}

// unappliedCount は、会話 id の worktree の、まだ本体に取り込んでいないファイルの数(会話を消す前の確認)。
// worktree が無い・取れないときは 0(消すのを止めない)
export async function unappliedCount(id: string): Promise<number> {
  if (isBrowser) return 0;
  try {
    const list = (await ChatService.WorktreeChanges(id)) ?? [];
    return list.filter(c => !c.applied).length;
  } catch {
    return 0;
  }
}

// baseText は、worktree を作る元(作ったあとは作った元)の文:「main の 1a2b3c4」。ブランチから外れていれば「コミット 1a2b3c4」
export function baseText(st: WorktreeState): string {
  const commit = (st.base ?? "").slice(0, 7);
  return st.base_branch ? `${st.base_branch} の ${commit}` : `コミット ${commit}`;
}

// fromText は、どこから作るか(作ったか)の文。件名があれば「「件名」から」、無ければ(件名を覚える前に作った worktree)「main の 1a2b3c4 から」。
// 吹き出しに出す(件名のほうが、どのコミットかが分かりやすい。2026-11-04、ユーザーと決めた)
export function fromText(st: WorktreeState): string {
  return st.base_subject ? `「${st.base_subject}」から` : `${baseText(st)} から`;
}

// BaseLabel は、元のブランチとコミット(短く)の表示。ブランチから外れていればコミットだけ
export function BaseLabel({ state }: { state: WorktreeState }) {
  return (
    <>
      {state.base_branch && <span className="worktree-base-branch">{state.base_branch}</span>}
      <code className="worktree-base-commit">{(state.base ?? "").slice(0, 7)}</code>
    </>
  );
}

// STATUS_LABELS は、変わり方の画面での名前
export const STATUS_LABELS: Record<string, string> = { added: "足した", modified: "変えた", deleted: "消した" };
