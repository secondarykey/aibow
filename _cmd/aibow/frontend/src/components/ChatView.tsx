// 会話の表示: 吹き出し・エラー・要約の区切り
import { useState } from "react";
import { LuChevronDown, LuChevronRight, LuCircleStop } from "react-icons/lu";
import { CopyButton } from "./CopyButton";
import type { Compaction } from "../../bindings/aibow";
import { compactionStats } from "../transcript";
import type { AssistantView, Item } from "../transcript";
import { RenderedText } from "./RenderedText";
import { useStickToBottom } from "../stickToBottom";

const FADED_TITLE = "この発言はモデルに渡していません(要約に入ったか、会話の上限に収めるために外しました)";

// onStop は生成中の返事(要約中なら要約だけ)を止める。生成中の返事は1つだけ(生成は同時に1つ)なので、どの返事かは渡さない。
// firstId はモデルに渡す最初の発言(Go の Usage.first_id)。これより前の発言は、要約に入ったか
// truncate で外れて、モデルに渡していないので薄く表示する(発言そのものは保存されている)
// conversationId は表示している会話の ID。会話を切り替えたら、一番下(最新の発言)から見せる
export function ChatView({ items, firstId, conversationId, onStop }: {
  items: Item[];
  firstId: string | null;
  conversationId: string | null;
  onStop(): void;
}) {
  const faded = (msgId?: string) => (msgId && firstId && msgId < firstId ? " faded" : "");
  const fadedTitle = (msgId?: string) => (faded(msgId) ? FADED_TITLE : undefined);

  // 何か増えたり書き換わったりしたら、一番下にいるときだけ一番下まで送る(上にスクロールして読んでいる間は送らない)。
  // 会話を切り替えたときと、自分で送信したとき(ユーザーの発言が増えたとき)は、どこを読んでいても一番下へ
  const userCount = items.filter(it => it.kind === "user").length;
  const ref = useStickToBottom<HTMLDivElement>(items, `${conversationId}:${userCount}`);

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
      {!view.generating && (view.content || view.stats) && (
        <ReplyActions content={view.content} stats={view.stats} createdAt={view.createdAt} />
      )}
      {view.generating && (
        <div className="stop-row">
          {/* 要約中の停止は要約だけを止める(返事は truncate で生成する)ので、要約が終わったらまた押せる */}
          <StopButton key={view.summarizing ? "summary" : "reply"} label={view.summarizing ? "要約を停止" : "停止"} onStop={onStop} />
        </div>
      )}
    </div>
  );
}

// 返事の最後の行: 答えのコピーと、詳細情報(モデル名・速さ・トークン数など)の開閉。
// 詳細情報はふだんは使わないので畳んでおき、「詳細」を押したときだけ出す。生成中は出さない(停止ボタンだけ)
function ReplyActions({ content, stats, createdAt }: { content: string; stats?: string; createdAt?: string }) {
  const [open, setOpen] = useState(false);
  const time = replyTime(createdAt);
  return (
    <>
      <div className="reply-actions">
        {content && <CopyButton text={content} />}
        {time && <span className="reply-time" title={time.full}>{time.short}</span>}
        {stats && (
          <button className="reply-action" onClick={() => setOpen(o => !o)} aria-expanded={open} title="モデル名・速さ・トークン数など">
            {open ? <LuChevronDown /> : <LuChevronRight />} 詳細
          </button>
        )}
      </div>
      {open && stats && <div className="stats">{stats}</div>}
    </>
  );
}

// 返事の時刻の表示。今日なら「14:05」、今年なら「10/12 14:05」、それより前は「2025/10/12 14:05」。
// マウスを乗せると秒まで出す。今日かどうかは表示するときに決める(日をまたいで開いたままでも合うように)
function replyTime(createdAt?: string): { short: string; full: string } | null {
  if (!createdAt) return null;
  const t = new Date(createdAt);
  if (Number.isNaN(t.getTime())) return null;
  const now = new Date();
  const pad = (n: number) => String(n).padStart(2, "0");
  const hm = `${pad(t.getHours())}:${pad(t.getMinutes())}`;
  const md = `${t.getMonth() + 1}/${t.getDate()}`;
  const full = `${t.getFullYear()}/${md} ${hm}:${pad(t.getSeconds())}`;
  if (t.toDateString() === now.toDateString()) return { short: hm, full };
  if (t.getFullYear() === now.getFullYear()) return { short: `${md} ${hm}`, full };
  return { short: `${t.getFullYear()}/${md} ${hm}`, full };
}

// 停止のボタン。赤い停止のアイコンだけを出し、何を止めるか(停止 / 要約を停止)はマウスを乗せると出る。
// 停止を押してから、Go が止まるまでの間は押せなくする
function StopButton({ label, onStop }: { label: string; onStop(): void }) {
  const [stopping, setStopping] = useState(false);
  return (
    <button className="stop" title={label} aria-label={label} disabled={stopping} onClick={() => { setStopping(true); onStop(); }}>
      <LuCircleStop />
    </button>
  );
}

// 要約を作っている途中: 要約の文が流れてくる様子を見せる
function Summarizing({ text }: { text: string }) {
  const box = useStickToBottom<HTMLDivElement>(text);
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
  // 開いているときは、思考の続きが見えるように下まで送る(上にスクロールして読んでいる間は送らない)。
  // 開いたときは、一番下(最新の思考)から見せる
  const [open, setOpen] = useState(false);
  const box = useStickToBottom<HTMLDivElement>(text, open);

  return (
    <details className="thinking" onToggle={e => setOpen(e.currentTarget.open)}>
      <summary>{label}</summary>
      <div ref={box}>{text}</div>
    </details>
  );
}
