// transcript.ts: 画面に並べる会話の表示(吹き出し・メモ・エラー)を持つ部分。
//
// 「表示」と「モデルに送る会話履歴」は別物として扱う。
// 表示にはエラーやモデル切り替えのメモ、思考も出すが、モデルに送る履歴(useChat の history)は
// user と assistant の答えだけにする。
import { useCallback, useRef, useState } from "react";

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
type NewItem = DistributiveOmit<Item, "id">;

export interface Transcript {
  items: Item[];
  addUser(text: string): void;
  addNote(text: string): void;
  addError(text: string): void;
  addAssistant(): number; // 追加した返事の id を返す
  updateAssistant(id: number, patch: Partial<AssistantView>): void;
  remove(id: number): void;
  clear(): void;
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
      () => add({ kind: "assistant", waiting: true, thinking: "", thinkLabel: "", content: "" }),
      [add],
    ),
    updateAssistant: useCallback((id, patch) => {
      setItems(prev => prev.map(it => (it.id === id && it.kind === "assistant" ? { ...it, ...patch } : it)));
    }, []),
    remove: useCallback(id => setItems(prev => prev.filter(it => it.id !== id)), []),
    clear: useCallback(() => setItems([]), []),
  };
}
