// 返事の中のコードブロック。右上にそのコードだけをコピーするボタンを出す。
//   - 言語が書かれていれば、highlight.js で色を付ける(highlight.ts)
//   - 言語が mermaid なら図にする(mermaid.ts)。図とコードは切り替えられる
// 図とコードの切り替えは、コードのアイコンのトグル(押している間はコードを見せる)。図は SVG のファイルに保存できる。
// 生成中はコードブロックが途中のことがあるので、図にするのは閉じる記号(```)が届いてから。
// それまでと、図の書き方が間違っていたときはコードのまま見せる
import { useEffect, useState } from "react";
import { LuCode, LuDownload } from "react-icons/lu";
import { CopyButton } from "./CopyButton";
import { highlight, isHighlighterLoaded, loadHighlighter } from "../highlight";
import { downloadSVG, renderMermaid, usePrefersDark } from "../mermaid";

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

// 保存する図に敷く背景。図は返事の吹き出しの上に出ているので、吹き出しの背景(テーマの --panel)にする
function panelBackground(): string {
  return getComputedStyle(document.documentElement).getPropertyValue("--panel").trim() || "#ffffff";
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
          <button
            className={"reply-action code-toggle" + (showCode ? " on" : "")}
            onClick={() => setShowCode(v => !v)}
            aria-pressed={showCode}
            title={showCode ? "図に戻す" : "コードを表示"}
            aria-label={showCode ? "図に戻す" : "コードを表示"}
          >
            <LuCode />
          </button>
        )}
        {diagram.state === "done" && (
          <button
            className="reply-action"
            onClick={() => downloadSVG(diagram.svg, panelBackground())}
            title="SVG で保存"
            aria-label="SVG で保存"
          >
            <LuDownload />
          </button>
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
