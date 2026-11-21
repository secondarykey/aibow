// ツールのカード: モデルがツールを呼んだ位置に出す。「思考の過程」と同じく畳んだ1行で、開くと引数と結果が読める。
// モデルが書いた <tool_call> の生の文字列は答えの欄には出さない(「モデルに渡した文字列」のパネルでは見える)。
//
// 書き換えるツール(write_file / edit_file)とコマンド(run_command)は、実行する前に確認を待つ。待っている間はカードを開いたままにし、
// 何をするか(パス・上書きか)と差分(```diff。+ は緑、- は赤)、コマンドならコマンドそのもの、理由の欄と「拒否」「実行」を出す。
// 「今後も同じ答えにする」は、しない・この返事の間・この会話の間から選ぶ(0051 の決定事項 10・11。0049 ではチェック1つだった)。
// この会話の間は、拒否なら「今後許可しない」(この会話ではモデルから隠す)、実行なら「この会話では今後聞かない」(0010)。
// この返事の間は、この返事が終わるまで聞かずに実行する・断る(会話の設定には書かない。次の送信ではまた聞く)。
// MCP のサーバーのツールは中身が見えないので、全部確認する。差分の代わりに、サーバーに渡す引数(JSON)を出す。
// MCP のツールは、名前の前にサーバーの名前を小さく出す(モデルに見せる名前は「サーバーの名前__ツールの名前」なので、前の部分は外して出す)。
// 答えると Go の待っているループに届き、受け取られたら確認を閉じて「実行中… N秒」にする(0049)。結果が届いたら、ほかのカードと同じく畳んだ1行に戻る。
// 作業の予定(update_plan。0037)は、引数と結果ではなく、予定のチェックリストとして出す(PlanCard)。
import { useEffect, useState } from "react";
import { LuBan, LuCheck, LuFolder, LuGitBranch, LuHourglass, LuLoaderCircle, LuWrench, LuX } from "react-icons/lu";
import { ToolPolicy } from "../../bindings/github.com/secondarykey/aibow/config";
import type { ConfirmRequest } from "../../bindings/github.com/secondarykey/aibow";
import type { ToolView } from "../transcript";
import { CodeBlock } from "./CodeBlock";
import { TOOL_LABELS } from "../toolLabels";
import { PlanCard, planOf } from "./PlanCard";

interface Props {
  tool: ToolView;
  generating: boolean;
  // 戻り値が false なら、Go が答えを受け取らなかった(確認のカードのままにして、もう一度答えられるようにする)
  onAnswer(reqId: string, approved: boolean, reason: string, remember: ToolPolicy, forReply: boolean): void | Promise<boolean>;
}

