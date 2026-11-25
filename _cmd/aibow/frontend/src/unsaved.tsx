// unsaved.tsx: 保存していない変更(下書き)の見張りと、離れる前の知らせ(0053 の決定事項 16〜19)。
//
// 確定式の画面(プロファイル・管理のツール・会話の設定)は、画面で変えている値(下書き)と保存した値を分けて持つ。
// 比べて違えば「保存していない変更がある」(dirty)。その間に画面を離れようとしたら、知らせて選んでもらう:
//
//	「保存していない変更があります。保存しますか?」+「保存」「保存しない」「キャンセル」
//	  保存       … 画面の保存を呼び、できたら離れる(できなければ離れない。エラーは画面に出る)
//	  保存しない … 下書きを捨てて離れる
//	  キャンセル … 離れずに編集に戻る
//
// 画面は useUnsavedEntry で「今 dirty か・保存の仕方・捨て方」を登録し、離れる操作をする側(App・タブ・一覧)は
// guard(離れる処理) を通して呼ぶ。dirty が無ければ、すぐ離れる処理を呼ぶ。
// ブラウザのタブを閉じる・再読み込みは、ブラウザの決まった確かめ(beforeunload)しか出せないので、それに任せる。
// デスクトップのアプリのウィンドウを閉じるときは、Go が閉じるのを止めて知らせてくる(closeguard.go)ので、同じ知らせを出す。
//
// 下書きは画面の中だけに持つ(手元に残さない。決定事項 15)。
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { appWindow, isBrowser } from "./backend";

// Entry は、dirty な画面1つ分。save は保存できたら true(できなければ理由を画面に出して false)
interface Entry {
  save(): Promise<boolean>;
  discard(): void;
}

export interface Unsaved {
  // guard は、離れる処理 proceed を、保存していない変更があれば知らせてから呼ぶ
  guard(proceed: () => void): void;
  register(id: number, entry: Entry | null): void;
}

const noop: Unsaved = { guard: proceed => proceed(), register: () => {} };
const UnsavedContext = createContext<Unsaved>(noop);

export function useUnsaved(): Unsaved {
  return useContext(UnsavedContext);
}

let nextId = 0;

// useUnsavedEntry は、確定式の画面が自分の dirty を登録する。save・discard は毎回の描画のものを使う(古い下書きを保存しない)
export function useUnsavedEntry(dirty: boolean, save: () => Promise<boolean>, discard: () => void) {
  const { register } = useUnsaved();
  const id = useRef(0);
  if (id.current === 0) id.current = ++nextId;
  const latest = useRef({ save, discard });
  latest.current = { save, discard };
  useEffect(() => {
    register(id.current, dirty ? { save: () => latest.current.save(), discard: () => latest.current.discard() } : null);
  }, [dirty, register]);
  useEffect(() => () => register(id.current, null), [register]);
}

