// 会話の表示: 吹き出し(ツールのカードを含む)・エラー・要約の区切り
import { useCallback, useEffect, useRef, useState } from "react";
import type { ToolPolicy } from "../../bindings/github.com/secondarykey/aibow";
import type { MouseEvent, ReactNode } from "react";
import { LuChevronDown, LuChevronRight, LuCircleStop, LuEye, LuEyeOff, LuListFilter, LuPencil, LuRefreshCw, LuTriangleAlert } from "react-icons/lu";
import { CopyButton } from "./CopyButton";
import type { Compaction } from "../../bindings/github.com/secondarykey/aibow";
import { compactionSize, compactionStats, lastIndex } from "../transcript";
import type { AssistantView, Item } from "../transcript";
import { RenderedText } from "./RenderedText";
import { ToolCard } from "./ToolCard";
import { useStickToBottom } from "../stickToBottom";
import { QUESTION_ATTR, usePinnedQuestion } from "../pinnedQuestion";
import { useQuestionsOnly } from "../questionsOnly";

const FADED_TITLE = "この発言はモデルに渡していません(要約に入ったか、会話の上限に収めるために外しました)";
const EXCLUDED_TITLE = "モデルに渡さないにしています";

// Retry は最後のやり取りのやり直し(0011): 最後の返事を作り直す・最後の発言を直して送り直す
export interface Retry {
  disabled: boolean; // 生成中・読み込み中は押せない
  onRegenerate(): void;
  onResend(text: string): void;
}

// Exclude は吹き出しの「モデルに渡さない」を切り替える(0011)。msgId は吹き出しの中の発言のどれか(Go が吹き出し1つ分に広げる)
export interface Exclude {
  disabled: boolean; // 生成中の会話では変えられない
  onChange(msgId: string, excluded: boolean): void;
}

