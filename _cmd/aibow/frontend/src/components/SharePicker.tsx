// SharePicker は、共有の範囲と相手を選ぶ部品(0031 の決定事項 6〜13)。会話の「共有」とプロファイルの「配る」の両方で使う。
//
// 同じ「人を選ぶ」が2か所に別々にあると、直すときに片方を忘れる。部品にして使い回す。
// 選んでいる間は下書きで、確定のボタンを押すまで保存しない(チェック1つごとに保存すると、途中の状態が相手に見えるため。決定事項 8)。
// 「指定した人」に切り替えても、だれも選ばない(決定事項 7。以前は名前順の先頭の人を選んでいて、選んだつもりのない人に共有されていた)。
//
//	共有  [ 指定した人 ▼ ]  5人に共有中:営業 グループ・佐藤 花子 ほか1人   … 閉じているときは今の共有を1行で(決定事項 10)
//	  [営業 グループ ×] [佐藤 花子 ×] [tanaka ×]                          … 選んだ相手のチップ
//	  [ 名前・グループで探す…   ]                                          … 打つと候補(最近選んだ相手が先頭。決定事項 12)
//	  佐藤・鈴木 ほか3人 に共有します。                                    … 確かめの文(決定事項 9・11)
//	  [ 共有する(5人) ]  [ やめる ]
import { useMemo, useRef, useState } from "react";
import type { KeyboardEvent } from "react";
import { LuUsers, LuX } from "react-icons/lu";
import { countPeople, listLabels, loadRecent, personLabel, rememberRecent, targetLabel, type Directory, type Target } from "../directory";

// ShareWords は、会話の共有とプロファイルの配るで違う言葉
export interface ShareWords {
  none: string;                          // 範囲の「非公開」/「配らない」
  commitUsers(n: number): string;        // 確定のボタン(指定した人)
  commitAll: string;                     // 確定のボタン(全員)
  commitNone: string;                    // 確定のボタン(非公開)
  willUsers(names: string): string;      // 確かめの文(指定した人)
  willAll: string;
  willNone: string;
  becomesNone: string;                   // 相手を全員外したとき(決定事項 11)
  nowUsers(n: number, names: string): string; // 閉じているときの1行
  nowAll: string;
  nowNone: string;
}

export const CONVERSATION_WORDS: ShareWords = {
  none: "非公開",
  commitUsers: n => `共有する(${n}人)`,
  commitAll: "全員に共有する",
  commitNone: "非公開にする",
  willUsers: names => `${names} に共有します。`,
  willAll: "全員に共有します。",
  willNone: "非公開にします。",
  becomesNone: "相手がいないので、非公開になります。",
  nowUsers: (n, names) => `${n}人に共有中:${names}`,
  nowAll: "全員に共有中",
  nowNone: "",
};

export const PROFILE_WORDS: ShareWords = {
  none: "配らない",
  commitUsers: n => `配る(${n}人)`,
  commitAll: "全員に配る",
  commitNone: "配るのをやめる",
  willUsers: names => `${names} に配ります。`,
  willAll: "全員に配ります。",
  willNone: "配るのをやめます。",
  becomesNone: "相手がいないので、配らなくなります。",
  nowUsers: (n, names) => `${n}人に配っています:${names}`,
  nowAll: "全員に配っています",
  nowNone: "",
};

// Next は確定しようとしている共有(notes に渡す)
export interface Next {
  share: string; // 相手を全員外したときは ""
  targets: Target[];
}

interface Props {
  id: string;         // 範囲の select の id(label の htmlFor)
  label: string;      // 共有 / 配る
  labelTitle: string; // 見出しの吹き出し
  words: ShareWords;
  share: string;      // 今の範囲(""・all・users)
  users: string[];    // 今の相手(人)
  groups: string[];   // 今の相手(グループ)
  dir: Directory;
  self: string;       // 自分(候補に出さず、人数にも数えない)
  notes?(next: Next): string[]; // 確かめの文に足すこと(ツールの結果も見える、など)
  onCommit(share: string, users: string[], groups: string[]): void;
}

const same = (a: Target, b: Target) => a.kind === b.kind && a.name === b.name;

