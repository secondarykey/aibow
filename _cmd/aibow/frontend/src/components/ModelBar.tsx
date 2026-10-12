// チャットの入力欄の下: モデルの選択と、読み込み中のモデルの情報
import type { Models } from "../useModels";

interface Props {
  models: Models;
  disabled: boolean;
}

// モデルはアプリ全体で1つ(VRAM に1つしか載らない)。会話を切り替えてもモデルは変わらない
export function ModelBar({ models, disabled }: Props) {
  const { current, gpu, loading } = models;
  const groups = [
    { source: "file", label: "models フォルダ" },
    { source: "ollama", label: "Ollama" },
  ];
  // AIBOW_MODEL で一覧の外のファイルを指定した場合も、選択肢に出しておく
  const outside = current && !models.models.some(m => m.id === current.id);

  const info = loading
    ? "読み込み中…"
    : current
      ? `${current.params_b.toFixed(1)}B ・ ${gpu ? "GPU" : "CPU"} ・ コンテキスト ${current.n_ctx} ・ 読み込み ${current.load_sec.toFixed(1)}s`
      : "モデル未読み込み";

  return (
    <div className="model-bar">
      <select
        className="model-select"
        title="使うモデル(models フォルダと Ollama のモデル)"
        value={current?.id ?? ""}
        disabled={disabled}
        onChange={e => models.switchModel(e.target.value)}
      >
        {!current && <option value="" disabled>モデルを選んでください</option>}
        {groups.map(g => {
          const items = models.models.filter(m => m.source === g.source);
          if (!items.length) return null;
          return (
            <optgroup key={g.source} label={g.label}>
              {items.map(m => <option key={m.id} value={m.id}>{`${m.name}(${m.size_gb.toFixed(1)} GB)`}</option>)}
            </optgroup>
          );
        })}
        {outside && <option value={current.id}>{current.name}</option>}
      </select>
      <span className="model-info" title={current?.desc ?? ""}>{info}</span>
    </div>
  );
}