// onStop は生成中の返事(要約中なら要約だけ)を止める。生成中の返事は1つだけ(生成は同時に1つ)なので、どの返事かは渡さない。
// firstId はモデルに渡す最初の発言(Go の Usage.first_id)。これより前の発言は、要約に入ったか
// truncate で外れて、モデルに渡していないので薄く表示する(発言そのものは保存されている)
// conversationId は表示している会話の ID。会話を切り替えたら、一番下(最新の発言)から見せる
// pinQuestion は、スクロールして質問が上に隠れたら、その質問を右上に出しておくか(アプリの設定の「画面」)
export function ChatView({ items, firstId, conversationId, pinQuestion, readOnly = false, onStop, onAnswer, exclude, retry }: {
  items: Item[];
  firstId: string | null;
  conversationId: string | null;
  pinQuestion: boolean;
  readOnly?: boolean; // ほかの人の会話を読むだけ(0018 の 11-2)。直す・作り直す・モデルに渡さないを出さない
  onStop(): void;
  exclude: Exclude;
  retry: Retry;
  onAnswer(reqId: string, approved: boolean, reason: string, remember: ToolPolicy): void; // ツールの実行前の確認に答える(remember はこれから先の扱い)
}) {
  const faded = (msgId?: string) => (msgId && firstId && msgId < firstId ? " faded" : "");
  const fadedTitle = (msgId?: string, excluded?: boolean) => (excluded ? EXCLUDED_TITLE : faded(msgId) ? FADED_TITLE : undefined);

  // 何か増えたり書き換わったりしたら、一番下にいるときだけ一番下まで送る(上にスクロールして読んでいる間は送らない)。
  // 会話を切り替えたときと、自分で送信したとき(ユーザーの発言が増えたとき)は、どこを読んでいても一番下へ
  // 「質問だけ」の表示(右下のボタン)も、会話を切り替えたとき・送信したときにやめる
  const userCount = items.filter(it => it.kind === "user").length;
  const sendKey = `${conversationId}:${userCount}`;
  const el = useRef<HTMLDivElement | null>(null);
  const questions = useQuestionsOnly(el, sendKey);
  // 「質問だけ」を切り替えたときは、位置は useQuestionsOnly が決める(stickToBottom は高さを覚え直すだけ)
  const stickRef = useStickToBottom<HTMLDivElement>(items, sendKey, questions.on);
  // 今読んでいる返事の質問が上に隠れていたら、右上に出す(「質問だけ」の間は出さない)
  const { ref: pinRef, pinned } = usePinnedQuestion<HTMLDivElement>(pinQuestion && !questions.on, items);
  const ref = useCallback((node: HTMLDivElement | null) => {
    el.current = node;
    stickRef(node);
    pinRef(node);
  }, [stickRef, pinRef]);
  const pinnedItem = pinned !== null ? items[pinned] : undefined;
  // やり直せるのは最後のやり取りだけ: 最後の発言(直す)と、その後ろの最後の返事(作り直す)
  const lastUser = lastIndex(items, it => it.kind === "user");
  const lastAssistant = lastIndex(items, it => it.kind === "assistant");
  // 直している発言(吹き出しの中で直す)。会話を切り替えたらやめる
  const [editing, setEditing] = useState<number | null>(null);
  useEffect(() => setEditing(null), [conversationId]);

  return (
    <div className="chat-area">
      <div className="chat" ref={ref}>
        {pinnedItem?.kind === "user" && <PinnedQuestion index={pinned!} text={pinnedItem.text} />}
        <div className={"chat-inner" + (questions.on ? " questions-only" : "")}>
          {items.map((it, i) => {
            switch (it.kind) {
              case "user":
                if (editing === it.id) {
                  return (
                    <EditUser
                      key={it.id}
                      initial={it.text}
                      disabled={retry.disabled}
                      onCancel={() => setEditing(null)}
                      onSend={text => {
                        setEditing(null);
                        retry.onResend(text);
                      }}
                    />
                  );
                }
                return (
                  <div key={it.id} className="user-turn">
                    <div
                      className={"msg user" + faded(it.msgId) + (it.excluded ? " excluded" : "") + (questions.current === i ? " current" : "")}
                      title={questions.on ? "この質問へ飛ぶ" : fadedTitle(it.msgId, it.excluded)}
                      onClick={questions.on ? () => questions.exit(i) : undefined}
                      {...{ [QUESTION_ATTR]: i }}
                    >
                      {it.text}
                    </div>
                    <UserActions text={it.text} createdAt={it.createdAt}>
                      {!readOnly && i === lastUser && it.msgId && (
                        <button className="reply-action" disabled={retry.disabled} title="この発言を直して送り直す(この発言と返事は消える)" onClick={() => setEditing(it.id)}>
                          <LuPencil />
                        </button>
                      )}
                      {!readOnly && it.msgId && <ExcludeButton msgId={it.msgId} excluded={!!it.excluded} exclude={exclude} />}
                    </UserActions>
                  </div>
                );
              case "error":
                // 注意(理由と対処を書いた断り)は、要約できなかったお知らせと同じ見た目にする
                return it.caution
                  ? <div key={it.id} className="compact-note"><LuTriangleAlert className="icon" /><div>{it.text}</div></div>
                  : <div key={it.id} className="msg error">{it.text}</div>;
              case "divider":
                return <Divider key={it.id} compaction={it.compaction} />;
              case "assistant":
                return (
                  <AssistantMessage
                    key={it.id}
                    view={it}
                  msgId={it.msgId}
                    faded={!!faded(it.msgId)}
                    onStop={onStop}
                    onAnswer={onAnswer}
                    excludeButton={!readOnly && it.msgId && (
                      <>
                        <ExcludeButton msgId={it.msgId} excluded={!!it.excluded} exclude={exclude} />
                        {i === lastAssistant && i > lastUser && (
                          <button
                            className="reply-action"
                            disabled={retry.disabled}
                            title="この返事を作り直す(今のモデル・設定で。この返事は消える)"
                            onClick={retry.onRegenerate}
                          >
                            <LuRefreshCw />
                          </button>
                        )}
                      </>
                    )}
                    excluded={!!it.excluded}
                  />
                );
            }
          })}
        </div>
      </div>
      {userCount > 0 && (
        <button
          className={"questions-toggle" + (questions.on ? " on" : "")}
          title={questions.on ? "返事も表示する(元の位置に戻る。Esc)" : "質問だけを表示する(選んだ質問へ飛べる)"}
          onClick={() => (questions.on ? questions.exit() : questions.enter())}
        >
          <LuListFilter />
        </button>
      )}
    </div>
  );
}

