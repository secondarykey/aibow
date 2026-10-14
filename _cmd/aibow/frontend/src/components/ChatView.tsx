// 会話の表示: 吹き出し(ツールのカードを含む)・エラー・要約の区切り
import { useEffect, useState } from "react";
import type { ToolPolicy } from "../../bindings/github.com/secondarykey/aibow";
import type { ReactNode } from "react";
import { LuChevronDown, LuChevronRight, LuCircleStop, LuEye, LuEyeOff } from "react-icons/lu";
import { CopyButton } from "./CopyButton";
import type { Compaction } from "../../bindings/github.com/secondarykey/aibow";
import { compactionStats } from "../transcript";
import type { AssistantView, Item } from "../transcript";
import { RenderedText } from "./RenderedText";
import { ToolCard } from "./ToolCard";
import { useStickToBottom } from "../stickToBottom";

const FADED_TITLE = "この発言はモデルに渡していません(要約に入ったか、会話の上限に収めるために外しました)";
const EXCLUDED_TITLE = "モデルに渡さないにしています";

// Exclude は吹き出しの「モデルに渡さない」を切り替える(0011)。msgId は吹き出しの中の発言のどれか(Go が吹き出し1つ分に広げる)
export interface Exclude {
  disabled: boolean; // 生成中の会話では変えられない
  onChange(msgId: string, excluded: boolean): void;
}

// onStop は生成中の返事(要約中なら要約だけ)を止める。生成中の返事は1つだけ(生成は同時に1つ)なので、どの返事かは渡さない。
// firstId はモデルに渡す最初の発言(Go の Usage.first_id)。これより前の発言は、要約に入ったか
// truncate で外れて、モデルに渡していないので薄く表示する(発言そのものは保存されている)
// conversationId は表示している会話の ID。会話を切り替えたら、一番下(最新の発言)から見せる
export function ChatView({ items, firstId, conversationId, onStop, onAnswer, exclude }: {
  items: Item[];
  firstId: string | null;
  conversationId: string | null;
  onStop(): void;
  exclude: Exclude;
  onAnswer(reqId: string, approved: boolean, reason: string, remember: ToolPolicy): void; // ツールの実行前の確認に答える(remember はこれから先の扱い)
}) {
  const faded = (msgId?: string) => (msgId && firstId && msgId < firstId ? " faded" : "");
  const fadedTitle = (msgId?: string, excluded?: boolean) => (excluded ? EXCLUDED_TITLE : faded(msgId) ? FADED_TITLE : undefined);

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
              return (
                <div key={it.id} className="user-turn">
                  <div className={"msg user" + faded(it.msgId) + (it.excluded ? " excluded" : "")} title={fadedTitle(it.msgId, it.excluded)}>{it.text}</div>
                  <UserActions text={it.text} createdAt={it.createdAt}>
                    {it.msgId && <ExcludeButton msgId={it.msgId} excluded={!!it.excluded} exclude={exclude} />}
                  </UserActions>
                </div>
              );
            case "error":
              return <div key={it.id} className="msg error">{it.text}</div>;
            case "divider":
              return <Divider key={it.id} compaction={it.compaction} />;
            case "assistant":
              return (
                <AssistantMessage
                  key={it.id}
                  view={it}
                  faded={!!faded(it.msgId)}
                  onStop={onStop}
                  onAnswer={onAnswer}
                  excludeButton={it.msgId && <ExcludeButton msgId={it.msgId} excluded={!!it.excluded} exclude={exclude} />}
                  excluded={!!it.excluded}
                />
              );
          }
        })}
      </div>
    </div>
  );
}

