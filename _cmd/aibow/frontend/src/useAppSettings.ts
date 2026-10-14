// useAppSettings.ts: アプリ全体の設定(会話の上限・返事の上限・GPU に載せる層の数・新しい会話の既定の設定)。
//
// 会話ごとの設定と違い、変えるとエンジンに手を入れる必要がある:
//   会話の上限・返事の上限 … 合計の大きさで作業領域(KV キャッシュの置き場)だけ作り直す。モデルは読み直さない
//   GPU の層数             … 層を GPU と CPU のどちらに置くかは読み込み時に決まるので、モデルを読み直す
import { useCallback, useState } from "react";
import { ChatService } from "../bindings/github.com/secondarykey/aibow/_cmd/aibow";
import type { AppSettings, ConversationSettings, WorkspaceSettings } from "../bindings/github.com/secondarykey/aibow";
import type { Transcript } from "./transcript";
import type { Notifier } from "./toast";
import type { Busy } from "./useModels";
import { errorMessage } from "./errors";

export interface AppSettingsState {
  settings: AppSettings | null;
  reload(): Promise<void>;
  apply(w: WorkspaceSettings): Promise<void>;
  setDefaults(s: ConversationSettings): Promise<void>;
}

export function useAppSettings(
  n: Notifier,
  t: Pick<Transcript, "addError">,
  setBusy: (b: Busy) => void,
  reloadModels: () => Promise<boolean>,
): AppSettingsState {
  const { notify, dismissKey } = n;
  const { addError } = t;
  const [settings, setSettings] = useState<AppSettings | null>(null);

  const reload = useCallback(async () => {
    setSettings(await ChatService.AppSettings());
  }, []);

  const apply = useCallback(async (w: WorkspaceSettings) => {
    if (!settings) return;
    const old = settings.workspace;
    const { history_tokens: history, reply_tokens: reply, gpu_layers: gpuLayers } = w;
    setBusy("loading");
    try {
      if (history !== old.history_tokens || reply !== old.reply_tokens) {
        const info = await ChatService.SetLimits(history, reply);
        const size = (history + reply).toLocaleString();
        notify(info
          ? `会話の上限 ${history.toLocaleString()} + 返事の上限 ${reply.toLocaleString()} = 作業領域 ${size} トークンにしました(モデルは読み直していません)`
          : `会話の上限・返事の上限を変えました(作業領域 ${size} トークン。次にモデルを読み込むときから効きます)`);
      }
      if (gpuLayers !== old.gpu_layers) {
        notify(`GPU に載せる層を ${gpuLayers} にして、モデルを読み込み直しています…`, { key: "model", sticky: true });
        const info = await ChatService.SetGPULayers(gpuLayers);
        notify(info
          ? `GPU に載せる層を ${gpuLayers} にして読み込み直しました(${info.load_sec.toFixed(1)} 秒)`
          : `GPU に載せる層を ${gpuLayers} にしました(次にモデルを読み込むときから効きます)`, { key: "model" });
      }
    } catch (err) {
      // Go 側で元の設定に戻している
      dismissKey("model");
      addError(errorMessage(err));
    }
    await Promise.all([reload(), reloadModels()]).catch(err => addError(errorMessage(err)));
    setBusy("idle");
  }, [settings, reload, reloadModels, notify, dismissKey, addError, setBusy]);

  const setDefaults = useCallback(async (s: ConversationSettings) => {
    try {
      await ChatService.SetDefaultSettings(s);
      notify("この設定を新しい会話の既定にしました");
      await reload();
    } catch (err) {
      addError(errorMessage(err));
    }
  }, [reload, notify, addError]);

  return { settings, reload, apply, setDefaults };
}
