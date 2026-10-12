// 新しい会話の画面: 入力欄(とモデルの選択)を真ん中に出す。
// 起動したときと「+ 新規」を押したときはこの画面になる。ここで最初に送ると会話ができ、いつものチャットの画面に変わる。
// 吹き出しは無いので、表示するのはモデルの読み込みのメモやエラーだけ。
import type { ReactNode } from "react";
import type { Item } from "../transcript";

export function NewChat({ items, children }: { items: Item[]; children: ReactNode }) {
  return (
    <section className="center home">
      <div className="home-inner">
        <div className="home-notes">
          {items.map(it => {
            if (it.kind === "note") return <div key={it.id} className="note">— {it.text} —</div>;
            if (it.kind === "error") return <div key={it.id} className="msg error">{it.text}</div>;
            return null;
          })}
        </div>
        {children}
      </div>
    </section>
  );
}
