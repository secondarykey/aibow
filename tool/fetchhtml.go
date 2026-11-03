package tool

// fetchhtml.go: HTML のページから本文を取り出して、簡単な Markdown にする(fetch_url。0036 の決定事項 6・7)。
//
// 字句の読み取りと木の組み立て(閉じ忘れたタグの補い方など、ブラウザと同じ決まり)は golang.org/x/net/html に任せ、
// ここは「どこが本文か」と「Markdown にどう書くか」だけを書く。
//
//   - 落とすもの:script・style など読む文ではないものと、nav・header・footer・aside・form(どのページにもある案内やメニュー)
//   - 本文の場所:main があれば最初の main、無ければいちばん長い article、どちらも無ければ body 全体
//   - Markdown にするもの:見出し(#)・段落・リスト・リンク [文](URL)・コードブロック・表(行ごとに | で区切る)。強調や画像は文だけ・落とす
//
// 読みやすさの細かい工夫(広告の除き方・本文らしさの点数付け)は作らない。試して困ってから足す(決定事項 7)。

import (
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// skipped は、中身を読まない要素(決定事項 7)。
var skipped = map[atom.Atom]bool{
	atom.Script: true, atom.Style: true, atom.Noscript: true, atom.Template: true, atom.Svg: true, atom.Iframe: true,
	atom.Nav: true, atom.Header: true, atom.Footer: true, atom.Aside: true, atom.Form: true,
	atom.Head: true, atom.Button: true, atom.Select: true, atom.Img: true,
}

// htmlToMarkdown は、HTML の文書の本文を Markdown にする。base は相対のリンクを絶対の URL にするため。
func htmlToMarkdown(src string, base *url.URL) (string, error) {
	doc, err := html.Parse(strings.NewReader(src))
	if err != nil {
		return "", fmt.Errorf("HTML を読めません: %w", err)
	}
	w := &mdWriter{base: base}
	if title := strings.Join(strings.Fields(textOf(find(doc, atom.Title))), " "); title != "" {
		w.block()
		w.raw("# " + title)
		w.block()
	}
	w.children(contentRoot(doc))
	return strings.TrimSpace(w.out.String()), nil
}

// contentRoot は本文の場所(最初の main → いちばん長い article → body → 文書全体)。
func contentRoot(doc *html.Node) *html.Node {
	if m := find(doc, atom.Main); m != nil {
		return m
	}
	var best *html.Node
	longest := -1
	for n := range doc.Descendants() {
		if n.Type == html.ElementNode && n.DataAtom == atom.Article {
			if l := utf8.RuneCountInString(textOf(n)); l > longest {
				best, longest = n, l
			}
		}
	}
	if best != nil {
		return best
	}
	if b := find(doc, atom.Body); b != nil {
		return b
	}
	return doc
}

// find は、a の要素のうち文書の順で最初のもの(落とす要素の中は探さない)。
func find(n *html.Node, a atom.Atom) *html.Node {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode {
			continue
		}
		if c.DataAtom == a {
			return c
		}
		if skipped[c.DataAtom] && a != atom.Title {
			continue
		}
		if f := find(c, a); f != nil {
			return f
		}
	}
	return nil
}

// textOf は、要素の中の文(落とす要素の中は除く)。nil なら空。
func textOf(n *html.Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			switch {
			case c.Type == html.TextNode:
				b.WriteString(c.Data)
			case c.Type == html.ElementNode && skipped[c.DataAtom] && n.DataAtom != atom.Title:
			default:
				walk(c)
			}
		}
	}
	walk(n)
	return b.String()
}

// mdWriter は Markdown を書く係。HTML の空白(改行・連続した空白)は1つの空白にまとめ、
// 塊(段落・見出し・リスト)の間には、次に文字を書くときにまとめて改行を入れる(空の段落で空行が続かないように)。
type mdWriter struct {
	out    strings.Builder
	base   *url.URL
	sep    int    // 次の文字の前に入れる改行の数(1 は改行、2 は空行)
	space  bool   // 次の文字の前に空白が要る
	indent string // リストの中の行の頭(入れ子のリストで深くする)
}

// block は塊の区切り(空行)、line は行の区切り。
func (w *mdWriter) block() { w.sep = max(w.sep, 2) }
func (w *mdWriter) line()  { w.sep = max(w.sep, 1) }

// raw は、s をそのまま書く(前に要る改行・空白を入れてから)。
func (w *mdWriter) raw(s string) {
	if w.out.Len() == 0 {
		w.sep, w.space = 0, false
	}
	if w.sep > 0 {
		w.out.WriteString(strings.Repeat("\n", w.sep) + w.indent)
		w.sep, w.space = 0, false
	} else if w.space {
		w.out.WriteByte(' ')
		w.space = false
	}
	w.out.WriteString(s)
}

