// conversationGroups.ts: 会話の一覧の並べ方(0038)。新しい順に1列に並べる(flat)か、ワークスペースごとにまとめる(workspace)か。
//
// 並べ方は「表示の仕方の1つ」として扱う(会話の中身・保存には関わらない)。プロジェクト(まだ無い概念。backlog の「共有の再検討」)ができたら、
// LIST_GROUPINGS に「プロジェクトごと」を足し、同じ形(ConversationGroup の列)を返す関数を書けば、一覧の部品はそのまま使える。
//
// どの並べ方にするかは、その PC の好みとして localStorage に覚える(stored.ts の STORED_LIST_GROUPING)。
// ワークスペースは PC のもの(workspaces.json)なので、まとまりの中身も PC によって変わる。人ごと(DB)にすると、
// ワークスペースの無いほかの PC でも「ワークスペースごと」になってしまうため(0038 の決定事項)。
// ブラウザ(aibow-server)の会話はワークスペースを使わない(サーバーのワークスペース。0024)ので、ブラウザではいつも新しい順。
//
// 一覧に混ぜる(0055):自分の会話(Conversation)と、ほかの人に共有された会話(ConversationRef)は、保存の形は別のまま、
// ここで1つの列(ListItem)にして更新の新しい順に並べる。探す(タイトルと持ち主の名前)・非アクティブで分けるのも、この列に対して行う。
import { useSyncExternalStore } from "react";
import type { Conversation, ConversationRef } from "../bindings/github.com/secondarykey/aibow";
import type { WorkspaceView } from "../bindings/github.com/secondarykey/aibow/ui";
import { isBrowser } from "./backend";
import { loadStored, saveStored, STORED_LIST_GROUPING, STORED_LIST_GROUPS_CLOSED, STORED_LIST_INACTIVE_OPEN, STORED_LIST_SHOW_EMPTY_WORKSPACES } from "./stored";

// ListItem は一覧の1行。自分の会話(own)か、ほかの人に共有された会話(shared。押すと読むだけで開く)
export type ListItem =
  | { kind: "own"; id: string; updated: number; inactive: boolean; c: Conversation }
  | { kind: "shared"; id: string; updated: number; inactive: boolean; r: ConversationRef };

// mergeList は、自分の会話と共有された会話を、更新の新しい順の1列にする(0055 の決定事項 10)。
// 同じ時刻なら ID の大きい(新しく作った)ほうを先にする(保存先の ORDER BY updated_at DESC, id DESC と同じ)
export function mergeList(own: Conversation[], shared: ConversationRef[]): ListItem[] {
  const items: ListItem[] = [
    ...own.map(c => ({ kind: "own" as const, id: c.id, updated: Date.parse(c.updated_at), inactive: !!c.inactive, c })),
    ...shared.map(r => ({ kind: "shared" as const, id: r.id, updated: Date.parse(r.updated_at), inactive: !!r.inactive, r })),
  ];
  return items.sort((a, b) => b.updated - a.updated || (a.id < b.id ? 1 : a.id > b.id ? -1 : 0));
}

// itemTitle は、行に出す会話の名前(名前がまだ無ければ「新しい会話」)
export function itemTitle(it: ListItem): string {
  return (it.kind === "own" ? it.c.title : it.r.title) || "新しい会話";
}

// matchItem は、検索ボックスの文字に当たるか(0055 の決定事項 9)。大文字小文字を区別しない部分一致で、
// 自分の会話はタイトル、共有された会話はタイトルと持ち主の名前(表示名・ログインの名前)に当てる。q は小文字にして渡す
export function matchItem(it: ListItem, q: string): boolean {
  if (q === "") return true;
  if (itemTitle(it).toLowerCase().includes(q)) return true;
  if (it.kind === "own") return false;
  return it.r.owner_name.toLowerCase().includes(q) || (it.r.owner_display ?? "").toLowerCase().includes(q);
}

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
  list: ListItem[];
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

// 一覧の下の「非アクティブ」の折りたたみを開いているか(0055 の決定事項 18。既定は閉じている。PC ごとに覚える)
let inactiveOpen: boolean | null = null;
const getInactiveOpen = (): boolean => (inactiveOpen ??= loadStored(STORED_LIST_INACTIVE_OPEN) === "true");

function setInactiveOpen(open: boolean) {
  if (getInactiveOpen() === open) return;
  inactiveOpen = open;
  saveStored(STORED_LIST_INACTIVE_OPEN, open ? "true" : null); // 既定(閉じている)は覚えない
  emit();
}

