// transcript.ts: 画面に並べる会話の表示(吹き出し・メモ・エラー)を持つ部分。
//
// 会話の履歴そのものは Go が持っていて(SQLite に保存)、画面は会話を開くたびに受け取って表示に変える。
// 表示にはエラーも出すが、これは保存しない(会話を開き直すと消える)。
// モデルの読み込みなどのお知らせは、会話の表示ではなくトースト(toast.tsx)に出す。
import { useCallback, useRef, useState } from "react";
import type { Compaction, Confirmation, ConfirmRequest, ReplyStats, StoredMessage } from "../bindings/github.com/secondarykey/aibow";
import type { Arg as ToolArg } from "../bindings/github.com/secondarykey/aibow/tool";

// ツールの呼び出し1つの表示(カード)。結果がまだ無ければ実行中
export interface ToolView {
  name: string;
  args: ToolArg[];
  raw?: string;     // 書式が崩れて読み取れなかったときの、モデルが書いたままの呼び出し
  result?: string;  // 結果の文(エラーなら「エラー: …」。モデルにもこの文が渡る)
  error?: boolean;
  sec?: number;     // 実行にかかった秒数
  server?: string;  // MCP のサーバーのツールなら、そのサーバーの名前(結果に保存されている)。組み込みのツールは空
  confirm?: ConfirmRequest; // 実行前の確認を待っている(書き換えるツール)。カードに差分と「実行する / 拒否」を出す
  decision?: Confirmation;  // 確認への答え(保存されている。拒否したら状態を「拒否」にする)
}

// ツールを呼んだ回(エージェントループの途中の1回分)の表示: 思考 → 答え → カード
export interface StepView {
  thinking: string;
  thinkLabel: string;
  content: string;
  tools: ToolView[];
}

// アシスタントの返事の表示。1回の送信につき吹き出し1つ。生成中は少しずつ書き換わる。
// ツールを使うと1回の送信で何回も生成するので、ツールを呼んだ回は steps に入れて前に並べ、
// thinking / content には最後の回(ツールを呼ばずに答えた回、または生成中の回)を入れる。
export interface AssistantView {
  steps?: StepView[];    // ツールを呼んだ回(古い順)
  processLabel?: string; // ツールを呼んだ回があるとき、答えまでの過程をまとめた枠の見出し(「思考の過程(12秒)」など)
  generating?: boolean;  // 生成中か(吹き出しの中に停止ボタンを出す)
  waiting: boolean;      // 最初の文字が届くまでの「応答待ち…」
  thinking: string;      // 思考(ない場合は空)
  thinkLabel: string;    // 思考を畳んだときの見出し(「思考中… 3秒」など)
  content: string;       // 答え
  cutoff?: string;       // 答えが出ないまま終わったときの理由の文
  cutoffHint?: boolean;  // 打ち切りの対処のヒントを出すか
  stats?: string;        // 統計行
  createdAt?: string;    // 返事を保存した時刻(= 書き終えた時刻。Go の time.Time が RFC 3339 の文字列で届く)

  // 要約(compact の summary)。返事の前や「今すぐ要約」で、同じ吹き出しの中に要約の文を流す
  summarizing?: boolean; // 要約を作っている途中か(停止ボタンは要約だけを止める)
  summary?: string;      // 生成中の要約の文
  compactNote?: string;  // 要約できなかった理由など(この回は truncate で収めた)。保存はしない
}

// msgId は保存された発言の ID(UUIDv7。文字列の順 = 作った順)。モデルに渡していない発言を薄く表示するのに使う。
// 送信した発言と返事には、Go が保存したときに付ける(user イベントと done の reply)。
// createdAt は保存した時刻(Go の time.Time が RFC 3339 の文字列で届く)。発言の下と返事の最後に「N分前」で出す
// divider は要約した位置の区切り。開くと要約の文が読める
// excluded は、ユーザーが選んで「モデルに渡さない」にした吹き出し(0011。返事はツールの呼び出しと結果も一緒)
export type Item =
  | { id: number; kind: "user"; text: string; msgId?: string; createdAt?: string; excluded?: boolean }
  | { id: number; kind: "error"; text: string }
  | { id: number; kind: "divider"; compaction: Compaction }
  | ({ id: number; kind: "assistant"; msgId?: string; excluded?: boolean } & AssistantView);

