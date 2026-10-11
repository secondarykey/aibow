// useChat.ts: 送信・生成の途中経過の受け取り・停止。
import { useCallback, useEffect, useRef, useState } from "react";
import { CancelError, Events } from "@wailsio/runtime";
import { ChatService } from "../bindings/aibow/cmd/aibow";
import type { ChatEvent } from "../bindings/aibow/cmd/aibow";
import type { GenerateStats, Message, ModelInfo } from "../bindings/aibow";
import type { AssistantView, Transcript } from "./transcript";
import type { Busy } from "./useModels";
import { errorMessage } from "./errors";

export interface ChatSettings {
  system: string;
  think: boolean;
  temperature: number;
}

// 答えが出ないまま終わったときに、答えの代わりに表示して履歴にも残す文
const NO_ANSWER: Record<string, string> = {
  length: "(考えている途中で上限トークン数に達したため、答えを出せませんでした)",
  context: "(考えている途中でコンテキストがいっぱいになったため、答えを出せませんでした)",
  cancel: "(考えている途中で停止されたため、答えを出していません)",
};

// 生成が「発言の終わり」以外の理由で止まったときの表示
const STOP_REASONS: Record<string, string> = {
  length: "上限トークン数で打ち切り",
  context: "コンテキストがいっぱいで打ち切り",
  cancel: "停止",
};

const secSince = (t: number) => Math.round((performance.now() - t) / 1000);

export interface Chat {
  lastPrompt: string; // モデルに実際に渡した文字列
  send(text: string): Promise<void>;
  stop(): void;
  clear(): void;
}