// PinnedQuestion は、上に隠れた質問の写し(右上に止めておく)。長い質問は数行で切る。押すと元の吹き出しまで戻る
function PinnedQuestion({ index, text }: { index: number; text: string }) {
  const jump = (e: MouseEvent<HTMLButtonElement>) => {
    const chat = e.currentTarget.closest(".chat");
    chat?.querySelector(`[${QUESTION_ATTR}="${index}"]`)?.scrollIntoView({ block: "start", behavior: "smooth" });
  };
  return (
    <div className="pinned-question-anchor">
      <button className="pinned-question" title="この返事の質問。押すと質問まで戻る" onClick={jump}>
        {text}
      </button>
    </div>
  );
}

function AssistantMessage({ view, msgId, faded, onStop, onAnswer, excludeButton, excluded }: {
  view: AssistantView;
  msgId?: string; // 保存済みの返事の最初の発言の ID(詳細に出す)
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
      {view.compactNote && <CompactNote note={view.compactNote} draft={view.compactDraft} />}
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
        <ReplyActions content={answer} stats={view.stats} createdAt={view.createdAt} msgId={msgId}>{excludeButton}</ReplyActions>
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
// msgId は返事の最初の発言の ID(詳細に出す。ログや DB を調べるときの手がかり。ツールを使った返事は、この ID から後ろの発言がまとまっている)
function ReplyActions({ content, stats, createdAt, msgId, children }: {
  content: string; stats?: string; createdAt?: string; msgId?: string; children?: ReactNode;
}) {
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
      {open && stats && (
        <div className="stats">
          {stats}
          {msgId && (
            <div className="stats-id" title="発言の ID(UUIDv7)。DB の messages の id と同じ値">
              ID: {msgId} <CopyButton text={msgId} />
            </div>
          )}
        </div>
      )}
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

// EditUser は最後の発言を直す欄(0011)。「送り直す」で、この発言と返事を消して直した文で送る
function EditUser({ initial, disabled, onCancel, onSend }: {
  initial: string;
  disabled: boolean;
  onCancel(): void;
  onSend(text: string): void;
}) {
  const [text, setText] = useState(initial);
  return (
    <div className="user-turn">
      <div className="msg user editing">
        <textarea value={text} rows={Math.min(10, Math.max(2, text.split("\n").length))} autoFocus onChange={e => setText(e.target.value)} />
      </div>
      <div className="edit-actions">
        <button onClick={onCancel}>やめる</button>
        <button className="primary" disabled={disabled || !text.trim()} onClick={() => onSend(text.trim())}>送り直す</button>
      </div>
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
// 要約中の箱。思考と同じく畳んで出し、「古い発言を要約しています…」を押したときだけ要約の文を見せる(ふだんは要約の中身まで読まないため)。
// 開いているときは、続きが見えるように下まで送る(開いたときは一番下から見せる)
function Summarizing({ text }: { text: string }) {
  const [open, setOpen] = useState(false);
  const box = useStickToBottom<HTMLDivElement>(text, open);
  return (
    <details className="summarizing" onToggle={e => setOpen(e.currentTarget.open)}>
      <summary title="押すと、作っている要約の文を見る">古い発言を要約しています…</summary>
      {text && <div className="summarizing-text" ref={box}>{text}</div>}
    </details>
  );
}

// CompactNote は、要約できなかったお知らせ。返事は truncate で続けて作るので、エラー(赤)ではなく注意として出す。
// 文は Go が「何が起きたか・代わりに何をしたか・どうすればよいか」を書く。途中まで書いた要約があれば、畳んで見られるようにする
function CompactNote({ note, draft }: { note: string; draft?: string }) {
  return (
    <div className="compact-note">
      <LuTriangleAlert className="icon" />
      <div>
        {note}
        {draft && (
          <details>
            <summary>途中まで書いた要約を見る({draft.length.toLocaleString()} 字)</summary>
            <div className="summarizing-text">{draft}</div>
          </details>
        )}
      </div>
    </div>
  );
}

// 要約した位置の区切り。見出しに前後の大きさを添え、開くと要約の文(モデルに渡しているもの)と記録が読める
function Divider({ compaction }: { compaction: Compaction }) {
  const size = compactionSize(compaction);
  return (
    <details className="divider">
      <summary>― ここまでを要約しました{size && ` ${size}`} ―</summary>
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