export function SharePicker({ id, label, labelTitle, words, share, users, groups, dir, self, notes, onCommit }: Props) {
  const current = useMemo<Target[]>(
    () => [...groups.map(name => ({ kind: "group" as const, name })), ...users.map(name => ({ kind: "user" as const, name }))],
    [users, groups],
  );
  const [draft, setDraft] = useState<{ share: string; targets: Target[] } | null>(null); // 選んでいる間だけ
  const editing = draft !== null;
  const shownShare = draft?.share ?? share;

  const start = (next: string) => {
    // 指定した人に切り替えても、だれも選ばない(決定事項 7)。もとから指定した人なら、今の相手から始める
    setDraft({ share: next, targets: next === "users" && share === "users" ? current : [] });
  };
  const cancel = () => setDraft(null);

  const anyone = dir.people.some(p => p.name !== self) || dir.groups.length > 0;
  return (
    <div className="share-picker">
      <div className="share-line">
        <label htmlFor={id} title={labelTitle}>{label}</label>
        <select id={id} value={shownShare} onChange={e => start(e.target.value)}>
          <option value="">{words.none}</option>
          <option value="all">全員</option>
          <option value="users" disabled={!anyone}>指定した人</option>
        </select>
        {!editing && <ShareNow share={share} targets={current} dir={dir} self={self} words={words} onEdit={() => start(share)} />}
      </div>
      {draft && (
        <ShareEditor
          draft={draft}
          share={share}
          current={current}
          dir={dir}
          self={self}
          words={words}
          notes={notes}
          onChange={targets => setDraft({ ...draft, targets })}
          onCancel={cancel}
          onCommit={next => {
            setDraft(null);
            if (next.share === "users") rememberRecent(next.targets);
            onCommit(
              next.share,
              next.targets.filter(t => t.kind === "user").map(t => t.name),
              next.targets.filter(t => t.kind === "group").map(t => t.name),
            );
          }}
        />
      )}
    </div>
  );
}

// ShareNow は、閉じているときの今の共有の1行(決定事項 10)。指定した人なら、押すと相手を変える
function ShareNow({ share, targets, dir, self, words, onEdit }: { share: string; targets: Target[]; dir: Directory; self: string; words: ShareWords; onEdit(): void }) {
  if (share === "all") return <span className="share-now">{words.nowAll}</span>;
  if (share !== "users") return words.nowNone ? <span className="share-now">{words.nowNone}</span> : null;
  return (
    <>
      <span className="share-now" title={targets.map(t => targetLabel(t, dir)).join("・")}>
        {words.nowUsers(countPeople(targets, dir, self), listLabels(targets, dir))}
      </span>
      <button type="button" className="share-edit" onClick={onEdit}>相手を変える</button>
    </>
  );
}

function ShareEditor({ draft, share, current, dir, self, words, notes, onChange, onCancel, onCommit }: {
  draft: { share: string; targets: Target[] };
  share: string;
  current: Target[];
  dir: Directory;
  self: string;
  words: ShareWords;
  notes?(next: Next): string[];
  onChange(targets: Target[]): void;
  onCancel(): void;
  onCommit(next: Next): void;
}) {
  const targets = draft.share === "users" ? draft.targets : [];
  // 相手を全員外すと非公開(配らない)。もとから指定した人のときだけ確定できる(新しく切り替えて、だれも選ばずに確定はできない。決定事項 7・11)
  const next: Next = { share: draft.share === "users" && targets.length === 0 ? "" : draft.share, targets };
  const changed = next.share !== share || (share === "users" && (targets.length !== current.length || targets.some(t => !current.some(c => same(c, t)))));
  const blocked = draft.share === "users" && targets.length === 0 && share !== "users";
  const n = countPeople(targets, dir, self);

  let will = "";
  if (draft.share === "users" && targets.length === 0) will = share === "users" ? words.becomesNone : "";
  else if (next.share === "users") will = words.willUsers(listLabels(targets, dir));
  else if (next.share === "all") will = words.willAll;
  else will = words.willNone;
  const sentences = changed && !blocked ? [will, ...(notes?.(next) ?? [])].filter(Boolean) : [];

  const button = next.share === "users" ? words.commitUsers(n) : next.share === "all" ? words.commitAll : words.commitNone;
  return (
    <div className="share-editor">
      {draft.share === "users" && (
        <>
          {targets.length > 0 && (
            <div className="pick-chips">
              {targets.map(t => (
                <span key={t.kind + ":" + t.name} className={"pick-chip" + (t.kind === "group" ? " group" : "")} title={t.kind === "group" ? groupTitle(t.name, dir) : personLabel(dir.people.find(p => p.name === t.name), t.name)}>
                  {t.kind === "group" && <LuUsers className="icon" />}
                  <span className="pick-name">{targetLabel(t, dir)}</span>
                  <button type="button" className="pick-remove" aria-label={`${targetLabel(t, dir)} を外す`} onClick={() => onChange(targets.filter(x => !same(x, t)))}><LuX /></button>
                </span>
              ))}
            </div>
          )}
          <TargetSearch dir={dir} self={self} chosen={targets} onPick={t => onChange([...targets, t])} />
        </>
      )}
      {sentences.length > 0 && <div className="share-confirm">{sentences.map(s => <span key={s}>{s}</span>)}</div>}
      <div className="share-actions">
        <button type="button" className="primary" disabled={!changed || blocked} onClick={() => onCommit(next)}>{button}</button>
        <button type="button" onClick={onCancel}>やめる</button>
      </div>
    </div>
  );
}

