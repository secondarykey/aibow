// stickToBottom.ts: 中身が増えたら一番下まで送る。ただし、利用者が上にスクロールして読んでいる間は送らない。
//
// 生成中は思考や答えの断片が次々に届き、そのたびに一番下へ送っていると、過去の発言を読もうとして
// 上にスクロールしても、すぐ下に引き戻されてしまう。チャットアプリでよくある「下に張り付いているときだけ追いかける」にする。
// 一番下まで戻れば、また追いかけ始める。
import { useCallback, useLayoutEffect, useRef } from "react";

// 一番下からこの px 以内なら「一番下にいる」とみなす(端数や、少しだけずれたときに追いかけるのをやめないように)
const BOTTOM_SLACK = 24;

// useStickToBottom は、スクロールする要素に付ける ref を返す。
//   content  : 変わったら(中身が増えたら)、一番下にいれば一番下まで送る
//   forceKey : 変わったら、どこを読んでいても一番下まで送る(会話を開いた・自分で送信した、など)
//
// 「一番下にいたか」は、描画のたびに「今のスクロール位置」と「前の描画のときの中身の高さ」で決める。
// scroll イベントで覚えておく方法だと、イベントが届くのは次のフレームなので、断片が数ミリ秒ごとに届く生成中は、
// 上にスクロールしたことが伝わる前に次の描画で下へ送ってしまい、引き戻されることがある。
// ref は関数の形(callback ref)にしている。要素があとから出てくる(最初の文字が届いてから表示する)場合でも使えるように。
export function useStickToBottom<T extends HTMLElement>(content: unknown, forceKey?: unknown) {
  const el = useRef<T | null>(null);
  const lastHeight = useRef(0); // 前の描画のあとの中身の高さ(scrollHeight)

  const toBottom = (e: T) => {
    e.scrollTop = e.scrollHeight;
    lastHeight.current = e.scrollHeight;
  };

  const ref = useCallback((node: T | null) => {
    el.current = node;
    if (node) toBottom(node); // 出てきたときは一番下(最新)から見せる
  }, []);

  const lastForce = useRef(forceKey);
  // 描画の直後(画面に出る前)に送るので、一瞬上の位置が見えてから下に飛ぶ、ということがない
  useLayoutEffect(() => {
    const e = el.current;
    if (!e) return;
    const forced = lastForce.current !== forceKey;
    lastForce.current = forceKey;
    // 中身が増える前の一番下に、今のスクロール位置がいたか
    const wasAtBottom = lastHeight.current - e.scrollTop - e.clientHeight <= BOTTOM_SLACK;
    if (forced || wasAtBottom) toBottom(e);
    else lastHeight.current = e.scrollHeight;
  }, [content, forceKey]);

  return ref;
}
