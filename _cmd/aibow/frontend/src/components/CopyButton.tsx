// コピーのボタン。返事の最後(答え全体)と、コードブロックの右上(そのコードだけ)で使う。
// 文字は出さずアイコンだけにし、マウスを乗せると title の「コピー」が出る。押したら少しの間 ✓ のアイコンにする。
//
// 書き方(navigator.clipboard と、使えないときの Wails のクリップボード)は Go を呼ぶ係の copyText(backend.ts)。
import { useEffect, useState } from "react";
import { copyText } from "../backend";
import { LuCheck, LuCopy } from "react-icons/lu";

export function CopyButton({ text, className = "reply-action" }: { text: string; className?: string }) {
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    if (!copied) return;
    const timer = window.setTimeout(() => setCopied(false), 1500);
    return () => window.clearTimeout(timer);
  }, [copied]);
  const copy = async () => {
    await copyText(text);
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
