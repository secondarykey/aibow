// 会話の一覧と会話の間の仕切り(スプリットバー)。つかんで左右に動かすと、一覧の幅が変わる。
// ダブルクリックで元の幅に戻す。幅は WebView2 の localStorage に覚えておき、次の起動でも同じ幅で始める
// (画面の見た目だけの好みなので、Go の設定(app_settings)には入れていない)。
//
// Frameless のウィンドウでは、画面のランタイムが mousedown を見てウィンドウを動かす(--wails-draggable: drag の場所)。
// 仕切りは drag の場所の中に置かないので、つかんでもウィンドウは動かない。
import { useCallback, useRef, useState } from "react";
import type { PointerEvent } from "react";

const DEFAULT_WIDTH = 230; // 一覧の既定の幅(px)
const MIN_WIDTH = 160;     // これより狭いと会話の名前がほとんど読めない
const MIN_CHAT = 280;      // 会話の側に必ず残す幅(ウィンドウの最小の幅 480 でも会話が読めるように)
const STORAGE_KEY = "aibow.listWidth";

// clamp は一覧の幅を、狭すぎず、会話の側を押しつぶさない範囲に収める
function clamp(w: number): number {
  const max = Math.max(MIN_WIDTH, window.innerWidth - MIN_CHAT);
  return Math.round(Math.min(Math.max(w, MIN_WIDTH), max));
}

// localStorage は使えないこともある(壊れている・無効にされている)ので、失敗したら既定の幅で始める
function load(): number {
  try {
    const v = Number(window.localStorage.getItem(STORAGE_KEY));
    return v > 0 ? clamp(v) : DEFAULT_WIDTH;
  } catch {
    return DEFAULT_WIDTH;
  }
}

function save(w: number) {
  try {
    window.localStorage.setItem(STORAGE_KEY, String(w));
  } catch {
    // 覚えておけなくても、今の起動の間は効いているので困らない
  }
}

// useListWidth は一覧の幅。set はドラッグ中に呼び、commit で覚えておく
export function useListWidth() {
  const [width, setWidth] = useState(load);
  const set = useCallback((w: number) => setWidth(clamp(w)), []);
  const commit = useCallback((w: number) => save(clamp(w)), []);
  const reset = useCallback(() => {
    setWidth(DEFAULT_WIDTH);
    save(DEFAULT_WIDTH);
  }, []);
  return { width, set, commit, reset };
}

export function Splitter({ width, onResize, onCommit, onReset }: {
  width: number;
  onResize(width: number): void; // ドラッグ中、動かすたびに呼ぶ
  onCommit(width: number): void; // 放したときに呼ぶ
  onReset(): void;               // ダブルクリックで元の幅に戻す
}) {
  // ドラッグを始めたときのマウスの位置と幅。ドラッグしていなければ null
  const start = useRef<{ x: number; width: number } | null>(null);
  const last = useRef(width);

  const onPointerDown = (e: PointerEvent<HTMLDivElement>) => {
    if (e.button !== 0) return;
    // ポインターを捕まえておくと、仕切りから外れて会話の上を動いても、放すまでこの要素にイベントが届く
    e.currentTarget.setPointerCapture(e.pointerId);
    start.current = { x: e.clientX, width };
    last.current = width;
    document.body.classList.add("resizing-list"); // ドラッグ中はどこでも ↔ のカーソルにし、文字が選択されないようにする
    e.preventDefault();
  };
  const onPointerMove = (e: PointerEvent<HTMLDivElement>) => {
    if (!start.current) return;
    last.current = start.current.width + e.clientX - start.current.x;
    onResize(last.current);
  };
  const end = () => {
    if (!start.current) return;
    start.current = null;
    document.body.classList.remove("resizing-list");
    onCommit(last.current);
  };

  return (
    <div
      className="splitter"
      role="separator"
      aria-orientation="vertical"
      title="ドラッグで一覧の幅を変える(ダブルクリックで元の幅に戻す)"
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={end}
      onLostPointerCapture={end}
      onDoubleClick={onReset}
    />
  );
}
