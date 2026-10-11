// imeFocus.tsx: ウィンドウを切り替えて戻ると IME(日本語入力)が効かなくなる問題の対策。
//
// Windows の WebView2 では、ウィンドウが非アクティブ → アクティブに戻ったときに、
// IME の状態(TSF のコンテキスト)が戻らないことがある。すると入力欄で変換できず、
// アルファベットがそのまま入る。
//
// 同じ入力欄に blur → focus しても直らないが、別の入力欄を経由すると直る(Tab で移動すると直るのと同じ)。
// そこで、Go から「ウィンドウに戻ってきた」(aibow:window:focus)が届いたら、
// 画面外の見えない input に一度フォーカスを移し、次のフレームで元の入力欄に戻す。
import { useEffect, useRef } from "react";
import { Events } from "@wailsio/runtime";

// main.go の windowFocusEventName と同じ名前
const windowFocusEvent = "aibow:window:focus";

// テキストを打つ入力欄か(ボタンやチェックボックスは対象外)
function isTextInput(el: Element | null): el is HTMLTextAreaElement | HTMLInputElement {
  if (el instanceof HTMLTextAreaElement) return true;
  return el instanceof HTMLInputElement && ["text", "search"].includes(el.type);
}

// 中継用の見えない input を置き、ウィンドウに戻ってきたときにフォーカスを付け直す。
// 画面に1つだけ置けばよく、入力欄が複数あっても(チャットの入力とシステムプロンプト)使える。
export function ImeFocusRelay() {
  const relay = useRef<HTMLInputElement>(null);
  const composing = useRef(false); // IME で変換中か
  const lastInput = useRef<HTMLTextAreaElement | HTMLInputElement | null>(null); // ウィンドウを離れる前の入力欄

  useEffect(() => {
    const onCompositionStart = () => (composing.current = true);
    const onCompositionEnd = () => (composing.current = false);
    // 戻ってきたときにフォーカスが body に戻っていることがあるので、離れる前の入力欄を覚えておく
    const onWindowBlur = () => {
      const active = document.activeElement;
      lastInput.current = isTextInput(active) && active !== relay.current ? active : null;
    };

    const onWindowFocus = () => {
      // 変換中にフォーカスを動かすと、入力中の文字が壊れる
      if (composing.current || !relay.current) return;
      const active = document.activeElement;
      let target: HTMLTextAreaElement | HTMLInputElement | null = null;
      if (isTextInput(active) && active !== relay.current) {
        target = active;
      } else if (!active || active === document.body) {
        target = lastInput.current;
      }
      // ボタンなど、入力欄以外にフォーカスがあるときは何もしない
      if (!target || !target.isConnected) return;

      // カーソル位置を覚えておき、中継の input を経由して戻す
      const { selectionStart, selectionEnd } = target;
      relay.current.focus();
      requestAnimationFrame(() => {
        // 戻す前にクリックなどで別の場所にフォーカスが移っていたら、奪い返さない
        // (中継からその場所への移動も「別の要素への移動」なので、そちらの IME も直っている)
        if (document.activeElement !== relay.current) return;
        target.focus();
        if (selectionStart !== null && selectionEnd !== null) target.setSelectionRange(selectionStart, selectionEnd);
      });
    };

    document.addEventListener("compositionstart", onCompositionStart, true);
    document.addEventListener("compositionend", onCompositionEnd, true);
    window.addEventListener("blur", onWindowBlur);
    const off = Events.On(windowFocusEvent, onWindowFocus);
    return () => {
      document.removeEventListener("compositionstart", onCompositionStart, true);
      document.removeEventListener("compositionend", onCompositionEnd, true);
      window.removeEventListener("blur", onWindowBlur);
      off();
    };
  }, []);

  return (
    <input
      ref={relay}
      type="text"
      tabIndex={-1}
      aria-hidden="true"
      readOnly
      style={{
        position: "fixed", top: "-9999px", left: "-9999px",
        width: 0, height: 0, opacity: 0, pointerEvents: "none",
      }}
    />
  );
}
