// 会話の表示: 吹き出し・エラー
import { useEffect, useRef, useState } from "react";
import type { AssistantView, Item } from "../transcript";
import { RenderedText } from "./RenderedText";

const FADED_TITLE = "この発言はモデルに渡していません(要約に入ったか、会話の上限に収めるために外しました)";

// onStop は生成中の返事を止める。生成中の返事は1つだけ(生成は同時に1つ)なので、どの返事かは渡さない。
// firstId はモデルに渡す最初の発言(Go の Usage.first_id)。これより前の発言は、要約に入ったか
// truncate で外れて、モデルに渡していないので薄く表示する(発言そのものは保存されている)
export function ChatView({ items, firstId, onStop }: { items: Item[]; firstId: string | null; onStop(): void }) {
  const faded = (msgId?: string) => (msgId && firstId && msgId < firstId ? " faded" : "");
  const fadedTitle = (msgId?: string) => (faded(msgId) ? FADED_TITLE : undefined);

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
              return <div key={it.id} className={"msg user" + faded(it.msgId)} title={fadedTitle(it.msgId)}>{it.text}</div>;
            case "error":
              return <div key={it.id} className="msg error">{it.text}</div>;
            case "assistant":
              return <AssistantMessage key={it.id} view={it} faded={!!faded(it.msgId)} onStop={onStop} />;
          }
        })}
      </div>
    </div>
  );
}

function AssistantMessage({ view, faded, onStop }: { view: AssistantView; faded: boolean; onStop(): void }) {
  // 停止を押してから、Go が止まって done が届くまでの間は押せなくする
  const [stopping, setStopping] = useState(false);
  return (
    <div className={"msg assistant" + (faded ? " faded" : "")} title={faded ? FADED_TITLE : undefined}>
      {view.thinking && <Thinking label={view.thinkLabel} text={view.thinking} />}
      <div className="body">
        {view.waiting && <p className="waiting">応答待ち…</p>}
        <RenderedText text={view.content} />
        {view.cutoff && <p className="cutoff">{view.cutoff}</p>}
        {view.cutoffHint && (
          <p className="hint">「思考する」をオフにするか、アプリ全体の設定の「返事の上限」を増やすと答えまで届きやすくなります。</p>
        )}
      </div>
      {view.stats && <div className="stats">{view.stats}</div>}
      {view.generating && (
        <div className="stop-row">
          <button disabled={stopping} onClick={() => { setStopping(true); onStop(); }}>停止</button>
        </div>
      )}
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