// text は HTML の文を書く(空白をまとめる)。
func (w *mdWriter) text(s string) {
	words := strings.Fields(s)
	if len(words) == 0 {
		if s != "" {
			w.space = true
		}
		return
	}
	if strings.TrimLeft(s, " \t\r\n\f") != s {
		w.space = true
	}
	w.raw(strings.Join(words, " "))
	if strings.TrimRight(s, " \t\r\n\f") != s {
		w.space = true
	}
}

func (w *mdWriter) children(n *html.Node) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		w.node(c)
	}
}

func (w *mdWriter) node(n *html.Node) {
	switch n.Type {
	case html.TextNode:
		w.text(n.Data)
		return
	case html.ElementNode:
	default:
		w.children(n) // 文書そのもの
		return
	}
	if skipped[n.DataAtom] {
		return
	}
	switch n.DataAtom {
	case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
		if t := w.inline(n); t != "" {
			w.block()
			w.raw(strings.Repeat("#", int(n.Data[1]-'0')) + " " + t)
			w.block()
		}
	case atom.P, atom.Blockquote, atom.Figure, atom.Details, atom.Dl:
		w.block()
		w.children(n)
		w.block()
	case atom.Div, atom.Section, atom.Article, atom.Main, atom.Figcaption, atom.Summary, atom.Dt, atom.Dd, atom.Address:
		w.line()
		w.children(n)
		w.line()
	case atom.Br:
		w.line()
	case atom.Hr:
		w.block()
		w.raw("---")
		w.block()
	case atom.Ul, atom.Ol:
		w.list(n)
	case atom.Pre:
		w.pre(n)
	case atom.Code, atom.Kbd, atom.Samp:
		if t := strings.Join(strings.Fields(textOf(n)), " "); t != "" {
			w.raw("`" + t + "`")
		}
	case atom.A:
		w.link(n)
	case atom.Table:
		w.table(n)
	default:
		w.children(n) // span・strong・em など:文だけ
	}
}

// list はリスト。項目ごとに「- 」(番号つきは「1. 」)を付け、入れ子のリストは字下げする。
func (w *mdWriter) list(n *html.Node) {
	if w.indent == "" {
		w.block()
	} else {
		w.line()
	}
	parent := w.indent
	i := 0
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode || c.DataAtom != atom.Li {
			continue
		}
		i++
		marker := "- "
		if n.DataAtom == atom.Ol {
			marker = fmt.Sprintf("%d. ", i)
		}
		w.indent = parent
		w.line()
		w.raw(marker)
		w.indent = parent + "  "
		w.children(c)
	}
	w.indent = parent
	if parent == "" {
		w.block()
	} else {
		w.line()
	}
}

// pre はコードブロック。中の空白と改行はそのまま。
func (w *mdWriter) pre(n *html.Node) {
	code := strings.Trim(textOf(n), "\n")
	if strings.TrimSpace(code) == "" {
		return
	}
	w.block()
	w.raw("```\n" + code + "\n```")
	w.block()
}

// link はリンク [文](URL)。URL は絶対にする(モデルが続けて fetch_url で読めるように)。
// ページの中への移動(#…)・javascript: は文だけにし、文の無いリンク(画像だけのリンク)は書かない。
func (w *mdWriter) link(n *html.Node) {
	href := ""
	for _, a := range n.Attr {
		if a.Key == "href" {
			href = strings.TrimSpace(a.Val)
		}
	}
	text := w.inline(n)
	if text == "" {
		return
	}
	u, err := url.Parse(href)
	if href == "" || strings.HasPrefix(href, "#") || err != nil || u.Scheme == "javascript" {
		w.raw(text)
		return
	}
	if w.base != nil {
		u = w.base.ResolveReference(u)
	}
	w.raw("[" + text + "](" + u.String() + ")")
}

// table は表。行ごとに | で区切る。最初の行が見出し(th)だけなら、Markdown の表の区切りの行を足す。
func (w *mdWriter) table(n *html.Node) {
	w.block()
	first := true
	for tr := range n.Descendants() {
		if tr.Type != html.ElementNode || tr.DataAtom != atom.Tr {
			continue
		}
		var cells []string
		header := true
		for c := tr.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode || c.DataAtom != atom.Td && c.DataAtom != atom.Th {
				continue
			}
			header = header && c.DataAtom == atom.Th
			cells = append(cells, strings.ReplaceAll(w.inline(c), "|", `\|`))
		}
		if len(cells) == 0 {
			continue
		}
		w.line()
		w.raw("| " + strings.Join(cells, " | ") + " |")
		if first && header {
			w.line()
			w.raw("|" + strings.Repeat(" --- |", len(cells)))
		}
		first = false
	}
	w.block()
}

// inline は、要素の中身を1行の Markdown にする(見出し・リンクの文・表の欄)。
func (w *mdWriter) inline(n *html.Node) string {
	sub := &mdWriter{base: w.base}
	sub.children(n)
	return strings.Join(strings.Fields(sub.out.String()), " ")
}
