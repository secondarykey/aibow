// conversationGroups.ts: 会話の一覧の並べ方(0038)。新しい順に1列に並べる(flat)か、ワークスペースごとにまとめる(workspace)か。
//
// 並べ方は「表示の仕方の1つ」として扱う(会話の中身・保存には関わらない)。プロジェクト(まだ無い概念。backlog の「共有の再検討」)ができたら、
// LIST_GROUPINGS に「プロジェクトごと」を足し、同じ形(ConversationGroup の列)を返す関数を書けば、一覧の部品はそのまま使える。
//
// どの並べ方にするかは、その PC の好みとして localStorage に覚える(stored.ts の STORED_LIST_GROUPING)。
// ワークスペースは PC のもの(workspaces.json)なので、まとまりの中身も PC によって変わる。人ごと(DB)にすると、
// ワークスペースの無いほかの PC でも「ワークスペースごと」になってしまうため(0038 の決定事項)。
// ブラウザ(aibow-server)の会話はワークスペースを使わない(資料のフォルダ。0024)ので、ブラウザではいつも新しい順。
import { useSyncExternalStore } from "react";
import type { Conversation } from "../bindings/github.com/secondarykey/aibow";
import type { WorkspaceView } from "../bindings/github.com/secondarykey/aibow/ui";
import { isBrowser } from "./backend";
import { loadStored, saveStored, STORED_LIST_GROUPING, STORED_LIST_GROUPS_CLOSED } from "./stored";

export type ListGrouping = "flat" | "workspace";

// LIST_GROUPINGS は、選べる並べ方と、メニューに出す名前(並べた順にメニューに出す)
export const LIST_GROUPINGS: { value: ListGrouping; label: string }[] = [
  { value: "flat", label: "新しい順" },
  { value: "workspace", label: "ワークスペースごと" },
];

// ConversationGroup は、一覧の1つのまとまり
export interface ConversationGroup {
  // key は、開け閉めを覚えるときの名前(ワークスペースは "ws:<ID>"、ほかは "none" / "missing")
  key: string;
  label: string;
  // title は、まとまりの名前にマウスを乗せたときの説明(ワークスペースはフォルダのパス)
  title: string;
  // workspace は、まとまりの「+」で新しい会話に選んでおくワークスペースの ID。空なら「+」を出さない
  workspace: string;
  list: Conversation[];
}

// ---- 並べ方と、畳んでいるまとまり ----------------------------------------------------------------
//
// 狭いウィンドウでは一覧の部品が2つ描かれる(横の細い列と、重ねて出す一覧。App.tsx)。部品ごとに持つと食い違うので、
// 部品の外に1つだけ持ち、useSyncExternalStore で両方が同じ値を見る。localStorage を読むのは、最初に描くときの1回だけ

const listeners = new Set<() => void>();
const subscribe = (l: () => void) => {
  listeners.add(l);
  return () => { listeners.delete(l); };
};
const emit = () => listeners.forEach(l => l());

let grouping: ListGrouping | null = null;
const getGrouping = (): ListGrouping => (grouping ??= loadGrouping());

// loadGrouping は、覚えている並べ方(覚えていない・知らない値・ブラウザなら新しい順)
function loadGrouping(): ListGrouping {
  if (isBrowser) return "flat";
  const v = loadStored(STORED_LIST_GROUPING);
  return LIST_GROUPINGS.some(g => g.value === v) ? (v as ListGrouping) : "flat";
}

function setGrouping(g: ListGrouping) {
  grouping = g;
  saveStored(STORED_LIST_GROUPING, g === "flat" ? null : g); // 既定(新しい順)は覚えない
  emit();
}

// useListGrouping は、一覧の並べ方と、それを変える関数
export function useListGrouping(): [ListGrouping, (g: ListGrouping) => void] {
  return [useSyncExternalStore(subscribe, getGrouping), setGrouping];
}

// 畳んでいるまとまりの key(ConversationGroup.key)。既定は開いている。消したワークスペースの key が残っても、出てこないだけで困らない
let closed: ReadonlySet<string> | null = null;
const getClosed = (): ReadonlySet<string> => (closed ??= loadClosed());

function loadClosed(): ReadonlySet<string> {
  try {
    const v: unknown = JSON.parse(loadStored(STORED_LIST_GROUPS_CLOSED) ?? "[]");
    return new Set(Array.isArray(v) ? v.filter((k): k is string => typeof k === "string") : []);
  } catch {
    return new Set(); // 壊れていたら、全部開いているものとして始める
  }
}

function setClosed(key: string, close: boolean) {
  const cur = getClosed();
  if (cur.has(key) === close) return;
  const next = new Set(cur);
  if (close) next.add(key);
  else next.delete(key);
  closed = next; // 新しい Set にする(useSyncExternalStore は値が替わったかを === で見るため)
  saveStored(STORED_LIST_GROUPS_CLOSED, next.size > 0 ? JSON.stringify([...next]) : null);
  emit();
}

// useClosedGroups は、畳んでいるまとまりと、畳む / 開く関数
export function useClosedGroups(): [ReadonlySet<string>, (key: string, close: boolean) => void] {
  return [useSyncExternalStore(subscribe, getClosed), setClosed];
}

// groupByWorkspace は、会話をワークスペースごとにまとめる。list は新しい順に並んでいるので、まとまりの中もその順のまま。
//
//   ワークスペース(この PC にあるもの) … 中のいちばん新しい会話の順。会話の無いワークスペースも、「+」で始められるよう名前の順で後ろに出す
//   ワークスペースなし                   … 決めていない会話(ふつうのチャット)。あるときだけ
//   この PC に無いワークスペース        … 別の PC で選んだ・消したワークスペースの会話(名前はその PC の workspaces.json にしか無いので、1つにまとめる)。あるときだけ
//
// override は、一覧を読み直す前の会話の設定(開いている会話で、入力欄の上のワークスペースを変えたとき。すぐに移すため)
export function groupByWorkspace(list: Conversation[], workspaces: WorkspaceView[], override?: { id: string; workspace: string }): ConversationGroup[] {
  const known = new Map(workspaces.map(w => [w.id, w]));
  const byWorkspace = new Map<string, Conversation[]>(); // 入れた順 = いちばん新しい会話の順
  const none: Conversation[] = [];
  const missing: Conversation[] = [];
  for (const c of list) {
    const ws = c.id === override?.id ? override.workspace : c.settings.workspace ?? "";
    if (ws === "") none.push(c);
    else if (!known.has(ws)) missing.push(c);
    else byWorkspace.set(ws, [...(byWorkspace.get(ws) ?? []), c]);
  }

  const groups: ConversationGroup[] = [];
  const add = (w: WorkspaceView, l: Conversation[]) => groups.push({
    key: "ws:" + w.id,
    label: w.name,
    title: w.folders?.map(f => f.path).join("\n") ?? w.name,
    workspace: w.id,
    list: l,
  });
  for (const [id, l] of byWorkspace) add(known.get(id)!, l);
  const empty = workspaces.filter(w => !byWorkspace.has(w.id)).sort((a, b) => a.name.localeCompare(b.name));
  for (const w of empty) add(w, []);
  if (none.length > 0) {
    groups.push({ key: "none", label: "ワークスペースなし", title: "ワークスペースを決めていない会話", workspace: "", list: none });
  }
  if (missing.length > 0) {
    groups.push({
      key: "missing", label: "この PC に無いワークスペース", workspace: "", list: missing,
      title: "別の PC で選んだか、消したワークスペースの会話。この PC ではフォルダのツールを使えない",
    });
  }
  return groups;
}
