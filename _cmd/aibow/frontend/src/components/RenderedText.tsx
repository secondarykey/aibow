// 返事(と要約)の文を Markdown として表示する。HTML にするのは marked(設定と安全のための差し替えは markdown.ts)。
// 文をブロックに分け、コードブロックだけは右上にそのコードだけをコピーするボタンを出すため React で組み立てる。
// それ以外のブロックは、続いている分をまとめて marked で HTML にする。
//
// リンクを押したときは、アプリのウィンドウの中では開かず、OS のふだんのブラウザで開く
// (ウィンドウの中で開くと画面がそのページに移り、チャットに戻れなくなる)。
// 生成中は届くたびに全体を解析し直す。閉じていない書式(**生成 など)は、閉じる記号が届くまで記号のまま見える
import { useMemo } from "react";
import type { MouseEvent } from "react";
import { Browser } from "@wailsio/runtime";
import type { Token, Tokens } from "marked";
import { CopyButton } from "./CopyButton";
import { isWebURL, lex, toHTML } from "../markdown";

type Block = { kind: "code"; code: string } | { kind: "html"; html: string };

export function RenderedText({ text }: { text: string }) {
  const blocks = useMemo(() => toBlocks(text), [text]);
  if (!text.trim()) return null;
  return (
    <div className="md" onClick={openLinkInBrowser}>
      {blocks.map((b, i) =>
        b.kind === "code" ? (
          <div key={i} className="code-block">
            <pre><code>{b.code}</code></pre>
            <CopyButton text={b.code} className="code-copy" />
          </div>
        ) : (
          // markdown.ts で HTML とリンク先を無害にしてあるので、そのまま入れてよい
          <div key={i} dangerouslySetInnerHTML={{ __html: b.html }} />
        ),
      )}
    </div>
  );
}

// コードブロックとそれ以外に分ける。それ以外は、続いている分をまとめて1つの HTML にする
// (段落や箇条書きを1つずつ別の div にすると、余白の付き方がずれるため)
function toBlocks(text: string): Block[] {
  const blocks: Block[] = [];
  let pending: Token[] = [];
  const flush = () => {
    if (pending.length) blocks.push({ kind: "html", html: toHTML(pending) });
    pending = [];
  };
  for (const token of lex(text)) {
    if (token.type === "code") {
      flush();
      blocks.push({ kind: "code", code: (token as Tokens.Code).text });
    } else {
      pending.push(token);
    }
  }
  flush();
  return blocks;
}

function openLinkInBrowser(e: MouseEvent<HTMLDivElement>) {
  const a = (e.target as HTMLElement).closest("a");
  if (!a) return;
  e.preventDefault();
  const href = a.getAttribute("href") ?? "";
  if (!isWebURL(href)) return;
  Browser.OpenURL(href).catch(err => console.error("リンクをブラウザで開けませんでした:", href, err));
}
