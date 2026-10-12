// transcript.ts: 画面に並べる会話の表示(吹き出し・メモ・エラー)を持つ部分。
//
// 会話の履歴そのものは Go が持っていて(SQLite に保存)、画面は会話を開くたびに受け取って表示に変える。
// 表示にはエラーやモデル切り替えのメモも出すが、これは保存しない(会話を開き直すと消える)。
import { useCallback, useRef, useState } from "react";
import type { StoredMessage } from "../bindings/aibow";

// アシスタントの返事1件の表示。生成中は少しずつ書き換わる。
export interface AssistantView {
  waiting: boolean;      // 最初の文字が届くまでの「応答待ち…」
  thinking: string;      // 思考(ない場合は空)
  thinkLabel: string;    // 思考を畳んだときの見出し(「思考中… 3秒」など)
  content: string;       // 答え
  cutoff?: string;       // 答えが出ないまま終わったときの理由の文
  cutoffHint?: boolean;  // 打ち切りの対処のヒントを出すか
  stats?: string;        // 統計行
}

export type Item =
  | { id: number; kind: "user"; text: string }
  | { id: number; kind: "note"; text: string }
  | { id: number; kind: "error"; text: string }
  | ({ id: number; kind: "assistant" } & AssistantView);

// id を除いた Item(種類ごとに Omit する。union にそのまま Omit を使うと共通の項目しか残らないため)
type DistributiveOmit<T, K extends PropertyKey> = T extends unknown ? Omit<T, K> : never;
export type NewItem = DistributiveOmit<Item, "id">;

export interface Transcript {
  items: Item[];
  addUser(text: string): void;
  addNote(text: string): void;
  addError(text: string): void;
  addAssistant(view?: Partial<AssistantView>): number; // 追加した返事の id を返す
  updateAssistant(id: number, patch: Partial<AssistantView>): void;
  remove(id: number): void;
  reset(items: NewItem[]): void; // 別の会話を開いたときに、表示を丸ごと入れ替える
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
    addUser: useCallback(text => { add({ kind: "user", text }); }, [add]),
    addNote: useCallback(text => { add({ kind: "note", text }); }, [add]),
    addError: useCallback(text => { add({ kind: "error", text }); }, [add]),
    addAssistant: useCallback(
      view => add({ kind: "assistant", waiting: true, thinking: "", thinkLabel: "", content: "", ...view }),
      [add],
    ),
    updateAssistant: useCallback((id, patch) => {
      setItems(prev => prev.map(it => (it.id === id && it.kind === "assistant" ? { ...it, ...patch } : it)));
    }, []),
    remove: useCallback(id => setItems(prev => prev.filter(it => it.id !== id)), []),
    reset: useCallback(newItems => {
      setItems(newItems.map(it => ({ ...it, id: nextId.current++ }) as Item));
    }, []),
  };
}

// ---- 保存された発言 → 表示 ---------------------------------------------------------------

// 生成が「発言の終わり」以外の理由で止まったときの表示
const STOP_REASONS: Record<string, string> = {
  length: "上限トークン数で打ち切り",
  context: "コンテキストがいっぱいで打ち切り",
  cancel: "停止",
};

export const thinkLabel = (sec: number) => `思考の過程(${Math.round(sec)}秒)`;

// replyView は保存された返事を表示に変える。思考の秒数や統計も保存されているので、再起動後も同じ表示になる。
export function replyView(m: StoredMessage): AssistantView {
  const s = m.stats;
  const view: AssistantView = {
    waiting: false,
    thinking: m.thinking,
    thinkLabel: thinkLabel(s?.think_sec ?? 0),
    content: m.content,
  };
  if (s?.no_answer) {
    // 答えが出なかった回は、content に理由の文が入っている(モデルにもこの文が渡る)
    view.content = "";
    view.cutoff = m.content;
    view.cutoffHint = s.stop_reason !== "cancel";
  }
  if (s) {
    const parts = [m.model || "(モデル不明)", `初回応答 ${s.first_sec.toFixed(2)}s`];
    parts.push(`プロンプト ${s.prompt_tokens} tok(再利用 ${s.reused_tokens})${s.prompt_sec.toFixed(2)}s`);
    const rate = s.gen_sec > 0 ? (s.gen_tokens / s.gen_sec).toFixed(1) : "-";
    parts.push(`生成 ${s.gen_tokens} tok ・ ${rate} tok/s`);
    if (s.stop_reason !== "eos") parts.push(STOP_REASONS[s.stop_reason] ?? s.stop_reason);
    view.stats = parts.join(" ・ ");
  }
  return view;
}

// messageItems は会話の発言の列を表示の列に変える。
export function messageItems(messages: StoredMessage[]): NewItem[] {
  return messages.map(m =>
    m.role === "assistant" ? { kind: "assistant", ...replyView(m) } : { kind: "user", text: m.content },
  );
}
