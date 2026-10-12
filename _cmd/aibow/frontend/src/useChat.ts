// useChat.ts: 送信・生成の途中経過の受け取り・停止。
//
// 会話の履歴は Go が持っているので、ここで送るのは「会話の ID」と「新しい発言」だけ。
// Go が履歴を保存し、全部を並べ直してモデルに渡す(モデル自身は何も覚えていない)。
import { useCallback, useEffect, useRef, useState } from "react";
import { CancelError, Events } from "@wailsio/runtime";
import { ChatService } from "../bindings/aibow/cmd/aibow";
import type { ChatEvent, StoredMessage } from "../bindings/aibow";
import { messageItems, replyView, thinkLabel } from "./transcript";
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
  view: AssistantView;
  thinkStart: number;
}

export interface Chat {
  generatingId: string | null; // 返事を生成中の会話の ID
  lastPrompt: string;          // 開いている会話で、モデルに実際に渡した文字列
  send(text: string): Promise<void>;
  stop(): void;
}

export function useChat(t: Transcript, setBusy: (b: Busy) => void, convs: Conversations): Chat {
  const { addUser, addError, addAssistant, updateAssistant, remove, reset } = t;
  const { current, openCount, open, flushSettings, reloadList } = convs;
  const [generatingId, setGeneratingId] = useState<string | null>(null);
  // モデルに渡した文字列は保存しないので、会話ごとにここで覚えておく(再起動すると消える)
  const [prompts, setPrompts] = useState<Record<string, string>>({});

  const live = useRef<Live | null>(null);
  const viewing = useRef<string | null>(null); // 表示している会話の ID
  // 生成中の呼び出し(stop() で停止できる)。生成中でなければ null
  const controller = useRef<{ stop(): void } | null>(null);
  // 生成中の返事に chat イベントを渡す関数
  const listener = useRef<((ev: ChatEvent) => void) | null>(null);

  // Go の Send が送ってくる途中経過(aibow.ChatEvent)。
  // main.go の RegisterEvent で型を登録してあるので、ev.data は ChatEvent 型になる。
  useEffect(() => Events.On("chat", ev => listener.current?.(ev.data)), []);

  // 会話を開いたら、保存された発言から表示を作り直す。
  // その会話で生成中なら、生成中の返事を末尾に足す(Go がまだ保存していないため)。
  useEffect(() => {
    if (!current) return;
    const id = current.conversation.id;
    viewing.current = id;
    reset(messageItems(current.messages ?? []));
    const l = live.current;
    if (l) l.itemId = l.convId === id && current.generating ? addAssistant(l.view) : null;
    // 開くたびに増える openCount だけを見る(設定の変更で current が変わっても作り直さない)
  }, [openCount]);

  const send = useCallback(async (text: string) => {
    if (!current || controller.current) return;
    const id = current.conversation.id;
    try {
      await flushSettings(); // 変えたばかりの設定を、送信より先に Go に届ける
    } catch (err) {
      addError(`設定を保存できません: ${errorMessage(err)}`);
      return;
    }

    addUser(text);
    const l: Live = { convId: id, itemId: null, thinkStart: 0, view: { waiting: true, thinking: "", thinkLabel: "", content: "" } };
    l.itemId = addAssistant(l.view);
    live.current = l;
    setGeneratingId(id);
    setBusy("generating");

    // 生成中の返事を書き換える。表示していない会話なら、覚えておくだけ
    const show = (patch: Partial<AssistantView>) => {
      l.view = { ...l.view, ...patch };
      if (l.itemId !== null) updateAssistant(l.itemId, patch);
    };

    // 終わったら、Go が保存した返事で吹き出しを置き換える(統計や「思考の過程(N秒)」も付く)
    const done = (reply: StoredMessage | null) => {
      if (l.itemId !== null) {
        if (reply) updateAssistant(l.itemId, replyView(reply));
        else remove(l.itemId); // 何も生成されなかった
      } else if (reply && viewing.current === id) {
        // 会話を開き直している最中に終わった。保存された返事が読めるように開き直す
        open(id).catch(() => {});
      }
    };

    // 返事は Send の戻り値ではなく、chat イベントで少しずつ届く。done が来たら終わり。
    let finish = () => {};
    const finished = new Promise<void>(resolve => (finish = resolve));
    listener.current = ev => {
      if (ev.conversation_id !== id) return;
      switch (ev.type) {
        case "done":
          done(ev.reply ?? null);
          finish();
          return;
        case "prompt":
          setPrompts(p => ({ ...p, [id]: ev.text ?? "" }));
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
          return;
        }
        case "content": {
          const patch: Partial<AssistantView> = { waiting: false, content: l.view.content + (ev.text ?? "") };
          if (l.view.thinking && !l.view.content) patch.thinkLabel = thinkLabel(secSince(l.thinkStart));
          show(patch);
          return;
        }
      }
    };

    // Go の Send を呼ぶ。戻り値の Promise の cancel() を呼ぶと、Go 側の ctx がキャンセルされる
    const call = ChatService.Send(id, text);
    controller.current = { stop: () => call.cancel() };
    call.catch(err => {
      if (err instanceof CancelError) return; // 停止ボタン。done イベントは Go から届く
      // Go のメソッドを呼べなかった(done イベントは来ない)
      addError(`呼び出しエラー: ${errorMessage(err)}`);
      done(null);
      finish();
    });
    await finished;
    listener.current = null;
    controller.current = null;
    live.current = null;
    setGeneratingId(null);
    setBusy("idle");
    reloadList().catch(() => {}); // タイトルと並び順(更新の新しい順)が変わる
  }, [current, flushSettings, open, reloadList, addUser, addError, addAssistant, updateAssistant, remove, setBusy]);

  const stop = useCallback(() => controller.current?.stop(), []);

  const lastPrompt = current ? prompts[current.conversation.id] ?? "" : "";
  return { generatingId, lastPrompt, send, stop };
}
