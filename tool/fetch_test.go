package tool

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"testing"
)

func TestPublicAddr(t *testing.T) {
	for addr, want := range map[string]bool{
		"93.184.215.14":    true,
		"2606:4700::1111":  true,
		"127.0.0.1":        false,
		"127.1.2.3":        false,
		"::1":              false,
		"10.0.0.1":         false,
		"172.16.5.4":       false,
		"192.168.1.10":     false,
		"fc00::1":          false,
		"169.254.169.254":  false,
		"fe80::1":          false,
		"100.64.0.1":       false,
		"0.0.0.0":          false,
		"::":               false,
		"224.0.0.1":        false,
		"255.255.255.255":  false,
		"::ffff:127.0.0.1": false, // IPv4 を IPv6 で書いたもの
		"::ffff:8.8.8.8":   true,
	} {
		if got := PublicAddr(netip.MustParseAddr(addr)); got != want {
			t.Errorf("PublicAddr(%s) = %v、期待 %v", addr, got, want)
		}
	}
}

// pages は、テストのサーバーが返すページ(パス → Content-Type と中身)。
func testServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	page := func(path, ctype, body string) {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", ctype)
			fmt.Fprint(w, body)
		})
	}
	page("/main", "text/html; charset=utf-8", `<html><head><title>題 の
 ページ</title><script>alert(1)</script></head><body>
<header>サイトの頭</header><nav><a href="/">ホーム</a></nav>
<main><h1>見出し</h1><p>本文の  段落。
<a href="/next">次へ</a>と<a href="#top">上へ</a>。</p>
<ul><li>一つ目<ul><li>入れ子</li></ul></li><li>二つ目</li></ul>
<ol><li>手順1</li><li>手順2</li></ol>
<pre><code>func main() {
	fmt.Println("hi")
}</code></pre>
<table><tr><th>名前</th><th>値</th></tr><tr><td>a|b</td><td><code>1</code></td></tr></table>
</main><aside>広告</aside><footer>著作権</footer></body></html>`)
	page("/article", "text/html", `<html><body><div>前置き</div>
<article><p>短い</p></article>
<article><h2>長い記事</h2><p>こちらがいちばん長い記事の本文です。</p></article></body></html>`)
	page("/body", "text/html", `<html><body><nav>メニュー</nav><div>体の文</div><form><input>送る</form></body></html>`)
	// Shift_JIS の「日本語」(93 FA 96 7B 8C EA)。Content-Type に charset が無く、<meta charset> で決める
	page("/sjis", "text/html", "<html><head><meta charset=\"Shift_JIS\"></head><body><p>\x93\xfa\x96\x7b\x8c\xea</p></body></html>")
	page("/sjis-header", "text/plain; charset=shift_jis", "\x93\xfa\x96\x7b\x8c\xea")
	page("/json", "application/json", `{"a": 1}`)
	page("/png", "image/png", "\x89PNG")
	page("/empty", "text/html", `<html><body><script>render()</script></body></html>`)
	page("/long", "text/plain", strings.Repeat("あ", fetchMaxRunes)+strings.Repeat("い", 100))
	mux.HandleFunc("/missing", http.NotFound)
	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, r.URL.Query().Get("to"), http.StatusFound)
	})
	mux.HandleFunc("/loop", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/loop", http.StatusFound)
	})
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func fetch(t *testing.T, f fetchURL, args Args) (string, error) {
	t.Helper()
	return f.Run(context.Background(), args)
}

// テストのサーバーは 127.0.0.1 にいるので、中身を確かめるテストは PC の中のアドレスを許して読む。
func TestFetchHTML(t *testing.T) {
	s := testServer(t)
	f := fetchURL{opt: FetchOptions{AllowPrivate: true}}

	got, err := fetch(t, f, Args{"url": s.URL + "/main#section"})
	if err != nil {
		t.Fatal(err)
	}
	want := "URL: " + s.URL + "/main(200 OK・text/html)\n\n" +
		"# 題 の ページ\n\n" +
		"# 見出し\n\n" +
		"本文の 段落。 [次へ](" + s.URL + "/next)と上へ。\n\n" +
		"- 一つ目\n  - 入れ子\n- 二つ目\n\n" +
		"1. 手順1\n2. 手順2\n\n" +
		"```\nfunc main() {\n\tfmt.Println(\"hi\")\n}\n```\n\n" +
		"| 名前 | 値 |\n| --- | --- |\n| a\\|b | `1` |"
	if got != want {
		t.Errorf("Markdown が違います\n--- 結果\n%s\n--- 期待\n%s", got, want)
	}

	for path, want := range map[string]string{
		"/article": "## 長い記事\n\nこちらがいちばん長い記事の本文です。",
		"/body":    "体の文",
		"/sjis":    "日本語",
		"/empty":   "(本文が見つかりません。JavaScript で中身を作るページかもしれません)",
	} {
		got, err := fetch(t, f, Args{"url": s.URL + path})
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		if _, body, _ := strings.Cut(got, "\n\n"); body != want {
			t.Errorf("%s の本文が %q です(期待 %q)", path, body, want)
		}
	}
}

func TestFetchText(t *testing.T) {
	s := testServer(t)
	f := fetchURL{opt: FetchOptions{AllowPrivate: true}}
	for path, want := range map[string]string{
		"/sjis-header": "日本語",
		"/json":        `{"a": 1}`,
	} {
		got, err := fetch(t, f, Args{"url": s.URL + path})
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		if _, body, _ := strings.Cut(got, "\n\n"); body != want {
			t.Errorf("%s の本文が %q です(期待 %q)", path, body, want)
		}
	}
}

