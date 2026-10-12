// toast.tsx: 右上に出して数秒で消えるお知らせ(トースト)。
//
// モデルの読み込みや設定の変更など、会話の中身ではないお知らせはここに出す(会話の表示には混ぜない)。
// エラーは見落とされないよう、今までどおり会話の表示に出す(transcript.ts の addError)。
import { useCallback, useRef, useState } from "react";
import { LuLoaderCircle } from "react-icons/lu";

// 普通のトーストが消えるまでの時間
const TOAST_MS = 4000;

export interface ToastItem {
  id: number;
  text: string;
  key?: string;     // 同じ key のトーストは置き換える(「読み込み中…」→「読み込みました」)
  sticky?: boolean; // 消さずに出したままにする(終わるまで時間がかかる処理。くるくる回るアイコンを付ける)
}

export interface NotifyOptions {
  key?: string;
  sticky?: boolean;
  ms?: number; // 消えるまでの時間(既定 TOAST_MS)。読む量が多いお知らせは長くする。クリックでも消える
}

export interface Notifier {
  notify(text: string, opts?: NotifyOptions): void;
  dismissKey(key: string): void; // 出したままのトーストを、置き換えずに消す(失敗したときなど)
}

export function useToasts(): Notifier & { toasts: ToastItem[]; dismiss(id: number): void } {
  const [toasts, setToasts] = useState<ToastItem[]>([]);
  const nextId = useRef(1);
  const timers = useRef(new Map<number, number>());

  const dismiss = useCallback((id: number) => {
    window.clearTimeout(timers.current.get(id));
    timers.current.delete(id);
    setToasts(prev => prev.filter(t => t.id !== id));
  }, []);

  const notify = useCallback((text: string, opts: NotifyOptions = {}) => {
    const id = nextId.current++;
    setToasts(prev => {
      // 同じ key のものは消して、新しいものに置き換える
      for (const t of prev) {
        if (opts.key && t.key === opts.key) {
          window.clearTimeout(timers.current.get(t.id));
          timers.current.delete(t.id);
        }
      }
      return [...prev.filter(t => !(opts.key && t.key === opts.key)), { id, text, ...opts }];
    });
    if (!opts.sticky) timers.current.set(id, window.setTimeout(() => dismiss(id), opts.ms ?? TOAST_MS));
  }, [dismiss]);

  const dismissKey = useCallback((key: string) => {
    setToasts(prev => {
      for (const t of prev) {
        if (t.key === key) {
          window.clearTimeout(timers.current.get(t.id));
          timers.current.delete(t.id);
        }
      }
      return prev.filter(t => t.key !== key);
    });
  }, []);

  return { toasts, notify, dismissKey, dismiss };
}

export function Toasts({ toasts, onDismiss }: { toasts: ToastItem[]; onDismiss(id: number): void }) {
  return (
    <div className="toasts">
      {toasts.map(t => (
        <div key={t.id} className="toast" onClick={() => onDismiss(t.id)}>
          {t.sticky && <LuLoaderCircle className="spin" />}
          <span>{t.text}</span>
        </div>
      ))}
    </div>
  );
}
