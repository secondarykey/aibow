// useConversations.ts: 会話の一覧・開いている会話・会話ごとの設定。
//
// 会話は Go が SQLite に保存している。画面は一覧と、開いている会話1つ分だけを受け取って表示する。
//
// 「新しい会話の画面」(current が null)では、会話はまだ作らない。最初の送信のときに作る(useChat.ts)。
// 空の会話が一覧に並ばないようにするため。その画面で変えた設定は draft に持ち、作るときに渡す。
// 新しい会話は、その画面で選んだプロファイル(draftProfile。最初は「最初に選ぶ」の印のもの)から作る(0014)。
import { useCallback, useRef, useState } from "react";
import { ChatService } from "./backend";
import type { ConversationDetail } from "../bindings/github.com/secondarykey/aibow/ui";
import type { Conversation, ConversationSettings, ProfileInfo, Usage } from "../bindings/github.com/secondarykey/aibow";
import { errorMessage } from "./errors";

// 設定を変えてから Go に保存するまで待つ時間。
// 1文字打つたびに保存すると、呼び出しの到着順が入れ替わって古い値が残ることがあるので、まとめて送る
const SAVE_DELAY_MS = 400;

// NIL は「選んでいない」の ID(Go の空の uuid.UUID)。Go は、新しい会話で最初に選ばれるプロファイルとして扱う
export const NIL = "00000000-0000-0000-0000-000000000000";

export interface Conversations {
  list: Conversation[];
  current: ConversationDetail | null; // 開いている会話。null なら新しい会話の画面
  draft: ConversationSettings | null; // 新しい会話の画面で、これから作る会話の設定(プロファイルの値の写し)
  draftProfile: string;               // 新しい会話の画面で選んだプロファイルの ID
  profiles: ProfileInfo[];            // プロファイルの一覧(新しい会話の画面で選ぶ・決まりを見る)
  selectProfile(id: string): Promise<void>; // 新しい会話の画面でプロファイルを選ぶ(その値の写しで draft を作り直す)
  reloadProfiles(): Promise<void>;    // プロファイルを変えたあとに読み直す(新しい会話の画面なら draft も作り直す)
  openCount: number;                  // 会話を開く・新しい会話の画面に移るたびに増える。表示を作り直す合図
  init(lastId: string | null): Promise<void>; // 起動時: 最後に開いていた会話を開く。無ければ(消した・新しい会話の画面だった)新しい会話の画面で始める
  open(id: string): Promise<void>;
  goHome(): Promise<void>;            // 新しい会話の画面に移る
  adopt(c: Conversation): void;       // 新しい会話の画面で作った会話を、開いている会話にする(表示は作り直さない)
  // 新しい会話の画面から送れなかった(発言が保存されなかった)ときに、作った会話を消して、同じ下書きの新しい会話の画面に戻す(表示は作り直さない)
  unadopt(id: string, profile: string, settings: ConversationSettings): Promise<void>;
  remove(id: string): Promise<void>;
  rename(id: string, title: string): Promise<void>;
  reloadList(): Promise<void>;
  updateSettings(patch: Partial<ConversationSettings>): void; // 開いている会話(または draft)の設定を変える
  flushSettings(): Promise<void>;     // まだ Go に送っていない設定を送る(送信の前に呼ぶ)
  setUsage(id: string, usage: Usage | null): void; // 開いている会話のメーターを書き換える(chat イベントの usage)
  refreshUsage(): Promise<void>;      // メーターを Go に数え直してもらう(モデル・上限・会話の設定を変えたあと)
  reloadSettings(id: string): Promise<void>; // Go が書き換えた会話の設定を読み直す(確認のカードの「この会話では今後聞かない」など)
}

