// 設定の画面: タブで「基本設定」(画面)・「モデル」(モデルの一覧・置き場所・GPU の層数。0015 の 8-4)・「会話」(プロファイル・ツール)に分ける。
// 会話に関わらないアプリの設定と、会話の既定として使われる値を、同じ画面に混ぜないため(2026-10-19、ユーザーの指示)。
// GPU の層数は、以前は基本設定で読み込み中のモデルの値だけを変えていたが、「モデル」のタブでモデルごとに変えるようにした。
// ツールは、プロファイルの中の1つの一覧で決める(最初から使う・選べば使える・使えないはプロファイル、確認しないはこの PC。0014)。
//
// 会話の一覧の下の「設定」で開き、チャットの代わりに会話の場所に出す(別の画面として扱う)。
// 「会話に戻る」か、一覧で会話を選ぶ・「+ 新規」を押すと閉じる。
// 開いている会話だけの設定は、入力欄の下(メーターの右)の歯車で開く会話の設定の画面(ConversationSettingsView)で変える。
// 新しい会話は、選んだプロファイル(名前の付いた新しい会話の既定)の写しから始まる(_docs/features/0009-conversation-settings.md・0014)。
import { useState } from "react";
import { LuArrowLeft } from "react-icons/lu";
import type { AppSettings, ConversationSettings, DisplaySettings } from "../../bindings/github.com/secondarykey/aibow";
import type { ServerInfo } from "../../bindings/github.com/secondarykey/aibow/mcp/client";
import { ProfileSettings } from "./ProfileSettings";
import { ModelSettings } from "./ModelSettings";
import { UserSettings } from "./UserSettings";
import { isBrowser, me } from "../backend";
import type { Models } from "../useModels";
// Go の models パッケージ。"…/aibow/models" と書くと aibow パッケージの models.ts が選ばれるので、index まで書く
import type { Entry as ModelEntry } from "../../bindings/github.com/secondarykey/aibow/models/index";

interface Props {
  onClose(): void;                        // 会話に戻る
  errors: { id: number; text: string }[]; // 設定の画面を開いてから出たエラー(ふだんは会話の表示に出るもの)
  app: AppSettings | null;
  models: ModelEntry[];  // 選べるモデル(プロファイルのモデル)
  modelState: Models;    // モデルの一覧・読み込み・降ろす・層の数(「モデル」のタブ)
  servers: ServerInfo[]; // settings.json の MCP のサーバー(プロファイルの使うツールのグループ)
  onProfilesChanged(): void; // プロファイルを変えた(新しい会話の画面の写しを合わせ直す)
  // プロファイルの会話の上限・返事の上限(作業領域を作り直してみてから save で保存する)
  onApplyLimits(history: number, reply: number, save: (patch: Partial<ConversationSettings>) => void): void;
  onError(text: string): void;
  appDisabled: boolean; // 生成中・読み込み中は変えられない(エンジンが1つのため)
  onDisplayChange(d: DisplaySettings): void; // 画面の設定を変える(生成に関わらないので、生成中でも変えられる)
  onToolsChanged(): void; // ツールを使う / 使わないを変えた(メーターと、会話で選べる MCP のサーバーを読み直す)
}

type Tab = "basic" | "model" | "conversation" | "users";

const TABS: { id: Tab; label: string; title: string }[] = [
  { id: "basic", label: "基本設定", title: "会話によらず、この PC で1つだけのもの(画面)" },
  { id: "model", label: "モデル", title: "モデルの一覧(読み込む / 降ろす・GPU の層数)と置き場所" },
  { id: "conversation", label: "会話", title: "プロファイル(新しい会話の既定・ツールの扱い)" },
  // 利用者の管理は、ブラウザでログインした管理者だけ(0018 の決定事項 7)
  { id: "users", label: "利用者", title: "aibow-server の利用者(作る・パスワードと API キーの作り直し・消す)" },
];

export function SettingsView({ onClose, errors, app, models, modelState, servers, onProfilesChanged, onApplyLimits, onError, appDisabled, onDisplayChange, onToolsChanged }: Props) {
  // 開くたびに「基本設定」から(開いている間だけ覚える)
  const [tab, setTab] = useState<Tab>("basic");
  return (
    <section className="settings-view">
      <div className="settings-inner">
        <div className="settings-header">
          <h1>設定</h1>
          <button className="back" onClick={onClose} title="設定を閉じて、会話に戻る"><LuArrowLeft /> 会話に戻る</button>
        </div>
        {errors.map(e => <div key={e.id} className="msg error">{e.text}</div>)}

        <div className="settings-tabs" role="tablist">
          {TABS.filter(t => t.id !== "users" || (isBrowser && me.admin)).map(t => (
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

        {tab === "basic" && (
          <>
            <h2 title="画面の見せ方。生成には関わらない">画面</h2>
            {app && <DisplaySettingsForm display={app.display} onChange={onDisplayChange} />}
          </>
        )}

        {tab === "model" && <ModelSettings models={modelState} disabled={appDisabled} onError={onError} />}

        {tab === "users" && <UserSettings onError={onError} />}

        {tab === "conversation" && (
          <>
            <h2 title="名前の付いた新しい会話の既定。新しい会話は、選んだプロファイルの写しから始まる(今ある会話には効かない)。決まり(固定・使えない)は、そのプロファイルから作った会話にすぐ効く">プロファイル</h2>
            <ProfileSettings
              models={models}
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

// 画面の設定。会話によらずアプリ全体で1つ。選んだらすぐ効く(「適用」は要らない)
function DisplaySettingsForm({ display, onChange }: { display: DisplaySettings; onChange(d: DisplaySettings): void }) {
  return (
    <>
      <label htmlFor="theme" title="システムなら、OS のライト / ダークの設定に合わせる(OS で切り替えるとすぐ変わる)">明るさ</label>
      <select id="theme" value={display.theme || "system"} onChange={e => onChange({ ...display, theme: e.target.value })}>
        <option value="system">システム(OS の設定に合わせる)</option>
        <option value="light">ライト</option>
        <option value="dark">ダーク</option>
      </select>

      <label className="check">
        <input
          type="checkbox"
          checked={display.pin_question}
          onChange={e => onChange({ ...display, pin_question: e.target.checked })}
        />
        スクロールしたら、質問を右上に出しておく
      </label>
      <p className="hint">長い返事を読んでいて質問が上に隠れたら、その質問を右上に小さく出します。押すと質問まで戻ります。</p>
    </>
  );
}
