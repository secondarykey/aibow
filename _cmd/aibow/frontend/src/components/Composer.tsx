// 入力欄。送信ボタンは枠の中の右下に Enter のマークで置く(停止ボタンは生成中の返事の吹き出しの中にある)
import { useEffect, useRef, useState } from "react";
import { LuCornerDownLeft } from "react-icons/lu";
import type { FormEvent, KeyboardEvent } from "react";

interface Props {
  canSend: boolean;
  blockedReason?: string; // 送信できない理由(ボタンのツールチップに出す)
  home?: boolean;         // 新しい会話の画面(真ん中に出す。上の区切り線と背景を付けない)
  onSend(text: string): void;
}

export function Composer({ canSend, blockedReason, home, onSend }: Props) {
  const [text, setText] = useState("");
  const input = useRef<HTMLTextAreaElement>(null);
  const composing = useRef(false); // IME で変換中か

  // 入力に合わせて高さを伸ばす(最大 200px)
  useEffect(() => {
    const el = input.current;
    if (!el) return;
    el.style.height = "";
    el.style.height = Math.min(el.scrollHeight, 200) + "px";
  }, [text]);

  // 返事が終わったら、すぐ次を打てるように入力欄に戻る
  useEffect(() => {
    if (canSend) input.current?.focus();
  }, [canSend]);

  const submit = (e?: FormEvent) => {
    e?.preventDefault();
    const t = text.trim();
    if (!t || !canSend) return;
    setText("");
    onSend(t);
  };

  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    // IME で変換中の Enter は確定なので送信しない。
    // isComposing だけだと確定の Enter の直後を取りこぼすことがあるので、
    // compositionstart〜end の記録と、IME 処理中のキーに付く keyCode 229 でも確かめる
    if (composing.current || e.nativeEvent.isComposing || e.keyCode === 229) return;
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      submit();
    }
  };

  return (
    <footer className={home ? "home" : undefined}>
      <form className="composer" onSubmit={submit}>
        <textarea
          ref={input}
          rows={1}
          value={text}
          placeholder="メッセージを入力(Enterで送信 / Shift+Enterで改行)"
          onChange={e => setText(e.target.value)}
          onKeyDown={onKeyDown}
          onCompositionStart={() => (composing.current = true)}
          onCompositionEnd={() => (composing.current = false)}
        />
        <button
          className="send"
          type="submit"
          disabled={!canSend || !text.trim()}
          title={canSend ? "送信(Enter)" : blockedReason}
        >
          <LuCornerDownLeft />
        </button>
      </form>
    </footer>
  );
}
