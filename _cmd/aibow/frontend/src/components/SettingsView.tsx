// 設定の画面: 新しい会話の既定 / アプリ全体の設定 / ツール
//
// 会話の一覧の下の「設定」で開き、チャットの代わりに会話の場所に出す(別の画面として扱う)。
// 「会話に戻る」か、一覧で会話を選ぶ・「+ 新規」を押すと閉じる。
// 開いている会話だけの設定は、一覧の各行の歯車で開く会話の設定の画面(ConversationSettingsView)で変える。
// 新しい会話はここの既定の写しから始まる(_docs/features/0009-conversation-settings.md)。
import { useEffect, useState } from "react";
import { LuArrowLeft } from "react-icons/lu";
import type { AppSettings, ConversationSettings, WorkspaceSettings } from "../../bindings/github.com/secondarykey/aibow";
import { ToolSettings } from "./ToolSettings";
import { ConversationSettingsForm } from "./ConversationSettingsForm";

interface Props {
  onClose(): void;                        // 会話に戻る
  errors: { id: number; text: string }[]; // 設定の画面を開いてから出たエラー(ふだんは会話の表示に出るもの)
  app: AppSettings | null;
  onDefaultsChange(patch: Partial<ConversationSettings>): void; // 新しい会話の既定を変える
  appDisabled: boolean; // 生成中・読み込み中は変えられない(エンジンが1つのため)
  onApplyApp(w: WorkspaceSettings): void;
  onToolsChanged(): void; // ツールを使う / 使わないを変えた(メーターと、会話で選べる MCP のサーバーを読み直す)
}

export function SettingsView({ onClose, errors, app, onDefaultsChange, appDisabled, onApplyApp, onToolsChanged }: Props) {
  return (
    <section className="settings-view">
      <div className="settings-inner">
        <div className="settings-header">
          <h1>設定</h1>
          <button className="back" onClick={onClose} title="設定を閉じて、会話に戻る"><LuArrowLeft /> 会話に戻る</button>
        </div>
        {errors.map(e => <div key={e.id} className="msg error">{e.text}</div>)}

        <h2 title="新しい会話は、この設定の写しから始まる。今ある会話には効かない">新しい会話の既定</h2>
        {app && <ConversationSettingsForm settings={app.defaults} onChange={onDefaultsChange} />}

        <h2 className="section">アプリ全体の設定</h2>
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
  // 変えるとエンジンの作り直しになるので、入力しただけでは反映せず「適用」で反映する
  const w = app.workspace;
  const [history, setHistory] = useState(w.history_tokens);
  const [reply, setReply] = useState(w.reply_tokens);
  const [gpu, setGpu] = useState(w.gpu_layers);
  useEffect(() => {
    setHistory(w.history_tokens);
    setReply(w.reply_tokens);
    setGpu(w.gpu_layers);
  }, [w.history_tokens, w.reply_tokens, w.gpu_layers]);
  const changed = history !== w.history_tokens || reply !== w.reply_tokens || gpu !== w.gpu_layers;

  return (
    <>
      <label htmlFor="history-tokens" title="モデルに渡す文字列(これまでの会話)のトークン数の上限。超えたら会話の設定の compact で収める">
        会話の上限
      </label>
      <NumberInput id="history-tokens" value={history} min={512} step={1024} onChange={setHistory} />

      <label htmlFor="reply-tokens" title="1回の返事で生成するトークン数の上限(思考 + 答え)">
        返事の上限
      </label>
      <NumberInput id="reply-tokens" value={reply} min={256} step={1024} onChange={setReply} />

      <p className="hint">
        作業領域: <strong>{(history + reply).toLocaleString()}</strong> トークン(会話の上限 + 返事の上限)。
        会話を上限まで詰めても、返事を上限まで書ける大きさを確保します。変えても、モデルは読み直しません。
      </p>

      <label htmlFor="gpu-layers" title="GPU に載せる層の数。モデルの層数以上なら全部 GPU。変えるとモデルを読み直す">
        GPU に載せる層の数
      </label>
      <NumberInput id="gpu-layers" value={gpu} min={0} step={1} onChange={setGpu} />

      <button
        className="apply"
        disabled={disabled || !changed}
        onClick={() => onApply({ ...w, history_tokens: history, reply_tokens: reply, gpu_layers: gpu })}
      >
        適用
      </button>
    </>
  );
}

// 数値の入力欄。空や数字でない入力のあいだは親に渡さない(0 として保存されないように)
function NumberInput({ id, value, min, step, onChange }: {
  id: string;
  value: number;
  min: number;
  step: number;
  onChange(v: number): void;
}) {
  const [text, setText] = useState(String(value));
  useEffect(() => setText(String(value)), [value]);
  return (
    <input
      id={id}
      type="number"
      min={min}
      step={step}
      value={text}
      onChange={e => {
        setText(e.target.value);
        const v = Number(e.target.value);
        if (e.target.value !== "" && Number.isInteger(v) && v >= min) onChange(v);
      }}
    />
  );
}
