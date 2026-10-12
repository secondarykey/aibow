// useConversations.ts: 会話の一覧・開いている会話・会話ごとの設定。
//
// 会話は Go が SQLite に保存している。画面は一覧と、開いている会話1つ分だけを受け取って表示する。
import { useCallback, useRef, useState } from "react";
import { ChatService } from "../bindings/aibow/cmd/aibow";
import type { ConversationDetail } from "../bindings/aibow/cmd/aibow";
import type { Conversation, ConversationSettings } from "../bindings/aibow";
import { errorMessage } from "./errors";

// 設定を変えてから Go に保存するまで待つ時間。
// 1文字打つたびに保存すると、呼び出しの到着順が入れ替わって古い値が残ることがあるので、まとめて送る
const SAVE_DELAY_MS = 400;

export interface Conversations {
  list: Conversation[];
  current: ConversationDetail | null; // 開いている会話
  openCount: number;                  // 会話を開くたびに増える(設定の変更では増えない)。表示を作り直す合図
  init(): Promise<void>;              // 起動時: 一番新しい会話を開く(無ければ作る)
  open(id: string): Promise<void>;
  create(): Promise<void>;           // 新しい会話を作って開く
  remove(id: string): Promise<void>;
  reloadList(): Promise<void>;
  updateSettings(patch: Partial<ConversationSettings>): void; // 開いている会話の設定を変える
  flushSettings(): Promise<void>;     // まだ Go に送っていない設定を送る(送信の前に呼ぶ)
}

// onError は、設定の保存のように裏で行う処理が失敗したときに呼ぶ
export function useConversations(onError: (text: string) => void): Conversations {
  const [list, setList] = useState<Conversation[]>([]);
  const [current, setCurrentState] = useState<ConversationDetail | null>(null);
  // 設定の変更をまとめるときに、最新の current を読むための ref
  const currentRef = useRef<ConversationDetail | null>(null);
  const [openCount, setOpenCount] = useState(0);
  const setCurrent = useCallback((d: ConversationDetail) => {
    currentRef.current = d;
    setCurrentState(d);
  }, []);

  // open が続けて呼ばれたとき、遅れて返ってきた古い会話で上書きしないように番号を付ける
  const openSeq = useRef(0);
  // まだ Go に保存していない設定
  const pending = useRef<{ id: string; settings: ConversationSettings; timer: number } | null>(null);

  const flushSettings = useCallback(async () => {
    const p = pending.current;
    if (!p) return;
    pending.current = null;
    window.clearTimeout(p.timer);
    await ChatService.UpdateConversationSettings(p.id, p.settings);
  }, []);

  const reloadList = useCallback(async () => {
    setList((await ChatService.Conversations()) ?? []);
  }, []);

  const open = useCallback(async (id: string) => {
    await flushSettings(); // 前の会話で変えた設定を先に保存する
    const seq = ++openSeq.current;
    const d = await ChatService.Conversation(id);
    if (seq !== openSeq.current) return;
    setCurrent({ ...d, messages: d.messages ?? [] });
    setOpenCount(n => n + 1);
  }, [flushSettings, setCurrent]);

  const create = useCallback(async () => {
    const c = await ChatService.NewConversation();
    await reloadList();
    await open(c.id);
  }, [reloadList, open]);

  const init = useCallback(async () => {
    const l = (await ChatService.Conversations()) ?? [];
    setList(l);
    if (l.length) {
      await open(l[0].id);
      return;
    }
    const c = await ChatService.NewConversation();
    setList([c]);
    await open(c.id);
  }, [open]);

  const remove = useCallback(async (id: string) => {
    if (pending.current?.id === id) {
      window.clearTimeout(pending.current.timer);
      pending.current = null;
    }
    await ChatService.DeleteConversation(id);
    const l = (await ChatService.Conversations()) ?? [];
    setList(l);
    if (currentRef.current?.conversation.id !== id) return;
    // 開いていた会話を消したら、一番新しい会話を開く。1つも残っていなければ作る
    if (l.length) {
      await open(l[0].id);
    } else {
      const c = await ChatService.NewConversation();
      setList([c]);
      await open(c.id);
    }
  }, [open]);

  const updateSettings = useCallback((patch: Partial<ConversationSettings>) => {
    const cur = currentRef.current;
    if (!cur) return;
    const settings = { ...cur.conversation.settings, ...patch };
    setCurrent({ ...cur, conversation: { ...cur.conversation, settings } });

    if (pending.current) window.clearTimeout(pending.current.timer);
    const timer = window.setTimeout(() => {
      flushSettings().catch(err => onError(`設定を保存できません: ${errorMessage(err)}`));
    }, SAVE_DELAY_MS);
    pending.current = { id: cur.conversation.id, settings, timer };
  }, [flushSettings, setCurrent, onError]);

  return { list, current, openCount, init, open, create, remove, reloadList, updateSettings, flushSettings };
}
