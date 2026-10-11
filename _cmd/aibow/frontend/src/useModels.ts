// useModels.ts: モデルの一覧と切り替え。
//
// 一覧は models フォルダの GGUF と、Ollama が保存したモデル。
// 選ぶと Go が今のモデルを解放して、新しいモデルを読み込む(数秒〜十数秒)。
import { useCallback, useEffect, useRef, useState } from "react";
import { Events } from "@wailsio/runtime";
import { ChatService } from "../bindings/aibow/cmd/aibow";
import type { ModelEntry, ModelInfo } from "../bindings/aibow";
import type { Transcript } from "./transcript";
import { errorMessage } from "./errors";

// unavailable は起動に失敗した状態(lib が無いなど)。何もできないので、ずっと操作できなくする
export type Busy = "idle" | "generating" | "loading" | "unavailable";

export interface Models {
  models: ModelEntry[];
  current: ModelInfo | null; // 今読み込まれているモデル(Go の ModelInfo)
  gpu: boolean;
  loading: boolean;          // モデルを読み込み中か(起動時・切り替え時)
  switchModel(id: string): Promise<void>;
  reload(): Promise<void>;
}

export function useModels(t: Pick<Transcript, "addNote" | "addError">, setBusy: (b: Busy) => void): Models {
  const { addNote, addError } = t;
  const [models, setModels] = useState<ModelEntry[]>([]);
  const [current, setCurrent] = useState<ModelInfo | null>(null);
  const [gpu, setGpu] = useState(false);
  const [loading, setLoading] = useState(false);

  // 起動直後は Go がモデルを裏で読み込んでいる最中のことがある。終わると models イベントが来る。
  // Models の戻り値とイベントは別の経路で届くので、イベントが先に着いても大丈夫なように覚えておく。
  const startupLoading = useRef(false);
  const startupDone = useRef(false);
  const unavailable = useRef(false);

  const reload = useCallback(async () => {
    const d = await ChatService.Models();
    if (d.error) {
      // リリース版はコンソールが無いので、起動に失敗した理由はここでしか見えない
      if (!unavailable.current) {
        unavailable.current = true;
        setBusy("unavailable");
        addError(`起動できません: ${d.error}`);
      }
      return;
    }
    setModels(d.models ?? []);
    setCurrent(d.current);
    setGpu(d.gpu);
    if (d.loading && !startupLoading.current && !startupDone.current) {
      startupLoading.current = true;
      setLoading(true);
      setBusy("loading");
      addNote(`${d.loading} を読み込み中…`);
    }
  }, [addNote, setBusy]);

  // 起動時: 一覧を取り、起動時の読み込みが終わった合図(models イベント)を待つ
  useEffect(() => {
    const off = Events.On("models", async () => {
      startupDone.current = true;
      const wasLoading = startupLoading.current;
      startupLoading.current = false;
      const d = await ChatService.Models();
      setModels(d.models ?? []);
      setCurrent(d.current);
      if (!wasLoading) return;
      addNote(d.current
        ? `${d.current.name} を読み込みました(${d.current.load_sec.toFixed(1)} 秒)`
        : "モデルを読み込めませんでした。上で選び直してください");
      setLoading(false);
      setBusy("idle");
    });
    reload().catch(err => addError(`モデル一覧を取得できません: ${errorMessage(err)}`));
    return off;
  }, [reload, addNote, addError, setBusy]);

  const switchModel = useCallback(async (id: string) => {
    const name = models.find(m => m.id === id)?.name ?? id;
    setLoading(true);
    setBusy("loading");
    addNote(`${name} を読み込み中…`);
    let failed = false;
    try {
      const info = await ChatService.SwitchModel(id); // 読み込み終わるまで返らない
      if (info) addNote(`モデルを ${info.name} に切り替えました(読み込み ${info.load_sec.toFixed(1)} 秒)`);
    } catch (err) {
      // Go が error を返すと、ここに来る
      addError(errorMessage(err));
      failed = true;
    }
    const d = await ChatService.Models(); // 失敗したときは元のモデルの表示に戻る
    setModels(d.models ?? []);
    setCurrent(d.current);
    if (failed && d.current) addNote(`元のモデル ${d.current.name} に戻しました`);
    setLoading(false);
    setBusy("idle");
  }, [models, addNote, addError, setBusy]);

  return { models, current, gpu, loading, switchModel, reload };
}
