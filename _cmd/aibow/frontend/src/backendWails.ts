// backendWails.ts: デスクトップのアプリ(Wails)での Go を呼ぶ係の中身(backend.ts)。
//
// Wails のバインディングとランタイムをそのまま渡す。
// @wailsio/runtime は、読み込まれるとページ全体のマウス操作を見て、Frameless のウィンドウのドラッグとリサイズを行う。
// main.tsx が最初の描画の前にこのファイルを読み込む(initBackend)ので、ここで読み込めば足りる。
import { Browser, CancelError, Clipboard, Events, Window } from "@wailsio/runtime";
import { ChatService } from "../bindings/github.com/secondarykey/aibow/_cmd/aibow";
import type { Backend } from "./backend";

export const backend: Backend = {
  ChatService,
  isCancel: err => err instanceof CancelError,

  // main.go の RegisterEvent で型を登録してあるので、ev.data は ChatEvent 型になる
  onChat: f => Events.On("chat", ev => f(ev.data)),
  onModelLoaded: f => Events.On("models", () => f()),
  // 名前は main.go の windowFocusEventName と同じ
  onWindowFocus: f => Events.On("aibow:window:focus", () => f()),

  // まずブラウザの navigator.clipboard を使う(WebView2 の画面は http://wails.localhost で、安全な場所として扱われるので使える)。
  // 使えなかったときは、Wails のランタイムの Clipboard.SetText(Go 側で OS のクリップボードに書く)を使う
  copyText: async text => {
    try {
      await navigator.clipboard.writeText(text);
    } catch {
      await Clipboard.SetText(text);
    }
  },
  openURL: url => Browser.OpenURL(url),

  appWindow: {
    minimise: () => Window.Minimise(),
    toggleMaximise: () => Window.ToggleMaximise(),
    isMaximised: () => Window.IsMaximised(),
    close: () => Window.Close(),
  },
};
