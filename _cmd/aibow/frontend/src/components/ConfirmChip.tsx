// ConfirmChip は、ツール(かグループ)の「確認しない」を切り替える小さな枠(0010)。
// 「確認しない」のときは橙で出したままにし、そうでないときは行にマウスを乗せたときだけ薄く出す(style.css の .tool-confirm)。
// ツールの行は label なので、押したときに行のチェック(使う / 使わない)まで切り替わらないよう、既定の動きを止める。
import type { MouseEvent } from "react";

export function ConfirmChip({ allow, title, onToggle }: {
  allow: boolean; // 今「確認しない」か
  title: string;
  onToggle(allow: boolean): void;
}) {
  const click = (e: MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
    onToggle(!allow);
  };
  return (
    <button type="button" className={"tool-confirm" + (allow ? " on" : "")} title={title} onClick={click}>
      確認しない
    </button>
  );
}
