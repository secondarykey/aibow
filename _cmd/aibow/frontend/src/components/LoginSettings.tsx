// ログインの画面から開く設定(ログインの前。デスクトップのアプリだけ。0047)。
//
// ログインの前に触れるのは、その PC に置いたものだけ:
//   - 「基本設定」:画面の明るさ。この画面の見た目を変えるだけで、サーバーには送らない(手元の写し localStorage を変える。theme.ts)。
//     サーバーモードでは明るさは人ごとに aibow-server にあり(0047 の未決事項 1 の案 A)、ログインしたらその人の値で描き直す。
//     将来、言語の設定を作ったときも、ここで切り替えられるようにしておく(2026-11-21、ユーザーの指示)
//   - 「サーバー」:つなぎ先と「接続」・アプリモードに戻す(connection.json。ふだんの設定の画面と同じ ServerSettings)
//
// まだ誰か分からない人が触る画面なので、サーバーに何かを頼む操作は置かない(「接続」は、サーバーがあるかを確かめるだけ)。
// ふだんの設定の画面(SettingsView)は、ログインしたあとの App の状態(設定・モデル・MCP のサーバーの一覧など)を前提にしているので使い回さず、
// この小さい入れ物を別に作る(0047 の決定事項 11)。
// 「起動し直してください」の画面(RestartGate)からも開く。そのときは「サーバー」のタブだけ(serverOnly。0051 の決定事項 4)
import { useCallback, useState } from "react";
import { LuArrowLeft } from "react-icons/lu";
import { ScreenErrors } from "./ScreenErrors";
import { ServerSettings } from "./ServerSettings";
import { setTheme, themeSetting } from "../theme";

type Tab = "basic" | "server";

const TABS: { id: Tab; label: string; title: string }[] = [
  { id: "basic", label: "基本設定", title: "この画面の見せ方(ログインしたあとは、その人の設定で描きます)" },
  { id: "server", label: "サーバー", title: "aibow-server のつなぎ先・アプリモードへの切り替え" },
];

export function LoginSettings({ onClose, serverOnly }: { onClose(): void; serverOnly?: boolean }) {
  const [tab, setTab] = useState<Tab>("server");
  const [errors, setErrors] = useState<{ id: number; text: string }[]>([]);
  const [theme, setThemeValue] = useState(themeSetting());
  const onError = useCallback((text: string) => setErrors(e => [...e, { id: Date.now() + Math.random(), text }]), []);

  return (
    <section className="settings-view">
      <div className="settings-inner">
        <div className="settings-header">
          <h1>{serverOnly ? "設定(起動し直す前)" : "設定(ログインの前)"}</h1>
          <button className="back" onClick={onClose} title={serverOnly ? "設定を閉じる" : "設定を閉じて、ログインの画面に戻る"}>
            <LuArrowLeft /> {serverOnly ? "戻る" : "ログインの画面に戻る"}
          </button>
        </div>
        <ScreenErrors errors={errors} />

        <div className="settings-tabs" role="tablist">
          {TABS.filter(t => !serverOnly || t.id === "server").map(t => (
            <button
              key={t.id}
              role="tab"
              aria-selected={tab === t.id}
              className={"settings-tab" + (tab === t.id ? " current" : "")}
              title={t.title}
              onClick={() => { setTab(t.id); setErrors([]); }}
            >
              {t.label}
            </button>
          ))}
        </div>

        {tab === "basic" && (
          <>
            <h2 title="この画面の見せ方。ログインしたあとは、その人の設定で描きます">画面</h2>
            <label htmlFor="login-theme" title="システムなら、OS のライト / ダークの設定に合わせる(OS で切り替えるとすぐ変わる)">明るさ</label>
            <select id="login-theme" value={theme} onChange={e => { setTheme(e.target.value); setThemeValue(themeSetting()); }}>
              <option value="system">システム(OS の設定に合わせる)</option>
              <option value="light">ライト</option>
              <option value="dark">ダーク</option>
            </select>
          </>
        )}
        {tab === "server" && <ServerSettings disabled={false} onError={onError} />}
      </div>
    </section>
  );
}
