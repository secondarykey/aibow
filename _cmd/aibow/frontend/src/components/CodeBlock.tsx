// 返事の中のコードブロック。右上にそのコードだけをコピーするボタンを出す。
//   - 言語が書かれていれば、highlight.js で色を付ける(highlight.ts)
//   - 言語が mermaid なら図にする(mermaid.ts)。図とコードは切り替えられる
// 生成中はコードブロックが途中のことがあるので、図にするのは閉じる記号(```)が届いてから。
// それまでと、図の書き方が間違っていたときはコードのまま見せる
import { useEffect, useState } from "react";
import { CopyButton } from "./CopyButton";
import { highlight, isHighlighterLoaded, loadHighlighter } from "../highlight";
import { renderMermaid, usePrefersDark } from "../mermaid";

interface Props {
  code: string;
  lang: string;   // ``` の後ろの最初の語(無ければ "")
  closed: boolean; // 閉じる記号まで届いているか
}

export function CodeBlock({ code, lang, closed }: Props) {
  if (lang === "mermaid" && closed) return <MermaidBlock code={code} />;
  return (
    <div className="code-block">
      <Code code={code} lang={lang} />
      <CopyButton text={code} className="code-copy" />
    </div>
  );
}

function Code({ code, lang }: { code: string; lang: string }) {
  // 色付けの読み込みが終わったら描き直す(読み込みは最初の1回だけ)
  const [, setLoaded] = useState(isHighlighterLoaded());
  useEffect(() => {
    if (lang && !isHighlighterLoaded()) {
      loadHighlighter().then(() => setLoaded(true)).catch(err => console.error("色付けを読み込めませんでした:", err));
    }
  }, [lang]);
  const html = lang ? highlight(code, lang) : null;
  if (html === null) return <pre><code>{code}</code></pre>;
  return <pre><code className="hljs" dangerouslySetInnerHTML={{ __html: html }} /></pre>;
}

type Diagram = { state: "drawing" } | { state: "done"; svg: string } | { state: "error"; message: string };

function MermaidBlock({ code }: { code: string }) {
  const dark = usePrefersDark();
  const [diagram, setDiagram] = useState<Diagram>({ state: "drawing" });
  const [showCode, setShowCode] = useState(false);

  useEffect(() => {
    let alive = true; // 描いている間に別の図に変わったら、古い結果は捨てる
    setDiagram({ state: "drawing" });
    renderMermaid(code, dark)
      .then(svg => alive && setDiagram({ state: "done", svg }))
      .catch(err => alive && setDiagram({ state: "error", message: err instanceof Error ? err.message : String(err) }));
    return () => { alive = false; };
  }, [code, dark]);

  const codeView = <pre><code>{code}</code></pre>;
  return (
    <div className="mermaid-block">
      <div className="mermaid-tools">
        {diagram.state === "done" && (
          <button className="reply-action" onClick={() => setShowCode(v => !v)}>{showCode ? "図" : "コード"}</button>
        )}
        <CopyButton text={code} />
      </div>
      {diagram.state === "drawing" && <><p className="mermaid-note">図を描いています…</p>{codeView}</>}
      {diagram.state === "error" && <><p className="mermaid-note error">図にできませんでした: {diagram.message}</p>{codeView}</>}
      {diagram.state === "done" && (showCode
        ? codeView
        // mermaid が securityLevel: "strict" で作った SVG(ラベルの HTML は mermaid が無害にしてある)
        : <div className="mermaid-svg" dangerouslySetInnerHTML={{ __html: diagram.svg }} />)}
    </div>
  );
}
