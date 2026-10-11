// 右側の設定パネルと、モデルに渡した文字列の表示
import type { ChatSettings } from "../useChat";

interface Props {
  settings: ChatSettings;
  onChange(s: ChatSettings): void;
  lastPrompt: string;
}

export function SettingsPanel({ settings, onChange, lastPrompt }: Props) {
  return (
    <aside>
      <h2>設定</h2>
      <label htmlFor="system">システムプロンプト</label>
      <textarea
        id="system"
        rows={4}
        value={settings.system}
        onChange={e => onChange({ ...settings, system: e.target.value })}
      />

      <label htmlFor="temp">temperature: {settings.temperature.toFixed(1)}</label>
      <input
        id="temp"
        type="range"
        min={0}
        max={1.5}
        step={0.1}
        value={settings.temperature}
        onChange={e => onChange({ ...settings, temperature: parseFloat(e.target.value) })}
      />
      <p className="hint">0 に近いほど毎回同じ答え、大きいほどランダムになります。</p>

      <label className="check">
        <input
          type="checkbox"
          checked={settings.think}
          onChange={e => onChange({ ...settings, think: e.target.checked })}
        />
        思考する
      </label>
      <p className="hint">オフにすると、プロンプトの最後に空の思考 <code>&lt;think&gt;&lt;/think&gt;</code> を入れて、すぐ答えさせます。</p>

      <h2 className="section">モデルに渡した文字列</h2>
      <p className="hint">
        モデルは会話を覚えていません。送信のたびに、これまでの会話を全部この書式の1本の文字列にして読ませています。
        最後の <code>&lt;|im_start|&gt;assistant</code> の続きを書かせることで返事になります。
      </p>
      <pre className="last-prompt">{lastPrompt || "(まだ送信していません)"}</pre>
    </aside>
  );
}
