// 管理の画面(0032)。サーバー管理者の作業を、自分の設定(SettingsView)から分けて集める。
//
// 入口は左下の設定の歯車の隣の盾(管理できる人にだけ出す。ConversationList)。設定の画面と同じく、会話の場所に出す別の画面で、
// 「会話に戻る」・一覧で会話を選ぶ・「+ 新規」で閉じる。
// 画面を分けても、断るのは Go の口(管理者の口 /aibow/admin/・グループのメソッド・store の条件)。画面は出し分けるだけ(0032 の「学ぶこと」)。
//
//	利用者・グループ … ログインした管理者だけ(0032 の決定事項 4)
//	(段2 でモデル・ツール、段3 で配るプロファイルを移す)
import { useState } from "react";
import { LuArrowLeft } from "react-icons/lu";
import { isBrowser, me } from "../backend";
import { useDirectory } from "../directory";
import { UserSettings } from "./UserSettings";
import { GroupAdmin } from "./GroupSettings";

type Tab = "users" | "groups";

const TABS: { id: Tab; label: string; title: string }[] = [
  { id: "users", label: "利用者", title: "aibow-server の利用者(作る・パスワードのリンクと API キー・表示名・消す・その人の会話)" },
  { id: "groups", label: "グループ", title: "グループ(作る・名前・メンバーと長・消す)。共有の相手に選べる" },
];

// canAdmin は、管理の画面を出すか(歯車の隣の盾を出すか)。
// 段1 では中身が利用者とグループだけなので、ブラウザでログインした管理者だけ(段2 でサーバーの PC から開いた画面にも広げる。0032 の決定事項 4)
export function canAdmin(): boolean {
  return isBrowser && me.login && !!me.admin;
}

export function AdminView({ onClose, errors, onError, onReadConversation }: {
  onClose(): void;                        // 会話に戻る
  errors: { id: number; text: string }[]; // 管理の画面を開いてから出たエラー
  onError(text: string): void;
  onReadConversation(id: string): void;   // 利用者の会話を読むだけで開く(読んだことはログに残る。0018 の決定事項 25)
}) {
  const [tab, setTab] = useState<Tab>("users");
  const [dir, reloadDir] = useDirectory(); // 利用者の詳細のグループと、グループのタブで分け合う
  return (
    <section className="settings-view">
      <div className="settings-inner">
        <div className="settings-header">
          <h1>管理</h1>
          <button className="back" onClick={onClose} title="管理の画面を閉じて、会話に戻る"><LuArrowLeft /> 会話に戻る</button>
        </div>
        {errors.map(e => <div key={e.id} className="msg error">{e.text}</div>)}

        <div className="settings-tabs" role="tablist">
          {TABS.map(t => (
            <button
              key={t.id}
              role="tab"
              aria-selected={tab === t.id}
              className={"settings-tab" + (tab === t.id ? " current" : "")}
              title={t.title}
              onClick={() => setTab(t.id)}
            >
              {t.label}
            </button>
          ))}
        </div>

        {tab === "users" && <UserSettings dir={dir} reloadDir={reloadDir} onError={onError} onRead={onReadConversation} />}
        {tab === "groups" && (
          <>
            <h2 title="グループは共有の相手に選べる。長はメンバーを足す・外せる(冠を押すと長にする / 外す)">グループ</h2>
            <GroupAdmin dir={dir} reload={reloadDir} onError={onError} />
          </>
        )}
      </div>
    </section>
  );
}