function AssistantMessage({ view, faded, onStop, onAnswer, excludeButton, excluded }: {
  view: AssistantView;
  faded: boolean;
  onStop(): void;
  excludeButton?: ReactNode; // 「モデルに渡さない」の切り替え(保存済みの返事だけ)
  excluded: boolean;
  onAnswer(reqId: string, approved: boolean, reason: string, remember: ToolPolicy): void;
}) {
  // コピーするのは答えの文だけ(ツールを使った送信では、各回の答えをつなげる)
  const answer = [...(view.steps ?? []).map(s => s.content), view.content].filter(Boolean).join("\n\n");
  return (
    <div
      className={"msg assistant" + (faded ? " faded" : "") + (excluded ? " excluded" : "")}
      title={excluded ? EXCLUDED_TITLE : faded ? FADED_TITLE : undefined}
    >
      {view.summarizing && <Summarizing text={view.summary ?? ""} />}
      {view.compactNote && <p className="compact-note">{view.compactNote}</p>}
      {view.steps?.length ? (
        // ツールを使った送信: 答えより前(各回の思考 → 途中の答え → カード、最後の回の思考)を1つの枠にまとめて畳む
        // 確認を待っているカードがあれば、枠を開いたままにする(畳んでいると、待っていることに気づけない)
        <Process label={view.processLabel ?? ""} forceOpen={view.steps.some(s => s.tools.some(t => t.confirm))}>
          {view.steps.map((s, i) => (
            <div key={i} className="step">
              {s.thinking && <Thinking label={s.thinkLabel} text={s.thinking} />}
              {s.content && <div className="body"><RenderedText text={s.content} /></div>}
              {s.tools.map((t, j) => <ToolCard key={j} tool={t} generating={!!view.generating} onAnswer={onAnswer} />)}
            </div>
          ))}
          {view.thinking && <Thinking label={view.thinkLabel} text={view.thinking} />}
        </Process>
      ) : (
        view.thinking && <Thinking label={view.thinkLabel} text={view.thinking} />
      )}
      <div className="body">
        {view.waiting && !view.summarizing && <p className="waiting">応答待ち…</p>}
        <RenderedText text={view.content} />
        {view.cutoff && <p className="cutoff">{view.cutoff}</p>}
        {view.cutoffHint && (
          <p className="hint">「思考する」をオフにするか、アプリ全体の設定の「返事の上限」を増やすと答えまで届きやすくなります。</p>
        )}
      </div>
      {!view.generating && (answer || view.stats) && (
        <ReplyActions content={answer} stats={view.stats} createdAt={view.createdAt}>{excludeButton}</ReplyActions>
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
function ReplyActions({ content, stats, createdAt, children }: { content: string; stats?: string; createdAt?: string; children?: ReactNode }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <div className="reply-actions">
        {content && <CopyButton text={content} />}
        {createdAt && <MessageTime createdAt={createdAt} />}
        {children}
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

// 発言の下の行: 時刻とコピー。発言と同じく右に寄せる(返事の側とは並びを逆にして「時刻 コピー」)
function UserActions({ text, createdAt, children }: { text: string; createdAt?: string; children?: ReactNode }) {
  return (
    <div className="user-actions">
      {children}
      {createdAt && <MessageTime createdAt={createdAt} />}
      <CopyButton text={text} />
    </div>
  );
}

// ExcludeButton は吹き出しの「モデルに渡さない」の切り替え(0011)。目のアイコンで、渡さないにしたら斜線の入った目のまま残す
function ExcludeButton({ msgId, excluded, exclude }: { msgId: string; excluded: boolean; exclude: Exclude }) {
  return (
    <button
      className={"reply-action exclude" + (excluded ? " on" : "")}
      disabled={exclude.disabled}
      title={exclude.disabled ? "返事を生成中は変えられません" : excluded ? "モデルに渡すに戻す" : "モデルに渡さない"}
      onClick={() => exclude.onChange(msgId, !excluded)}
    >
      {excluded ? <LuEyeOff /> : <LuEye />}
    </button>
  );
}

// 発言と返事の時刻。ふだんは今からどれくらい前か(「3分前」「2時間前」「4日前」)で出し、マウスを乗せると実際の時刻を秒まで出す。
// 5日以上前は「前」で数えても分かりにくいので、日時(「2025/10/12 14:05」)で出す。
// 開いたままでも「たった今」→「1分前」と進むよう、30 秒ごとに描き直す
function MessageTime({ createdAt }: { createdAt: string }) {
  const now = useNow(30_000);
  const t = new Date(createdAt);
  if (Number.isNaN(t.getTime())) return null;
  return <span className="msg-time" title={formatDateTime(t, true)}>{ago(t, now)}</span>;
}

function useNow(intervalMs: number): Date {
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    const timer = window.setInterval(() => setNow(new Date()), intervalMs);
    return () => window.clearInterval(timer);
  }, [intervalMs]);
  return now;
}

function ago(t: Date, now: Date): string {
  const sec = (now.getTime() - t.getTime()) / 1000; // 時計のずれで未来になったときは「たった今」
  if (sec < 60) return "たった今";
  if (sec < 60 * 60) return `${Math.floor(sec / 60)}分前`;
  if (sec < 24 * 60 * 60) return `${Math.floor(sec / (60 * 60))}時間前`;
  if (sec < 5 * 24 * 60 * 60) return `${Math.floor(sec / (24 * 60 * 60))}日前`;
  return formatDateTime(t, false);
}

// 「2025/10/12 14:05」。withSeconds なら「2025/10/12 14:05:09」
function formatDateTime(t: Date, withSeconds: boolean): string {
  const pad = (n: number) => String(n).padStart(2, "0");
  const time = `${pad(t.getHours())}:${pad(t.getMinutes())}` + (withSeconds ? `:${pad(t.getSeconds())}` : "");
  return `${t.getFullYear()}/${t.getMonth() + 1}/${t.getDate()} ${time}`;
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

// ツールを使った送信の、答えまでの過程の枠。畳んだ状態で出し、開くと中に思考とツールのカードが並ぶ
// forceOpen の間は開いたままにする(外すと、ふだんの畳んだ状態に戻る)
function Process({ label, forceOpen, children }: { label: string; forceOpen: boolean; children: ReactNode }) {
  return (
    <details className="process" open={forceOpen ? true : undefined}>
      <summary>{label}</summary>
      <div className="process-body">{children}</div>
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
