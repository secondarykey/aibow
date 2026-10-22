// ドロップダウン付きのボタン(スプリットボタン)。本体を押すといつもの動き、横の ▾ でほかの動きのメニューを出す。
// メニューの外を押す・Esc・項目を選ぶと閉じる
import { useEffect, useRef, useState } from "react";
import type { ReactNode, RefObject } from "react";
import { LuChevronDown } from "react-icons/lu";

export interface MenuItem {
  label: string;
  onSelect(): void;
}

export function SplitButton({ children, menuLabel, items }: {
  children: ReactNode; // 本体のボタン(いつもの動き)
  menuLabel: string;   // ▾ にマウスを乗せたときの説明
  items: MenuItem[];
}) {
  const [open, setOpen] = useState(false);
  const box = useRef<HTMLDivElement>(null);
  useMenuDismiss(open, () => setOpen(false), box);

  return (
    <div className="split" ref={box}>
      {children}
      <button
        className="reply-action split-caret"
        onClick={() => setOpen(o => !o)}
        aria-haspopup="menu"
        aria-expanded={open}
        title={menuLabel}
        aria-label={menuLabel}
      >
        <LuChevronDown />
      </button>
      {open && (
        <div className="split-menu" role="menu">
          {items.map(item => (
            <button key={item.label} role="menuitem" onClick={() => { setOpen(false); item.onSelect(); }}>
              {item.label}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

// useMenuDismiss は、開いているメニューを、box の外を押す・Esc で閉じる(SplitButton と、一覧の下の利用者のメニュー)。
export function useMenuDismiss(open: boolean, close: () => void, box: RefObject<HTMLElement | null>) {
  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (!box.current?.contains(e.target as Node)) close();
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") close();
    };
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [open, close, box]);
}
