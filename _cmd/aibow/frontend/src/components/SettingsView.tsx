// 設定の画面: 新しい会話の既定 / アプリの設定 / ツール
//
// 会話の一覧の下の「設定」で開き、チャットの代わりに会話の場所に出す(別の画面として扱う)。
// 「会話に戻る」か、一覧で会話を選ぶ・「+ 新規」を押すと閉じる。
// 開いている会話だけの設定は、一覧の各行の歯車で開く会話の設定の画面(ConversationSettingsView)で変える。
// 新しい会話はここの既定の写しから始まる(_docs/features/0009-conversation-settings.md)。
import { useEffect, useState } from "react";
import { LuArrowLeft } from "react-icons/lu";
import type { AppSettings, ConversationSettings, WorkspaceSettings } from "../../bindings/github.com/secondarykey/aibow";
import { ToolSettings } from "./ToolSettings";
import { ConversationSettingsForm, LimitsForm, NumberInput } from "./ConversationSettingsForm";

interface Props {
  onClose(): void;                        // 会話に戻る
  errors: { id: number; text: string }[]; // 設定の画面を開いてから出たエラー(ふだんは会話の表示に出るもの)
  app: AppSettings | null;
  onDefaultsChange(patch: Partial<ConversationSettings>): void; // 新しい会話の既定を変える
  onApplyDefaultLimits(history: number, reply: number): void;   // 新しい会話の既定の会話の上限・返事の上限(作業領域を作り直してみてから保存する)
  appDisabled: boolean; // 生成中・読み込み中は変えられない(エンジンが1つのため)
  onApplyApp(w: WorkspaceSettings): void;
  onToolsChanged(): void; // ツールを使う / 使わないを変えた(メーターと、会話で選べる MCP のサーバーを読み直す)
}

export function SettingsView({ onClose, errors, app, onDefaultsChange, onApplyDefaultLimits, appDisabled, onApplyApp, onToolsChanged }: Props) {
  return (
    <section className="settings-view">
      <div className="settings-inner">
        <div className="settings-header">
          <h1>設定</h1>
          <button className="back" onClick={onClose} title="設定を閉じて、会話に戻る"><LuArrowLeft /> 会話に戻る</button>
        </div>
        {errors.map(e => <div key={e.id} className="msg error">{e.text}</div>)}

        <h2 title="新しい会話は、この設定の写しから始まる。今ある会話には効かない">新しい会話の既定</h2>
        {app && (
          <>
            <ConversationSettingsForm settings={app.defaults} onChange={onDefaultsChange} />
            <LimitsForm
              history={app.defaults.history_tokens}
              reply={app.defaults.reply_tokens}
              disabled={appDisabled}
              onApply={onApplyDefaultLimits}
            />
          </>
        )}

        <h2 className="section" title="会話によらず、この PC で1つだけのもの">アプリの設定</h2>
        {app && <AppSettingsForm app={app} disabled={appDisabled} onApply={onApplyApp} />}

        <h2 className="section" title="この PC で、ツールを使うかどうか(全部の会話に効く。tools.json に保存する)">ツール</h2>
        <ToolSettings onChanged={onToolsChanged} />
      </div>
    </section>
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
      <label htmlFor="gpu-layers" title="GPU に載せる層の数。モデルの層数以上なら全部 GPU。変えるとモデルを読み直す">
        GPU に載せる層の数
      </label>
      <NumberInput id="gpu-layers" value={gpu} min={0} step={1} onChange={setGpu} />

      <button className="apply" disabled={disabled || gpu === w.gpu_layers} onClick={() => onApply({ ...w, gpu_layers: gpu })}>
        適用
      </button>
    </>
  );
}
