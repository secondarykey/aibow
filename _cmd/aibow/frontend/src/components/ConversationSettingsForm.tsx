// 会話の設定の入力欄(システムプロンプト・compact・temperature・presence penalty・思考)。
//
// 同じ欄を2か所で使う: 会話の設定の画面(一覧の歯車。その会話の設定)と、設定の画面(新しい会話の既定)。
// 新しい会話は既定の写しから始まり、そのあとは会話ごとに変える(_docs/features/0009-conversation-settings.md)。
import type { ConversationSettings } from "../../bindings/github.com/secondarykey/aibow";

export function ConversationSettingsForm({ settings, onChange }: {
  settings: ConversationSettings;
  onChange(patch: Partial<ConversationSettings>): void;
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
    </>
  );
}

// compact の方式ごとの説明(_docs/features/0002-compact.md)
const COMPACT_HINTS: Record<string, string> = {
  summary: "古い部分をモデル自身に要約させ、「要約 + 最近の発言」を渡します。要約を作る分だけ待ち時間がかかります。",
  truncate: "古い発言から順に、モデルに渡すのをやめます(保存した発言は消えません)。渡さなかった内容はモデルには完全に消えます。",
  none: "何もしません。会話の上限を超えたら、生成する前に止まります。",
};
