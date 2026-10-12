// コードブロックの色付け(highlight.js)。よく使う 37 言語(lib/common)に対応する。
// 起動を遅くしないよう、最初に言語付きのコードブロックが出たときに読み込む(約 150KB。exe の中から読むので通信はしない)。
// 言語が書かれていない・知らない言語のときは色を付けない(言語の推測は遅く、よく外れるので使わない)
import type { HLJSApi } from "highlight.js";

let hljs: HLJSApi | null = null;
let loading: Promise<HLJSApi> | null = null;

// 読み込み済みなら色付けした HTML を返す。まだなら null(読み込みは loadHighlighter で始める)。
// highlight.js はコードの文字をエスケープしてから <span class="hljs-…"> で囲むので、そのまま HTML として入れてよい
export function highlight(code: string, lang: string): string | null {
  if (!hljs || !hljs.getLanguage(lang)) return null;
  return hljs.highlight(code, { language: lang, ignoreIllegals: true }).value;
}

export function isHighlighterLoaded(): boolean {
  return hljs !== null;
}

export function loadHighlighter(): Promise<HLJSApi> {
  loading ??= import("highlight.js/lib/common").then(m => (hljs = m.default));
  return loading;
}
