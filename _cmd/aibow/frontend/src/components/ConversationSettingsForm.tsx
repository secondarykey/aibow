// 会話の設定の入力欄(モデル・システムプロンプト・compact・temperature・presence penalty・思考)。
//
// 同じ欄を2か所で使う: 会話の設定の画面(一覧の歯車。その会話の設定)と、設定の画面(プロファイルの値)。
// 新しい会話はプロファイルの値の写しから始まり、そのあとは会話ごとに変える(_docs/features/0009-conversation-settings.md・0014)。
//
// lock はプロファイルの決まりの「固定」(0014)。プロファイルの編集では、項目の名前の横の錠で固定を付け外しする。
// 会話の設定では、固定した項目を押せなくして「プロファイルで固定」と添える(保存のときにも Go が断る)。
//
// 会話の上限・返事の上限(LimitsForm)だけは、入力しただけでは反映せず「適用」で反映する。
// 作業領域(会話の上限 + 返事の上限)を作り直して VRAM に入るかを確かめてから保存するため。
import { useEffect, useState, type ReactNode } from "react";
import { LuLock, LuLockOpen } from "react-icons/lu";
import type { ConversationSettings } from "../../bindings/github.com/secondarykey/aibow";
// Go の models パッケージ。"…/aibow/models" と書くと aibow パッケージの models.ts が選ばれるので、index まで書く
import type { Entry as ModelEntry } from "../../bindings/github.com/secondarykey/aibow/models/index";
import { FIX, fixedTitle, locked, type Lock } from "../profileRules";

export function ConversationSettingsForm({ settings, models, lock, onChange }: {
  settings: ConversationSettings;
  models: ModelEntry[]; // 選べるモデル(models フォルダと Ollama)
  lock?: Lock;
  onChange(patch: Partial<ConversationSettings>): void;
}) {
  const off = (item: string) => locked(lock, item);
  return (
    <>
      <ModelSelect value={settings.model} models={models} lock={lock} onChange={model => onChange({ model })} />

      <FieldLabel htmlFor="system" item={FIX.system} lock={lock}>システムプロンプト</FieldLabel>
      <textarea id="system" rows={4} value={settings.system} disabled={off(FIX.system)} onChange={e => onChange({ system: e.target.value })} />

      <FieldLabel htmlFor="compact" item={FIX.compact} lock={lock} title="会話が「会話の上限」を超えたときに、モデルに渡す履歴をどう収めるか">
        会話が長くなったら(compact)
      </FieldLabel>
      <select id="compact" value={settings.compact} disabled={off(FIX.compact)} onChange={e => onChange({ compact: e.target.value })}>
        <option value="summary">要約する(summary)</option>
        <option value="truncate">古い発言を捨てる(truncate)</option>
        <option value="none">何もしない(none)</option>
      </select>
      <p className="hint">{COMPACT_HINTS[settings.compact] ?? ""}</p>

      <FieldLabel htmlFor="temp" item={FIX.temperature} lock={lock}>temperature: {settings.temperature.toFixed(1)}</FieldLabel>
      <input
        id="temp"
        type="range"
        min={0}
        max={1.5}
        step={0.1}
        value={settings.temperature}
        disabled={off(FIX.temperature)}
        onChange={e => onChange({ temperature: parseFloat(e.target.value) })}
      />
      <p className="hint">0 に近いほど毎回同じ答え、大きいほどランダムになります。</p>

      <FieldLabel htmlFor="presence" item={FIX.presencePenalty} lock={lock} title="直近に生成した言葉の点数を下げる強さ。思考の中で同じことを繰り返すのを防ぐ(0 で無効)">
        presence penalty: {settings.presence_penalty.toFixed(1)}
      </FieldLabel>
      <input
        id="presence"
        type="range"
        min={0}
        max={2}
        step={0.1}
        value={settings.presence_penalty}
        disabled={off(FIX.presencePenalty)}
        onChange={e => onChange({ presence_penalty: parseFloat(e.target.value) })}
      />

      <div className="check-row">
        <label className="check">
          <input type="checkbox" checked={settings.think} disabled={off(FIX.think)} onChange={e => onChange({ think: e.target.checked })} />
          思考する
        </label>
        <FixMark item={FIX.think} lock={lock} />
      </div>
      <p className="hint">オフにすると、プロンプトの最後に空の思考 <code>&lt;think&gt;&lt;/think&gt;</code> を入れて、すぐ答えさせます。</p>
    </>
  );
}

