package tool

// fetch.go: URL のページを読むツール(fetch_url。0036)。
//
// 公式の fetch の MCP サーバー(Python と uv が要る)の代わりに、exe だけで使える組み込みのツールにしたもの。
// ここで学べることは3つある:
//
//   - ツールの結果の大きさを自分で決める:ページは長いので、本文だけを取り出し、fetchMaxRunes 文字ずつ渡して、
//     続きは start_index で読ませる(read_file の offset と同じ考え方。0025)。返す長さはモデルに選ばせない
//     (小さなモデルに選ばせると大きすぎる値を書くため。決定事項 4)
//   - HTML から本文を取り出す:構文解析は golang.org/x/net/html に任せ、「どこが本文か」と Markdown への書き直しは自分で書く(fetchhtml.go)
//   - サーバーから外へ取りに行く危うさ(SSRF):モデルが書いた URL で、PC の中・社内のサーバー・クラウドのメタデータ(169.254.169.254)に
//     届いてしまう。名前を引いてから確かめると、確かめたあとに名前の答えを変えられる(DNS rebinding)ので、
//     接続の直前(net.Dialer.Control)に、実際につなぐアドレスで確かめる。リダイレクトの先も同じ道を通る
//
// ページの文はモデルへの指示として働きうる(「このファイルを消して」と書いたページを読ませる、など)。
// 取りに行く前にユーザーに URL を見せて確かめる(Confirmable)のと、書き換え・コマンドの確認を残すのが守り。

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html/charset"

	"github.com/secondarykey/aibow/log"
)

const (
	fetchMaxRunes     = 5000             // 1回に渡す本文の文字数(決定事項 4。4B / 9B で足りるかは試して決める)
	fetchMaxBytes     = 2 << 20          // 受け取る大きさの上限(2MB。超えた分は読まない)
	fetchTimeLimit    = 15 * time.Second // 時間の上限(ツールが自分で数える。tool.TimeLimiter)
	fetchMaxRedirects = 5
)

// FetchOptions は fetch_url の設定。
type FetchOptions struct {
	// AllowPrivate は、PC の中・社内のアドレス(loopback・private・link-local など)も読むか。
	// アプリは settings.json、aibow-server は server.json の fetchAllowPrivate(画面からは変えない。決定事項 12)
	AllowPrivate bool
	// Version は User-Agent に入れる aibow の版(aibow/<版> (+fetch_url)。決定事項 9)
	Version string
}

// FetchURL は、URL のページを読むツール。作業フォルダは使わない(決定事項 16)。
func FetchURL(opt FetchOptions) Tool {
	return fetchURL{opt: opt}
}

type fetchURL struct {
	opt FetchOptions
	// dial はテストで名前の引き方とつなぎ先を差し替えるため。nil なら本物(fetchURL.dialContext)
	dial func(ctx context.Context, network, addr string) (net.Conn, error)
}

func (fetchURL) Def() Def {
	return Def{
		Name: "fetch_url",
		Description: "Fetch a web page by URL and return its main text as Markdown (or plain text for text and JSON). " +
			"Returns at most 5000 characters at a time; use start_index to read the rest. The user is asked to approve before it runs.",
		Parameters: json.RawMessage(`{"type": "object", "properties": {` +
			`"url": {"type": "string", "description": "The http or https URL to fetch."}, ` +
			`"start_index": {"type": "integer", "description": "Character index to start reading from (0-based). Default 0."}}, "required": ["url"]}`),
	}
}

func (fetchURL) TimeLimit() time.Duration { return fetchTimeLimit }

// Preview は、確認のカードに URL を出す(取りに行くのは許可されてから)。URL の形が違えば、聞かずにモデルに返す。
func (fetchURL) Preview(args Args) (Preview, error) {
	u, err := checkURL(args.String("url"))
	if err != nil {
		return Preview{}, err
	}
	return Preview{Note: "URL を読む", URL: u.String()}, nil
}