// id を除いた Item(種類ごとに Omit する。union にそのまま Omit を使うと共通の項目しか残らないため)
type DistributiveOmit<T, K extends PropertyKey> = T extends unknown ? Omit<T, K> : never;
export type NewItem = DistributiveOmit<Item, "id">;

export interface Transcript {
  items: Item[];
  addUser(text: string): number; // 追加した発言の id を返す
  addError(text: string): void;
  addAssistant(view?: Partial<AssistantView>): number; // 追加した返事の id を返す
  updateAssistant(id: number, patch: Partial<AssistantView>): void;
  // 送信した発言・返事が保存されたら、保存された ID(薄い表示に使う)と時刻を付ける
  setSaved(id: number, m: Pick<StoredMessage, "id" | "created_at">): void;
  remove(id: number): void;
  reset(items: NewItem[]): void; // 別の会話を開いたときに、表示を丸ごと入れ替える
  setExcluded(msgId: string, excluded: boolean): void; // 「モデルに渡さない」の印を付け替える(Go で変えたあと)
  // dropLastTurn は最後のユーザーの発言の後ろ(withUser なら発言も)を表示から外す(作り直す・送り直すの前。0011)
  dropLastTurn(withUser: boolean): void;
}

export function useTranscript(): Transcript {
  const [items, setItems] = useState<Item[]>([]);
  const nextId = useRef(1);

  const add = useCallback((item: NewItem) => {
    const id = nextId.current++;
    setItems(prev => [...prev, { ...item, id } as Item]);
    return id;
  }, []);

  return {
    items,
    addUser: useCallback(text => add({ kind: "user", text }), [add]),
    addError: useCallback(text => { add({ kind: "error", text }); }, [add]),
    addAssistant: useCallback(
      view => add({ kind: "assistant", waiting: true, thinking: "", thinkLabel: "", content: "", ...view }),
      [add],
    ),
    updateAssistant: useCallback((id, patch) => {
      setItems(prev => prev.map(it => (it.id === id && it.kind === "assistant" ? { ...it, ...patch } : it)));
    }, []),
    setSaved: useCallback((id, m) => {
      setItems(prev => prev.map(it => (
        it.id === id && (it.kind === "user" || it.kind === "assistant") ? { ...it, msgId: m.id, createdAt: m.created_at } : it
      )));
    }, []),
    remove: useCallback(id => setItems(prev => prev.filter(it => it.id !== id)), []),
    dropLastTurn: useCallback(withUser => {
      setItems(prev => {
        const last = lastIndex(prev, it => it.kind === "user");
        if (last < 0) return prev;
        return prev.slice(0, withUser ? last : last + 1);
      });
    }, []),
    setExcluded: useCallback((msgId, excluded) => {
      setItems(prev => prev.map(it => (
        (it.kind === "user" || it.kind === "assistant") && it.msgId === msgId ? { ...it, excluded } : it
      )));
    }, []),
    reset: useCallback(newItems => {
      setItems(newItems.map(it => ({ ...it, id: nextId.current++ }) as Item));
    }, []),
  };
}

// lastIndex は条件に合う最後の要素の位置。無ければ -1(Array.findLastIndex は tsconfig の lib が ES2023 より前なので使えない)
export function lastIndex<T>(xs: T[], f: (x: T) => boolean): number {
  for (let i = xs.length - 1; i >= 0; i--) if (f(xs[i])) return i;
  return -1;
}

// ---- 保存された発言 → 表示 ---------------------------------------------------------------

// 生成が「発言の終わり」以外の理由で止まったときの表示
const STOP_REASONS: Record<string, string> = {
  length: "返事の上限で打ち切り",
  context: "コンテキストがいっぱいで打ち切り",
  cancel: "停止",
  stop: "ツールの結果を書き始めたので打ち切り",
};

export const thinkLabel = (sec: number) => `思考の過程(${Math.round(sec)}秒)`;

