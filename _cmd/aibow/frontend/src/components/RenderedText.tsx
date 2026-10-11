// ``` で囲まれた部分だけコードブロックにする簡易レンダラー。
// React が文字列をエスケープしてくれるので、HTML として解釈される心配はない。
export function RenderedText({ text }: { text: string }) {
  const parts = text.split("```");
  return (
    <>
      {parts.map((part, i) => {
        if (i % 2 === 1) {
          const code = part.replace(/^[^\n]*\n/, ""); // 1行目の言語名を落とす
          return <pre key={i}><code>{code}</code></pre>;
        }
        return part.trim() ? <p key={i}>{part.replace(/^\n+|\n+$/g, "")}</p> : null;
      })}
    </>
  );
}
