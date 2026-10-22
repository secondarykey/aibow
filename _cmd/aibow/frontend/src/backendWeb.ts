// backendWeb.ts: ブラウザ(aibow-server が配った画面)での Go を呼ぶ係の中身(backend.ts。0016 の 9-2)。
//
// デスクトップで Wails がしていることを、aibow-server の画面用の口(ui/web.go)に向けて自分で書いたもの:
//
//	メソッドの呼び出し … POST /aibow/ui/call/{メソッド}。本文は引数の JSON の配列、返事は戻り値の JSON(Wails の呼び出しと同じ形)
//	Go からの知らせ   … GET /aibow/ui/events(SSE)を EventSource で受け取る。event の名前は Wails のイベントと同じ(chat・models)
//	停止             … fetch を AbortController で止める。つながりが切れて、Go の ctx がキャンセルされる
//
// ブラウザには OS のウィンドウもクリップボードの Go 側の書き込みも無いので、そこはブラウザの仕組みを使う(0016 の 17)。
import type { ChatEvent } from "../bindings/github.com/secondarykey/aibow";
import type { Backend, Call, ChatAPI } from "./backend";
import { errorMessage } from "./errors";

// CancelError は、cancel() で止めた呼び出しのエラー(Wails の CancelError と同じ役)
class CancelError extends Error {}

// call は Go のメソッドを1つ呼ぶ。生成が終わるまで返らない長い呼び出し(Send など)も、返事が来るまで待つ。
function call<T>(method: string, args: unknown[]): Call<T> {
  const ac = new AbortController();
  const p = (async () => {
    try {
      const res = await fetch(`/aibow/ui/call/${method}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(args),
        signal: ac.signal,
      });
      const body = await res.json().catch(() => null);
      if (ac.signal.aborted) throw new CancelError("停止しました"); // 本文を読んでいる間に止めた
      if (!res.ok) {
        // aibow-server のエラーの形:{"error": {"message", "type"}}。Wails と同じく、message が Go のエラーの文の Error にする
        throw new Error(body?.error?.message ?? `${method}: ${res.status} ${res.statusText}`);
      }
      return body as T;
    } catch (err) {
      if (ac.signal.aborted) throw new CancelError("停止しました");
      if (err instanceof TypeError) {
        // fetch が投げる TypeError は、つながらなかったとき(aibow-server を止めた・PC がスリープから戻った直後など)
        throw new Error(`aibow-server につながりません(${errorMessage(err)})。aibow-server が動いているか確かめてください`);
      }
      throw err;
    }
  })();
  return Object.assign(p, { cancel: () => ac.abort() });
}

// ChatService は、どのメソッドの名前で呼ばれても、その名前で aibow-server に頼む。
// メソッドの一覧を画面に持たない(Go の側が、呼べるメソッドかを決める。呼べなければ 404 のエラーになる)
const ChatService = new Proxy({} as ChatAPI, {
  get: (_target, name) => (...args: unknown[]) => call(String(name), args),
});

// events は、知らせを受け取る SSE のつながり。最初に受け取るとき(画面を開いたとき)に1つだけ作り、画面を閉じるまで使う。
// 切れたら EventSource が自分でつなぎ直す。切れている間の知らせは届かないので、つなぎ直したら画面が取り直す(onReconnect。0016 の 9-3)
let source: EventSource | null = null;
let opened = false; // 一度つながったか(2回目からの open がつなぎ直し)
const reconnected = new Set<() => void>();

function events(): EventSource {
  if (!source) {
    source = new EventSource("/aibow/ui/events");
    source.addEventListener("open", () => {
      if (opened) reconnected.forEach(f => f());
      opened = true;
    });
  }
  return source;
}

function on(name: string, f: (data: string) => void): () => void {
  const s = events();
  const handler = (e: MessageEvent<string>) => f(e.data);
  s.addEventListener(name, handler);
  return () => s.removeEventListener(name, handler);
}

export const backend: Backend = {
  ChatService,
  isCancel: err => err instanceof CancelError,

  onChat: f => on("chat", data => f(JSON.parse(data) as ChatEvent)),
  onModelLoaded: f => on("models", () => f()),
  // ブラウザには Wails のウィンドウのイベントが無いので、ブラウザの focus で代わりにする(0016 の 17)
  onWindowFocus: f => {
    const handler = () => f();
    window.addEventListener("focus", handler);
    return () => window.removeEventListener("focus", handler);
  },
  onReconnect: f => {
    events();
    reconnected.add(f);
    return () => reconnected.delete(f);
  },

  // http://127.0.0.1 は安全な場所として扱われるので、navigator.clipboard が使える
  copyText: text => navigator.clipboard.writeText(text),
  // 新しいタブで開く(画面の中で開くと、チャットに戻れなくなる)。noopener で、開いたページからこの画面を触らせない
  openURL: async url => {
    window.open(url, "_blank", "noopener,noreferrer");
  },

  // ブラウザにはタイトルバーを出さないので、呼ばれることは無い(呼ばれても何もしない)
  appWindow: {
    minimise: async () => {},
    toggleMaximise: async () => {},
    isMaximised: async () => false,
    close: async () => {},
  },
};
