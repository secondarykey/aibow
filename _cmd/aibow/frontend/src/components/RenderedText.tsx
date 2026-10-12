// ``` で囲まれた部分だけコードブロックにする簡易レンダラー。
// React が文字列をエスケープしてくれるので、HTML として解釈される心配はない。
// コードブロックの右上には、そのコードだけをコピーするボタンを出す。
import { CopyButton } from "./CopyButton";

export function RenderedText({ text }: { text: string }) {
  const parts = text.split("```");
  return (
    <>
      {parts.map((part, i) => {
        if (i % 2 === 1) {
          const code = part.replace(/^[^\n]*\n/, ""); // 1行目の言語名を落とす
          return (
            <div key={i} className="code-block">
              <pre><code>{code}</code></pre>
              <CopyButton text={code.replace(/\n$/, "")} className="code-copy" />
            </div>
          );
        }
        return part.trim() ? <p key={i}>{part.replace(/^\n+|\n+$/g, "")}</p> : null;
      })}
    </>
  );
}
