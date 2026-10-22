// backend.ts: Go を呼ぶ係(0016 の 3)。
//
// 画面の部品は、Go のメソッドの呼び出し・Go からの知らせ(イベント)・呼び出しの停止・ウィンドウやクリップボードなど
// 実行する場所に頼ることを、ここだけを通して使う。中身は2つあり、起動のとき(main.tsx の initBackend)にどちらかを読み込む:
//
//	デスクトップのアプリ(Wails)… backendWails.ts。Wails のバインディングとランタイムをそのまま渡す
//	ブラウザ(aibow-server)    … backendWeb.ts。fetch(POST /aibow/ui/call/{メソッド})と SSE(GET /aibow/ui/events)
//
// ブラウザでは @wailsio/runtime を読み込まない(読み込むと、ページ全体の右クリックのメニューを消すなどの動きが入るため)。
// そのため、どちらも import() であとから読み込み、ここは型と、読み込んだほうへの受け渡しだけを持つ。
//
// Go の型(bindings の models.ts。ConversationSettings など)は、部品から直接 import してよい。
// interface と enum だけで Wails の呼び出しの仕組みを含まない(wails3 generate bindings の -i)ので、ブラウザでもそのまま使える。
import type { ChatEvent } from "../bindings/github.com/secondarykey/aibow";
import type { ChatService as Bindings } from "../bindings/github.com/secondarykey/aibow/_cmd/aibow";

// Call は止められる呼び出し(Send など、生成が終わるまで返らない長い呼び出し)。
// cancel() を呼ぶと Go の ctx がキャンセルされ、Promise は isCancel(err) が true になるエラーで終わる
export type Call<T> = Promise<T> & { cancel(): void };

// ChatAPI は Go のメソッド。名前と引数は Go の ChatService(中身は ui.Service)のメソッドと同じ(型はバインディングから取る)。
// Go で error を返すと、Promise が reject される(message が Go のエラーの文)
type Methods = typeof Bindings;
export type ChatAPI = {
  [K in keyof Methods]: (...args: Parameters<Methods[K]>) => Call<Awaited<ReturnType<Methods[K]>>>;
};

// Backend は、Go を呼ぶ係の中身(デスクトップとブラウザの2つ)が揃えるもの。
export interface Backend {
  ChatService: ChatAPI;
  isCancel(err: unknown): boolean;
  // Go からの知らせ。戻り値を呼ぶと受け取るのをやめる
  onChat(f: (ev: ChatEvent) => void): () => void; // 生成の途中経過(Go の ui.ChatEventName)
  onModelLoaded(f: () => void): () => void;       // 起動時のモデルの読み込みが終わった(Go の ui.ModelsEventName)
  onWindowFocus(f: () => void): () => void;       // ウィンドウがアクティブに戻った(IME の対策。imeFocus.tsx)
  onReconnect(f: () => void): () => void;         // 知らせのつながりが切れて、つなぎ直した(ブラウザだけ。デスクトップでは来ない)
  // ログイン(ブラウザだけ。0017 の 10-1)。デスクトップは画面でログインしない(aibow-server につないでいれば、API キーの持ち主を返す)
  session(): Promise<Me>;                         // ログインが要るか・ログインしている人(Go の auth.Me)
  login(name: string, password: string): Promise<Me>; // 違えば reject(message が理由の文)
  logout(): Promise<void>;
  onUnauthorized(f: () => void): () => void;      // 呼び出しが「ログインしてください」(401)で断られた(ログインの期限が切れた)
  copyText(text: string): Promise<void>;
  openURL(url: string): Promise<void>;            // リンクを開く(デスクトップは OS のブラウザ、ブラウザは新しいタブ)
  appWindow: {                                    // Frameless のウィンドウの操作(タイトルバー。ブラウザでは何もしない)
    minimise(): Promise<void>;
    toggleMaximise(): Promise<void>;
    isMaximised(): Promise<boolean>;
    close(): Promise<void>;
  };
}

// Me は、ログインの様子(Go の auth.Me)。login が false なら、ログインが要らない形(ログインもログアウトも出さない)
export interface Me {
  login: boolean;
  name: string; // ログインしている人の名前。していなければ空
  // viaKey は、ログインではなく API キーで利用者が決まっている(デスクトップのアプリが aibow-server につないでいるとき。0017 の 10-4)。
  // 名前は出すが、ログアウトは出さない(キーは settings.json に書いたもの)
  viaKey?: boolean;
}

// isBrowser は、ブラウザ(aibow-server が配った画面)か(0016 の 16)。見た目の分かれ道にだけ使う(呼び出しは分けない)。
// aibow-server は index.html にこの印を足して配る(_cmd/aibow-server の browser.go)。
// 場所(URL)で見分けないのは、Wails の server モード(確認用の一時ビルド)の画面もブラウザだと思ってしまうため
export const isBrowser = document.querySelector('meta[name="aibow-browser"]') !== null;

let b: Backend;

// ChatService は Go のメソッド。initBackend のあとに使える(import した側にも、入れ替えたものが見える)
export let ChatService: ChatAPI;

// initBackend は、どちらの中身を使うかを決めて読み込む。最初の描画の前に1回呼ぶ(main.tsx)。
export async function initBackend(): Promise<void> {
  const m = isBrowser ? await import("./backendWeb") : await import("./backendWails");
  b = m.backend;
  ChatService = b.ChatService;
}

export function isCancel(err: unknown): boolean {
  return b.isCancel(err);
}

export function onChat(f: (ev: ChatEvent) => void): () => void {
  return b.onChat(f);
}

export function onModelLoaded(f: () => void): () => void {
  return b.onModelLoaded(f);
}

export function onWindowFocus(f: () => void): () => void {
  return b.onWindowFocus(f);
}

export function onReconnect(f: () => void): () => void {
  return b.onReconnect(f);
}

// me は、起動のときに聞いたログインの様子(main.tsx が描く前に入れ、ログインしたら入れ直す)
export let me: Me = { login: false, name: "" };

export async function session(): Promise<Me> {
  me = await b.session();
  return me;
}

export async function login(name: string, password: string): Promise<Me> {
  me = await b.login(name, password);
  return me;
}

export function logout(): Promise<void> {
  return b.logout();
}

export function onUnauthorized(f: () => void): () => void {
  return b.onUnauthorized(f);
}

export function copyText(text: string): Promise<void> {
  return b.copyText(text);
}

export function openURL(url: string): Promise<void> {
  return b.openURL(url);
}

export const appWindow: Backend["appWindow"] = {
  minimise: () => b.appWindow.minimise(),
  toggleMaximise: () => b.appWindow.toggleMaximise(),
  isMaximised: () => b.appWindow.isMaximised(),
  close: () => b.appWindow.close(),
};