// onError は、設定の保存のように裏で行う処理が失敗したときに呼ぶ
export function useConversations(onError: (text: string) => void): Conversations {
  const [list, setList] = useState<Conversation[]>([]);
  const [current, setCurrentState] = useState<ConversationDetail | null>(null);
  const [draft, setDraftState] = useState<ConversationSettings | null>(null);
  const [draftProfile, setDraftProfileState] = useState("");
  const draftProfileRef = useRef("");
  const setDraftProfile = useCallback((id: string) => {
    draftProfileRef.current = id;
    setDraftProfileState(id);
  }, []);
  const [profiles, setProfilesState] = useState<ProfileInfo[]>([]);
  const profilesRef = useRef<ProfileInfo[]>([]);
  const loadProfiles = useCallback(async () => {
    const list = (await ChatService.Profiles()) ?? [];
    profilesRef.current = list;
    setProfilesState(list);
    return list;
  }, []);
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
    // 設定は、「最初に選ぶ」の印のプロファイルの写しから始める
    const list = await loadProfiles();
    const def = list.find(i => i.default)?.profile.id ?? "";
    const settings = await ChatService.ProfileDefaults(def || NIL);
    if (seq !== openSeq.current) return;
    setCurrent(null);
    setDraftProfile(def);
    setDraft({ ...settings });
    setOpenCount(n => n + 1);
  }, [flushSettings, setCurrent, setDraft, setDraftProfile, loadProfiles]);

  const selectProfile = useCallback(async (id: string) => {
    const settings = await ChatService.ProfileDefaults(id || NIL);
    if (currentRef.current) return; // 選んでいる間に会話を開いた
    setDraftProfile(id);
    setDraft({ ...settings });
  }, [setDraft, setDraftProfile]);

  const adopt = useCallback((c: Conversation) => {
    ++openSeq.current;
    // 会話のプロファイル(決まりを画面に出すため)は、選んでいたもの
    const profile = profilesRef.current.find(i => i.profile.id === c.profile_id)?.profile ?? null;
    setCurrent({ conversation: c, messages: [], compactions: [], generating: false, usage: null, profile });
    setDraft(null);
    reloadList().catch(() => {});
  }, [setCurrent, setDraft, reloadList]);

  // 表示を作り直す合図(openCount)は進めない。送れなかった理由のエラーのカードを、新しい会話の画面に出したままにするため
  const unadopt = useCallback(async (id: string, profile: string, settings: ConversationSettings) => {
    await ChatService.DeleteConversation(id);
    if (currentRef.current?.conversation.id === id) {
      ++openSeq.current;
      setCurrent(null);
      setDraftProfile(profile);
      setDraft({ ...settings });
    }
    await reloadList();
  }, [setCurrent, setDraft, setDraftProfile, reloadList]);

  const init = useCallback(async (lastId: string | null) => {
    const l = (await ChatService.Conversations()) ?? [];
    setList(l);
    if (lastId !== null && l.some(c => c.id === lastId)) await open(lastId);
    else await goHome();
  }, [open, goHome]);

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

  const setUsage = useCallback((id: string, usage: Usage | null) => {
    const cur = currentRef.current;
    if (cur?.conversation.id === id) setCurrent({ ...cur, usage });
  }, [setCurrent]);

  // トークン数は今のモデルで数えるので、モデルや上限、会話の設定(システムプロンプト・思考・compact)が変わると変わる
  const refreshUsage = useCallback(async () => {
    const id = currentRef.current?.conversation.id;
    if (!id) return;
    setUsage(id, await ChatService.Usage(id));
  }, [setUsage]);

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
      flushSettings()
        .then(refreshUsage)
        .catch(err => onError(`設定を保存できません: ${errorMessage(err)}`));
    }, SAVE_DELAY_MS);
    pending.current = { id: cur.conversation.id, settings, timer };
  }, [flushSettings, refreshUsage, setCurrent, setDraft, onError]);

  const reloadSettings = useCallback(async (id: string) => {
    const d = await ChatService.Conversation(id);
    const cur = currentRef.current;
    // 読み直している間に別の会話を開いた・設定を変え始めたときは、そちらを優先する
    if (cur?.conversation.id !== id || pending.current) return;
    // プロファイル(決まり)も読み直す。プロファイルを変えると、固定した項目の値も変わるため
    setCurrent({ ...cur, conversation: { ...cur.conversation, settings: d.conversation.settings }, profile: d.profile });
  }, [setCurrent]);

  const reloadProfiles = useCallback(async () => {
    const list = await loadProfiles();
    const cur = currentRef.current;
    if (cur) {
      await reloadSettings(cur.conversation.id);
      return;
    }
    // 新しい会話の画面: 選んでいたプロファイルが無くなった・しまった なら、最初に選ぶものに戻す
    const id = draftProfileRef.current;
    const ok = list.some(i => i.profile.id === id && !i.profile.archived);
    await selectProfile(ok ? id : list.find(i => i.default)?.profile.id ?? "");
  }, [loadProfiles, reloadSettings, selectProfile]);

  return {
    list, current, draft, draftProfile, profiles, selectProfile, reloadProfiles,
    openCount, init, open, goHome, adopt, unadopt, remove, rename, reloadList, updateSettings, flushSettings,
    setUsage, refreshUsage, reloadSettings,
  };
}
