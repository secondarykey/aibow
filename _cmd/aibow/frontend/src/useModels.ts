// useModels.ts: モデルの一覧と切り替え。
//
// 一覧は models フォルダの GGUF と、Ollama が保存したモデル。
// 選ぶと Go が今のモデルを解放して、新しいモデルを読み込む(数秒〜十数秒)。
import { useCallback, useEffect, useRef, useState } from "react";
import { Events } from "@wailsio/runtime";
import { ChatService } from "../bindings/github.com/secondarykey/aibow/_cmd/aibow";
// Go の models パッケージ。"…/aibow/models" と書くと、Wails が aibow パッケージに作る models.ts の方が選ばれるので、index まで書く
import type { Entry as ModelEntry } from "../bindings/github.com/secondarykey/aibow/models/index";
import type { ModelInfo } from "../bindings/github.com/secondarykey/aibow/engine";
import type { Transcript } from "./transcript";
import type { Notifier } from "./toast";
import { errorMessage } from "./errors";

// 起動時のお知らせ(使われなくなった環境変数など)を出しておく時間。文が長いので普通のトーストより長くする
const NOTICE_MS = 20000;

// unavailable は起動に失敗した状態(lib が無いなど)。何もできないので、ずっと操作できなくする
export type Busy = "idle" | "generating" | "loading" | "unavailable";

export interface Models {
  models: ModelEntry[];
  current: ModelInfo | null; // 今読み込まれているモデル(Go の ModelInfo)
  gpu: boolean;
  loading: boolean;          // モデルを読み込み中か(起動時・切り替え時)
  switchModel(id: string): Promise<void>;
  reload(): Promise<boolean>; // 一覧を取り直す。起動に失敗していたら false
}

// formatWarning は、書式(チャットテンプレート)に対応していない種類のモデルのときのお知らせ。対応していれば null
function formatWarning(info: ModelInfo): string | null {
  return info.format ? null : `${info.name} の書式(種類: ${info.arch || "不明"})には未対応です。送信するとエラーになります`;
}

// 読み込みのお知らせはトースト(n)に出す。「読み込み中…」は出したままにし、終わったら同じ key で置き換える
export function useModels(n: Notifier, t: Pick<Transcript, "addError">, setBusy: (b: Busy) => void): Models {
  const { notify, dismissKey } = n;
  const { addError } = t;
  const [models, setModels] = useState<ModelEntry[]>([]);
  const [current, setCurrent] = useState<ModelInfo | null>(null);
  const [gpu, setGpu] = useState(false);
  const [loading, setLoading] = useState(false);

  // 起動直後は Go がモデルを裏で読み込んでいる最中のことがある。終わると models イベントが来る。
  // Models の戻り値とイベントは別の経路で届くので、イベントが先に着いても大丈夫なように覚えておく。
  const startupLoading = useRef(false);
  const startupDone = useRef(false);
  const unavailable = useRef(false);
  const noticesShown = useRef(false);

  const reload = useCallback(async () => {
    const d = await ChatService.Models();
    if (d.error) {
      // リリース版はコンソールが無いので、起動に失敗した理由はここでしか見えない
      if (!unavailable.current) {
        unavailable.current = true;
        for (const text of d.notices ?? []) notify(text, { ms: NOTICE_MS });
        setBusy("unavailable");
        addError(`起動できません: ${d.error}`);
      }
      return false;
    }
    setModels(d.models ?? []);
    setCurrent(d.current);
    setGpu(d.gpu);
    // 起動時のお知らせ(使われなくなった環境変数など)。一覧は何度も取り直すので、最初の1回だけ出す
    if (!noticesShown.current) {
      noticesShown.current = true;
      for (const text of d.notices ?? []) notify(text, { ms: NOTICE_MS });
    }
    if (d.loading && !startupLoading.current && !startupDone.current) {
      startupLoading.current = true;
      setLoading(true);
      setBusy("loading");
      notify(`${d.loading} を読み込み中…`, { key: "model", sticky: true });
    }
    return true;
  }, [notify, addError, setBusy]);

  // 起動時の読み込みが終わった合図(models イベント)を待つ。最初の一覧の取得(reload)は App が呼ぶ
  useEffect(() => {
    const off = Events.On("models", async () => {
      startupDone.current = true;
      const wasLoading = startupLoading.current;
      startupLoading.current = false;
      const d = await ChatService.Models();
      setModels(d.models ?? []);
      setCurrent(d.current);
      if (!wasLoading) return;
      if (d.current) {
        notify(formatWarning(d.current) ?? `${d.current.name} を読み込みました(${d.current.load_sec.toFixed(1)} 秒)`, { key: "model" });
      } else {
        dismissKey("model");
        addError("モデルを読み込めませんでした。入力欄の下で選び直してください");
      }
      setLoading(false);
      setBusy("idle");
    });
    return off;
  }, [notify, dismissKey, addError, setBusy]);

  const switchModel = useCallback(async (id: string) => {
    const name = models.find(m => m.id === id)?.name ?? id;
    setLoading(true);
    setBusy("loading");
    notify(`${name} を読み込み中…`, { key: "model", sticky: true });
    let failed = false;
    try {
      const info = await ChatService.SwitchModel(id); // 読み込み終わるまで返らない
      if (info) notify(formatWarning(info) ?? `モデルを ${info.name} に切り替えました(読み込み ${info.load_sec.toFixed(1)} 秒)`, { key: "model" });
    } catch (err) {
      // Go が error を返すと、ここに来る
      addError(errorMessage(err));
      failed = true;
    }
    const d = await ChatService.Models(); // 失敗したときは元のモデルの表示に戻る
    setModels(d.models ?? []);
    setCurrent(d.current);
    if (failed) {
      if (d.current) notify(`元のモデル ${d.current.name} に戻しました`, { key: "model" });
      else dismissKey("model");
    }
    setLoading(false);
    setBusy("idle");
  }, [models, notify, dismissKey, addError, setBusy]);

  return { models, current, gpu, loading, switchModel, reload };
}
