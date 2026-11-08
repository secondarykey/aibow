// backendWails.ts: デスクトップのアプリ(Wails)での Go を呼ぶ係の中身(backend.ts)。
//
// Wails のバインディングとランタイムをそのまま渡す。
// @wailsio/runtime は、読み込まれるとページ全体のマウス操作を見て、Frameless のウィンドウのドラッグとリサイズを行う。
// main.tsx が最初の描画の前にこのファイルを読み込む(initBackend)ので、ここで読み込めば足りる。
import { Browser, CancelError, Clipboard, Events, Window } from "@wailsio/runtime";
import { ChatService } from "../bindings/github.com/secondarykey/aibow/_cmd/aibow";
import type { AccountInfo } from "../bindings/github.com/secondarykey/aibow/_cmd/aibow";
import type { Backend, Me, MyUsage } from "./backend";

const notInApp = async (): Promise<never> => {
  throw new Error("利用者の管理は、aibow-server の画面をブラウザで開いて、管理者でログインして行います");
};
const linkInBrowser = async (): Promise<never> => {
  throw new Error("パスワードを決めるリンクは、ブラウザで開いてください");
};

// asMe は、Go の AccountInfo(アプリのアカウント。account.go)を画面の Me にする。
// 画面でつないだのにログインしていなければ、名前を空にする(LoginGate がログインの画面を出す)
function asMe(a: AccountInfo): Me {
  if (a.needLogin) return { login: true, name: "", lastUser: a.lastUser };
  return { login: a.login, name: a.name, viaKey: a.login, admin: a.admin, display_name: a.displayName, email: a.email, canLogout: a.canLogout };
}

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
  // aibow-server につないでいれば、キー(アプリ用のトークンか settings.json の serverKey)の持ち主を聞く。
  // 画面でつないだのにログインしていなければ、ログインの画面を出す(ブラウザと同じ。0031 の決定事項 44)。
  // 聞けなければ(aibow-server が止まっている)名前を出さない
  session: async () => {
    try {
      return asMe(await ChatService.Account());
    } catch {
      return { login: false, name: "" };
    }
  },
  // ログイン・ログアウトは、起動したままトークンを替える(つなぎ先は残す。account.go)
  login: async (name, password) => asMe(await ChatService.Login(name, password)),
  logout: async () => { await ChatService.Logout(); },
  // パスワードを決めるリンクはブラウザで開く(0022)。変えるのはアプリからもできる(アプリ用のトークンで。0031 の決定事項 45)
  linkUser: linkInBrowser,
  applyLink: linkInBrowser,
  changePassword: async (current, password) => { await ChatService.ChangeMyPassword(current, password); },
  setDisplayName: async name => asMe(await ChatService.SetMyDisplayName(name)),
  setEmail: async email => asMe(await ChatService.SetMyEmail(email)),
  // 自分の使用量は、aibow-server の口をアプリ用のトークンで呼ぶ(0033)。Go の usage.Mine と同じ形
  myUsage: async () => (await ChatService.MyUsage()) as unknown as MyUsage,
  onUnauthorized: () => () => {},
  // 利用者の管理はブラウザの管理者だけ(0018 の 11-0)。デスクトップには「利用者」のタブを出さないので、呼ばれない
  admin: {
    users: notInApp,
    create: notInApp,
    resetPassword: notInApp,
    renewKey: notInApp,
    setDisplayName: notInApp,
    setEmail: notInApp,
    remove: notInApp,
    usage: notInApp,
    usageOf: notInApp,
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
