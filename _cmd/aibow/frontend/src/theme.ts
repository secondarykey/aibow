// theme.ts: 画面の明るさ(ライト / ダーク)。設定の「システム」なら OS の設定に合わせる。
//
// 実際に使う明るさ(light / dark)はここで1つに決め、<html data-theme="…"> に書く。
// CSS(style.css の :root[data-theme="dark"])も mermaid の図の配色(useDarkTheme)も、この値だけを見る。
// OS の設定(prefers-color-scheme)を直接見る場所を1つにしておくと、「システム」と手で選んだ明るさが食い違わない。
//
// 設定は Go の DB(画面の設定 DisplaySettings.Theme)が正。ただし Go から設定が届くのは画面を描いたあとなので、
// 起動の直後に OS の明るさで描いてから切り替わる(ちらつく)。それを防ぐため、最後の設定の写しを localStorage に置き、
// 最初の描画の前にそれで決める(initTheme)。
import { useSyncExternalStore } from "react";
import { STORED_THEME, loadStored, saveStored } from "./stored";

export type ThemeSetting = "system" | "light" | "dark";

const DARK_QUERY = "(prefers-color-scheme: dark)";

let setting: ThemeSetting = "system";
const listeners = new Set<() => void>();

function normalize(v: string | null | undefined): ThemeSetting {
  return v === "light" || v === "dark" ? v : "system";
}

// effective は、今使う明るさ。「システム」なら OS の設定
function effective(): "light" | "dark" {
  if (setting !== "system") return setting;
  return window.matchMedia(DARK_QUERY).matches ? "dark" : "light";
}

function apply() {
  document.documentElement.dataset.theme = effective();
  listeners.forEach(l => l());
}

// initTheme は、最初の描画の前に呼ぶ(main.tsx)。前回の設定の写しで明るさを決め、OS の切り替えを見始める
export function initTheme() {
  setting = normalize(loadStored(STORED_THEME));
  apply();
  // 「システム」の間は、OS の明るさを切り替えたらすぐ追いかける
  window.matchMedia(DARK_QUERY).addEventListener("change", () => {
    if (setting === "system") apply();
  });
}

// setTheme は、設定(Go から届いた値・設定の画面で選んだ値)を画面に効かせ、次の起動のために写しを覚える
export function setTheme(v: string) {
  setting = normalize(v);
  saveStored(STORED_THEME, setting);
  apply();
}

function subscribe(l: () => void) {
  listeners.add(l);
  return () => listeners.delete(l);
}

// useDarkTheme は、今ダークか(mermaid の図の配色を合わせるために使う)。明るさが変わったら描き直す
export function useDarkTheme(): boolean {
  return useSyncExternalStore(subscribe, () => effective() === "dark");
}