// turnView は、1回の送信で保存された返事とツールの結果(ユーザーの発言の後ろから、次のユーザーの発言の前まで)を、
// 吹き出し1つの表示に変える。再起動して会話を開き直しても、生成中と同じ並びになる。
export function turnView(ms: StoredMessage[]): AssistantView {
  const steps: StepView[] = [];
  let final: StoredMessage | null = null;
  for (const m of ms) {
    if (m.role === "assistant" && m.tool_calls?.length) {
      steps.push({
        thinking: m.thinking,
        thinkLabel: thinkLabel(m.stats?.think_sec ?? 0),
        content: m.content,
        tools: m.tool_calls.map(c => ({ name: c.name, args: c.args ?? [], raw: c.raw || undefined })),
      });
    } else if (m.role === "assistant") {
      final = m;
    } else if (m.role === "tool") {
      // 結果は呼び出しと同じ順に保存されている。まだ結果の無い最初のカードに付ける
      const card = steps[steps.length - 1]?.tools.find(t => t.result === undefined);
      if (card) {
        Object.assign(card, { result: m.content, error: m.tool_result?.error, sec: m.tool_result?.sec, decision: m.tool_result?.confirm ?? undefined, server: m.tool_result?.server || undefined });
      }
    }
  }
  // 最後の回が無い(ツールの上限や停止で、答える前に止めた)ときは、カードだけの吹き出しになる
  const view: AssistantView = final
    ? replyView(final)
    : { generating: false, waiting: false, thinking: "", thinkLabel: "", content: "" };
  view.steps = steps;
  if (steps.length > 0) view.processLabel = processLabel(ms, final, steps);
  view.stats = statsLine(ms.filter(m => m.role === "assistant"));
  view.createdAt = ms[ms.length - 1]?.created_at;
  return view;
}

// withConfirm は、確認を待っている呼び出しのカード(まだ結果の無い最初のカード)に、確認の内容を付ける。
// 確認は「実行する直前」に来るので、結果の無いカードのうち先頭のものが、今確認を待っている呼び出し
export function withConfirm(steps: StepView[] | undefined, req: ConfirmRequest | null): StepView[] | undefined {
  if (!steps || !req) return steps;
  let done = false;
  return steps.map(s => ({
    ...s,
    tools: s.tools.map(t => {
      if (done || t.result !== undefined) return t;
      done = true;
      return { ...t, confirm: req };
    }),
  }));
}

// processLabel は、ツールを使った送信の「答えまでの過程」の枠の見出し。秒数は送信から答えを書き始めるまでの時間で、
// 保存した記録から足し直す(ツールを呼んだ回: プロンプトの読み込み + 生成、ツールの実行、最後の回: プロンプトの読み込み + 思考)。
// 回の間のわずかな時間は入らないので、生成中に数えていた秒数と少しずれることがある
export function processLabel(ms: StoredMessage[], final: StoredMessage | null, steps: StepView[]): string {
  let sec = 0;
  for (const m of ms) {
    const s = m.stats;
    if (m.role === "tool") sec += m.tool_result?.sec ?? 0;
    else if (m === final && s) sec += s.prompt_sec + s.think_sec;
    else if (s) sec += s.prompt_sec + s.gen_sec;
  }
  const thought = !!final?.thinking || steps.some(s => s.thinking);
  return (thought ? thinkLabel(sec) : `ツールの使用(${Math.round(sec)}秒)`) + toolCount(steps);
}

// toolCount は枠の見出しに付ける、ツールを呼んだ回数(カードの数。エラーや実行しなかった呼び出しも数える)
export function toolCount(steps: StepView[]): string {
  const n = steps.reduce((a, s) => a + s.tools.length, 0);
  return n > 0 ? `・ツール ${n}回` : "";
}

// replyView は保存された返事1件(ツールを呼ばなかった回)を表示に変える。思考の秒数や統計も保存されているので、再起動後も同じ表示になる。
export function replyView(m: StoredMessage): AssistantView {
  const s = m.stats;
  const view: AssistantView = {
    generating: false,
    waiting: false,
    thinking: m.thinking,
    thinkLabel: thinkLabel(s?.think_sec ?? 0),
    content: m.content,
    createdAt: m.created_at,
  };
  if (s?.no_answer) {
    // 答えが出なかった回は、content に理由の文が入っている(モデルにもこの文が渡る)
    view.content = "";
    view.cutoff = m.content;
    view.cutoffHint = s.stop_reason !== "cancel";
  }
  view.stats = statsLine([m]);
  return view;
}

