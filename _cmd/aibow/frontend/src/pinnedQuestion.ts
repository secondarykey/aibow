// pinnedQuestion.ts: 長い返事をスクロールして読んでいる間、その返事の質問(ユーザーの発言)を右上に出しておく。
//
// 返事が長いと、スクロールしているうちに何を聞いたのかが画面の外に出て分からなくなる。
// 「今いちばん上に見えている返事」の質問を探し、その吹き出しが画面の上に隠れているときだけ返す。
//
// 発言の吹き出しそのものを position: sticky で止める方法もあるが、sticky は親の箱の中でしか止まらない
// (会話の表示は発言と返事が平らに並ぶので、吹き出しの親には返事が入っていない)。
// また、長い質問がそのまま止まると返事が読めなくなる。そこで、吹き出しは動かさずに、短く切った写しを別に出す。
import { useCallback, useLayoutEffect, useRef, useState } from "react";

// 質問の吹き出しに付ける印(data-question に items の中の位置を入れる)
export const QUESTION_ATTR = "data-question";

// 次の質問の吹き出しが上の端からこの px 以内まで上がってきたら、前の質問の写しを消す。
// 前の返事はもうほとんど見えておらず、右上の写しが次の質問の吹き出し(同じく右寄せ)に重なるため。写しの3行分くらい
const NEXT_SLACK = 80;

// usePinnedQuestion は、スクロールする要素に付ける ref と、右上に出す質問の位置(items の添字。出さないなら null)を返す。
//   enabled : 設定でオフなら探さない
//   content : 変わったら探し直す(会話を切り替えた・発言が増えた・返事が伸びた)
//
// 位置は scroll イベントのたびに、吹き出しの位置を上から順に見て決める(数十個なので毎回見ても軽い)。
// 「いちばん上に見えている返事の質問」は、画面の上の端より上にある最後の吹き出し。
// 吹き出しが上の端にかかっている(一部が見えている)ときと、次の質問が上の端の近くまで来ているときは出さない。
export function usePinnedQuestion<T extends HTMLElement>(enabled: boolean, content: unknown) {
  const el = useRef<T | null>(null);
  const [pinned, setPinned] = useState<number | null>(null);
  // update と ref は作り直さない(ref が変わると、ほかの ref と合わせて付け直され、stickToBottom が一番下へ送ってしまう)
  const enabledRef = useRef(enabled);
  enabledRef.current = enabled;

  const update = useCallback(() => {
    const e = el.current;
    if (!enabledRef.current || !e) {
      setPinned(null);
      return;
    }
    const top = e.getBoundingClientRect().top;
    let found: number | null = null;
    for (const q of e.querySelectorAll<HTMLElement>(`[${QUESTION_ATTR}]`)) {
      const r = q.getBoundingClientRect();
      if (r.top >= top) {
        // ここから下の吹き出しは見えている(か、まだ下にある)
        if (r.top < top + NEXT_SLACK) found = null;
        break;
      }
      found = r.bottom <= top ? Number(q.getAttribute(QUESTION_ATTR)) : null;
    }
    setPinned(found);
  }, []);

  // ref は関数の形(stickToBottom と同じ)。要素が変わったら scroll を付け直す
  const detach = useRef<(() => void) | null>(null);
  const ref = useCallback((node: T | null) => {
    detach.current?.();
    detach.current = null;
    el.current = node;
    if (node) {
      node.addEventListener("scroll", update, { passive: true });
      detach.current = () => node.removeEventListener("scroll", update);
    }
    update();
  }, [update]);

  // 中身が変わったあと(スクロールしなくても、吹き出しの位置が変わることがある)と、設定を変えたとき
  useLayoutEffect(update, [content, enabled, update]);

  return { ref, pinned };
}
