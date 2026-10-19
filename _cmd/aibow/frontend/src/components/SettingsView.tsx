// 設定の画面: タブで「基本設定」(アプリの設定: GPU の層数・画面)と「会話」(プロファイル・ツール)に分ける。
// 会話に関わらないアプリの設定と、会話の既定として使われる値を、同じ画面に混ぜないため(2026-10-19、ユーザーの指示)。
// ツールは、プロファイルの中の1つの一覧で決める(最初から使う・選べば使える・使えないはプロファイル、確認しないはこの PC。0014)。
//
// 会話の一覧の下の「設定」で開き、チャットの代わりに会話の場所に出す(別の画面として扱う)。
// 「会話に戻る」か、一覧で会話を選ぶ・「+ 新規」を押すと閉じる。
// 開いている会話だけの設定は、入力欄の下(メーターの右)の歯車で開く会話の設定の画面(ConversationSettingsView)で変える。
// 新しい会話は、選んだプロファイル(名前の付いた新しい会話の既定)の写しから始まる(_docs/features/0009-conversation-settings.md・0014)。
import { useEffect, useState } from "react";
import { LuArrowLeft } from "react-icons/lu";
import type { AppSettings, ConversationSettings, DisplaySettings, WorkspaceSettings } from "../../bindings/github.com/secondarykey/aibow";
import type { ServerInfo } from "../../bindings/github.com/secondarykey/aibow/mcp/client";
import { NumberInput } from "./ConversationSettingsForm";
import { ProfileSettings } from "./ProfileSettings";
// Go の models パッケージ。"…/aibow/models" と書くと aibow パッケージの models.ts が選ばれるので、index まで書く
import type { Entry as ModelEntry } from "../../bindings/github.com/secondarykey/aibow/models/index";

interface Props {
  onClose(): void;                        // 会話に戻る
  errors: { id: number; text: string }[]; // 設定の画面を開いてから出たエラー(ふだんは会話の表示に出るもの)
  app: AppSettings | null;
  models: ModelEntry[];  // 選べるモデル(プロファイルのモデル)
  servers: ServerInfo[]; // settings.json の MCP のサーバー(プロファイルの使うツールのグループ)
  onProfilesChanged(): void; // プロファイルを変えた(新しい会話の画面の写しを合わせ直す)
  // プロファイルの会話の上限・返事の上限(作業領域を作り直してみてから save で保存する)
  onApplyLimits(history: number, reply: number, save: (patch: Partial<ConversationSettings>) => void): void;
  onError(text: string): void;
  appDisabled: boolean; // 生成中・読み込み中は変えられない(エンジンが1つのため)
  onApplyApp(w: WorkspaceSettings): void;
  onDisplayChange(d: DisplaySettings): void; // 画面の設定を変える(生成に関わらないので、生成中でも変えられる)
  onToolsChanged(): void; // ツールを使う / 使わないを変えた(メーターと、会話で選べる MCP のサーバーを読み直す)
}

type Tab = "basic" | "conversation";

const TABS: { id: Tab; label: string; title: string }[] = [
  { id: "basic", label: "基本設定", title: "会話によらず、この PC で1つだけのもの(GPU の層数・画面)" },
  { id: "conversation", label: "会話", title: "プロファイル(新しい会話の既定・ツールの扱い)" },
];

export function SettingsView({ onClose, errors, app, models, servers, onProfilesChanged, onApplyLimits, onError, appDisabled, onApplyApp, onDisplayChange, onToolsChanged }: Props) {
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

        {tab === "basic" && (
          <>
            <h2 title="読み込みに関わる設定。変えるとモデルを読み直す">モデル</h2>
            {app && <AppSettingsForm app={app} disabled={appDisabled} onApply={onApplyApp} />}

            <h2 className="section" title="画面の見せ方。生成には関わらない">画面</h2>
            {app && <DisplaySettingsForm display={app.display} onChange={onDisplayChange} />}
          </>
        )}

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

function AppSettingsForm({ app, disabled, onApply }: {
  app: AppSettings;
  disabled: boolean;
  onApply(w: WorkspaceSettings): void;
}) {
  // 変えるとモデルの読み直しになるので、入力しただけでは反映せず「適用」で反映する
  const w = app.workspace;
  const [gpu, setGpu] = useState(w.gpu_layers);
  useEffect(() => setGpu(w.gpu_layers), [w.gpu_layers]);

  return (
    <>
      <label htmlFor="gpu-layers" title="読み込んでいるモデルの、GPU に載せる層の数(モデルごとに覚える)。モデルの層数以上なら全部 GPU。変えるとモデルを読み直す">
        GPU に載せる層の数
      </label>
      <NumberInput id="gpu-layers" value={gpu} min={0} step={1} onChange={setGpu} />

      <button className="apply" disabled={disabled || gpu === w.gpu_layers} onClick={() => onApply({ ...w, gpu_layers: gpu })}>
        適用
      </button>
    </>
  );
}
