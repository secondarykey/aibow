// 会話の表示: 吹き出し・エラー・要約の区切り
import { useEffect, useRef, useState } from "react";
import type { Compaction } from "../../bindings/aibow";
import { compactionStats } from "../transcript";
import type { AssistantView, Item } from "../transcript";
import { RenderedText } from "./RenderedText";

const FADED_TITLE = "この発言はモデルに渡していません(要約に入ったか、会話の上限に収めるために外しました)";

// onStop は生成中の返事(要約中なら要約だけ)を止める。生成中の返事は1つだけ(生成は同時に1つ)なので、どの返事かは渡さない。
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
            case "divider":
              return <Divider key={it.id} compaction={it.compaction} />;
            case "assistant":
              return <AssistantMessage key={it.id} view={it} faded={!!faded(it.msgId)} onStop={onStop} />;
          }
        })}
      </div>
    </div>
  );
}

function AssistantMessage({ view, faded, onStop }: { view: AssistantView; faded: boolean; onStop(): void }) {
  return (
    <div className={"msg assistant" + (faded ? " faded" : "")} title={faded ? FADED_TITLE : undefined}>
      {view.summarizing && <Summarizing text={view.summary ?? ""} />}
      {view.compactNote && <p className="compact-note">{view.compactNote}</p>}
      {view.thinking && <Thinking label={view.thinkLabel} text={view.thinking} />}
      <div className="body">
        {view.waiting && !view.summarizing && <p className="waiting">応答待ち…</p>}
        <RenderedText text={view.content} />
        {view.cutoff && <p className="cutoff">{view.cutoff}</p>}
        {view.cutoffHint && (
          <p className="hint">「思考する」をオフにするか、アプリ全体の設定の「返事の上限」を増やすと答えまで届きやすくなります。</p>
        )}
      </div>
      {view.stats && <div className="stats">{view.stats}</div>}
      {view.generating && (
        <div className="stop-row">
          {/* 要約中の停止は要約だけを止める(返事は truncate で生成する)ので、要約が終わったらまた押せる */}
          <StopButton key={view.summarizing ? "summary" : "reply"} label={view.summarizing ? "要約を停止" : "停止"} onStop={onStop} />
        </div>
      )}
    </div>
  );
}

// 停止を押してから、Go が止まるまでの間は押せなくする
function StopButton({ label, onStop }: { label: string; onStop(): void }) {
  const [stopping, setStopping] = useState(false);
  return <button disabled={stopping} onClick={() => { setStopping(true); onStop(); }}>{label}</button>;
}

// 要約を作っている途中: 要約の文が流れてくる様子を見せる
function Summarizing({ text }: { text: string }) {
  const box = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (box.current) box.current.scrollTop = box.current.scrollHeight;
  }, [text]);
  return (
    <div className="summarizing">
      <div className="summarizing-label">古い発言を要約しています…</div>
      {text && <div className="summarizing-text" ref={box}>{text}</div>}
    </div>
  );
}

// 要約した位置の区切り。開くと要約の文(モデルに渡しているもの)と記録が読める
function Divider({ compaction }: { compaction: Compaction }) {
  return (
    <details className="divider">
      <summary>― ここまでを要約しました ―</summary>
      <div className="divider-body">
        <RenderedText text={compaction.summary} />
        <div className="stats">{compactionStats(compaction)}</div>
      </div>
    </details>
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