func TestFetchContinue(t *testing.T) {
	s := testServer(t)
	f := fetchURL{opt: FetchOptions{AllowPrivate: true}}
	total := fetchMaxRunes + 100

	got, err := fetch(t, f, Args{"url": s.URL + "/long"})
	if err != nil {
		t.Fatal(err)
	}
	note := fmt.Sprintf("(全体は %d 文字です。続きは start_index=%d で読めます)", total, fetchMaxRunes)
	if !strings.HasSuffix(got, note) || strings.Contains(got, "い") {
		t.Errorf("切り方が違います(末尾: %q)", got[len(got)-200:])
	}

	got, err = fetch(t, f, Args{"url": s.URL + "/long", "start_index": fetchMaxRunes})
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("URL: %s/long(200 OK・text/plain・%d 文字目から)\n\n%s", s.URL, fetchMaxRunes, strings.Repeat("い", 100))
	if got != want {
		t.Errorf("続きが %q です(期待 %q)", got, want)
	}

	if _, err := fetch(t, f, Args{"url": s.URL + "/long", "start_index": total}); err == nil || !strings.Contains(err.Error(), "start_index は 0〜") {
		t.Errorf("本文より後ろの start_index が断られません: %v", err)
	}
}

func TestFetchErrors(t *testing.T) {
	s := testServer(t)
	f := fetchURL{opt: FetchOptions{AllowPrivate: true}}
	for args, want := range map[string]string{
		s.URL + "/png":     "この種類(image/png)は読めません",
		s.URL + "/missing": "404 Not Found",
		s.URL + "/loop":    "リダイレクトが 5 回を超えた",
		"ftp://example.com": "http と https の URL だけ",
		"https:///path":     "ホストがありません",
	} {
		_, err := fetch(t, f, Args{"url": args})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: エラーが %v です(期待: %s を含む)", args, err, want)
		}
	}
}

func TestFetchPreview(t *testing.T) {
	pv, err := fetchURL{}.Preview(Args{"url": " https://example.com/a#b "})
	if err != nil || pv.URL != "https://example.com/a" || pv.Note == "" {
		t.Errorf("確認の内容が %+v(%v)です", pv, err)
	}
	if _, err := (fetchURL{}).Preview(Args{"url": "file:///etc/passwd"}); err == nil {
		t.Error("file: の URL を聞こうとしました")
	}
}

// 本物のつなぎ方(net.Dialer.Control)で、PC の中のアドレスと、それを指す名前(localhost)が断られること。
func TestFetchBlocksPrivate(t *testing.T) {
	s := testServer(t)
	u, _ := url.Parse(s.URL)
	for _, target := range []string{s.URL + "/json", "http://localhost:" + u.Port() + "/json"} {
		_, err := fetch(t, fetchURL{}, Args{"url": target})
		if err == nil || !strings.Contains(err.Error(), "PC の中か社内のネットワークのアドレス") {
			t.Errorf("%s が断られません: %v", target, err)
		}
	}
}

// fakeDial は、名前の引き方を差し替えたつなぎ方(テスト用)。names の名前は決めたアドレスに引き、
// 本物と同じ control で確かめてから、実際には real(テストのサーバー)につなぐ。
func fakeDial(control func(network, address string, c syscall.RawConn) error, names map[string]string, real string) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		if ip, ok := names[host]; ok {
			host = ip
		}
		if err := control(network, net.JoinHostPort(host, port), nil); err != nil {
			return nil, err
		}
		var d net.Dialer
		return d.DialContext(ctx, network, real)
	}
}

// 名前が PC の中・社内のアドレスを指すとき、リダイレクトの先がそうしたアドレスのときも断ること。許す設定なら読めること。
func TestFetchBlocksByName(t *testing.T) {
	s := testServer(t)
	real := strings.TrimPrefix(s.URL, "http://")
	names := map[string]string{"public.test": "93.184.215.14", "evil.test": "192.168.0.10"}

	f := fetchURL{}
	f.dial = fakeDial(f.control, names, real)
	if _, err := fetch(t, f, Args{"url": "http://public.test/json"}); err != nil {
		t.Errorf("グローバルなアドレスの名前が読めません: %v", err)
	}
	for _, target := range []string{
		"http://evil.test/json",
		"http://169.254.169.254/latest/meta-data/",
		"http://public.test/redirect?to=" + url.QueryEscape("http://169.254.169.254/latest/meta-data/"),
		"http://public.test/redirect?to=" + url.QueryEscape("http://evil.test/json"),
	} {
		_, err := fetch(t, f, Args{"url": target})
		if err == nil || !strings.Contains(err.Error(), "PC の中か社内のネットワークのアドレス") {
			t.Errorf("%s が断られません: %v", target, err)
		}
	}

	allow := fetchURL{opt: FetchOptions{AllowPrivate: true}}
	allow.dial = fakeDial(allow.control, names, real)
	if _, err := fetch(t, allow, Args{"url": "http://public.test/redirect?to=" + url.QueryEscape("http://evil.test/json")}); err != nil {
		t.Errorf("許す設定でも読めません: %v", err)
	}
}

func TestFetchUserAgent(t *testing.T) {
	var ua string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua = r.UserAgent()
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "ok")
	}))
	defer s.Close()
	if _, err := fetch(t, fetchURL{opt: FetchOptions{AllowPrivate: true, Version: "1.2.3"}}, Args{"url": s.URL}); err != nil {
		t.Fatal(err)
	}
	if ua != "aibow/1.2.3 (+fetch_url)" {
		t.Errorf("User-Agent が %q です", ua)
	}
}
