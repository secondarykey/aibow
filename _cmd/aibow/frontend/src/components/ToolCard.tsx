// ツールのカード: モデルがツールを呼んだ位置に出す。「思考の過程」と同じく畳んだ1行で、開くと引数と結果が読める。
// モデルが書いた <tool_call> の生の文字列は答えの欄には出さない(「モデルに渡した文字列」のパネルでは見える)。
import { LuCheck, LuLoaderCircle, LuWrench, LuX } from "react-icons/lu";
import type { ToolView } from "../transcript";

// 組み込みのツールの日本語の説明(モデルに見せる説明は英語。カードの名前にマウスを乗せると出す)
const TOOL_LABELS: Record<string, string> = {
  get_current_time: "現在の日時を調べる",
  calculate: "式を計算する",
};

export function ToolCard({ tool, generating }: { tool: ToolView; generating: boolean }) {
  // 結果がまだ無いのは実行中(停止や上限で実行しなかったときも、結果は「エラー: …」として届く)
  const running = tool.result === undefined;
  const state = running ? "running" : tool.error ? "error" : "ok";
  return (
    <details className={`tool-card ${state}`}>
      <summary>
        <LuWrench className="tool-icon" />
        <code title={TOOL_LABELS[tool.name]}>{tool.name || "(読み取れない呼び出し)"}</code>
        {running && generating && <span className="tool-status"><LuLoaderCircle className="spin" /> 実行中…</span>}
        {state === "ok" && <span className="tool-status"><LuCheck /> {tool.sec?.toFixed(2)}秒</span>}
        {state === "error" && <span className="tool-status"><LuX /> エラー</span>}
      </summary>
      <div className="tool-body">
        {tool.raw ? (
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
