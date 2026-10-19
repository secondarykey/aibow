// questionsOnly.ts: 「質問だけ」の表示。返事を畳んで自分の質問だけを並べ、選んだ質問へ飛ぶ。
//
// 長い会話では、前に何を聞いたかを探すのにスクロールが長くなる。質問だけを並べれば一覧になり、
// 選んだ質問のところで元の表示に戻れば、そこから返事を読める。
//
// 返事は描画したまま CSS で隠す(表示から外さない)。外すと戻ったときに Markdown・コードの色付け・mermaid の図を
// 描き直すことになり、開いていた思考の箱なども閉じてしまうため。生成中でも、隠れたまま返事は伸びていく。
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import type { RefObject } from "react";
import { QUESTION_ATTR } from "./pinnedQuestion";

// useQuestionsOnly は「質問だけ」の表示の状態を返す。
//   el  : スクロールする要素(会話の表示)
//   key : 変わったら「質問だけ」をやめる(会話を切り替えた・自分で送信した)。そのときの位置は stickToBottom が一番下に決める
//
// 入るときは、読んでいた質問を真ん中に見せて印を付ける(current)。
// 出るときは、質問を選んだならその質問を上の端に、選ばずに出たなら入る前の位置に戻す。
export function useQuestionsOnly(el: RefObject<HTMLElement | null>, key: string) {
  // key を一緒に持ち、key が変わったら「質問だけ」ではなくなる(送信と同じ描画で戻るので、一番下へ送るのと食い違わない)
  const [state, setState] = useState<{ key: string; current: number | null } | null>(null);
  const on = state?.key === key;
  const saved = useRef(0);                   // 入る前のスクロール位置
  const target = useRef<number | null>(null); // 出たあとに見せる質問(items の添字)
  const enteredKey = useRef(key);
  const wasOn = useRef(false);

  const enter = useCallback(() => {
    const e = el.current;
    if (!e) return;
    saved.current = e.scrollTop;
    enteredKey.current = key;
    target.current = null;
    setState({ key, current: readingQuestion(e) });
  }, [el, key]);

  // jumpTo は選んだ質問(選ばずに戻るなら省く)
  const exit = useCallback((jumpTo?: number) => {
    target.current = jumpTo ?? null;
    setState(null);
  }, []);

  // 置いていかれた状態は消す(同じ会話・同じ発言の数に戻ったときに、また「質問だけ」にならないように)
  useEffect(() => {
    if (state && state.key !== key) setState(null);
  }, [state, key]);

  // 切り替えた直後(画面に出る前)に位置を決める
  useLayoutEffect(() => {
    const e = el.current;
    if (e && on && !wasOn.current) {
      if (state?.current != null) findQuestion(e, state.current)?.scrollIntoView({ block: "center" });
      else e.scrollTop = 0;
    } else if (e && !on && wasOn.current && enteredKey.current === key) {
      const q = target.current !== null ? findQuestion(e, target.current) : null;
      if (q) q.scrollIntoView({ block: "start" });
      else e.scrollTop = saved.current;
    }
    wasOn.current = on;
  }, [el, on, state, key]);

  // Esc で、選ばずに元の表示へ戻る
  useEffect(() => {
    if (!on) return;
    const onKey = (ev: KeyboardEvent) => {
      if (ev.key === "Escape" && !ev.isComposing) exit();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [on, exit]);

  return { on, current: on ? state?.current ?? null : null, enter, exit };
}

function findQuestion(e: HTMLElement, index: number) {
  return e.querySelector<HTMLElement>(`[${QUESTION_ATTR}="${index}"]`);
}

// readingQuestion は、今読んでいる返事の質問: 画面の真ん中より上にある最後の質問。無ければ null
function readingQuestion(e: HTMLElement): number | null {
  const r = e.getBoundingClientRect();
  const middle = r.top + e.clientHeight / 2;
  let found: number | null = null;
  for (const q of e.querySelectorAll<HTMLElement>(`[${QUESTION_ATTR}]`)) {
    if (q.getBoundingClientRect().top > middle) break;
    found = Number(q.getAttribute(QUESTION_ATTR));
  }
  return found;
}
