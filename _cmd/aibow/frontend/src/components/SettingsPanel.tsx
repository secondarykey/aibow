// 右側の設定パネル: この会話の設定 / アプリ全体の設定 / モデルに渡した文字列
import { useEffect, useState } from "react";
import type { AppSettings, ConversationSettings, WorkspaceSettings } from "../../bindings/aibow";

interface Props {
  settings: ConversationSettings | null; // 開いている会話の設定
  onChange(patch: Partial<ConversationSettings>): void;
  onMakeDefault(): void;
  app: AppSettings | null;
  appDisabled: boolean; // 生成中・読み込み中は変えられない(エンジンが1つのため)
  onApplyApp(w: WorkspaceSettings): void;
  lastPrompt: string;
}

export function SettingsPanel({ settings, onChange, onMakeDefault, app, appDisabled, onApplyApp, lastPrompt }: Props) {
  return (
    <aside>
      <h2>この会話の設定</h2>
      {settings && <ConversationSettingsForm settings={settings} onChange={onChange} onMakeDefault={onMakeDefault} />}

      <h2 className="section">アプリ全体の設定</h2>
      {app && <AppSettingsForm app={app} disabled={appDisabled} onApply={onApplyApp} />}

      <h2 className="section">モデルに渡した文字列</h2>
      <p className="hint">
        モデルは会話を覚えていません。送信のたびに、これまでの会話をこの書式の1本の文字列にして読ませています
        (会話の上限を超えたら、compact で収めたもの。要約があれば system の後ろに入ります)。
        最後の <code>&lt;|im_start|&gt;assistant</code> の続きを書かせることで返事になります。
      </p>
      <pre className="last-prompt">{lastPrompt || "(まだ送信していません)"}</pre>
    </aside>
  );
}

function ConversationSettingsForm({ settings, onChange, onMakeDefault }: {
  settings: ConversationSettings;
  onChange(patch: Partial<ConversationSettings>): void;
  onMakeDefault(): void;
}) {
  return (
    <>
      <label htmlFor="system">システムプロンプト</label>
      <textarea id="system" rows={4} value={settings.system} onChange={e => onChange({ system: e.target.value })} />

      <label htmlFor="compact" title="会話がアプリ全体の設定の「会話の上限」を超えたときに、モデルに渡す履歴をどう収めるか">
        会話が長くなったら(compact)
      </label>
      <select id="compact" value={settings.compact} onChange={e => onChange({ compact: e.target.value })}>
        <option value="summary">要約する(summary)</option>
        <option value="truncate">古い発言を捨てる(truncate)</option>
        <option value="none">何もしない(none)</option>
      </select>
      <p className="hint">{COMPACT_HINTS[settings.compact] ?? ""}</p>

      <label htmlFor="temp">temperature: {settings.temperature.toFixed(1)}</label>
      <input
        id="temp"
        type="range"
        min={0}
        max={1.5}
        step={0.1}
        value={settings.temperature}
        onChange={e => onChange({ temperature: parseFloat(e.target.value) })}
      />
      <p className="hint">0 に近いほど毎回同じ答え、大きいほどランダムになります。</p>

      <label htmlFor="presence" title="直近に生成した言葉の点数を下げる強さ。思考の中で同じことを繰り返すのを防ぐ(0 で無効)">
        presence penalty: {settings.presence_penalty.toFixed(1)}
      </label>
      <input
        id="presence"
        type="range"
        min={0}
        max={2}
        step={0.1}
        value={settings.presence_penalty}
        onChange={e => onChange({ presence_penalty: parseFloat(e.target.value) })}
      />

      <label className="check">
        <input type="checkbox" checked={settings.think} onChange={e => onChange({ think: e.target.checked })} />
        思考する
      </label>
      <p className="hint">オフにすると、プロンプトの最後に空の思考 <code>&lt;think&gt;&lt;/think&gt;</code> を入れて、すぐ答えさせます。</p>

      <button className="make-default" onClick={onMakeDefault}>この設定を新しい会話の既定にする</button>
    </>
  );
}

// compact の方式ごとの説明(_docs/features/0002-compact.md)
const COMPACT_HINTS: Record<string, string> = {
  summary: "古い部分をモデル自身に要約させ、「要約 + 最近の発言」を渡します。要約を作る分だけ待ち時間がかかります。",
  truncate: "古い発言から順に、モデルに渡すのをやめます(保存した発言は消えません)。渡さなかった内容はモデルには完全に消えます。",
  none: "何もしません。会話の上限を超えたら、生成する前に止まります。",
};

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