func (t fetchURL) Run(ctx context.Context, args Args) (string, error) {
	u, err := checkURL(args.String("url"))
	if err != nil {
		return "", err
	}
	start := args.Int("start_index", 0)
	if start < 0 {
		return "", errors.New("start_index は 0 以上で指定してください")
	}

	begin := time.Now()
	page, err := t.get(ctx, u)
	if err != nil {
		var b *blockedError
		if errors.As(err, &b) {
			log.Warnf("fetch_url: %s は PC の中か社内のアドレス(%s)なので読みません", u, b.addr)
			return "", fmt.Errorf("%s は PC の中か社内のネットワークのアドレス(%s)なので読めません", u.Host, b.addr)
		}
		return "", err
	}
	text, err := page.text()
	if err != nil {
		return "", err
	}
	out, sent, err := cutText(page, text, start)
	if err != nil {
		return "", err
	}
	log.Infof("fetch_url: %s(%s・%s)受け取り %d バイト・本文 %d 文字のうち %d 文字を渡しました(%.2f 秒)",
		page.url, page.status, page.mediaType, len(page.body), utf8.RuneCountInString(text), sent, time.Since(begin).Seconds())
	return out, nil
}

// checkURL は、モデルが書いた URL を確かめる(http か https で、ホストがあること)。
func checkURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || raw == "" {
		return nil, fmt.Errorf("URL を読めません(%q)。https://example.com/ の形で書いてください", raw)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("読めるのは http と https の URL だけです(%q)", raw)
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("URL にホストがありません(%q)", raw)
	}
	u.Fragment = "" // # から後ろはページの中の場所で、サーバーには送らない
	return u, nil
}

// fetched は取ってきたページ。
type fetched struct {
	url       string // 最終的な URL(リダイレクトの後)
	status    string // 200 OK
	mediaType string // text/html など(Content-Type から charset などを外したもの)
	header    string // Content-Type そのまま(文字コードを決めるため)
	body      []byte
	cut       bool // fetchMaxBytes を超えたので、先頭だけを読んだ
}

// get は URL を取りに行く。プロキシは使わない(使うとプロキシのアドレスしか確かめられないため。決定事項 11)。
func (t fetchURL) get(ctx context.Context, u *url.URL) (*fetched, error) {
	dial := t.dial
	if dial == nil {
		dial = t.dialContext
	}
	tr := &http.Transport{Proxy: nil, DialContext: dial, ForceAttemptHTTP2: true, TLSHandshakeTimeout: 10 * time.Second}
	defer tr.CloseIdleConnections()
	client := &http.Client{
		Transport: tr,
		// リダイレクトの先も、同じ Transport を通るので、つなぐ直前にアドレスを確かめられる。ここは回数と種類だけを見る
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > fetchMaxRedirects {
				return fmt.Errorf("リダイレクトが %d 回を超えたので止めました", fetchMaxRedirects)
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("リダイレクトの先 %s は http でも https でもないので読みません", req.URL)
			}
			return nil
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("URL を読めません(%q): %w", u, err)
	}
	req.Header.Set("User-Agent", "aibow/"+cmp.Or(t.opt.Version, "devel")+" (+fetch_url)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/*;q=0.9,application/json;q=0.8,*/*;q=0.5")
	resp, err := client.Do(req)
	if err != nil {
		var b *blockedError
		if errors.As(err, &b) || ctx.Err() != nil {
			return nil, err // 断ったアドレスと時間切れ・停止は、呼ぶ側が文を作る
		}
		return nil, fmt.Errorf("%s を読めません: %w", u, unwrapURLError(err))
	}
	defer resp.Body.Close()

	p := &fetched{url: resp.Request.URL.String(), status: resp.Status, header: resp.Header.Get("Content-Type")}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%s を読めません(%s)", p.url, resp.Status)
	}
	// 種類はヘッダーで先に見る(画像や zip を 2MB 受け取ってから断らないため)。ヘッダーが無ければ、中身の先頭で見分ける
	p.mediaType, _, _ = mime.ParseMediaType(p.header)
	if p.mediaType != "" && kindOf(p.mediaType) == kindOther {
		return nil, unreadable(p.mediaType)
	}
	p.body, err = io.ReadAll(io.LimitReader(resp.Body, fetchMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%s を受け取る途中で止まりました: %w", p.url, err)
	}
	if len(p.body) > fetchMaxBytes {
		p.body, p.cut = p.body[:fetchMaxBytes], true
	}
	if p.mediaType == "" {
		p.header = http.DetectContentType(p.body)
		p.mediaType, _, _ = mime.ParseMediaType(p.header)
		if kindOf(p.mediaType) == kindOther {
			return nil, unreadable(p.mediaType)
		}
	}
	return p, nil
}

