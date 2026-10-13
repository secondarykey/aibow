// ツールのカード: モデルがツールを呼んだ位置に出す。「思考の過程」と同じく畳んだ1行で、開くと引数と結果が読める。
// モデルが書いた <tool_call> の生の文字列は答えの欄には出さない(「モデルに渡した文字列」のパネルでは見える)。
//
// 書き換えるツール(write_file / edit_file)とコマンド(run_command)は、実行する前に確認を待つ。待っている間はカードを開いたままにし、
// 何をするか(パス・上書きか)と差分(```diff。+ は緑、- は赤)、コマンドならコマンドそのもの、理由の欄と「拒否」「実行する」を出す。
// MCP のサーバーのツールは中身が見えないので、全部確認する。差分の代わりに、サーバーに渡す引数(JSON)を出す。
// MCP のツールは、名前の前にサーバーの名前を小さく出す(モデルに見せる名前は「サーバーの名前__ツールの名前」なので、前の部分は外して出す)。
// 答えると Go の待っているループに届き、実行(か拒否)の結果が届いたら、ほかのカードと同じく畳んだ1行に戻る。
import { useState } from "react";
import { LuBan, LuCheck, LuHourglass, LuLoaderCircle, LuWrench, LuX } from "react-icons/lu";
import type { ConfirmRequest } from "../../bindings/aibow";
import type { ToolView } from "../transcript";
import { CodeBlock } from "./CodeBlock";
import { TOOL_LABELS } from "../toolLabels";

interface Props {
  tool: ToolView;
  generating: boolean;
  onAnswer(reqId: string, approved: boolean, reason: string): void;
}

export function ToolCard({ tool, generating, onAnswer }: Props) {
  // 結果がまだ無いのは実行中(停止や上限で実行しなかったときも、結果は「エラー: …」として届く)
  const running = tool.result === undefined;
  const rejected = tool.decision?.approved === false;
  const state = tool.confirm ? "confirm" : running ? "running" : rejected ? "rejected" : tool.error ? "error" : "ok";
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
        {state === "running" && generating && <span className="tool-status"><LuLoaderCircle className="spin" /> 実行中…</span>}
        {state === "ok" && <span className="tool-status"><LuCheck /> {tool.sec?.toFixed(2)}秒</span>}
        {state === "rejected" && <span className="tool-status"><LuBan /> 拒否</span>}
        {state === "error" && <span className="tool-status"><LuX /> エラー</span>}
      </summary>
      <div className="tool-body">
        {tool.confirm ? (
          <Confirm req={tool.confirm} onAnswer={onAnswer} />
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
function Confirm({ req, onAnswer }: { req: ConfirmRequest; onAnswer: Props["onAnswer"] }) {
  const [reason, setReason] = useState("");
  // 答えてから結果が届くまでの間は押せなくする(2回押しても、Go は1回目しか受け取らない)
  const [sent, setSent] = useState(false);
  const answer = (approved: boolean) => {
    setSent(true);
    onAnswer(req.id, approved, approved ? "" : reason);
  };
  const pv = req.preview;
  return (
    <div className="confirm">
      <div className="confirm-target">
        {pv.path && <code>{pv.path}</code>}
        {pv.server && <code>{pv.server}</code>}
        <span className={`confirm-note ${pv.note === "上書き" ? "overwrite" : ""}`}>{pv.note}</span>
      </div>
      {pv.command
        ? <CodeBlock code={pv.command} lang="" closed />
        : pv.server || pv.args
          ? <CodeBlock code={pv.args ?? "{}"} lang="json" closed /> // MCP のツールと、読むだけのツールを「確認する」にしたときは引数を見せる
          : pv.diff
          ? <CodeBlock code={pv.diff.replace(/\n$/, "")} lang="diff" closed />
          : <p className="tool-none">(大きいため差分は出していません)</p>}
      <div className="confirm-actions">
        <input
          type="text"
          placeholder="理由"
          title="拒否するときの理由。モデルに「ユーザーが実行を拒否しました。理由: …」として渡す(空でもよい)"
          value={reason}
          disabled={sent}
          onChange={e => setReason(e.target.value)}
        />
        <button disabled={sent} onClick={() => answer(false)}>拒否</button>
        <button className="primary" disabled={sent} onClick={() => answer(true)}>実行する</button>
      </div>
    </div>
  );
}
