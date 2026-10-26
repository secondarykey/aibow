// useChat.ts: 送信・生成の途中経過の受け取り・停止。
//
// 会話の履歴は Go が持っているので、ここで送るのは「会話の ID」と「新しい発言」だけ。
// Go が履歴を保存し、毎回並べ直してモデルに渡す(モデル自身は何も覚えていない)。
// 会話の上限を超えたら、Go が compact で収めてから渡す(compact.go)。
import { useCallback, useEffect, useRef, useState } from "react";
import { ChatService, isCancel, onChat } from "./backend";
import type { Call } from "./backend";
import type { ToolPolicy } from "../bindings/github.com/secondarykey/aibow";
import type { ChatEvent, ConfirmRequest, StoredMessage } from "../bindings/github.com/secondarykey/aibow";
import { messageItems, thinkLabel, toolCount, turnView, withConfirm } from "./transcript";
import type { AssistantView, Transcript } from "./transcript";
import { NIL, type Conversations } from "./useConversations";
import type { Busy } from "./useModels";
import type { Notifier } from "./toast";
import { errorMessage } from "./errors";

// ErrorNote は、送信で届いたエラー1つ。caution なら赤いエラーではなく注意で出す
interface ErrorNote {
  text: string;
  caution?: boolean;
}

const secSince = (t: number) => Math.round((performance.now() - t) / 1000);

// 生成中の返事。生成中に別の会話を開いても生成は続くので、画面の表示とは別に持っておき、
// その会話に戻ってきたら、ここから吹き出しを作り直す。
interface Live {
  convId: string;
  itemId: number | null; // 表示している吹き出しの id。その会話を表示していなければ null
  userItemId: number | null; // 送信した発言の表示の id(会話を開き直したら、保存された発言から作り直すので null)
  view: AssistantView;
  thinkStart: number;
  processStart: number; // 最初の回のプロンプトを渡した時刻。ツールを使った送信の、答えまでの過程の枠の秒数に使う
  // saved はこの送信で Go が保存し終えた返事とツールの結果(reply / tool_result イベント)。
  // ツールを使うと1回の送信で何件も保存されるので、吹き出しはここから作り直す(開き直したときと同じ並びになる)
  saved: StoredMessage[];
  // confirm は、実行前の確認を待っているツール(答えるか、結果が届くまで)。待っていなければ null。
  // 会話を切り替えても Go は待ち続けるので、ここに覚えておき、戻ったときもボタン付きのカードを出す
  confirm: ConfirmRequest | null;
}

// Ended は、ほかの会話を見ている間に送信が終わった会話の終わり方(会話の一覧の印。開くと消える)
export type Ended = "done" | "error";

// RunResult は run の戻り値。
interface RunResult {
  errors: ErrorNote[]; // この送信で届いたエラー
  userSaved: boolean; // 送った発言を Go が保存したか(user イベントが来たか)
}

export interface Chat {
  generatingId: string | null; // 返事(か要約)を生成中の会話の ID
  ended: Record<string, Ended>; // ほかの会話を見ている間に送信が終わった会話(会話の ID → 終わり方)。起動をまたいでは覚えない
  confirmingId: string | null; // ツールの実行前の確認を待っている会話の ID(会話の一覧に「確認待ち」と出す)
  answer(reqId: string, approved: boolean, reason: string, remember: ToolPolicy): Promise<void>; // 確認に答える(「実行する」「拒否」と、これから先の扱い)
  lastPrompt: string;          // 開いている会話で、モデルに実際に渡した文字列
  send(text: string): Promise<boolean>; // 発言が保存されたら true(false なら送れていないので、入力欄に文を戻す)
  regenerate(): Promise<void>; // 最後の返事を作り直す(開いている会話。0011)
  resend(text: string): Promise<void>; // 最後の発言を直して送り直す(開いている会話。0011)
  compact(): Promise<void>;    // 今すぐ要約(開いている会話)
  stop(): void;
  resync(): void; // 知らせ(SSE)をつなぎ直したとき、生成中の送信で待っている確認を取り直す(ブラウザ。0024 の決定事項 15)
}

