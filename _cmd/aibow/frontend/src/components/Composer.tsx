// 画面下部: 入力欄と送信・停止ボタン
import { useEffect, useRef, useState } from "react";
import type { FormEvent, KeyboardEvent } from "react";

interface Props {
  canSend: boolean;
  canStop: boolean;
  onSend(text: string): void;
  onStop(): void;
}

export function Composer({ canSend, canStop, onSend, onStop }: Props) {
  const [text, setText] = useState("");
  const input = useRef<HTMLTextAreaElement>(null);

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
    // IME で変換中の Enter は確定なので送信しない
    if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault();
      submit();
    }
  };

  return (
    <footer>
      <form className="composer" onSubmit={submit}>
        <textarea
          ref={input}
          rows={1}
          value={text}
          placeholder="メッセージを入力(Enterで送信 / Shift+Enterで改行)"
          onChange={e => setText(e.target.value)}
          onKeyDown={onKeyDown}
        />
        <button className="primary" type="submit" disabled={!canSend}>送信</button>
        <button type="button" disabled={!canStop} onClick={onStop}>停止</button>
      </form>
    </footer>
  );
}
