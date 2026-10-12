// 右側の設定パネル: この会話の設定 / アプリ全体の設定 / モデルに渡した文字列
import { useEffect, useState } from "react";
import type { AppSettings, ConversationSettings } from "../../bindings/aibow";

interface Props {
  settings: ConversationSettings | null; // 開いている会話の設定
  onChange(patch: Partial<ConversationSettings>): void;
  onMakeDefault(): void;
  app: AppSettings | null;
  appDisabled: boolean; // 生成中・読み込み中は変えられない(エンジンが1つのため)
  onApplyApp(nCtx: number, gpuLayers: number): void;
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
        モデルは会話を覚えていません。送信のたびに、これまでの会話を全部この書式の1本の文字列にして読ませています。
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

      <label htmlFor="max-tokens" title="1回の返事で生成するトークン数の上限(思考 + 答え)">返事の上限トークン数</label>
      <NumberInput id="max-tokens" value={settings.max_tokens} min={1} step={256} onChange={v => onChange({ max_tokens: v })} />

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

function AppSettingsForm({ app, disabled, onApply }: {
  app: AppSettings;
  disabled: boolean;
  onApply(nCtx: number, gpuLayers: number): void;
}) {
  // 変えるとエンジンの作り直しになるので、入力しただけでは反映せず「適用」で反映する
  const [nCtx, setNCtx] = useState(app.n_ctx);
  const [gpu, setGpu] = useState(app.gpu_layers);
  useEffect(() => {
    setNCtx(app.n_ctx);
    setGpu(app.gpu_layers);
  }, [app.n_ctx, app.gpu_layers]);
  const changed = nCtx !== app.n_ctx || gpu !== app.gpu_layers;

  return (
    <>
      <label htmlFor="n-ctx" title="一度に扱えるトークン数(プロンプト + 返事)。変えるとコンテキストだけ作り直す(モデルは読み直さない)">
        コンテキスト長
      </label>
      <NumberInput id="n-ctx" value={nCtx} min={256} step={1024} onChange={setNCtx} />

      <label htmlFor="gpu-layers" title="GPU に載せる層の数。モデルの層数以上なら全部 GPU。変えるとモデルを読み直す">
        GPU に載せる層の数
      </label>
      <NumberInput id="gpu-layers" value={gpu} min={0} step={1} onChange={setGpu} />

      <button className="apply" disabled={disabled || !changed} onClick={() => onApply(nCtx, gpu)}>適用</button>
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
