// useChat.ts: 送信・生成の途中経過の受け取り・停止。
//
// 会話の履歴は Go が持っているので、ここで送るのは「会話の ID」と「新しい発言」だけ。
// Go が履歴を保存し、毎回並べ直してモデルに渡す(モデル自身は何も覚えていない)。
// 会話の上限を超えたら、Go が compact で収めてから渡す(compact.go)。
import { useCallback, useEffect, useRef, useState } from "react";
import { CancelError, Events } from "@wailsio/runtime";
import type { CancellablePromise } from "@wailsio/runtime";
import { ChatService } from "../bindings/aibow/cmd/aibow";
import type { ChatEvent, StoredMessage } from "../bindings/aibow";
import { messageItems, thinkLabel, turnView } from "./transcript";
import type { AssistantView, Transcript } from "./transcript";
import type { Conversations } from "./useConversations";
import type { Busy } from "./useModels";
import { errorMessage } from "./errors";

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
}

export interface Chat {
  generatingId: string | null; // 返事(か要約)を生成中の会話の ID
  lastPrompt: string;          // 開いている会話で、モデルに実際に渡した文字列
  send(text: string): Promise<void>;
  compact(): Promise<void>;    // 今すぐ要約(開いている会話)
  stop(): void;
}

export function useChat(t: Transcript, setBusy: (b: Busy) => void, convs: Conversations): Chat {
  const { addUser, addError, addAssistant, updateAssistant, setSaved, remove, reset } = t;
  const { current, draft, openCount, open, adopt, flushSettings, reloadList, setUsage } = convs;
  const [generatingId, setGeneratingId] = useState<string | null>(null);
  // モデルに渡した文字列は保存しないので、会話ごとにここで覚えておく(再起動すると消える)
  const [prompts, setPrompts] = useState<Record<string, string>>({});

  const live = useRef<Live | null>(null);
  const viewing = useRef<string | null>(null); // 表示している会話の ID
  // 生成中の呼び出し(stop() で停止できる)。生成中でなければ null
  const controller = useRef<{ stop(): void } | null>(null);
  // 送信を受け付けてから生成が始まるまでの間に、もう一度送られないようにする
  // (新しい会話の画面では、会話を作る分だけ間があく)
  const starting = useRef(false);
  // 生成中の返事に chat イベントを渡す関数
  const listener = useRef<((ev: ChatEvent) => void) | null>(null);

  // Go の Send が送ってくる途中経過(aibow.ChatEvent)。
  // main.go の RegisterEvent で型を登録してあるので、ev.data は ChatEvent 型になる。
  useEffect(() => Events.On("chat", ev => listener.current?.(ev.data)), []);

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
    // 開くたびに増える openCount だけを見る(設定の変更で current が変わっても作り直さない)
  }, [openCount]);

  // target は送り先の会話の ID を決める。新しい会話の画面なら、ここで会話を作る。
  const target = useCallback(async (): Promise<string> => {
    if (current) {
      await flushSettings(); // 変えたばかりの設定を、送信より先に Go に届ける
      return current.conversation.id;
    }
    // 新しい会話の画面: 画面で変えた設定(draft)で会話を作り、開いている会話にする。
    // 表示は作り直さず(開き直すと、このあと足す吹き出しと順番が入れ替わることがある)、ここで空にする
    const c = await ChatService.NewConversation(draft);
    adopt(c);
    viewing.current = c.id;
    reset([]);
    return c.id;
  }, [current, draft, flushSettings, adopt, reset]);

  // run は送信と「今すぐ要約」の共通部分。生成中の吹き出しを出し、chat イベントで書き換え、done で終わる。
  // manual は「今すぐ要約」(返事は生成しない。終わったら吹き出しを消して、区切りが出るように開き直す)
  const run = useCallback(async (id: string, start: () => CancellablePromise<void>, manual: boolean, userItemId: number | null) => {
    const l: Live = {
      convId: id, itemId: null, userItemId, thinkStart: 0, processStart: 0, saved: [],
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
      if (answering) return { processLabel: thought ? thinkLabel(sec) : `ツールの使用(${sec}秒)` };
      return { processLabel: thought ? `思考中… ${sec}秒` : `ツールを使用中… ${sec}秒` };
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
    let finish = () => {};
    const finished = new Promise<void>(resolve => (finish = resolve));
    listener.current = ev => {
      if (ev.conversation_id !== id) return;
      switch (ev.type) {
        case "done":
          done();
          live.current = null; // 開き直しても、もう生成中の吹き出しは足さない
          if (manual && compacted) reopen();
          finish();
          return;
        case "user":
          // 送信した発言が保存された。ID を付けておくと、あとで truncate や要約で外れたときに薄く表示できる。
          // 発言の下の時刻も、保存した時刻(Go の時計)で出す
          if (l.userItemId !== null && ev.message) setSaved(l.userItemId, ev.message);
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
          show({ summarizing: false, summary: "", waiting: true, compactNote: c.error || undefined });
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
        case "tool_result":
          // ツールの結果を保存した。結果の無いカードは「実行中」と出ている
          if (!ev.message) return;
          l.saved.push(ev.message);
          show({ steps: turnView(l.saved).steps });
          show(liveProcess(false));
          return;
        case "error":
          if (viewing.current === id) addError(ev.error ?? "");
          else console.warn(`別の会話でのエラー: ${ev.error}`);
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
    call.catch(err => {
      if (err instanceof CancelError) return; // 停止ボタン。done イベントは Go から届く
      // Go のメソッドを呼べなかった(done イベントは来ない)
      addError(`呼び出しエラー: ${errorMessage(err)}`);
      done();
      finish();
    });
    await finished;
    listener.current = null;
    controller.current = null;
    live.current = null;
    setGeneratingId(null);
    setBusy("idle");
    reloadList().catch(() => {}); // タイトルと並び順(更新の新しい順)が変わる
  }, [open, reloadList, setUsage, addError, addAssistant, updateAssistant, setSaved, remove, setBusy]);

  const send = useCallback(async (text: string) => {
    if (controller.current || starting.current) return;
    starting.current = true;
    let id: string;
    try {
      id = await target();
    } catch (err) {
      addError(`送信できません: ${errorMessage(err)}`);
      return;
    } finally {
      starting.current = false;
    }
    const userItemId = addUser(text);
    await run(id, () => ChatService.Send(id, text), false, userItemId);
  }, [target, run, addUser, addError]);

  const compact = useCallback(async () => {
    if (controller.current || starting.current || !current) return;
    const id = current.conversation.id;
    await run(id, () => ChatService.Compact(id), true, null);
  }, [current, run]);

  const stop = useCallback(() => controller.current?.stop(), []);

  const lastPrompt = current ? prompts[current.conversation.id] ?? "" : "";
  return { generatingId, lastPrompt, send, compact, stop };
}
