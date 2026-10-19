// チャットの入力欄の下: モデルの選択。右端に会話のメーター。
// 読み込んだモデルの情報(パラメータ数・GPU / CPU・作業領域・読み込み時間)は、いつも出しておかず、
// モデルの選択にマウスを乗せたときの吹き出しに出す(2026-10-15、ユーザーの指示)
import type { ReactNode } from "react";
import type { Models } from "../useModels";

interface Props {
  models: Models;
  disabled: boolean;
  selected?: string;    // 開いている会話(新しい会話の画面では、これから作る会話)のモデルの ID。無ければ読み込んでいるモデル
  lockReason?: string;  // モデルを選べない理由(プロファイルで固定。0014)。選べるなら undefined
  onSelect(id: string): void;
  size?: number;        // 開いている会話(新しい会話の画面では、これから作る会話)の作業領域の大きさ(会話の上限 + 返事の上限)
  children?: ReactNode; // 右端に出すもの(会話を開いているときのメーター)
}

// 選ぶのは「この会話のモデル」(0014 の 3)。読み込めるモデルは1つ(VRAM に1つしか載らない)なので、
// 会話を開いただけでは読み直さず、読み込んでいるモデルと違う会話は、送信するときに Go が読み直す
export function ModelBar({ models, disabled, size, selected, lockReason, onSelect, children }: Props) {
  const { current, gpu, loading } = models;
  const value = selected ?? current?.id ?? "";
  // 会話のモデルが、読み込んでいるモデルと違う(送信するときに読み直す)
  const reload = !loading && current !== null && value !== "" && value !== current.id;
  const groups = [
    { source: "file", label: "models フォルダ" },
    { source: "ollama", label: "Ollama" },
  ];
  // AIBOW_MODEL で一覧の外のファイルを指定した場合や、会話のモデルが一覧に無い(消した)場合も、選択肢に出しておく
  const outside = value !== "" && !models.models.some(m => m.id === value);
  const outsideName = current?.id === value ? current.name : `${value}(見つかりません)`;

  // 作業領域は会話ごとに違う(0009)ので、開いている会話の大きさを出す。今の作業領域と違えば、次の送信で作り直す。
  // llama.cpp は大きさを 256 の倍数に切り上げることがあるので、その差は同じとみなす
  const workspace = size ?? current?.n_ctx ?? 0;
  const resize = current !== null && size !== undefined && (current.n_ctx < size || current.n_ctx >= size + 256);
  // 吹き出しの文。1行目は何を選ぶ欄か、続けて読み込んだモデルの情報
  const info = loading
    ? ["読み込み中…"]
    : current
      ? [
        `${current.name}(${current.params_b.toFixed(1)}B ・ ${gpu ? "GPU" : "CPU"} ・ 読み込み ${current.load_sec.toFixed(1)}s)`,
        `作業領域: ${workspace.toLocaleString()} トークン${resize ? `(今は ${current.n_ctx.toLocaleString()}。送信時に作り直し)` : ""}`,
        current.desc,
      ]
      : ["モデル未読み込み"];
  if (reload) info.unshift(`この会話のモデルは送信するときに読み込みます(今読み込んでいるのは ${current.name})`);
  if (lockReason) info.unshift(lockReason);
  const title = ["使うモデル(models フォルダと Ollama のモデル)", ...info].filter(Boolean).join("\n");

  return (
    <div className="model-bar">
      <select
        title={title}
        className={reload ? "model-select model-reload" : "model-select"}
        value={value}
        disabled={disabled || !!lockReason}
        onChange={e => onSelect(e.target.value)}
      >
        {value === "" && <option value="" disabled>モデルを選んでください</option>}
        {groups.map(g => {
          const items = models.models.filter(m => m.source === g.source);
          if (!items.length) return null;
          return (
            <optgroup key={g.source} label={g.label}>
              {items.map(m => <option key={m.id} value={m.id}>{`${m.name}(${m.size_gb.toFixed(1)} GB)`}</option>)}
            </optgroup>
          );
        })}
        {outside && <option value={value}>{outsideName}</option>}
      </select>
      {children && <span className="model-bar-end">{children}</span>}
    </div>
  );
}
