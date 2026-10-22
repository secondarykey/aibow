// useAppSettings.ts: アプリの設定(画面の設定)。
//
// 画面の設定(質問を右上に出す・明るさ)は生成に関わらないので、変えたらすぐ保存する。
// GPU の層数は、設定の画面の「モデル」のタブでモデルごとに変える(useModels の setLayers。0015 の 8-4)。
//
// 新しい会話の既定は、プロファイル(設定の画面の「会話」の「プロファイル」。ProfileSettings.tsx。0014)で変える。
// ここの defaults は、新しい会話で最初に選ばれるプロファイルの値(Go が返す。表示のためだけに持つ)。
import { useCallback, useRef, useState } from "react";
import { ChatService } from "./backend";
import type { AppSettings, DisplaySettings } from "../bindings/github.com/secondarykey/aibow";
import type { Transcript } from "./transcript";
import { errorMessage } from "./errors";

export interface AppSettingsState {
  settings: AppSettings | null;
  reload(): Promise<void>;
  setDisplay(d: DisplaySettings): void;                         // 画面の設定を変える(すぐ保存する)
}


export function useAppSettings(t: Pick<Transcript, "addError">): AppSettingsState {
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

  const setDisplay = useCallback((d: DisplaySettings) => {
    const cur = settingsRef.current;
    if (!cur) return;
    setSettings({ ...cur, display: d }); // 先に画面に効かせる。保存できなければ読み直して戻す
    ChatService.SetDisplay(d).catch(err => {
      addError(`画面の設定を保存できません: ${errorMessage(err)}`);
      reload().catch(e => addError(errorMessage(e)));
    });
  }, [setSettings, reload, addError]);

  return { settings, reload, setDisplay };
}
