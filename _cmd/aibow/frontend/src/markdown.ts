// 返事の Markdown を HTML にする設定(marked)。
// Markdown の解析そのものは学習の対象ではないので、ライブラリ(marked)に任せる。
// ここで決めているのは「モデルが書いた文字列を、安全に画面に出す」ための差し替えだけ。
//
// marked は文章の中の HTML やリンク先をそのまま通す。モデルの出力は信用できない入力として扱い、
//   - HTML(<img onerror=…> など)は、タグとして解釈させず文字として見せる
//   - リンクは http / https だけを <a> にし、それ以外(javascript: や相対パスなど)は文字にする
//   - 画像は読み込まない(外のサーバーに勝手に取りに行かない)。http / https ならリンクとして出す
// それ以外の文字は marked がエスケープしてから HTML にする。
import { Marked, type Token, type TokensList, type Tokens } from "marked";

const md = new Marked({
  gfm: true, // 表・打ち消し線・URL の自動リンク
  breaks: true, // 1つの改行も改行として見せる(今までの表示と同じ。日本語の返事は1行ずつ改行することが多い)
  async: false,
});

md.use({
  renderer: {
    html({ text }: Tokens.HTML | Tokens.Tag) {
      return escapeHtml(text);
    },
    link(token: Tokens.Link) {
      if (!isWebURL(token.href)) return this.parser.parseInline(token.tokens);
      return false; // false を返すと marked の標準の出力(<a href=…>)になる
    },
    image({ href, text }: Tokens.Image) {
      const label = escapeHtml(text || href);
      if (!isWebURL(href)) return label;
      return `<a href="${escapeHtml(href)}" title="画像: ${escapeHtml(href)}">${label}</a>`;
    },
  },
});

// Markdown をブロック(段落・見出し・コードブロックなど)に分ける。
// コードブロックだけは画面の側(コピーボタン付き)で出したいので、HTML にする前の形で返す
export function lex(text: string): TokensList {
  return md.lexer(text);
}

// コードブロック以外のブロックの並びを HTML にする
export function toHTML(tokens: Token[]): string {
  return md.parser(tokens);
}

// ふだんのブラウザで開いてよいリンクか。アプリのウィンドウの中で開くと画面が別のページに移ってしまうので、
// 開くのは http / https だけにして、押したときは RenderedText が OS のブラウザに渡す
export function isWebURL(href: string): boolean {
  try {
    const url = new URL(href);
    return url.protocol === "http:" || url.protocol === "https:";
  } catch {
    return false; // 相対パスなど、URL として読めないもの
  }
}

function escapeHtml(s: string): string {
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;").replace(/'/g, "&#39;");
}
