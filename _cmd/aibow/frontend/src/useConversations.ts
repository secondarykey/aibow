// useConversations.ts: 会話の一覧・開いている会話・会話ごとの設定。
//
// 会話は Go が SQLite に保存している。画面は一覧と、開いている会話1つ分だけを受け取って表示する。
//
// 「新しい会話の画面」(current が null)では、会話はまだ作らない。最初の送信のときに作る(useChat.ts)。
// 空の会話が一覧に並ばないようにするため。その画面で変えた設定は draft に持ち、作るときに渡す。
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
  current: ConversationDetail | null; // 開いている会話。null なら新しい会話の画面
  draft: ConversationSettings | null; // 新しい会話の画面で、これから作る会話の設定(既定の写し)
  openCount: number;                  // 会話を開く・新しい会話の画面に移るたびに増える。表示を作り直す合図
  init(): Promise<void>;              // 起動時: 新しい会話の画面で始める
  open(id: string): Promise<void>;
  goHome(): Promise<void>;            // 新しい会話の画面に移る
  adopt(c: Conversation): void;       // 新しい会話の画面で作った会話を、開いている会話にする(表示は作り直さない)
  remove(id: string): Promise<void>;
  rename(id: string, title: string): Promise<void>;
  reloadList(): Promise<void>;
  updateSettings(patch: Partial<ConversationSettings>): void; // 開いている会話(または draft)の設定を変える
  flushSettings(): Promise<void>;     // まだ Go に送っていない設定を送る(送信の前に呼ぶ)
}

// onError は、設定の保存のように裏で行う処理が失敗したときに呼ぶ
export function useConversations(onError: (text: string) => void): Conversations {
  const [list, setList] = useState<Conversation[]>([]);
  const [current, setCurrentState] = useState<ConversationDetail | null>(null);
  const [draft, setDraftState] = useState<ConversationSettings | null>(null);
  // 設定の変更をまとめるときに、最新の current / draft を読むための ref
  const currentRef = useRef<ConversationDetail | null>(null);
  const draftRef = useRef<ConversationSettings | null>(null);
  const [openCount, setOpenCount] = useState(0);
  const setCurrent = useCallback((d: ConversationDetail | null) => {
    currentRef.current = d;
    setCurrentState(d);
  }, []);
  const setDraft = useCallback((s: ConversationSettings | null) => {
    draftRef.current = s;
    setDraftState(s);
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
    setDraft(null);
    setOpenCount(n => n + 1);
  }, [flushSettings, setCurrent, setDraft]);

  const goHome = useCallback(async () => {
    await flushSettings();
    const seq = ++openSeq.current; // 読み込み中の open があれば、その結果は捨てる
    // 設定は、そのときの「新しい会話の既定の設定」の写しから始める
    const app = await ChatService.AppSettings();
    if (seq !== openSeq.current) return;
    setCurrent(null);
    setDraft({ ...app.defaults });
    setOpenCount(n => n + 1);
  }, [flushSettings, setCurrent, setDraft]);

  const adopt = useCallback((c: Conversation) => {
    ++openSeq.current;
    setCurrent({ conversation: c, messages: [], generating: false });
    setDraft(null);
    reloadList().catch(() => {});
  }, [setCurrent, setDraft, reloadList]);

  const init = useCallback(async () => {
    await reloadList();
    await goHome();
  }, [reloadList, goHome]);

  const remove = useCallback(async (id: string) => {
    if (pending.current?.id === id) {
      window.clearTimeout(pending.current.timer);
      pending.current = null;
    }
    await ChatService.DeleteConversation(id);
    await reloadList();
    // 開いていた会話を消したら、新しい会話の画面に戻る
    if (currentRef.current?.conversation.id === id) await goHome();
  }, [reloadList, goHome]);

  const rename = useCallback(async (id: string, title: string) => {
    await ChatService.RenameConversation(id, title);
    await reloadList();
  }, [reloadList]);

  const updateSettings = useCallback((patch: Partial<ConversationSettings>) => {
    const cur = currentRef.current;
    if (!cur) {
      // 新しい会話の画面: まだ会話が無いので保存せず、作るときに渡す
      if (draftRef.current) setDraft({ ...draftRef.current, ...patch });
      return;
    }
    const settings = { ...cur.conversation.settings, ...patch };
    setCurrent({ ...cur, conversation: { ...cur.conversation, settings } });

    if (pending.current) window.clearTimeout(pending.current.timer);
    const timer = window.setTimeout(() => {
      flushSettings().catch(err => onError(`設定を保存できません: ${errorMessage(err)}`));
    }, SAVE_DELAY_MS);
    pending.current = { id: cur.conversation.id, settings, timer };
  }, [flushSettings, setCurrent, setDraft, onError]);

  return {
    list, current, draft, openCount, init, open, goHome, adopt, remove, rename, reloadList, updateSettings, flushSettings,
  };
}
