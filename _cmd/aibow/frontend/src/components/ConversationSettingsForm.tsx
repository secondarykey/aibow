// 会話の設定の入力欄(モデル・システムプロンプト・compact・temperature・presence penalty・思考)。
//
// 同じ欄を2か所で使う: 会話の設定の画面(一覧の歯車。その会話の設定)と、設定の画面(新しい会話の既定)。
// 新しい会話は既定の写しから始まり、そのあとは会話ごとに変える(_docs/features/0009-conversation-settings.md)。
//
// 会話の上限・返事の上限(LimitsForm)だけは、入力しただけでは反映せず「適用」で反映する。
// 作業領域(会話の上限 + 返事の上限)を作り直して VRAM に入るかを確かめてから保存するため。
import { useEffect, useState } from "react";
import type { ConversationSettings } from "../../bindings/github.com/secondarykey/aibow";
// Go の models パッケージ。"…/aibow/models" と書くと aibow パッケージの models.ts が選ばれるので、index まで書く
import type { Entry as ModelEntry } from "../../bindings/github.com/secondarykey/aibow/models/index";

export function ConversationSettingsForm({ settings, models, onChange }: {
  settings: ConversationSettings;
  models: ModelEntry[]; // 選べるモデル(models フォルダと Ollama)
  onChange(patch: Partial<ConversationSettings>): void;
}) {
  return (
    <>
      <ModelSelect value={settings.model} models={models} onChange={model => onChange({ model })} />

      <label htmlFor="system">システムプロンプト</label>
      <textarea id="system" rows={4} value={settings.system} onChange={e => onChange({ system: e.target.value })} />

      <label htmlFor="compact" title="会話が「会話の上限」を超えたときに、モデルに渡す履歴をどう収めるか">
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

// ModelSelect は、会話のモデル(新しい会話の既定では、新しい会話のモデル)の選択(0014 の 3)。
// ここでは値を変えるだけで、モデルは読み込まない(会話のモデルは、その会話で送信するときに読み込む)。
// 入力欄の下のモデルの選択は、選んだらすぐ読み込む(今から使うものを選ぶ欄なので)。
function ModelSelect({ value, models, onChange }: { value: string; models: ModelEntry[]; onChange(id: string): void }) {
  const groups = [
    { source: "file", label: "models フォルダ" },
    { source: "ollama", label: "Ollama" },
  ];
  // 一覧に無いモデル(消したモデルや、AIBOW_MODEL で一覧の外のファイルを指定したもの)も、今の値として出しておく
  const outside = value !== "" && !models.some(m => m.id === value);
  return (
    <>
      <label htmlFor="model" title="この設定で使うモデル。変えても、ここでは読み込まない(送信するときに読み込む)">モデル</label>
      <select id="model" value={value} onChange={e => onChange(e.target.value)}>
        {value === "" && <option value="">決めていない(読み込んでいるモデル)</option>}
        {groups.map(g => {
          const items = models.filter(m => m.source === g.source);
          if (!items.length) return null;
          return (
            <optgroup key={g.source} label={g.label}>
              {items.map(m => <option key={m.id} value={m.id}>{`${m.name}(${m.size_gb.toFixed(1)} GB)`}</option>)}
            </optgroup>
          );
        })}
        {outside && <option value={value}>{`${value}(見つかりません)`}</option>}
      </select>
    </>
  );
}

// LimitsForm は会話の上限・返事の上限。「適用」で onApply を呼ぶ(呼ぶ側が作業領域を作り直してみてから保存する)
export function LimitsForm({ history, reply, disabled, onApply }: {
  history: number;
  reply: number;
  disabled: boolean; // 生成中・読み込み中は変えられない(作業領域が1つのため)
  onApply(history: number, reply: number): void;
}) {
  const [h, setH] = useState(history);
  const [r, setR] = useState(reply);
  useEffect(() => {
    setH(history);
    setR(reply);
  }, [history, reply]);
  const changed = h !== history || r !== reply;

  return (
    <>
      <label htmlFor="history-tokens" title="モデルに渡す文字列(これまでの会話)のトークン数の上限。超えたら compact で収める">
        会話の上限
      </label>
      <NumberInput id="history-tokens" value={h} min={512} step={1024} onChange={setH} />

      <label htmlFor="reply-tokens" title="1回の返事で生成するトークン数の上限(思考 + 答え)">
        返事の上限
      </label>
      <NumberInput id="reply-tokens" value={r} min={256} step={1024} onChange={setR} />

      <p className="hint">
        作業領域: <strong>{(h + r).toLocaleString()}</strong> トークン(会話の上限 + 返事の上限)。
        会話を上限まで詰めても、返事を上限まで書ける大きさを確保します。変えても、モデルは読み直しません。
      </p>
      <button className="apply" disabled={disabled || !changed} onClick={() => onApply(h, r)}>適用</button>
    </>
  );
}

// 数値の入力欄。空や数字でない入力のあいだは親に渡さない(0 として保存されないように)
export function NumberInput({ id, value, min, step, onChange }: {
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

// compact の方式ごとの説明(_docs/features/0002-compact.md)
const COMPACT_HINTS: Record<string, string> = {
  summary: "古い部分をモデル自身に要約させ、「要約 + 最近の発言」を渡します。要約を作る分だけ待ち時間がかかります。",
  truncate: "古い発言から順に、モデルに渡すのをやめます(保存した発言は消えません)。渡さなかった内容はモデルには完全に消えます。",
  none: "何もしません。会話の上限を超えたら、生成する前に止まります。",
};