// onModelLoaded は、送信の前に Go が会話のモデルを読み直したときに呼ぶ(読み込んでいるモデルの表示を取り直す。0014 の 3)
export function useChat(t: Transcript, setBusy: (b: Busy) => void, convs: Conversations, n: Notifier, onModelLoaded: () => Promise<unknown>): Chat {
  const { addUser, addError, addAssistant, updateAssistant, setSaved, remove, reset, dropLastTurn } = t;
  const { current, draft, draftProfile, openCount, open, adopt, unadopt, flushSettings, reloadList, setUsage, reloadSettings } = convs;
  const { notify, dismissKey } = n;
  const [generatingId, setGeneratingId] = useState<string | null>(null);
  const [confirmingId, setConfirmingId] = useState<string | null>(null);
  // モデルに渡した文字列は保存しないので、会話ごとにここで覚えておく(再起動すると消える)
  const [prompts, setPrompts] = useState<Record<string, string>>({});

  const live = useRef<Live | null>(null);
  const viewing = useRef<string | null>(null); // 表示している会話の ID
  const [ended, setEnded] = useState<Record<string, Ended>>({});
  // ほかの会話を見ている間に届いたエラー。エラーは保存しないので、ここで持っておき、その会話を開いたときに出す
  const heldErrors = useRef<Record<string, ErrorNote[]>>({});
  // 生成中の呼び出し(stop() で停止できる)。生成中でなければ null
  const controller = useRef<{ stop(): void } | null>(null);
  // 送信を受け付けてから生成が始まるまでの間に、もう一度送られないようにする
  // (新しい会話の画面では、会話を作る分だけ間があく)
  const starting = useRef(false);
  // 生成中の返事に chat イベントを渡す関数
  const listener = useRef<((ev: ChatEvent) => void) | null>(null);

  // Go の Send が送ってくる途中経過(aibow.ChatEvent)。
  useEffect(() => onChat(ev => listener.current?.(ev)), []);

  // 会話を開いたら、保存された発言から表示を作り直す。
  // その会話で生成中なら、生成中の返事を末尾に足す(Go がまだ保存していないため)。
  // 新しい会話の画面(current が null)に移ったときは、表示を空にする。
  useEffect(() => {
    if (!current) {
      viewing.current = null;
      reset([]);
      if (live.current) live.current.itemId = null;
      return;
    }
    const id = current.conversation.id;
    viewing.current = id;
    const l = live.current;
    // 生成中の送信で保存済みの返事とツールの結果は、生成中の吹き出しの中に出すので、保存された発言の側からは外す
    const inLive = new Set(l && l.convId === id ? l.saved.map(m => m.id) : []);
    reset(messageItems((current.messages ?? []).filter(m => !inLive.has(m.id)), current.compactions ?? []));
    if (l) {
      l.itemId = l.convId === id && current.generating ? addAssistant(l.view) : null;
      l.userItemId = null;
    }
    // 見ていない間に終わった会話を開いたら、一覧の印を消し、届いていたエラーを出す
    heldErrors.current[id]?.forEach(e => addError(e.text, e.caution));
    delete heldErrors.current[id];
    setEnded(e => {
      if (!(id in e)) return e;
      const { [id]: _, ...rest } = e;
      return rest;
    });
    // 開くたびに増える openCount だけを見る(設定の変更で current が変わっても作り直さない)
  }, [openCount]);

  // target は送り先の会話の ID を決める。新しい会話の画面なら、ここで会話を作る。
  const target = useCallback(async (): Promise<string> => {
    if (current) {
      await flushSettings(); // 変えたばかりの設定を、送信より先に Go に届ける
      return current.conversation.id;
    }
    // 新しい会話の画面: 選んだプロファイルから、画面で変えた設定(draft)で会話を作り、開いている会話にする。
    // 表示は作り直さず(開き直すと、このあと足す吹き出しと順番が入れ替わることがある)、ここで空にする
    const c = await ChatService.NewConversation(draftProfile || NIL, draft);
    adopt(c);
    viewing.current = c.id;
    reset([]);
    return c.id;
  }, [current, draft, draftProfile, flushSettings, adopt, reset]);

  // run は送信と「今すぐ要約」の共通部分。生成中の吹き出しを出し、chat イベントで書き換え、done で終わる。
  // manual は「今すぐ要約」(返事は生成しない。終わったら吹き出しを消して、区切りが出るように開き直す)
  // 戻り値は、この送信で届いたエラー(作り直す・送り直すで、開き直したあとに出し直すため)と、送った発言を Go が保存したか
  const run = useCallback(async (id: string, start: () => Call<void>, manual: boolean, userItemId: number | null): Promise<RunResult> => {
    const errors: ErrorNote[] = [];
    let userSaved = false;
    const l: Live = {
      convId: id, itemId: null, userItemId, thinkStart: 0, processStart: 0, saved: [], confirm: null,
      view: { generating: true, waiting: true, thinking: "", thinkLabel: "", content: "", summarizing: manual, summary: "" },
    };
    l.itemId = addAssistant(l.view);
    live.current = l;
    setGeneratingId(id);
    setBusy("generating");

    // 生成中の返事を書き換える。表示していない会話なら、覚えておくだけ
    const show = (patch: Partial<AssistantView>) => {
      l.view = { ...l.view, ...patch };
      if (l.itemId !== null) updateAssistant(l.itemId, patch);
    };
    // 答えまでの過程の枠(ツールを呼んだ回があるときだけ出す)の見出し。answering は最後の答えを書き始めたか
    const liveProcess = (answering: boolean): Partial<AssistantView> => {
      if (!l.view.steps?.length) return {};
      const sec = secSince(l.processStart);
      const thought = !!l.view.thinking || l.view.steps.some(s => s.thinking);
      const count = toolCount(l.view.steps);
      if (answering) return { processLabel: (thought ? thinkLabel(sec) : `ツールの使用(${sec}秒)`) + count };
      return { processLabel: (thought ? `思考中… ${sec}秒` : `ツールを使用中… ${sec}秒`) + count };
    };

    // 表示している会話なら開き直す(要約の区切りと薄い表示を、保存された内容から作り直す)。
    // 生成中なら、開き直したあとに生成中の吹き出しが末尾に足し直される
    const reopen = () => {
      if (viewing.current === id) open(id).catch(err => addError(errorMessage(err)));
    };

    // 終わったら、Go が保存した返事とツールの結果で吹き出しを置き換える(統計や「思考の過程(N秒)」も付く)
    const done = () => {
      const first = l.saved[0], last = l.saved[l.saved.length - 1];
      if (l.itemId !== null) {
        if (first) {
          updateAssistant(l.itemId, turnView(l.saved));
          setSaved(l.itemId, { id: first.id, created_at: last.created_at });
        } else remove(l.itemId); // 何も生成されなかった(今すぐ要約もここ)
      } else if (first && viewing.current === id) {
        // 会話を開き直している最中に終わった。保存された返事が読めるように開き直す
        open(id).catch(() => {});
      }
    };

    // 返事は Send の戻り値ではなく、chat イベントで少しずつ届く。done が来たら終わり。
    let compacted = false; // 今すぐ要約で、要約が保存された
    let failed = false;    // エラーが届いた(一覧の印を赤にする)
    // エラーは、見ている会話なら表示に出し、ほかの会話なら開いたときに出すよう持っておく
    const showError = (text: string, caution?: boolean) => {
      failed = true;
      if (viewing.current === id) addError(text, caution);
      else (heldErrors.current[id] ??= []).push({ text, caution });
    };
    let finish = () => {};
    let ended = false; // done が届いた・呼び出しが失敗した(この送信の終わりを済ませた)
    const finished = new Promise<void>(resolve => (finish = () => { ended = true; resolve(); }));
    listener.current = ev => {
      if (ev.conversation_id !== id) return;
      switch (ev.type) {
        case "done":
          setConfirmingId(null);
          // 会話のモデルを読み込めずに終わったとき(使えないモデルなど)は、出したままの「読み込み中…」を消す(読み込めたら model で置き換え済み)
          if (failed) dismissKey("model");
          done();
          live.current = null; // 開き直しても、もう生成中の吹き出しは足さない
          if (manual && compacted) reopen();
          finish();
          return;
        case "user":
          // 送信した発言が保存された。ID を付けておくと、あとで truncate や要約で外れたときに薄く表示できる。
          // 発言の下の時刻も、保存した時刻(Go の時計)で出す
          if (l.userItemId !== null && ev.message) setSaved(l.userItemId, ev.message);
          userSaved = true;
          return;
        case "prompt":
          // prompt が届いた時点で、Go はユーザーの発言とタイトルを保存し終えている。一覧のタイトルを先に更新する
          setPrompts(p => ({ ...p, [id]: ev.text ?? "" }));
          if (!l.processStart) l.processStart = performance.now();
          reloadList().catch(() => {});
          return;
        case "usage":
          setUsage(id, ev.usage ?? null);
          return;
        case "summarizing":
          // 要約を作り始めた(何回かに分けて要約するときは、そのたびに来る)
          show({ summarizing: true, summary: "", waiting: true });
          return;
        case "summary":
          show({ summary: (l.view.summary ?? "") + (ev.text ?? "") });
          return;
        case "compact": {
          const c = ev.compact;
          if (!c || c.method !== "summary") return; // truncate は、メーターと薄い表示で分かる
          // 失敗したときは、途中まで書いた要約の文を残す(畳んで出す。なぜ失敗したかを見られるように)
          show({
            summarizing: false, summary: "", waiting: true,
            compactNote: c.error || undefined, compactDraft: (c.error && l.view.summary) || undefined,
          });
          if (c.error) return;
          if (manual) compacted = true;
          else reopen(); // 返事を書いている間に、区切りと薄い表示が見えるように
          return;
        }
        case "reply":
          // 返事を1件保存した。ツールを呼んだ回なら、カードの並ぶ「回」にして、次の回の思考と答えを空から書き始める
          if (!ev.reply) return;
          l.saved.push(ev.reply);
          if (ev.reply.tool_calls?.length) {
            show({ steps: turnView(l.saved).steps, thinking: "", thinkLabel: "", content: "", waiting: true });
            show(liveProcess(false));
          }
          return;
        case "confirm":
          // ツールを実行する前に、確認を待っている。そのカードに差分と「実行する / 拒否」を出す
          if (!ev.confirm) return;
          l.confirm = ev.confirm;
          setConfirmingId(id);
          // 待っているのはモデルではなくユーザーなので、「応答待ち…」は消す(答えたら結果が届いたときに戻す)
          show({ steps: withConfirm(turnView(l.saved).steps, l.confirm), waiting: false });
          return;
        case "tool_result":
          // ツールの結果を保存した。結果の無いカードは「実行中」と出ている。確認を待っていたなら、答えが出た
          if (!ev.message) return;
          if (l.confirm) {
            l.confirm = null;
            setConfirmingId(null);
            show({ waiting: true }); // 次はモデルが結果を読んで続きを書く
          }
          l.saved.push(ev.message);
          show({ steps: turnView(l.saved).steps });
          show(liveProcess(false));
          return;
        case "error":
          // caution は、理由と対処を書いた断り(管理者が使えないようにしたモデル、など。0018 の決定事項 13)。赤ではなく注意で出す
          errors.push({ text: ev.error ?? "", caution: ev.caution });
          showError(ev.error ?? "", ev.caution);
          return;
        case "model_loading":
          // 送信の前に、この会話のモデルを読み込んでいる(モデルの違う会話に切り替えた最初の送信。数秒〜十数秒)
          notify(ev.text ?? "", { key: "model", sticky: true });
          return;
        case "model":
          notify(ev.text ?? "", { key: "model" });
          onModelLoaded().catch(() => {});
          return;
        case "notice":
          // エラーではないお知らせ(今すぐ要約で、要約するものが無かった、など)。会話の表示ではなくトーストに出す
          if (viewing.current === id) notify(ev.text ?? "", { ms: 8000 });
          return;
        case "thinking": {
          // 思考は畳んだ状態で出し、クリックで開いたときだけ中身を見せる
          if (!l.view.thinking) l.thinkStart = performance.now();
          const sec = secSince(l.thinkStart);
          show({
            waiting: false,
            thinking: l.view.thinking + (ev.text ?? ""),
            thinkLabel: l.view.content ? thinkLabel(sec) : `思考中… ${sec}秒`,
          });
          if (!l.view.content) show(liveProcess(false));
          return;
        }
        case "content": {
          const patch: Partial<AssistantView> = { waiting: false, content: l.view.content + (ev.text ?? "") };
          if (l.view.thinking && !l.view.content) patch.thinkLabel = thinkLabel(secSince(l.thinkStart));
          if (!l.view.content) Object.assign(patch, liveProcess(true));
          show(patch);
          return;
        }
      }
    };

    // Go のメソッドを呼ぶ。戻り値の Promise の cancel() を呼ぶと、Go 側の ctx がキャンセルされる(送信全体が止まる)。
    // 送信の途中の要約は、要約だけを止めて返事に進みたいので、別のメソッド(StopSummary)を呼ぶ。
    // 今すぐ要約は返事が無いので、全体を止めればよい
    const call = start();
    controller.current = {
      stop: () => {
        if (l.view.summarizing && !manual) ChatService.StopSummary().catch(err => addError(errorMessage(err)));
        else call.cancel();
      },
    };
    call.then(() => {
      // Go のメソッドは done を送ってから返る。返ったのに done が届かないのは、知らせのつながりが途中で切れていたとき(ブラウザ。0016 の 9-3)。
      // 少し待っても来なければ、保存された内容で開き直して終える(生成中の吹き出しが途中のまま残らないように)
      window.setTimeout(() => {
        if (ended) return;
        live.current = null;
        finish();
        reopen();
      }, 3000);
    }, () => {});
    call.catch(err => {
      if (isCancel(err)) return; // 停止ボタン。done イベントは Go から届く
      // Go のメソッドを呼べなかった(done イベントは来ない)
      showError(`呼び出しエラー: ${errorMessage(err)}`);
      done();
      finish();
    });
    await finished;
    listener.current = null;
    controller.current = null;
    live.current = null;
    // ほかの会話を見ている間に終わったら、一覧に印を出す(エラーなら赤、そうでなければ青)
    if (viewing.current !== id) setEnded(e => ({ ...e, [id]: failed ? "error" : "done" }));
    setGeneratingId(null);
    setBusy("idle");
    reloadList().catch(() => {}); // タイトルと並び順(更新の新しい順)が変わる
    return { errors, userSaved };
  }, [open, reloadList, setUsage, addError, addAssistant, updateAssistant, setSaved, remove, setBusy, notify, dismissKey, onModelLoaded]);

  const send = useCallback(async (text: string) => {
    if (controller.current || starting.current) return false;
    starting.current = true;
    // 新しい会話の画面なら、送る直前に会話を作る(target)。送れなかったときに同じ下書きに戻せるよう、今の下書きを持っておく
    const fromHome = current ? null : { profile: draftProfile, settings: draft };
    let id: string;
    try {
      id = await target();
    } catch (err) {
      addError(`送信できません: ${errorMessage(err)}`);
      return false;
    } finally {
      starting.current = false;
    }
    const userItemId = addUser(text);
    const { userSaved } = await run(id, () => ChatService.Send(id, text), false, userItemId);
    // 送る前に止まった(書式に未対応のモデル・会話の上限を超える、など)ときは、Go は発言を保存していない。
    // 吹き出しを消して、呼んだ側(App)が入力欄に文を戻す(保存されたように見えて、開き直すと消える、にしないため)
    // 新しい会話の画面から送ったなら、作った会話(発言の無い空の会話)も消して、新しい会話の画面に戻す
    if (!userSaved) {
      remove(userItemId);
      if (fromHome?.settings) {
        viewing.current = null;
        await unadopt(id, fromHome.profile, fromHome.settings).catch(err => addError(errorMessage(err)));
      }
      return false;
    }
    return true;
  }, [current, draft, draftProfile, target, run, addUser, addError, remove, unadopt]);

  const compact = useCallback(async () => {
    if (controller.current || starting.current || !current) return;
    const id = current.conversation.id;
    await run(id, () => ChatService.Compact(id), true, null);
  }, [current, run]);

  const stop = useCallback(() => controller.current?.stop(), []);

  // 知らせが切れている間に届かなかった confirm イベントを、Go が待っている確認から作り直す。
  // 待っている確認が無いか、もうカードを出している確認なら何もしない(切れている間に来た結果などは、送信の終わりに開き直して揃える)
  const resync = useCallback(() => {
    const l = live.current;
    if (!l) return;
    ChatService.PendingConfirm(l.convId).then(req => {
      if (req && live.current === l && l.confirm?.id !== req.id) {
        listener.current?.({ conversation_id: l.convId, type: "confirm", confirm: req });
      }
    }, err => addError(errorMessage(err)));
  }, [addError]);

  const lastPrompt = current ? prompts[current.conversation.id] ?? "" : "";
  // 確認に答える。答えは Go の待っているループに届き、実行(か拒否の結果)の tool_result が返ってくる。
  // remember はこのツールのこの会話でのこれから先の扱い(空 = この1回だけ、allow = この会話では今後聞かない、off = 今後許可しない。0010)。
  // Go が会話の上書きに書くので、先にまだ送っていない設定を送り(あとで丸ごと書き戻して、Go が書いた上書きを消さないように)、答えたら読み直す
  const answer = useCallback(async (reqId: string, approved: boolean, reason: string, remember: ToolPolicy) => {
    const l = live.current;
    if (!l) return;
    try {
      if (remember) await flushSettings();
      await ChatService.ConfirmTool(l.convId, reqId, approved, reason, remember);
      if (remember) await reloadSettings(l.convId);
    } catch (err) {
      addError(errorMessage(err));
    }
  }, [addError, flushSettings, reloadSettings]);

  // retry は作り直す・送り直すの共通部分(0011)。表示から最後のやり取りを外してから、送信と同じ流れで返事を作る。
  // 終わったら会話を開き直して、保存された内容に表示を合わせる(送れなかったときは Go が何も消していないので、元の表示に戻る)。
  // 開き直すと表示が作り直されるので、届いたエラーはそのあとに出し直す
  const retry = useCallback(async (withUser: boolean, text: string, start: (id: string) => Call<void>) => {
    if (controller.current || starting.current || !current) return;
    const id = current.conversation.id;
    starting.current = true;
    try {
      await flushSettings();
    } catch (err) {
      addError(errorMessage(err));
      return;
    } finally {
      starting.current = false;
    }
    dropLastTurn(withUser);
    const userItemId = withUser ? addUser(text) : null;
    const { errors } = await run(id, () => start(id), false, userItemId);
    if (errors.length && viewing.current === id) {
      await open(id).catch(err => addError(errorMessage(err)));
      errors.forEach(e => addError(e.text, e.caution));
    }
  }, [current, flushSettings, dropLastTurn, addUser, run, open, addError]);

  const regenerate = useCallback(() => retry(false, "", id => ChatService.Regenerate(id)), [retry]);
  const resend = useCallback((text: string) => retry(true, text, id => ChatService.Resend(id, text)), [retry]);

  return { generatingId, ended, confirmingId, answer, lastPrompt, send, regenerate, resend, compact, stop, resync };
}