function groupTitle(name: string, dir: Directory): string {
  const g = dir.groups.find(g => g.name === name);
  if (!g) return `${name} グループ`;
  return `${name} グループ(${g.members.length}人): ${g.members.map(m => personLabel(dir.people.find(p => p.name === m), m, true)).join("・")}`;
}

// Candidate は候補の1つ
interface Candidate {
  target: Target;
  label: string;  // 候補の見せ方(人は「表示名(ログインの名前)」)
  recent: boolean;
}

// TargetSearch は、名前・グループで探して足す欄。候補は全員とグループを画面で絞る(表示名とログインの名前の両方で引く。決定事項 13)。
// 最近選んだ相手を候補の先頭に出す(決定事項 12)。↑↓ で選び、Enter で足す。Esc で候補を閉じる
export function TargetSearch({ dir, self, chosen, onPick, groups = true, placeholder }: {
  dir: Directory;
  self: string;
  chosen: Target[];
  onPick(t: Target): void;
  groups?: boolean;      // グループも候補に出すか(グループの長がメンバーを足すときは人だけ)
  placeholder?: string;
}) {
  const [query, setQuery] = useState("");
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(0);
  const composing = useRef(false);

  const candidates = useMemo(() => {
    const q = query.trim().toLowerCase();
    const recent = loadRecent();
    const isRecent = (t: Target) => recent.some(r => same(r, t));
    const all: Candidate[] = [
      ...(groups ? dir.groups.map(g => ({ target: { kind: "group" as const, name: g.name }, label: `${g.name} グループ`, keys: [g.name] })) : []),
      ...dir.people.filter(p => p.name !== self).map(p => ({ target: { kind: "user" as const, name: p.name }, label: personLabel(p, p.name), keys: [p.name, p.display] })),
    ]
      .filter(c => !chosen.some(t => same(t, c.target)))
      .filter(c => q === "" || c.keys.some(k => k.toLowerCase().includes(q)))
      .map(c => ({ target: c.target, label: c.label, recent: isRecent(c.target) }));
    // 最近選んだ相手を、選んだ順のまま先頭に
    const order = (c: Candidate) => (c.recent ? recent.findIndex(r => same(r, c.target)) : recent.length);
    return all.sort((a, b) => order(a) - order(b));
  }, [query, dir, self, chosen, groups]);

  const pick = (c: Candidate | undefined) => {
    if (!c) return;
    onPick(c.target);
    setQuery("");
    setActive(0);
  };
  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (composing.current || e.nativeEvent.isComposing || e.keyCode === 229) return;
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setOpen(true);
      setActive(a => Math.min(a + 1, candidates.length - 1));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setActive(a => Math.max(a - 1, 0));
    } else if (e.key === "Enter") {
      e.preventDefault();
      pick(candidates[active]);
    } else if (e.key === "Escape" && open) {
      e.stopPropagation(); // 重ねて出した画面を Esc で閉じる仕組み(App.tsx)まで届けない
      setOpen(false);
    }
  };
  const firstOther = candidates.findIndex(c => !c.recent);
  return (
    <div className="pick-search">
      <input
        type="text"
        value={query}
        placeholder={placeholder ?? (groups ? "名前・グループで探す…" : "名前で探す…")}
        onChange={e => { setQuery(e.target.value); setOpen(true); setActive(0); }}
        onFocus={() => setOpen(true)}
        onBlur={() => setOpen(false)}
        onKeyDown={onKeyDown}
        onCompositionStart={() => (composing.current = true)}
        onCompositionEnd={() => (composing.current = false)}
      />
      {open && candidates.length > 0 && (
        <ul className="pick-list" role="listbox">
          {candidates.map((c, i) => (
            <li key={c.target.kind + ":" + c.target.name} role="option" aria-selected={i === active}>
              {i === 0 && c.recent && <div className="pick-section">最近選んだ相手</div>}
              {i === firstOther && firstOther > 0 && <div className="pick-section">すべて</div>}
              <button
                type="button"
                className={"pick-item" + (i === active ? " active" : "")}
                onMouseDown={e => e.preventDefault()} // 押しても欄のフォーカスを外さない(候補が閉じないように)
                onMouseEnter={() => setActive(i)}
                onClick={() => pick(c)}
              >
                {c.target.kind === "group" && <LuUsers className="icon" />}
                {c.label}
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
