// backendWeb.ts: ブラウザ(aibow-server が配った画面)での Go を呼ぶ係の中身(backend.ts。0016 の 9-2)。
//
// デスクトップで Wails がしていることを、aibow-server の画面用の口(ui/web.go)に向けて自分で書いたもの:
//
//	メソッドの呼び出し … POST /_aibow/ui/call/{メソッド}。本文は引数の JSON の配列、返事は戻り値の JSON(Wails の呼び出しと同じ形)
//	Go からの知らせ   … GET /_aibow/ui/events(SSE)を EventSource で受け取る。event の名前は Wails のイベントと同じ(chat・models)
//	停止             … fetch を AbortController で止める。つながりが切れて、Go の ctx がキャンセルされる
//	ログイン         … POST /_aibow/auth/login で cookie を受け取る(0017 の 10-1)。以後の fetch と EventSource には、ブラウザが cookie を付ける
//
// ブラウザには OS のウィンドウもクリップボードの Go 側の書き込みも無いので、そこはブラウザの仕組みを使う(0016 の 17)。
//
// 口のパスは、画面の場所からの相対パス(先頭に / を付けない "_aibow/ui/…")で書く(0042)。
// aibow-server を -base /aibow で動かす・リバースプロキシで /aibow/ の下に出すと、画面は /aibow/ に、口は /aibow/_aibow/ui/… になる。
// 相対パスなら、画面がどこに置かれていても、画面と同じ頭の下の口を呼ぶ。
import type { ChatEvent } from "../bindings/github.com/secondarykey/aibow";
import type { AdminAPI, Backend, Call, ChatAPI, Me, MyUsage } from "./backend";
import { errorMessage } from "./errors";

// CancelError は、cancel() で止めた呼び出しのエラー(Wails の CancelError と同じ役)
class CancelError extends Error {}

// unauthorized は、呼び出しが 401(ログインしてください)で断られたときに知らせる相手(onUnauthorized)
const unauthorized = new Set<() => void>();

// current は、この画面がログインしていると思っている人の名前(me・ログイン・リンクで決めたときに入れる。ログインなしの形では空)。
// cookie はブラウザのタブで分け合うので、ほかのタブで別の人がログインすると、この画面の呼び出しも別の人として受けられる。
// 画面は前の人のまま(名前・管理者のタブ・会話の一覧)なので、気づいたら画面を閉じる(2026-11-20、確認表の A10 で見つかった。
// alice の画面のまま bob として呼んでいた)。気づく道は2つ:
//
//	呼び出しの返事 … aibow-server が、誰として受けたかを見出し X-Aibow-User に付ける(auth.UserHeader)。それが current と違う
//	タブに戻ったとき … 何も押さないうちに気づくよう、me を聞き直す(押してから気づくと、その1回は別の人として動いてしまう)
let current = "";
const userChanged = new Set<(name: string) => void>();
let changedTo: string | null = null; // 一度知らせたら、もう知らせない(画面は閉じている)

function remember(m: Me): Me {
  current = m.login ? m.name : "";
  return m;
}

// changed は、この画面が別の人として呼ばれていると分かったとき。知らせのつながり(SSE)も前の人のものなので切る
function changed(name: string) {
  if (changedTo !== null) return;
  changedTo = name;
  source?.close();
  userChanged.forEach(f => f(name));
}

// checkUser は、返事の見出しで、誰として受けられたかを確かめる。見出しが無い(ログインしていない)ときは 401 の側に任せる
function checkUser(res: Response) {
  const h = res.headers.get("X-Aibow-User");
  if (current === "" || h === null) return;
  const name = decodeURIComponent(h);
  if (name !== current) changed(name);
}

// recheck は、タブに戻ったときに、ログインしている人が変わっていないかを聞き直す
async function recheck() {
  if (current === "" || changedTo !== null || document.visibilityState !== "visible") return;
  try {
    const res = await fetch("_aibow/auth/me");
    const m = (await res.json()) as Me;
    if (!m.login) return;
    if (m.name === "") unauthorized.forEach(f => f()); // ほかのタブでログアウトした(ログインの画面に戻す)
    else if (m.name !== current) changed(m.name);
  } catch {
    // つながらないときは、次の呼び出しのエラーで伝わる
  }
}
document.addEventListener("visibilitychange", () => void recheck());
window.addEventListener("focus", () => void recheck());

