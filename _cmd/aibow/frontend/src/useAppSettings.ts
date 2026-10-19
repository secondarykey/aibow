// useAppSettings.ts: アプリの設定(GPU に載せる層の数・画面の設定)と、新しい会話の既定の設定。
//
// GPU の層数は、層を GPU と CPU のどちらに置くかが読み込み時に決まるので、変えるとモデルを読み直す。
// 画面の設定(質問を右上に出す・明るさ)は生成に関わらないので、変えたらすぐ保存する。
//
// 新しい会話の既定(設定の画面の「新しい会話の既定」)は、会話の設定と同じく変えたらすぐ保存する。今ある会話には効かない。
// 会話の上限・返事の上限も既定の一部(会話を作るときに写す)。作業領域を作り直してみるのは App(applyLimits)。
import { useCallback, useRef, useState } from "react";
import { ChatService } from "../bindings/github.com/secondarykey/aibow/_cmd/aibow";
import type { AppSettings, ConversationSettings, DisplaySettings, WorkspaceSettings } from "../bindings/github.com/secondarykey/aibow";
import type { Transcript } from "./transcript";
import type { Notifier } from "./toast";
import type { Busy } from "./useModels";
import { errorMessage } from "./errors";

export interface AppSettingsState {
  settings: AppSettings | null;
  reload(): Promise<void>;
  apply(w: WorkspaceSettings): Promise<void>;
  setDisplay(d: DisplaySettings): void;                         // 画面の設定を変える(すぐ保存する)
  setDefaults(s: ConversationSettings): Promise<void>;           // 会話の設定を丸ごと既定にする(会話の設定の画面のボタン)
  updateDefaults(patch: Partial<ConversationSettings>): void; // 既定の一部を変える(設定の画面の「新しい会話の既定」)
}

// 既定を変えてから Go に保存するまで待つ時間(useConversations の SAVE_DELAY_MS と同じ理由。1文字ごとに送らない)
const SAVE_DELAY_MS = 400;

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
  const saveTimer = useRef<number | undefined>(undefined);

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

  const setDefaults = useCallback(async (s: ConversationSettings) => {
    window.clearTimeout(saveTimer.current); // まだ送っていない既定の変更で、あとから上書きしないように
    try {
      await ChatService.SetDefaultSettings(s);
      notify("この設定を新しい会話の既定にしました");
      await reload();
    } catch (err) {
      addError(errorMessage(err));
    }
  }, [reload, notify, addError]);

  const updateDefaults = useCallback((patch: Partial<ConversationSettings>) => {
    const cur = settingsRef.current;
    if (!cur) return;
    const defaults = { ...cur.defaults, ...patch };
    setSettings({ ...cur, defaults });
    window.clearTimeout(saveTimer.current);
    // 保存したあとに読み直さない(打っている途中の文が、届いた古い値で戻らないように)
    saveTimer.current = window.setTimeout(() => {
      ChatService.SetDefaultSettings(defaults)
        .catch(err => addError(`新しい会話の既定を保存できません: ${errorMessage(err)}`));
    }, SAVE_DELAY_MS);
  }, [setSettings, addError]);

  const setDisplay = useCallback((d: DisplaySettings) => {
    const cur = settingsRef.current;
    if (!cur) return;
    setSettings({ ...cur, display: d }); // 先に画面に効かせる。保存できなければ読み直して戻す
    ChatService.SetDisplay(d).catch(err => {
      addError(`画面の設定を保存できません: ${errorMessage(err)}`);
      reload().catch(e => addError(errorMessage(e)));
    });
  }, [setSettings, reload, addError]);

  return { settings, reload, apply, setDisplay, setDefaults, updateDefaults };
}