export function ToolCard({ tool, generating, onAnswer }: Props) {
  const plan = planOf(tool);
  if (plan) return <PlanCard items={plan} />;
  // 結果がまだ無いのは実行中(停止や上限で実行しなかったときも、結果は「エラー: …」として届く)
  const running = tool.result === undefined;
  const rejected = tool.decision?.approved === false;
  const state = tool.confirm ? "confirm" : running ? "running" : rejected ? "rejected" : tool.error ? "error" : "ok";
  const sec = useElapsed(state === "running" && generating);
  const server = tool.server || tool.confirm?.preview.server;
  // MCP のツールの名前は「サーバーの名前__ツールの名前」(名前空間の代わり)。サーバーの名前は枠で出すので、名前からは外す
  const prefix = server ? [...server].map(c => /[A-Za-z0-9_-]/.test(c) ? c : "_").join("") + "__" : "";
  const name = prefix && tool.name.startsWith(prefix) ? tool.name.slice(prefix.length) : tool.name;
  return (
    // 確認を待っている間だけ開いたままにする(open を外すと、ふだんの畳んだ1行に戻る)
    <details className={`tool-card ${state}`} open={tool.confirm ? true : undefined}>
      <summary>
        <LuWrench className="tool-icon" />
        {server && <span className="tool-server">{server}</span>}
        <code title={server ? `MCP のサーバー ${server} のツール(モデルに見せる名前: ${tool.name})` : TOOL_LABELS[tool.name]}>{name || "(読み取れない呼び出し)"}</code>
        {state === "confirm" && <span className="tool-status"><LuHourglass /> 確認待ち</span>}
        {state === "running" && generating && <span className="tool-status"><LuLoaderCircle className="spin" /> 実行中… {sec}秒</span>}
        {state === "ok" && <span className="tool-status"><LuCheck /> {tool.sec?.toFixed(2)}秒</span>}
        {state === "rejected" && <span className="tool-status"><LuBan /> {rejectedLabel(tool)}</span>}
        {state === "ok" && tool.decision?.remember === ToolPolicy.ToolAllow && (
          tool.decision.for_reply
            ? <span className="tool-tag" title="このツールは、この返事が終わるまで確認せずに実行する">この返事の間は聞かない</span>
            : <span className="tool-tag" title="このツールは、この会話では今後確認せずに実行する">この会話では今後聞かない</span>
        )}
        {state === "error" && <span className="tool-status"><LuX /> エラー</span>}
        {tool.elided && (
          <span className="tool-tag" title="会話の上限に収めるため、モデルにはこの結果を省いて、短い一文だけを渡しています。ここに出ている結果は保存したままです">省略して渡した</span>
        )}
      </summary>
      <div className="tool-body">
        {tool.confirm ? (
          // key: 1つの呼び出しで確認が2回来る(呼ぶ前の確認と、MCP のサーバーからの確認。0007)ので、確認ごとに作り直して「答えた」の状態を持ち越さない
          <Confirm key={tool.confirm.id} req={tool.confirm} onAnswer={onAnswer} />
        ) : tool.raw ? (
          <>
            <div className="tool-label">モデルが書いた呼び出し</div>
            <pre>{tool.raw}</pre>
          </>
        ) : (
          <>
            <div className="tool-label">引数</div>
            {tool.args.length === 0 ? <pre className="tool-none">(なし)</pre> : (
              <pre>{tool.args.map(a => `${a.name}: ${a.value}`).join("\n")}</pre>
            )}
          </>
        )}
        {tool.notice && (
          // アプリが実行の前にしたこと(worktree を作った、など)。モデルには渡していない
          <p className="tool-notice"><LuGitBranch /> {tool.notice}</p>
        )}
        {!running && (
          <>
            <div className="tool-label">結果</div>
            <pre>{tool.result}</pre>
          </>
        )}
      </div>
    </details>
  );
}

// Confirm は確認の中身: 何をするか・差分・理由の欄と答えのボタン
// useElapsed は、active になってからの秒数(1秒ごとに増える)。実行中のカードの「実行中… N秒」に使う(0049 の決定事項 8)。
// Go に新しい知らせは足さず、画面で数える。確認したツールは答えて確認を閉じた時から、確認の無いツールはカードが実行中で出た時から数える
// (別の会話を開いて戻ると、カードを作り直すので 0 から数え直す)
function useElapsed(active: boolean): number {
  const [sec, setSec] = useState(0);
  useEffect(() => {
    if (!active) return;
    const start = performance.now();
    setSec(0);
    const timer = window.setInterval(() => setSec(Math.floor((performance.now() - start) / 1000)), 1000);
    return () => window.clearInterval(timer);
  }, [active]);
  return sec;
}

// rejectedLabel は、拒否したカードの1行目に出す言葉(どの範囲で答えたか)
function rejectedLabel(tool: ToolView): string {
  if (tool.decision?.remember !== ToolPolicy.ToolOff) return "拒否";
  return tool.decision.for_reply ? "この返事の間は拒否" : "今後許可しない";
}

// Scope は「今後も同じ答えにする」の範囲(0051 の決定事項 10)。初めは「しない」
type Scope = "none" | "reply" | "conversation";
const SCOPES: { id: Scope; label: string; title: string }[] = [
  { id: "none", label: "しない", title: "この1回だけ答えます" },
  { id: "reply", label: "この返事の間", title: "「実行」なら、この返事が終わるまで、このツールは確認せずに実行します。\n「拒否」なら、この返事が終わるまで、確認せずに断ります。次の送信では、また確認します" },
  { id: "conversation", label: "この会話の間", title: "「実行」なら、この会話ではこのツールを今後は確認せずに実行します。\n「拒否」なら、この会話ではこのツールをモデルに見せません" },
];