// UnsavedProvider は、見張りと知らせを持つ(App の外側に1つ)。
export function UnsavedProvider({ children }: { children: ReactNode }) {
  const entries = useRef(new Map<number, Entry>());
  const [count, setCount] = useState(0); // dirty な画面の数(beforeunload と Go への知らせに使う)
  const [asking, setAsking] = useState<{ proceed: () => void } | null>(null);
  const [saving, setSaving] = useState(false);

  const register = useCallback((id: number, entry: Entry | null) => {
    if (entry) entries.current.set(id, entry);
    else entries.current.delete(id);
    setCount(entries.current.size);
  }, []);
  const guard = useCallback((proceed: () => void) => {
    if (entries.current.size === 0) proceed();
    else setAsking({ proceed });
  }, []);
  const api = useMemo(() => ({ guard, register }), [guard, register]);

  const dirty = count > 0;
  useEffect(() => {
    if (isBrowser) {
      if (!dirty) return;
      // 文はブラウザが決める(今のブラウザは、ページの文を出さない)。preventDefault で確かめを出させる
      const onUnload = (e: BeforeUnloadEvent) => e.preventDefault();
      window.addEventListener("beforeunload", onUnload);
      return () => window.removeEventListener("beforeunload", onUnload);
    }
    appWindow.setUnsaved(dirty).catch(() => {});
  }, [dirty]);
  // デスクトップのアプリ:閉じようとしたのを Go が止めたら、同じ知らせを出し、離れてよければ閉じる
  useEffect(() => appWindow.onCloseRequest(() => guard(() => { appWindow.closeNow().catch(() => {}); })), [guard]);

  const choose = async (how: "save" | "discard" | "cancel") => {
    const a = asking;
    if (!a) return;
    if (how === "cancel") {
      setAsking(null);
      return;
    }
    const list = [...entries.current.values()];
    if (how === "save") {
      setSaving(true);
      let ok = true;
      for (const e of list) {
        if (!(await e.save())) ok = false;
      }
      setSaving(false);
      setAsking(null);
      if (ok) a.proceed();
      return;
    }
    for (const e of list) e.discard();
    setAsking(null);
    a.proceed();
  };

  return (
    <UnsavedContext.Provider value={api}>
      {children}
      {asking && (
        <div className="unsaved-overlay" onMouseDown={e => e.target === e.currentTarget && !saving && setAsking(null)}>
          <div className="unsaved-dialog" role="alertdialog" aria-modal="true" aria-labelledby="unsaved-text"
            onKeyDown={e => { if (e.key === "Escape" && !saving) setAsking(null); }}>
            <p id="unsaved-text">保存していない変更があります。保存しますか?</p>
            <div className="unsaved-actions">
              <button type="button" className="primary" autoFocus disabled={saving} onClick={() => choose("save")}>保存</button>
              <button type="button" disabled={saving} onClick={() => choose("discard")}>保存しない</button>
              <button type="button" disabled={saving} onClick={() => choose("cancel")}>キャンセル</button>
            </div>
          </div>
        </div>
      )}
    </UnsavedContext.Provider>
  );
}

// SaveBar は、確定式の画面の「保存」「キャンセル」(0053 の決定事項 11〜14・未決事項 4)。
// 画面の下に貼り付けておく(長い画面でも、スクロールせずに押せる)。変えていない間は2つとも押せない。
// 「キャンセル」は確かめずに保存した値に戻す(消えるのは保存していない下書きだけなので)。保存できても「保存しました」は出さない(ボタンが押せなくなれば分かる)
// scope は、保存したものがほかの人に及ぶときに「保存」の左に添える短い文(「全員に効きます」など。0063 の決定事項 8)。
// 押す直前にもう一度確かめられるようにするため。自分にだけ効く画面(会話の設定・自分のプロファイル)には渡さない
export function SaveBar({ dirty, saving, blocked, scope, onSave, onCancel }: {
  dirty: boolean;
  saving: boolean;
  blocked?: string; // 保存できない理由(生成中に上限を変えた、など)。あれば「保存」を押せず、説明に出す
  scope?: string;
  onSave(): void;
  onCancel(): void;
}) {
  return (
    <div className={"save-bar" + (dirty ? " dirty" : "")}>
      {scope && <span className="save-scope">{scope}</span>}
      <button type="button" className="primary" disabled={!dirty || saving || !!blocked} title={blocked} onClick={onSave}>保存</button>
      <button type="button" disabled={!dirty || saving} onClick={onCancel}>キャンセル</button>
    </div>
  );
}

// canonical は、下書きと保存した値を比べるための形。空の値(undefined・null・false・""・[]・{})は無いのと同じに扱い、
// 文字の配列は並びによらず同じにする(付けて外した印・Go が omitempty で落とす欄で、変えていないのに dirty にならないように)。
function canonical(v: unknown): unknown {
  if (Array.isArray(v)) {
    const items = v.map(canonical);
    return items.every(i => typeof i === "string") ? [...(items as string[])].sort() : items;
  }
  if (v && typeof v === "object") {
    const out: Record<string, unknown> = {};
    for (const k of Object.keys(v).sort()) {
      const c = canonical((v as Record<string, unknown>)[k]);
      if (!isEmpty(c)) out[k] = c;
    }
    return out;
  }
  return v;
}

function isEmpty(v: unknown): boolean {
  if (v === undefined || v === null || v === false || v === "") return true;
  if (Array.isArray(v)) return v.length === 0;
  if (typeof v === "object") return Object.keys(v as object).length === 0;
  return false;
}

// sameValue は、下書き a と保存した値 b が同じか(変えていないか)
export function sameValue(a: unknown, b: unknown): boolean {
  return JSON.stringify(canonical(a)) === JSON.stringify(canonical(b));
}