// statsLine は統計行。ツールを使って何回も生成したときは、全部の回の合計にする
// (プロンプトは回ごとに読ませた分の合計。2回目からは KV キャッシュの再利用が多いので、再利用の数で効き目が見える)。
function statsLine(replies: StoredMessage[]): string | undefined {
  const rs = replies.filter(r => r.stats);
  if (rs.length === 0) return undefined;
  const sum = (f: (s: ReplyStats) => number) => rs.reduce((a, r) => a + f(r.stats!), 0);
  const first = rs[0].stats!, last = rs[rs.length - 1];
  const parts = [last.model || "(モデル不明)", `初回応答 ${first.first_sec.toFixed(2)}s`];
  parts.push(`プロンプト ${sum(s => s.prompt_tokens)} tok(再利用 ${sum(s => s.reused_tokens)})${sum(s => s.prompt_sec).toFixed(2)}s`);
  const gen = sum(s => s.gen_tokens), genSec = sum(s => s.gen_sec);
  parts.push(`生成 ${gen} tok ・ ${genSec > 0 ? (gen / genSec).toFixed(1) : "-"} tok/s`);
  if (rs.length > 1) parts.push(`${rs.length}回生成`);
  const calls = rs.reduce((a, r) => a + (r.tool_calls?.length ?? 0), 0);
  if (calls > 0) parts.push(`ツール ${calls}回`);
  const stop = last.stats!.stop_reason;
  if (stop !== "eos") parts.push(STOP_REASONS[stop] ?? stop);
  return parts.join(" ・ ");
}

// messageItems は会話の発言の列を表示の列に変える。ユーザーの発言から次のユーザーの発言までの返事とツールの結果は、
// 吹き出し1つにまとめる(turnView)。要約した発言の後ろには区切りを入れる。
// 古い要約も消さずに残っているので、要約を積み重ねた回数だけ区切りが出る。
export function messageItems(messages: StoredMessage[], compactions: Compaction[]): NewItem[] {
  const out: NewItem[] = [];
  const dividers = (m: StoredMessage) => {
    for (const c of compactions) {
      if (c.upto_message_id === m.id) out.push({ kind: "divider", compaction: c });
    }
  };
  let turn: StoredMessage[] = [];
  const flush = () => {
    if (turn.length === 0) return;
    out.push({ kind: "assistant", msgId: turn[0].id, excluded: turn.some(m => m.excluded), ...turnView(turn) });
    turn.forEach(dividers);
    turn = [];
  };
  for (const m of messages) {
    if (m.role !== "user") {
      turn.push(m);
      continue;
    }
    flush();
    out.push({ kind: "user", text: m.content, msgId: m.id, createdAt: m.created_at, excluded: m.excluded });
    dividers(m);
  }
  flush();
  return out;
}

// compactionSize は区切りの見出しに添える、要約の前後の大きさ(モデルに渡す文字列のトークン数)。
// 例: "12,000 → 3,000 tok(25%)"。% は要約したあとが前の何割になったか。記録が無い(古い要約)なら空
export function compactionSize(c: Compaction): string {
  const s = c.stats;
  if (!s || s.before_tokens <= 0) return "";
  const pct = Math.round((s.after_tokens / s.before_tokens) * 100);
  return `${s.before_tokens.toLocaleString()} → ${s.after_tokens.toLocaleString()} tok(${pct}%)`;
}

// compactionStats は区切りを開いたときに出す、要約の記録の1行。前後の大きさは見出しに出すので、ここには入れない
export function compactionStats(c: Compaction): string {
  const s = c.stats;
  const parts = [c.model || "(モデル不明)"];
  if (s) {
    parts.push(`発言 ${s.messages} 件を要約`);
    parts.push(`読ませた ${s.prompt_tokens.toLocaleString()} tok(再利用 ${s.reused_tokens})・生成 ${s.gen_tokens} tok`);
    parts.push(`${s.sec.toFixed(1)}s`);
  }
  return parts.join(" ・ ");
}