// unwrapURLError は、*url.Error の「Get "https://…": 」を外す(URL はこちらの文に書くので、2回出さない)。
func unwrapURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

func unreadable(mediaType string) error {
	return fmt.Errorf("この種類(%s)は読めません。読めるのは HTML・テキスト・JSON・XML です", mediaType)
}

// 中身の種類(決定事項 6)
const (
	kindOther = iota // 画像・PDF・zip など(読まない)
	kindHTML         // 本文を取り出して Markdown にする
	kindText         // そのまま(UTF-8 にして)
)

func kindOf(mediaType string) int {
	switch {
	case mediaType == "text/html" || mediaType == "application/xhtml+xml":
		return kindHTML
	case strings.HasPrefix(mediaType, "text/"),
		mediaType == "application/json", mediaType == "application/xml",
		strings.HasSuffix(mediaType, "+json"), strings.HasSuffix(mediaType, "+xml"):
		return kindText
	}
	return kindOther
}

// text は、ページの中身を UTF-8 にし、HTML なら本文を取り出して Markdown にする。
func (p *fetched) text() (string, error) {
	s := decode(p.body, p.header)
	if kindOf(p.mediaType) != kindHTML {
		return strings.TrimSpace(s), nil
	}
	base, _ := url.Parse(p.url)
	return htmlToMarkdown(s, base)
}

// decode は中身を UTF-8 の文字にする(決定事項 8)。
//
// 文字コードは、BOM → Content-Type の charset → HTML の <meta charset>(先頭 1024 バイト)の順に見る(charset.DetermineEncoding)。
// どれにも書いていなければ、UTF-8 として正しければ UTF-8。そうでなければ DetermineEncoding の決めたもの(windows-1252)。
// 日本のページには Shift_JIS・EUC-JP のものがまだあり、そのまま読むと文字化けして、モデルは中身を読めない。
func decode(body []byte, contentType string) string {
	e, _, certain := charset.DetermineEncoding(body, contentType)
	if !certain && utf8.Valid(body) {
		return string(body)
	}
	b, err := e.NewDecoder().Bytes(body)
	if err != nil {
		return strings.ToValidUTF8(string(body), "�")
	}
	return string(b)
}

// cutText は、本文の start 文字目から fetchMaxRunes 文字を、1行目の URL・状態・種類と一緒に返す(決定事項 5)。
// 続きがあれば、続きの読み方を書き添える。渡した本文の文字数も返す(ログ用)。
func cutText(p *fetched, text string, start int) (string, int, error) {
	runes := []rune(text)
	total := len(runes)
	var b strings.Builder
	fmt.Fprintf(&b, "URL: %s(%s・%s", p.url, p.status, p.mediaType)
	if start > 0 {
		fmt.Fprintf(&b, "・%d 文字目から", start)
	}
	b.WriteString(")\n\n")
	if total == 0 {
		b.WriteString("(本文が見つかりません。JavaScript で中身を作るページかもしれません)")
		return b.String(), 0, nil
	}
	if start >= total {
		return "", 0, fmt.Errorf("本文は全体で %d 文字です。start_index は 0〜%d で指定してください", total, total-1)
	}
	end := min(start+fetchMaxRunes, total)
	b.WriteString(string(runes[start:end]))
	if end < total {
		fmt.Fprintf(&b, "\n\n(全体は %d 文字です。続きは start_index=%d で読めます)", total, end)
	}
	if p.cut {
		fmt.Fprintf(&b, "\n\n(ページが %d MB を超えたので、先頭の %d MB だけを読みました)", fetchMaxBytes>>20, fetchMaxBytes>>20)
	}
	return b.String(), end - start, nil
}

