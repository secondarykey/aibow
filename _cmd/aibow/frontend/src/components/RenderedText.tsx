// 返事(と要約)の文を Markdown として表示する。HTML にするのは marked(設定と安全のための差し替えは markdown.ts)。
// 文をブロックに分け、コードブロックだけは React で組み立てる(コピーボタン・色付け・mermaid の図。CodeBlock.tsx)。
// それ以外のブロックは、続いている分をまとめて marked で HTML にする。
//
// リンクを押したときは、アプリのウィンドウの中では開かず、OS のふだんのブラウザで開く
// (ウィンドウの中で開くと画面がそのページに移り、チャットに戻れなくなる)。
// 生成中は届くたびに全体を解析し直す。閉じていない書式(**生成 など)は、閉じる記号が届くまで記号のまま見える
import { useMemo } from "react";
import type { MouseEvent } from "react";
import { Browser } from "@wailsio/runtime";
import type { Token, Tokens } from "marked";
import { CodeBlock } from "./CodeBlock";
import { isWebURL, lex, toHTML } from "../markdown";

type Block = { kind: "code"; code: string; lang: string; closed: boolean } | { kind: "html"; html: string };

export function RenderedText({ text }: { text: string }) {
  const blocks = useMemo(() => toBlocks(text), [text]);
  if (!text.trim()) return null;
  return (
    <div className="md" onClick={openLinkInBrowser}>
      {blocks.map((b, i) =>
        b.kind === "code" ? (
          <CodeBlock key={i} code={b.code} lang={b.lang} closed={b.closed} />
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
      const code = token as Tokens.Code;
      blocks.push({ kind: "code", code: code.text, lang: (code.lang ?? "").split(/\s/)[0].toLowerCase(), closed: isClosed(code) });
    } else {
      pending.push(token);
    }
  }
  flush();
  return blocks;
}

// コードブロックが閉じる記号(``` / ~~~)まで届いているか。生成中は途中で切れていることがある。
// 字下げ(4つの空白)のコードブロックには閉じる記号が無いので、いつも閉じているものとして扱う
function isClosed(code: Tokens.Code): boolean {
  const fence = /^ {0,3}(`{3,}|~{3,})/.exec(code.raw)?.[1];
  if (!fence) return true;
  const lines = code.raw.trimEnd().split("\n");
  const closing = new RegExp(`^ {0,3}${fence[0] === "`" ? "`" : "~"}{${fence.length},}\\s*$`);
  return lines.length > 1 && closing.test(lines[lines.length - 1]);
}

function openLinkInBrowser(e: MouseEvent<HTMLDivElement>) {
  const a = (e.target as HTMLElement).closest("a");
  if (!a) return;
  e.preventDefault();
  const href = a.getAttribute("href") ?? "";
  if (!isWebURL(href)) return;
  Browser.OpenURL(href).catch(err => console.error("リンクをブラウザで開けませんでした:", href, err));
}
