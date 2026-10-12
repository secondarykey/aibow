// 会話の表示: 吹き出し・メモ・エラー
import { useEffect, useRef } from "react";
import type { AssistantView, Item } from "../transcript";
import { RenderedText } from "./RenderedText";

export function ChatView({ items }: { items: Item[] }) {
  const ref = useRef<HTMLDivElement>(null);

  // 何か増えたり書き換わったりしたら、一番下までスクロールする
  useEffect(() => {
    if (ref.current) ref.current.scrollTop = ref.current.scrollHeight;
  }, [items]);

  return (
    <div className="chat" ref={ref}>
      <div className="chat-inner">
        {items.map(it => {
          switch (it.kind) {
            case "user":
              return <div key={it.id} className="msg user">{it.text}</div>;
            case "error":
              return <div key={it.id} className="msg error">{it.text}</div>;
            case "note":
              return <div key={it.id} className="note">— {it.text} —</div>;
            case "assistant":
              return <AssistantMessage key={it.id} view={it} />;
          }
        })}
      </div>
    </div>
  );
}

function AssistantMessage({ view }: { view: AssistantView }) {
  return (
    <div className="msg assistant">
      {view.thinking && <Thinking label={view.thinkLabel} text={view.thinking} />}
      <div className="body">
        {view.waiting && <p className="waiting">応答待ち…</p>}
        <RenderedText text={view.content} />
        {view.cutoff && <p className="cutoff">{view.cutoff}</p>}
        {view.cutoffHint && (
          <p className="hint">「思考する」をオフにするか、右の設定の「返事の上限トークン数」か「コンテキスト長」を増やすと答えまで届きやすくなります。</p>
        )}
      </div>
      {view.stats && <div className="stats">{view.stats}</div>}
    </div>
  );
}

// 思考は畳んだ状態で出し、クリックで開いたときだけ中身を見せる
function Thinking({ label, text }: { label: string; text: string }) {
  const box = useRef<HTMLDivElement>(null);
  const details = useRef<HTMLDetailsElement>(null);

  // 開いているときは、思考の続きが見えるように下までスクロールする
  useEffect(() => {
    if (details.current?.open && box.current) box.current.scrollTop = box.current.scrollHeight;
  }, [text]);

  return (
    <details className="thinking" ref={details}>
      <summary>{label}</summary>
      <div ref={box}>{text}</div>
    </details>
  );
}
