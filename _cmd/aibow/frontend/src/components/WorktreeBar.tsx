// 入力欄の上(ワークスペースの隣): 「worktree で作業する」の切り替え(会話ごとの設定。0039)。
//
// 入れた会話では、最初にメインのフォルダを書き換える前に、Go がリポジトリの中の .aibow/worktrees/<短い ID> に worktree を作り、
// 書き換えはそこに入る。本体に入れるのは、差分の画面で選んだファイルだけ。
//
//   作る前    … ブランチのアイコンのトグル。入れると枠付きで「worktree」
//   使えない  … 灰色(メインのフォルダが Git のリポジトリでない・git が PATH に無い)。理由は吹き出しに出す
//   作ったあと … 「worktree」の表示(切り替えられない)と、「差分(n ファイル)」。押すと差分の画面を開く
import { LuFileDiff, LuGitBranch } from "react-icons/lu";
import type { WorktreeChange, WorktreeState } from "../../bindings/github.com/secondarykey/aibow";

interface Props {
  state: WorktreeState;
  on: boolean; // 会話の設定の切り替え
  changes: WorktreeChange[];
  onToggle(on: boolean): void;
  onOpenDiff(): void;
}

export function WorktreeBar({ state, on, changes, onToggle, onOpenDiff }: Props) {
  if (state.active) {
    const pending = changes.filter(c => !c.applied).length;
    return (
      <div className="worktree-bar active">
        <span className="worktree-label" title={`${state.path ?? ""}\nブランチ ${state.branch ?? ""}(コミット ${(state.base ?? "").slice(0, 7)} から)`}>
          <LuGitBranch />
          worktree
        </span>
        <button type="button" className="worktree-diff" onClick={onOpenDiff}
          title={pending < changes.length ? `取り込んでいない ${pending} ファイル` : undefined}>
          <LuFileDiff />
          差分({changes.length} ファイル)
        </button>
      </div>
    );
  }
  // 使えないときは灰色にする。ただ、入れたまま使えなくなった(フォルダを変えた・git を消した)ときは外せるようにする
  const disabled = !state.usable && !on;
  const title = !state.usable ? state.reason
    : on ? "worktree で作業する(最初に書き換えるときに作る)" : "worktree で作業する";
  return (
    // 押せないボタンには吹き出しが出ないことがあるので、理由は外側に付ける
    <div className={"worktree-bar" + (on ? " set" : "") + (state.usable ? "" : " unusable")} title={title}>
      <button type="button" className="worktree-toggle" disabled={disabled} aria-pressed={on} onClick={() => onToggle(!on)}>
        <LuGitBranch />
        {on && <span>worktree</span>}
      </button>
    </div>
  );
}
