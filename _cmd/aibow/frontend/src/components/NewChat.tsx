// 新しい会話の画面: 入力欄(とモデルの選択)を真ん中に出す。入力欄の上に、新しい会話のプロファイルの選択(0014)。
// 起動したときと「+ 新規」を押したときはこの画面になる。ここで最初に送ると会話ができ、いつものチャットの画面に変わる。
// 吹き出しは無いので、表示するのはエラーだけ(お知らせはトースト)。
import type { ReactNode } from "react";
import type { ProfileInfo } from "../../bindings/github.com/secondarykey/aibow";
import type { Item } from "../transcript";

export function NewChat({ items, profiles, profile, onProfile, children }: {
  items: Item[];
  profiles: ProfileInfo[]; // しまったものは選べない
  profile: string;         // 選んでいるプロファイルの ID
  onProfile(id: string): void;
  children: ReactNode;
}) {
  const usable = profiles.filter(i => !i.profile.archived && !i.profile.withdrawn);
  // 自分のものと、配られたもの(0018 の 11-1)を分けて出す。管理者が配っているものも「配っている」のまとまり
  const own = usable.filter(i => !i.profile.share);
  const shared = usable.filter(i => i.profile.share);
  const option = (i: ProfileInfo) => <option key={i.profile.id} value={i.profile.id}>{i.profile.name}</option>;
  return (
    <section className="center home">
      <div className="home-inner">
        <div className="home-notes">
          {items.map(it => {
            if (it.kind === "error") return <div key={it.id} className="msg error">{it.text}</div>;
            return null;
          })}
        </div>
        {usable.length > 1 && (
          <div className="home-profile">
            <select
              value={profile}
              title="新しい会話のプロファイル。選ぶと、その値(モデルも)で会話を作る"
              onChange={e => onProfile(e.target.value)}
            >
              {shared.length === 0
                ? own.map(option)
                : (
                  <>
                    <optgroup label="自分のプロファイル">{own.map(option)}</optgroup>
                    <optgroup label="配られたプロファイル">{shared.map(option)}</optgroup>
                  </>
                )}
            </select>
          </div>
        )}
        {children}
      </div>
    </section>
  );
}
