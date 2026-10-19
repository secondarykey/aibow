// useAppSettings.ts: アプリの設定(GPU に載せる層の数・画面の設定)。
//
// GPU の層数は、層を GPU と CPU のどちらに置くかが読み込み時に決まるので、変えるとモデルを読み直す。
// 画面の設定(質問を右上に出す・明るさ)は生成に関わらないので、変えたらすぐ保存する。
//
// 新しい会話の既定は、プロファイル(設定の画面の「会話」の「プロファイル」。ProfileSettings.tsx。0014)で変える。
// ここの defaults は、新しい会話で最初に選ばれるプロファイルの値(Go が返す。表示のためだけに持つ)。
import { useCallback, useRef, useState } from "react";
import { ChatService } from "../bindings/github.com/secondarykey/aibow/_cmd/aibow";
import type { AppSettings, DisplaySettings, WorkspaceSettings } from "../bindings/github.com/secondarykey/aibow";
import type { Transcript } from "./transcript";
import type { Notifier } from "./toast";
import type { Busy } from "./useModels";
import { errorMessage } from "./errors";

export interface AppSettingsState {
  settings: AppSettings | null;
  reload(): Promise<void>;
  apply(w: WorkspaceSettings): Promise<void>;
  setDisplay(d: DisplaySettings): void;                         // 画面の設定を変える(すぐ保存する)
}


export function useAppSettings(
  n: Notifier,
  t: Pick<Transcript, "addError">,
  setBusy: (b: Busy) => void,
  reloadModels: () => Promise<boolean>,
): AppSettingsState {
  const { notify, dismissKey } = n;
  const { addError } = t;
  const [settings, setSettingsState] = useState<AppSettings | null>(null);
  // 既定の変更をまとめるときに、最新の設定を読むための ref
  const settingsRef = useRef<AppSettings | null>(null);
  const setSettings = useCallback((a: AppSettings | null) => {
    settingsRef.current = a;
    setSettingsState(a);
  }, []);

  const reload = useCallback(async () => {
    setSettings(await ChatService.AppSettings());
  }, [setSettings]);

  const apply = useCallback(async (w: WorkspaceSettings) => {
    if (!settings) return;
    const old = settings.workspace;
    const { gpu_layers: gpuLayers } = w;
    setBusy("loading");
    try {
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

  const setDisplay = useCallback((d: DisplaySettings) => {
    const cur = settingsRef.current;
    if (!cur) return;
    setSettings({ ...cur, display: d }); // 先に画面に効かせる。保存できなければ読み直して戻す
    ChatService.SetDisplay(d).catch(err => {
      addError(`画面の設定を保存できません: ${errorMessage(err)}`);
      reload().catch(e => addError(errorMessage(e)));
    });
  }, [setSettings, reload, addError]);

  return { settings, reload, apply, setDisplay };
}