// dialContext は、本物のつなぎ方。名前を引くのは net.Dialer に任せ、引いた結果のアドレスごとに、つなぐ直前に control が確かめる。
func (t fetchURL) dialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	d := &net.Dialer{Timeout: 10 * time.Second, Control: t.control}
	return d.DialContext(ctx, network, addr)
}

// control は、つなぐ直前に、実際につなぐアドレスを確かめる(決定事項 11)。
//
// 名前を引いて確かめてから、改めて名前でつなぐ形だと、その間に名前の答えを PC の中のアドレスに変えられる(DNS rebinding)。
// net.Dialer.Control は、名前を引いた後の「このアドレスにつなぐ」の直前に呼ばれるので、確かめたアドレスとつなぐアドレスが同じになる。
func (t fetchURL) control(network, address string, _ syscall.RawConn) error {
	if t.opt.AllowPrivate {
		return nil
	}
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("つなぐアドレス %q を読めません: %w", address, err)
	}
	if !PublicAddr(ap.Addr()) {
		return &blockedError{addr: ap.Addr()}
	}
	return nil
}

// blockedError は、PC の中・社内のアドレスなので断ったこと。
type blockedError struct{ addr netip.Addr }

func (e *blockedError) Error() string {
	return fmt.Sprintf("PC の中か社内のネットワークのアドレス(%s)には、つなぎません", e.addr)
}

// notGlobal は、netip.Addr のメソッドでは分からない、グローバルでないアドレスの範囲(IANA の特別な用途のアドレス)。
var notGlobal = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),       // 「このネットワーク」
	netip.MustParsePrefix("100.64.0.0/10"),   // キャリアの中のアドレス(CGNAT)。社内やクラウドの中で使われることがある
	netip.MustParsePrefix("192.0.0.0/24"),    // IETF の取り決め用
	netip.MustParsePrefix("192.0.2.0/24"),    // 説明用(TEST-NET-1)
	netip.MustParsePrefix("198.18.0.0/15"),   // 性能の試験用
	netip.MustParsePrefix("198.51.100.0/24"), // 説明用(TEST-NET-2)
	netip.MustParsePrefix("203.0.113.0/24"),  // 説明用(TEST-NET-3)
	netip.MustParsePrefix("240.0.0.0/4"),     // 予約(255.255.255.255 も含む)
	netip.MustParsePrefix("100::/64"),        // 捨てる用
	netip.MustParsePrefix("2001:db8::/32"),   // 説明用
}

// PublicAddr は、インターネットのアドレス(グローバル)か。loopback(127.0.0.0/8・::1)・private(10/8・172.16/12・192.168/16・fc00::/7)・
// link-local(169.254/16・fe80::/10。クラウドのメタデータ 169.254.169.254 もここ)・未指定(0.0.0.0・::)・マルチキャストなどは false。
func PublicAddr(a netip.Addr) bool {
	a = a.Unmap() // ::ffff:127.0.0.1 のような IPv4 を IPv6 で書いたものは、IPv4 として見る
	if !a.IsGlobalUnicast() || a.IsPrivate() {
		return false
	}
	for _, p := range notGlobal {
		if p.Contains(a) {
			return false
		}
	}
	return true
}