// 確認の答え方(0049 の決定事項 4〜7):
//   1行目 = 拒否する側(理由の欄と「拒否」)、2行目 = 実行する側(「今後も同じ答えにする」の範囲と「実行」)。
//   「今後も同じ」は1回押しただけでは決まらないよう、範囲を選ぶのとボタンの2つの操作にした。範囲は拒否にも実行にも効く。
//   「拒否」と「実行」は隣り合わせない。Enter だけで実行されないよう、フォーカスは「実行」に置かない
function Confirm({ req, onAnswer }: { req: ConfirmRequest; onAnswer: Props["onAnswer"] }) {
  const [reason, setReason] = useState("");
  const [scope, setScope] = useState<Scope>("none");
  // 答えてから確認が閉じるまでの間は押せなくする(2回押しても、Go は1回目しか受け取らない)。受け取られなかったら戻す
  const [sent, setSent] = useState(false);
  const answer = (approved: boolean) => {
    setSent(true);
    const policy = scope === "none" ? ToolPolicy.$zero : approved ? ToolPolicy.ToolAllow : ToolPolicy.ToolOff;
    Promise.resolve(onAnswer(req.id, approved, approved ? "" : reason, policy, scope === "reply")).then(ok => {
      if (ok === false) setSent(false);
    });
  };
  const pv = req.preview;
  return (
    <div className="confirm">
      <div className="confirm-target">
        {/* ワークスペースのフォルダが複数のとき(0035)は、どのフォルダかを先に出す(メインでも) */}
        {pv.folder && <span className="confirm-folder" title="ワークスペースのフォルダ"><LuFolder /> {pv.folder}</span>}
        {pv.path && <code>{pv.path}</code>}
        {pv.server && <code>{pv.server}</code>}
        {/* URL を読む(fetch_url。0036)は、どこへ取りに行くか(ホスト)を先に出す */}
        {pv.url && <code className="confirm-host">{hostOf(pv.url)}</code>}
        <span className={`confirm-note ${pv.note === "上書き" ? "overwrite" : ""}`}>{pv.note}</span>
      </div>
      {pv.url
        ? <p className="confirm-url"><code>{pv.url}</code></p>
        : pv.command
        ? <CodeBlock code={pv.command} lang="" closed />
        : pv.diff // MCP のサーバー(aibow-mcp)が送ってきた差分も、組み込みと同じに出す
          ? <CodeBlock code={pv.diff.replace(/\n$/, "")} lang="diff" closed />
          : pv.args
          ? <CodeBlock code={pv.args} lang="json" closed /> // MCP のツールと、読むだけのツールを「確認する」にしたときは引数を見せる
          : <p className="tool-none">(大きいため差分は出していません)</p>}
      <div className="confirm-actions">
        <div className="confirm-row">
          <input
            type="text"
            placeholder="拒否の理由(モデルに伝えます。空でも可)"
            title="「拒否」を押したとき、モデルに「ユーザーが実行を拒否しました。理由: …」として伝えます。「実行」では使いません"
            value={reason}
            disabled={sent}
            onChange={e => setReason(e.target.value)}
          />
          <button className="confirm-reject" disabled={sent} onClick={() => answer(false)}>拒否</button>
        </div>
        <div className="confirm-row">
          <div className="confirm-remember" role="radiogroup" aria-label="今後も同じ答えにする">
            <span>今後も同じ答えにする:</span>
            {SCOPES.map(s => (
              <label key={s.id} title={s.title}>
                <input type="radio" name={`scope-${req.id}`} checked={scope === s.id} disabled={sent} onChange={() => setScope(s.id)} />
                {s.label}
              </label>
            ))}
          </div>
          <button className="primary" disabled={sent} onClick={() => answer(true)}>実行</button>
        </div>
      </div>
    </div>
  );
}

// hostOf は URL のホスト(読めなければ URL のまま)。
function hostOf(url: string): string {
  try {
    return new URL(url).host;
  } catch {
    return url;
  }
}