// FieldLabel は、入力欄の名前と、その横の固定の印。
export function FieldLabel({ htmlFor, item, lock, title, children }: {
  htmlFor: string;
  item: string;
  lock?: Lock;
  title?: string;
  children: ReactNode;
}) {
  return (
    <div className="field-label">
      <label htmlFor={htmlFor} title={title}>{children}</label>
      <FixMark item={item} lock={lock} />
    </div>
  );
}

// FixMark は固定の印。プロファイルの編集では押して付け外しする錠、会話の設定では固定した項目にだけ出す枠。
export function FixMark({ item, lock }: { item: string; lock?: Lock }) {
  if (!lock) return null;
  const fixed = lock.fixed.includes(item);
  if (lock.onToggle) {
    const toggle = lock.onToggle;
    return (
      <button
        type="button"
        className={"fix-toggle" + (fixed ? " on" : "")}
        title={fixed ? "固定している(このプロファイルの会話では変えられない)。押すと外す" : "固定する(このプロファイルの会話では変えられなくする)"}
        onClick={() => toggle(item)}
      >
        {fixed ? <LuLock /> : <LuLockOpen />}
      </button>
    );
  }
  if (!fixed) return null;
  return <span className="tool-tag fix-tag" title={fixedTitle(lock)}><LuLock /> プロファイルで固定</span>;
}

// ModelSelect は、会話のモデル(プロファイルの編集では、新しい会話のモデル)の選択(0014 の 3)。
// ここでは値を変えるだけで、モデルは読み込まない(会話のモデルは、その会話で送信するときに読み込む)。
// 入力欄の下のモデルの選択は、選んだらすぐ読み込む(今から使うものを選ぶ欄なので)。
function ModelSelect({ value, models, lock, onChange }: { value: string; models: ModelEntry[]; lock?: Lock; onChange(id: string): void }) {
  const groups = [
    { source: "file", label: "models フォルダ" },
    { source: "ollama", label: "Ollama" },
  ];
  // 一覧に無いモデル(消したモデルや、AIBOW_MODEL で一覧の外のファイルを指定したもの)も、今の値として出しておく
  const outside = value !== "" && !models.some(m => m.id === value);
  return (
    <>
      <FieldLabel htmlFor="model" item={FIX.model} lock={lock} title="この設定で使うモデル。変えても、ここでは読み込まない(送信するときに読み込む)">モデル</FieldLabel>
      <select id="model" value={value} disabled={locked(lock, FIX.model)} onChange={e => onChange(e.target.value)}>
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
export function LimitsForm({ history, reply, disabled, lock, onApply }: {
  history: number;
  reply: number;
  disabled: boolean; // 生成中・読み込み中は変えられない(作業領域が1つのため)
  lock?: Lock;
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
      <FieldLabel htmlFor="history-tokens" item={FIX.historyTokens} lock={lock} title="モデルに渡す文字列(これまでの会話)のトークン数の上限。超えたら compact で収める">
        会話の上限
      </FieldLabel>
      <NumberInput id="history-tokens" value={h} min={512} step={1024} disabled={locked(lock, FIX.historyTokens)} onChange={setH} />

      <FieldLabel htmlFor="reply-tokens" item={FIX.replyTokens} lock={lock} title="1回の返事で生成するトークン数の上限(思考 + 答え)">
        返事の上限
      </FieldLabel>
      <NumberInput id="reply-tokens" value={r} min={256} step={1024} disabled={locked(lock, FIX.replyTokens)} onChange={setR} />

      <p className="hint">
        作業領域: <strong>{(h + r).toLocaleString()}</strong> トークン(会話の上限 + 返事の上限)。
        会話を上限まで詰めても、返事を上限まで書ける大きさを確保します。変えても、モデルは読み直しません。
      </p>
      <button className="apply" disabled={disabled || !changed} onClick={() => onApply(h, r)}>適用</button>
    </>
  );
}

// 数値の入力欄。空や数字でない入力のあいだは親に渡さない(0 として保存されないように)
export function NumberInput({ id, value, min, step, disabled, onChange }: {
  id: string;
  value: number;
  min: number;
  step: number;
  disabled?: boolean;
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
      disabled={disabled}
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