// call は Go のメソッドを1つ呼ぶ。生成が終わるまで返らない長い呼び出し(Send など)も、返事が来るまで待つ。
function call<T>(method: string, args: unknown[]): Call<T> {
  const ac = new AbortController();
  const p = (async () => {
    try {
      const res = await fetch(`_aibow/ui/call/${method}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(args),
        signal: ac.signal,
      });
      checkUser(res);
      const body = await res.json().catch(() => null);
      if (ac.signal.aborted) throw new CancelError("停止しました"); // 本文を読んでいる間に止めた
      if (res.status === 401) unauthorized.forEach(f => f()); // ログインの期限が切れた(画面はログインの画面に戻る)
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
    source = new EventSource("_aibow/ui/events");
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

// auth は、ログインの口(/_aibow/auth/)を呼ぶ。返事は auth.Me。エラーは call と同じ形で読む
async function auth(method: "GET" | "POST", path: string, body?: unknown): Promise<Me> {
  let res: Response;
  try {
    res = await fetch(`_aibow/auth/${path}`, {
      method,
      headers: body === undefined ? {} : { "Content-Type": "application/json" },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch (err) {
    throw new Error(`aibow-server につながりません(${errorMessage(err)})。aibow-server が動いているか確かめてください`);
  }
  const data = await res.json().catch(() => null);
  if (!res.ok) throw new Error(data?.error?.message ?? `${path}: ${res.status} ${res.statusText}`);
  return data as Me;
}

// adminCall は、管理者の口(/_aibow/admin/。0018 の 11-0)を呼ぶ。エラーは call と同じ形で読む
async function adminCall<T>(method: "GET" | "POST" | "DELETE", path: string, body?: unknown): Promise<T> {
  let res: Response;
  try {
    res = await fetch(`_aibow/admin/${path}`, {
      method,
      headers: body === undefined ? {} : { "Content-Type": "application/json" },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch (err) {
    throw new Error(`aibow-server につながりません(${errorMessage(err)})。aibow-server が動いているか確かめてください`);
  }
  checkUser(res);
  const data = await res.json().catch(() => null);
  if (res.status === 401) unauthorized.forEach(f => f());
  if (!res.ok) throw new Error(data?.error?.message ?? `${path}: ${res.status} ${res.statusText}`);
  return data as T;
}

const admin: AdminAPI = {
  users: () => adminCall("GET", "users"),
  create: name => adminCall("POST", "users", { name }),
  resetPassword: name => adminCall("POST", `users/${encodeURIComponent(name)}/password`),
  renewKey: (name, days) => adminCall("POST", `users/${encodeURIComponent(name)}/key`, { days }),
  removeKey: async name => { await adminCall("DELETE", `users/${encodeURIComponent(name)}/key`); },
  setDisabled: async (name, disabled) => { await adminCall("POST", `users/${encodeURIComponent(name)}/disabled`, { disabled }); },
  setDisplayName: async (name, display) => { await adminCall("POST", `users/${encodeURIComponent(name)}/display`, { display_name: display }); },
  setEmail: async (name, email) => { await adminCall("POST", `users/${encodeURIComponent(name)}/email`, { email }); },
  remove: async name => { await adminCall("DELETE", `users/${encodeURIComponent(name)}`); },
  usage: days => adminCall("GET", `usage?days=${days}`),
  usageOf: (id, days) => adminCall("GET", `usage/user?days=${days}&id=${encodeURIComponent(id)}`),
};

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

  session: async () => remember(await auth("GET", "me")),
  login: async (name, password) => remember(await auth("POST", "login", { name, password })),
  logout: async () => { await auth("POST", "logout"); current = ""; },
  linkUser: async token => ((await auth("POST", "link/check", { token })) as unknown as { name: string }).name,
  applyLink: async (token, password) => remember(await auth("POST", "link", { token, password })),
  changePassword: async (old, password) => { await auth("POST", "password", { current: old, password }); },
  setDisplayName: async name => remember(await auth("POST", "display", { display_name: name })),
  setEmail: async email => remember(await auth("POST", "email", { email })),
  myUsage: async () => (await auth("GET", "usage")) as unknown as MyUsage,
  onUnauthorized: f => {
    unauthorized.add(f);
    return () => unauthorized.delete(f);
  },
  onUserChanged: f => {
    userChanged.add(f);
    return () => userChanged.delete(f);
  },
  admin,

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
    // タブを閉じる・再読み込みは、ブラウザの決まった確かめ(beforeunload)に任せる(unsaved.tsx)
    setUnsaved: async () => {},
    onCloseRequest: () => () => {},
    closeNow: async () => {},
  },
};
