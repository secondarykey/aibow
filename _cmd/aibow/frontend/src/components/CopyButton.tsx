// コピーのボタン。返事の最後(答え全体)と、コードブロックの右上(そのコードだけ)で使う。
// 文字は出さずアイコンだけにし、マウスを乗せると title の「コピー」が出る。押したら少しの間 ✓ のアイコンにする。
//
// まずブラウザの navigator.clipboard を使う(WebView2 の画面は http://wails.localhost で、安全な場所として扱われるので使える)。
// 使えなかったときは、Wails のランタイムの Clipboard.SetText(Go 側で OS のクリップボードに書く)を使う
import { useEffect, useState } from "react";
import { Clipboard } from "@wailsio/runtime";
import { LuCheck, LuCopy } from "react-icons/lu";

export function CopyButton({ text, className = "reply-action" }: { text: string; className?: string }) {
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    if (!copied) return;
    const timer = window.setTimeout(() => setCopied(false), 1500);
    return () => window.clearTimeout(timer);
  }, [copied]);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(text);
    } catch {
      await Clipboard.SetText(text);
    }
    setCopied(true);
  };
  return (
    <button
      className={className}
      onClick={() => copy().catch(() => {})}
      title={copied ? "コピーしました" : "コピー"}
      aria-label={copied ? "コピーしました" : "コピー"}
    >
      {copied ? <LuCheck /> : <LuCopy />}
    </button>
  );
}