// useInactiveOpen は、非アクティブの折りたたみを開いているかと、開く / 閉じる関数
export function useInactiveOpen(): [boolean, (open: boolean) => void] {
  return [useSyncExternalStore(subscribe, getInactiveOpen), setInactiveOpen];
}

// ワークスペースごとのときに、会話の無いワークスペースも出すか(0062。既定は出さない。PC ごとに覚える)。
// ワークスペースが増えると会話の無い見出しだけで一覧が埋まるので、既定は少なく見せる側にし、出したいときだけ並べ方のメニューで出す
let showEmpty: boolean | null = null;
const getShowEmpty = (): boolean => (showEmpty ??= loadStored(STORED_LIST_SHOW_EMPTY_WORKSPACES) === "true");

function setShowEmpty(show: boolean) {
  if (getShowEmpty() === show) return;
  showEmpty = show;
  saveStored(STORED_LIST_SHOW_EMPTY_WORKSPACES, show ? "true" : null); // 既定(出さない)は覚えない
  emit();
}

// useShowEmptyWorkspaces は、会話の無いワークスペースも出すかと、それを変える関数
export function useShowEmptyWorkspaces(): [boolean, (show: boolean) => void] {
  return [useSyncExternalStore(subscribe, getShowEmpty), setShowEmpty];
}

// 検索ボックスの文字(0055)。狭いウィンドウで一覧の部品が2つ描かれても同じ文字を見るよう、部品の外に1つだけ持つ。覚えない(起動し直すと空)
let query = "";
const getQuery = () => query;

function setQuery(q: string) {
  if (q === query) return;
  query = q;
  emit();
}

// useListQuery は、検索ボックスの文字と、それを変える関数
export function useListQuery(): [string, (q: string) => void] {
  return [useSyncExternalStore(subscribe, getQuery), setQuery];
}

// groupByWorkspace は、会話をワークスペースごとにまとめる。list は新しい順に並んでいるので、まとまりの中もその順のまま。
//
//   ワークスペース(この PC にあるもの) … 中のいちばん新しい会話の順。showEmpty なら、会話の無いワークスペースも「+」で始められるよう
//                                          名前の順で後ろに出す(0062。list はアクティブの会話だけなので、会話が全部非アクティブのものも「会話の無い」側)
//   ワークスペースなし                   … 決めていない会話(ふつうのチャット)と、ほかの人に共有された会話(ワークスペースは自分の PC のもので、
//                                          ほかの人の会話には無い。0055 の決定事項 13)。あるときだけ
//   この PC に無いワークスペース        … 別の PC で選んだ・消したワークスペースの会話(名前はその PC の workspaces.json にしか無いので、1つにまとめる)。あるときだけ
//
// override は、一覧を読み直す前の会話の設定(開いている会話で、入力欄の上のワークスペースを変えたとき。すぐに移すため。
// 移った先には会話が1つあるので、showEmpty によらず出る)
export function groupByWorkspace(
  list: ListItem[], workspaces: WorkspaceView[], showEmpty: boolean, override?: { id: string; workspace: string },
): ConversationGroup[] {
  const known = new Map(workspaces.map(w => [w.id, w]));
  const byWorkspace = new Map<string, ListItem[]>(); // 入れた順 = いちばん新しい会話の順
  const none: ListItem[] = [];
  const missing: ListItem[] = [];
  for (const c of list) {
    const ws = c.kind === "shared" ? "" : c.id === override?.id ? override.workspace : c.c.settings.workspace ?? "";
    if (ws === "") none.push(c);
    else if (!known.has(ws)) missing.push(c);
    else byWorkspace.set(ws, [...(byWorkspace.get(ws) ?? []), c]);
  }

  const groups: ConversationGroup[] = [];
  const add = (w: WorkspaceView, l: ListItem[]) => groups.push({
    key: "ws:" + w.id,
    label: w.name,
    title: w.folders?.map(f => f.path).join("\n") ?? w.name,
    workspace: w.id,
    list: l,
  });
  for (const [id, l] of byWorkspace) add(known.get(id)!, l);
  if (showEmpty) {
    const empty = workspaces.filter(w => !byWorkspace.has(w.id)).sort((a, b) => a.name.localeCompare(b.name));
    for (const w of empty) add(w, []);
  }
  if (none.length > 0) {
    groups.push({ key: "none", label: "ワークスペースなし", title: "ワークスペースを決めていない会話と、共有された会話", workspace: "", list: none });
  }
  if (missing.length > 0) {
    groups.push({
      key: "missing", label: "この PC に無いワークスペース", workspace: "", list: missing,
      title: "別の PC で選んだか、削除したワークスペースの会話。この PC ではフォルダのツールを使えない",
    });
  }
  return groups;
}
