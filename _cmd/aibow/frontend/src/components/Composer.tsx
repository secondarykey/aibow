// 入力欄。送信ボタンは枠の中の右下に Enter のマークで置く(停止ボタンは生成中の返事の吹き出しの中にある)
import { useEffect, useRef, useState } from "react";
import { LuCornerDownLeft } from "react-icons/lu";
import type { FormEvent, KeyboardEvent } from "react";

interface Props {
  canSend: boolean;
  blockedReason?: string; // 送信できない理由(ボタンのツールチップに出す)
  home?: boolean;         // 新しい会話の画面(真ん中に出す。上の区切り線と背景を付けない)
  focusKey?: number;      // 変わるたびに入力欄にフォーカスを移す(会話を開いた・「+ 新規」を押した、など)
  onSend(text: string): void;
}

// テキストを打つ入力欄か(ボタンやチェックボックス、スライダーは対象外)
function isTextInput(el: Element | null): boolean {
  if (el instanceof HTMLTextAreaElement) return true;
  return el instanceof HTMLInputElement && ["text", "search", "number"].includes(el.type);
}

// focusUnlessTyping は入力欄にフォーカスを移す。ただし、ほかの入力欄で打っている最中なら奪わない。
//
// 会話の名前の変更は、一覧のダブルクリックで入力欄を出す。ダブルクリックの1回目・2回目のクリックで
// 「会話を開く → 開き終わったら入力欄へ」が動くので、開き終わるのが入力欄を出したあとになり、
// 名前の入力欄からフォーカスを奪って(blur で確定扱いになり)編集が閉じてしまっていた。
// 返事が終わったときに、設定パネルでシステムプロンプトを打っている最中だった場合も同じ。
function focusUnlessTyping(el: HTMLTextAreaElement | null) {
  if (!el) return;
  const active = document.activeElement;
  if (active !== el && isTextInput(active)) return;
  el.focus();
}

export function Composer({ canSend, blockedReason, home, focusKey, onSend }: Props) {
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
    if (canSend) focusUnlessTyping(input.current);
  }, [canSend]);

  // 一覧で会話を選んだり「+ 新規」を押したりしたあと、すぐ打てるように入力欄に戻る。
  // 押したボタンにフォーカスが残るので、こちらから移す(生成中で送れなくても、打つことはできる)
  useEffect(() => {
    focusUnlessTyping(input.current);
  }, [focusKey]);

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
