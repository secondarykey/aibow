// 管理の画面(0032)。サーバー管理者の作業を、自分の設定(SettingsView)から分けて集める。
//
// 入口は左下の設定の歯車の隣の盾(管理できる人にだけ出す。ConversationList)。設定の画面と同じく、会話の場所に出す別の画面で、
// 「会話に戻る」・一覧で会話を選ぶ・「+ 新規」で閉じる。
// 画面を分けても、断るのは Go の口(管理者の口 /_aibow/admin/・グループのメソッド・store の条件)。画面は出し分けるだけ(0032 の「学ぶこと」)。
//
//	利用者・グループ … ログインした管理者だけ(0032 の決定事項 4)
//	モデル          … サーバーの PC の事情(読み込む / 降ろす・GPU の層数・置き場所・使える。0015 の 8-4・0018 の決定事項 8)
//	ツール          … ブラウザの会話のツールの扱いの、aibow-server 全体の既定(0024 の決定事項 21)
//	配るプロファイル … 配っているプロファイルの値・決まり・配る相手・資料のフォルダ・写して配る(ログインした管理者だけ。0032 の段3)
//	使用量          … 利用者ごとの生成の回数・GPU の秒数・生成トークン(ログインした管理者と、ログインなしの形のサーバーの PC。0033)
import { useState } from "react";
import { LuArrowLeft } from "react-icons/lu";
import type { ServerInfo } from "../../bindings/github.com/secondarykey/aibow/mcp/client";
import { isBrowser, me } from "../backend";
import { useDirectory } from "../directory";
import type { Models } from "../useModels";
import { UserSettings } from "./UserSettings";
import { GroupAdmin } from "./GroupSettings";
import { ModelSettings } from "./ModelSettings";
import { ServerTools } from "./ServerTools";
import { ProfileSettings } from "./ProfileSettings";
import { UsageAdmin } from "./UsageSettings";
import type { ConversationSettings } from "../../bindings/github.com/secondarykey/aibow";

type Tab = "users" | "groups" | "models" | "tools" | "profiles" | "usage";

const TABS: { id: Tab; label: string; title: string; admin: boolean }[] = [
  { id: "users", label: "利用者", title: "aibow-server の利用者(作る・パスワードのリンクと API キー・表示名・消す・その人の会話)", admin: true },
  { id: "groups", label: "グループ", title: "グループ(作る・名前・メンバーと長・消す)。共有の相手に選べる", admin: true },
  { id: "models", label: "モデル", title: "サーバーの PC のモデル(読み込む / 降ろす・GPU の層数・置き場所・使える)", admin: false },
  { id: "tools", label: "ツール", title: "ブラウザの会話のツールの扱い(使わない・確認する・確認しない)。aibow-server 全体の既定", admin: false },
  { id: "profiles", label: "配るプロファイル", title: "組織に配るプロファイル(値と決まり・配る相手・資料のフォルダ・写して配る)", admin: true },
  { id: "usage", label: "使用量", title: "利用者ごとの生成の回数・GPU の秒数・生成トークン(今日・7日・30日)", admin: true },
];

// canAdmin は、管理の画面を出すか(歯車の隣の盾を出すか)。ブラウザで、管理者か、サーバーの PC の上から開いた画面(manage。Go の Caller.ManagesPC)。
// 0032 の決定事項 4。アプリ(serverUrl)からは開かない(決定事項 9)
export function canAdmin(manage: boolean): boolean {
  return isBrowser && manage;
}

// adminUser は、ログインした管理者か(利用者とグループのタブを出すか。ログインの要らない形には利用者がいない)
function adminUser(): boolean {
  return me.login && !!me.admin;
}

export function AdminView({ onClose, errors, onError, onReadConversation, models, servers, appDisabled, onToolsChanged, onApplyLimits, onProfilesChanged }: {
  onClose(): void;                        // 会話に戻る
  errors: { id: number; text: string }[]; // 管理の画面を開いてから出たエラー
  onError(text: string): void;
  onReadConversation(id: string): void;   // 利用者の会話を読むだけで開く(読んだことはログに残る。0018 の決定事項 25)
  models: Models;                         // モデルの一覧・読み込む / 降ろす・層の数(モデルのタブ)
  servers: ServerInfo[];                  // server.json の MCP のサーバー(ツールのタブ)
  appDisabled: boolean;                   // 生成中・読み込み中は、読み込む / 降ろす・層の数を変えられない
  onToolsChanged(): void;                 // ツールの扱いを変えた(メーターと、会話で選べる MCP のサーバーを読み直す)
  // 配るプロファイルの会話の上限・返事の上限(作業領域を作り直してみてから save で保存する。設定の画面のプロファイルと同じ)
  onApplyLimits(history: number, reply: number, save: (patch: Partial<ConversationSettings>) => void): void;
  onProfilesChanged(): void;              // プロファイルを変えた(新しい会話の画面の写しを合わせ直す)
}) {
  // 使用量は、ログインなしの形(利用者は local だけ)でも出す(管理の画面を開けるのはサーバーの PC の上の画面。0033 の決定事項 13)
  const tabs = TABS.filter(t => !t.admin || adminUser() || (t.id === "usage" && !me.login));
  const [tab, setTab] = useState<Tab>(tabs[0]?.id ?? "models");
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
          {tabs.map(t => (
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
        {tab === "usage" && <UsageAdmin onError={onError} />}
        {tab === "models" && <ModelSettings models={models} disabled={appDisabled} onError={onError} />}
        {tab === "tools" && <ServerTools servers={servers} manage={models.manage} onChanged={onToolsChanged} />}
        {tab === "profiles" && (
          <>
            <h2 title="組織に配るプロファイル。値と決まりは配った相手の会話に効き、ほかの管理者も変えられる">配るプロファイル</h2>
            <ProfileSettings
              mode="distributed"
              models={models.models}
              servers={servers}
              disabled={appDisabled}
              onApplyLimits={onApplyLimits}
              onError={onError}
              onChanged={onProfilesChanged}
              onToolsChanged={onToolsChanged}
            />
          </>
        )}
      </div>
    </section>
  );
}
