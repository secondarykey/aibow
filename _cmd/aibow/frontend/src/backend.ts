// backend.ts: Go を呼ぶ係(0016 の 3)。
//
// 画面の部品は、Go のメソッドの呼び出し・Go からの知らせ(イベント)・呼び出しの停止・ウィンドウやクリップボードなど
// Wails のランタイムに頼ることを、ここだけを通して使う。
// デスクトップのアプリ(Wails)では、中身は Wails のバインディングとランタイムをそのまま渡すだけ。
// ブラウザから aibow-server につなぐとき(9-2)は、ここを fetch と SSE に差し替える(部品は変えない)。
//
// Go の型(bindings の models.ts。ConversationSettings など)は、部品から直接 import してよい。
// interface と enum だけで Wails の呼び出しの仕組みを含まない(wails3 generate bindings の -i)ので、ブラウザでもそのまま使える。
//
// @wailsio/runtime は、読み込まれるとページ全体のマウス操作を見て、Frameless のウィンドウのドラッグとリサイズを行う。
// main.tsx がこのファイルを最初に import するので、ここで読み込めば足りる。
import { Browser, CancelError, Clipboard, Events, Window } from "@wailsio/runtime";
import { ChatService as Bindings } from "../bindings/github.com/secondarykey/aibow/_cmd/aibow";
import type { ChatEvent } from "../bindings/github.com/secondarykey/aibow";

// ChatService は Go のメソッド。名前と引数は Go の ChatService(中身は ui.Service)のメソッドと同じ。
// Go で error を返すと、Promise が reject される
export const ChatService = Bindings;

// Call は止められる呼び出し(Send など、生成が終わるまで返らない長い呼び出し)。
// cancel() を呼ぶと Go の ctx がキャンセルされ、Promise は isCancel(err) が true になるエラーで終わる
export type Call<T> = Promise<T> & { cancel(): void };

// isCancel は、cancel() で止めた呼び出しのエラーか
export function isCancel(err: unknown): boolean {
  return err instanceof CancelError;
}

// ---- Go からの知らせ(イベント)。戻り値を呼ぶと受け取るのをやめる ---------------------------

// onChat は、生成の途中経過(chat イベント。Go の ui.ChatEventName)を受け取る。
// main.go の RegisterEvent で型を登録してあるので、ev.data は ChatEvent 型になる
export function onChat(f: (ev: ChatEvent) => void): () => void {
  return Events.On("chat", ev => f(ev.data));
}

// onModelLoaded は、起動時のモデルの読み込みが終わった合図(models イベント。Go の ui.ModelsEventName)を受け取る
export function onModelLoaded(f: () => void): () => void {
  return Events.On("models", () => f());
}

// onWindowFocus は、ウィンドウがアクティブに戻った合図を受け取る(IME の対策。imeFocus.tsx)。
// 名前は main.go の windowFocusEventName と同じ
export function onWindowFocus(f: () => void): () => void {
  return Events.On("aibow:window:focus", () => f());
}

// ---- Wails のランタイムに頼ること ---------------------------------------------------------

// copyText は文字をクリップボードに書く。
// まずブラウザの navigator.clipboard を使う(WebView2 の画面は http://wails.localhost で、安全な場所として扱われるので使える)。
// 使えなかったときは、Wails のランタイムの Clipboard.SetText(Go 側で OS のクリップボードに書く)を使う
export async function copyText(text: string): Promise<void> {
  try {
    await navigator.clipboard.writeText(text);
  } catch {
    await Clipboard.SetText(text);
  }
}

// openURL はリンクを OS のブラウザで開く
export function openURL(url: string): Promise<void> {
  return Browser.OpenURL(url);
}

// appWindow は Frameless のウィンドウの操作(タイトルバーのボタン。TitleBar.tsx)
export const appWindow = {
  minimise: () => Window.Minimise(),
  toggleMaximise: () => Window.ToggleMaximise(),
  isMaximised: () => Window.IsMaximised(),
  close: () => Window.Close(),
};
