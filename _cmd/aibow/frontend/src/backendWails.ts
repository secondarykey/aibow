// backendWails.ts: デスクトップのアプリ(Wails)での Go を呼ぶ係の中身(backend.ts)。
//
// Wails のバインディングとランタイムをそのまま渡す。
// @wailsio/runtime は、読み込まれるとページ全体のマウス操作を見て、Frameless のウィンドウのドラッグとリサイズを行う。
// main.tsx が最初の描画の前にこのファイルを読み込む(initBackend)ので、ここで読み込めば足りる。
import { Browser, CancelError, Clipboard, Events, Window } from "@wailsio/runtime";
import { ChatService } from "../bindings/github.com/secondarykey/aibow/_cmd/aibow";
import type { Backend } from "./backend";

const notInApp = async (): Promise<never> => {
  throw new Error("利用者の管理は、aibow-server の画面をブラウザで開いて、管理者でログインして行います");
};
const passwordInBrowser = async (): Promise<never> => {
  throw new Error("パスワードを決める・変えるのは、aibow-server の画面をブラウザで開いて行います");
};

export const backend: Backend = {
  ChatService,
  isCancel: err => err instanceof CancelError,

  // main.go の RegisterEvent で型を登録してあるので、ev.data は ChatEvent 型になる
  onChat: f => Events.On("chat", ev => f(ev.data)),
  onModelLoaded: f => Events.On("models", () => f()),
  // 名前は main.go の windowFocusEventName と同じ
  onWindowFocus: f => Events.On("aibow:window:focus", () => f()),
  // Wails のイベントはアプリの中で届くので、切れてつなぎ直すことは無い
  onReconnect: () => () => {},
  // デスクトップは画面でログインしない。aibow-server につないでいれば、API キー(settings.json の serverKey)の持ち主の名前を聞く。
  // 聞けなければ(aibow-server が止まっている)名前を出さない
  session: async () => {
    try {
      const a = await ChatService.Account();
      return { login: a.login, name: a.name, viaKey: a.login };
    } catch {
      return { login: false, name: "" };
    }
  },
  login: async () => { throw new Error("デスクトップのアプリにはログインがありません"); },
  logout: async () => {},
  // パスワードを決める・変えるはブラウザで行う(0022 のやらないこと)
  linkUser: passwordInBrowser,
  applyLink: passwordInBrowser,
  changePassword: passwordInBrowser,
  onUnauthorized: () => () => {},
  // 利用者の管理はブラウザの管理者だけ(0018 の 11-0)。デスクトップには「利用者」のタブを出さないので、呼ばれない
  admin: {
    users: notInApp,
    create: notInApp,
    resetPassword: notInApp,
    renewKey: notInApp,
    remove: notInApp,
  },

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
