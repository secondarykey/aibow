// ScopeNote は、設定・管理・会話の設定の画面の見出し(タブがあればタブ)のすぐ下に出す「効く範囲の帯」(0063 の決定事項 1・5〜7)。
// 3つの画面は同じ部品でできていて見分けにくく、同じ入力欄でも効く範囲(この会話だけ・これから作る会話・ほかの人の会話)が違う。
// 間違えたときの被害が範囲で違うので、その画面で変えたものがどこに効くかを1行で書き、使う人が確かめられるようにする。
// 管理の画面では注意の色にする(admin。ほかの人に効く画面であることを色でも分ける。決定事項 3・6)。消す・畳む設定は持たない(いつも出す)
import { LuInfo } from "react-icons/lu";

export function ScopeNote({ text, admin }: { text: string; admin?: boolean }) {
  return (
    <p className={"scope-note" + (admin ? " admin" : "")}>
      <LuInfo className="icon" aria-hidden />
      <span>{text}</span>
    </p>
  );
}
