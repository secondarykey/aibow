// directory.ts: 共有の相手に選べる人(0031)。会話の共有・プロファイルの配る・利用者のタブで同じものを使う。
//
// 全員を一度に取って画面で絞る(サーバーで探す口は作らない。0031 の決定事項 13。100人なら足りる)。
// 「最近選んだ相手」は画面の手元(localStorage)に覚える(DB は変えない。決定事項 12・38)。
import { useEffect, useState } from "react";
import { ChatService } from "./backend";
import { loadStored, saveStored } from "./stored";

// Person は共有の相手に選べる人1人。display は表示名(空ならログインの名前を出す。決定事項 18)
export interface Person {
  name: string;
  display: string;
}

// Target は選んだ相手1つ(人かグループ)。グループは名前で覚える
export interface Target {
  kind: "user" | "group";
  name: string;
}

// Group は共有の相手に選べるグループ(members はログインの名前)
export interface Group {
  name: string;
  members: string[];
}

export interface Directory {
  people: Person[];
  groups: Group[];
}

// useDirectory は、共有の相手に選べる人とグループ。開いたときに1回だけ取る
export function useDirectory(): Directory {
  const [dir, setDir] = useState<Directory>({ people: [], groups: [] });
  useEffect(() => {
    ChatService.UserNames().then(
      list => setDir({ people: (list ?? []).map(name => ({ name, display: "" })), groups: [] }),
      () => setDir({ people: [], groups: [] }),
    );
  }, []);
  return dir;
}

// personLabel は人の見せ方。広いところは「表示名(ログインの名前)」、狭いところ(short)は表示名だけ(決定事項 18)
export function personLabel(p: Person | undefined, name: string, short = false): string {
  if (!p || !p.display) return name;
  return short ? p.display : `${p.display}(${name})`;
}

// targetLabel は選んだ相手の短い見せ方(チップ・確かめの文)
export function targetLabel(t: Target, dir: Directory): string {
  if (t.kind === "group") return `${t.name} グループ`;
  return personLabel(dir.people.find(p => p.name === t.name), t.name, true);
}

// countPeople は、相手を人に広げた人数(グループは今のメンバー。自分は数えない。決定事項 8・26)
export function countPeople(targets: Target[], dir: Directory, self: string): number {
  const names = new Set<string>();
  for (const t of targets) {
    if (t.kind === "user") names.add(t.name);
    else for (const m of dir.groups.find(g => g.name === t.name)?.members ?? []) names.add(m);
  }
  names.delete(self);
  return names.size;
}

// listLabels は、相手の名前を短くつなぐ(多ければ「佐藤・鈴木 ほか3人」。決定事項 9)
export function listLabels(targets: Target[], dir: Directory, max = 2): string {
  const shown = targets.slice(0, max).map(t => targetLabel(t, dir)).join("・");
  const rest = targets.slice(max);
  if (rest.length === 0) return shown;
  return `${shown} ほか${rest.length}${rest.every(t => t.kind === "user") ? "人" : "件"}`;
}

// ---- 最近選んだ相手 --------------------------------------------------------------------

const STORED_RECENT = "aibow.recentShareTargets";
const RECENT_MAX = 5;

export function loadRecent(): Target[] {
  try {
    const v = JSON.parse(loadStored(STORED_RECENT) ?? "[]");
    return Array.isArray(v) ? v.filter(t => (t.kind === "user" || t.kind === "group") && typeof t.name === "string") : [];
  } catch {
    return [];
  }
}

// rememberRecent は、確定した相手を最近選んだ相手の先頭に足す(会話の共有とプロファイルの配るで1つ。決定事項 38)
export function rememberRecent(targets: Target[]) {
  const same = (a: Target, b: Target) => a.kind === b.kind && a.name === b.name;
  const next = [...targets, ...loadRecent().filter(r => !targets.some(t => same(t, r)))].slice(0, RECENT_MAX);
  saveStored(STORED_RECENT, JSON.stringify(next));
}