export function useChat(
  t: Transcript,
  setBusy: (b: Busy) => void,
  current: ModelInfo | null,
  settings: ChatSettings,
): Chat {
  const { addUser, addError, addAssistant, updateAssistant, remove, clear: clearItems } = t;
  const [lastPrompt, setLastPrompt] = useState("");

  // 会話履歴。これが「LLMの記憶」の正体。画面側でこの配列を持ち、毎回まるごと送る。
  // 表示には使わないので state ではなく ref に持つ。
  const history = useRef<Message[]>([]);
  // 「新しい会話」を押すたびに増やす。生成中に押されたら、その返事は新しい会話の履歴に入れない
  const session = useRef(0);

  // 生成中の呼び出し(stop() で停止できる)。生成中でなければ null
  const controller = useRef<{ stopped: boolean; stop(): void } | null>(null);
  // 生成中の返事に chat イベントを渡す関数
  const listener = useRef<((ev: ChatEvent) => void) | null>(null);

  // Go の Chat が送ってくる途中経過(service.go の ChatEvent)。
  // main.go の RegisterEvent で型を登録してあるので、ev.data は ChatEvent 型になる。
  useEffect(() => Events.On("chat", ev => listener.current?.(ev.data)), []);

  const send = useCallback(async (text: string) => {
    if (controller.current) return;
    const mySession = session.current;
    history.current.push({ role: "user", content: text });
    addUser(text);

    // システムプロンプトは履歴の先頭に毎回付ける
    const system = settings.system.trim();
    const messages: Message[] = system ? [{ role: "system", content: system }, ...history.current] : [...history.current];

    const id = addAssistant();
    const modelName = current ? current.name : "(モデルなし)"; // どのモデルの返事か記録しておく
    const startAt = performance.now();
    let content = "", thinking = "", thinkStart = 0;
    let firstAt: number | null = null;
    let stats: GenerateStats | null = null;
    setBusy("generating");

    // 返事は Chat の戻り値ではなく、chat イベントで少しずつ届く。done が来たら終わり。
    let finish = () => {};
    const finished = new Promise<void>(resolve => (finish = resolve));
    listener.current = ev => {
      switch (ev.type) {
        case "done":
          finish();
          return;
        case "prompt":
          setLastPrompt(ev.text ?? "");
          return;
        case "stats":
          stats = ev.stats ?? null;
          return;
        case "error":
          addError(ev.message ?? "");
          return;
        case "thinking":
          // 思考は畳んだ状態で出し、クリックで開いたときだけ中身を見せる
          firstAt ??= performance.now();
          if (!thinking) thinkStart = performance.now();
          thinking += ev.text ?? "";
          updateAssistant(id, {
            waiting: false,
            thinking,
            thinkLabel: content ? `思考の過程(${secSince(thinkStart)}秒)` : `思考中… ${secSince(thinkStart)}秒`,
          });
          return;
        case "content":
          firstAt ??= performance.now();
          if (thinking && !content) updateAssistant(id, { thinkLabel: `思考の過程(${secSince(thinkStart)}秒)` });
          content += ev.text ?? "";
          updateAssistant(id, { waiting: false, content });
          return;
      }
    };

    // Go の Chat を呼ぶ。戻り値の Promise の cancel() を呼ぶと、Go 側の ctx がキャンセルされる
    const call = ChatService.Chat({ messages, think: settings.think, temperature: settings.temperature });
    const ctl = { stopped: false, stop() { this.stopped = true; call.cancel(); } };
    controller.current = ctl;
    call.catch(err => {
      if (err instanceof CancelError) return; // 停止ボタン。done イベントは Go から届く
      // Go のメソッドを呼べなかった(done イベントは来ない)
      addError(`呼び出しエラー: ${errorMessage(err)}`);
      finish();
    });
    await finished;
    listener.current = null;
    controller.current = null;
    setBusy("idle");
    if (mySession !== session.current) return; // 生成中に「新しい会話」が押された

    // 統計: 最初の文字が出るまでの時間と、Go から届いたトークン数・速度
    const patch: Partial<AssistantView> = { waiting: false };
    if (firstAt !== null) {
      const parts = [modelName, `初回応答 ${((firstAt - startAt) / 1000).toFixed(2)}s`];
      const s = stats as GenerateStats | null;
      if (s) {
        parts.push(`プロンプト ${s.prompt_tokens} tok(再利用 ${s.reused_tokens})${s.prompt_sec.toFixed(2)}s`);
        const rate = s.gen_sec > 0 ? (s.gen_tokens / s.gen_sec).toFixed(1) : "-";
        parts.push(`生成 ${s.gen_tokens} tok ・ ${rate} tok/s`);
        if (s.stop_reason !== "eos") parts.push(STOP_REASONS[s.stop_reason] ?? s.stop_reason);
      } else if (ctl.stopped) {
        parts.push("停止");
      }
      patch.stats = parts.join(" ・ ");
    }

    // 履歴に残すのは「答え」だけ。思考部分は次回以降は送らないのが一般的。
    if (content) {
      history.current.push({ role: "assistant", content });
    } else if (thinking) {
      // 考えている途中で終わり、答えが出なかった場合。
      // 何も残さないと、モデルからは「質問に返事をしていない」ように見えるので、
      // 何が起きたかを返事として履歴に残す。
      const reason = (stats as GenerateStats | null)?.stop_reason ?? (ctl.stopped ? "cancel" : "");
      const note = NO_ANSWER[reason] ?? "(考えている途中で終わったため、答えを出せませんでした)";
      patch.thinkLabel = `思考の過程(${secSince(thinkStart)}秒)`;
      patch.cutoff = note;
      patch.cutoffHint = reason !== "cancel";
      history.current.push({ role: "assistant", content: note });
    } else {
      remove(id);
    }
    if (content || thinking) updateAssistant(id, patch);
  }, [addUser, addError, addAssistant, updateAssistant, remove, setBusy, current, settings]);

  const stop = useCallback(() => controller.current?.stop(), []);

  const clear = useCallback(() => {
    controller.current?.stop();
    session.current++;
    history.current = [];
    clearItems();
    setLastPrompt("");
  }, [clearItems]);

  return { lastPrompt, send, stop, clear };
}
