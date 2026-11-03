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
  // ログイン(0017 の 10-1)。デスクトップは、aibow-server につないでいれば API キーかアプリ用のトークンの持ち主を返し、
  // 画面でつないだのにログインしていなければ(ログアウトした・期限が切れた)ログインの画面を出す(0031 の決定事項 44)
  session(): Promise<Me>;                         // ログインが要るか・ログインしている人(Go の auth.Me)
  login(name: string, password: string): Promise<Me>; // 違えば reject(message が理由の文)
  logout(): Promise<void>;
  onUnauthorized(f: () => void): () => void;      // 呼び出しが「ログインしてください」(401)で断られた(ログインの期限が切れた)
  // パスワードは本人が決める(0022)。リンクはブラウザだけ(合言葉は URL の #reset= の後ろ)。変えるのはアプリからも(0031)
  linkUser(token: string): Promise<string>;       // パスワードを決めるリンクの持ち主の名前。使えなければ reject
  applyLink(token: string, password: string): Promise<Me>; // リンクでパスワードを決めて、そのままログインする
  changePassword(current: string, password: string): Promise<void>; // ログインしている本人がパスワードを変える
  setDisplayName(name: string): Promise<Me>;      // ログインしている本人が表示名を変える(0031)
  setEmail(email: string): Promise<Me>;           // ログインしている本人がメールアドレスを変える(0031)
  myUsage(): Promise<MyUsage>;                    // ログインしている本人の使用量(今日・7日・30日。0033)
  admin: AdminAPI;                                // 利用者の管理(ブラウザの管理者だけ。0018 の 11-0。デスクトップでは断る)
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
  admin?: boolean; // 管理者か(0018 の 11-0)。ブラウザの設定の画面に「利用者」のタブを出す
  setup?: boolean; // ログインが要るのに、ログインできる管理者がまだいない(初めての起動など。0020)。ログインの画面に作り方を出す
  display_name?: string; // 表示名(0031。空ならログインの名前を出す)
  email?: string;        // 本人のメールアドレス(0031。本人にだけ返る)
  // canLogout は、アプリが画面でログインしてつないでいるか(ログアウトできる。手で書いた API キーではできない)。ブラウザは使わない
  canLogout?: boolean;
  // lastUser は、アプリがログインし直すときにログインの画面に入れておく名前(最後にログインした名前)
  lastUser?: string;
}

// AdminUser は、利用者の管理の一覧の1人(Go の _cmd/aibow-server の adminUser)。パスワードと API キーは、あるかだけ
export interface AdminUser {
  name: string;
  admin: boolean;
  password: boolean;
  key: boolean;
  local: boolean; // ログインなしの形の利用者(消せない)
  created_at: string;
  display_name?: string;     // 表示名(0031。空ならログインの名前を出す)
  email?: string;            // メールアドレス(0031。管理者と本人にだけ見せる)
  last_used_at?: string;     // 最後に使った時刻(1日に1回だけ書く。一度も使っていなければ無い)
  conversations: number;     // その人の会話の数(0031)
  link_expires_at?: string;  // まだ使っていないパスワードを決めるリンクの期限(招待中の人のリンクが切れたかを出す)
}

// Link は、本人がパスワードを決めるリンクの合言葉と期限(0022)。URL は画面が作る(開いているアドレス + /#reset=<合言葉>)
export interface Link {
  name: string;
  token: string;
  expiresAt: string;
}

// UsageTotal は、使用量の表の1行(Go の genlog.Total。0033)。key は何でまとめたか(利用者の ID・モデルの ID・日付 YYYY-MM-DD)
export interface UsageTotal {
  key: string;
  name?: string;         // 利用者でまとめたとき:記録に写した名前
  display_name?: string; // 今の表示名
  deleted?: boolean;     // もういない人(消された)
  count: number;         // 生成の回数
  summaries: number;     // うち要約の回数
  gpu_sec: number;       // GPU の秒数(プロンプト + 生成 + 画像のエンコード)
  gen_tokens: number;
  prompt_tokens: number;
  reused_tokens: number; // プロンプトのうち KV キャッシュを使い回した数
  image_tokens: number;
  wait_sec: number;      // 順番待ちの秒数(分かった分だけ)
}

// UsageDetail は、1人の使用量の内訳(モデルごと・日ごと)
export interface UsageDetail {
  models: UsageTotal[];
  days: UsageTotal[];
}

// MyUsage は、本人の使用量(Go の genlog.Mine)
export interface MyUsage {
  today: UsageTotal;
  week: UsageTotal;
  month: UsageTotal;
}

// AdminAPI は、利用者の管理の口(aibow-server の /aibow/admin/)。リンクの合言葉と API キーは返事でだけ受け取れる
export interface AdminAPI {
  users(): Promise<AdminUser[]>;
  create(name: string): Promise<Link>;          // 作って、招待のリンク
  resetPassword(name: string): Promise<Link>;   // 今のパスワードを使えなくして、再設定のリンク
  renewKey(name: string): Promise<{ name: string; key: string }>;
  setDisplayName(name: string, display: string): Promise<void>; // 表示名を変える(空なら無し。0031)
  setEmail(name: string, email: string): Promise<void>;         // メールアドレスを変える(空なら無し。0031)
  remove(name: string): Promise<void>;
  // 使用量(0033)。全員の分(利用者ごと)と、1人の内訳。days は今日を含めた日数。id が空ならだれでもない生成の分
  usage(days: number): Promise<UsageTotal[]>;
  usageOf(id: string, days: number): Promise<UsageDetail>;
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

export function linkUser(token: string): Promise<string> {
  return b.linkUser(token);
}

export async function applyLink(token: string, password: string): Promise<Me> {
  me = await b.applyLink(token, password);
  return me;
}

export function changePassword(current: string, password: string): Promise<void> {
  return b.changePassword(current, password);
}

export async function setDisplayName(name: string): Promise<Me> {
  me = await b.setDisplayName(name);
  return me;
}

export function myUsage(): Promise<MyUsage> {
  return b.myUsage();
}

export async function setEmail(email: string): Promise<Me> {
  me = await b.setEmail(email);
  return me;
}

// linkURL は、リンクの合言葉を URL にする(管理者が開いている画面のアドレス。0022 の決定事項 11)
export function linkURL(token: string): string {
  return `${location.origin}/#reset=${token}`;
}

export const admin: AdminAPI = {
  users: () => b.admin.users(),
  create: name => b.admin.create(name),
  resetPassword: name => b.admin.resetPassword(name),
  renewKey: name => b.admin.renewKey(name),
  setDisplayName: (name, display) => b.admin.setDisplayName(name, display),
  setEmail: (name, email) => b.admin.setEmail(name, email),
  remove: name => b.admin.remove(name),
  usage: days => b.admin.usage(days),
  usageOf: (id, days) => b.admin.usageOf(id, days),
};

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
