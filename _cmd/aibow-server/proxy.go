package main

// proxy.go: リバースプロキシの後ろに置いたとき、プロキシが付けたヘッダーから元の相手を取り出す(0044)。
//
// aibow-server は「どこから来たか」を接続の相手のアドレス(r.RemoteAddr)で見ている(サーバーの PC の上からだけの操作・
// ログインを続けて間違えたときの制限・ログ)。プロキシの後ろでは、相手はいつもプロキシになってしまう。
//
// ここで学べること:
//   - ヘッダーは誰でも書ける。X-Forwarded-For: 127.0.0.1 は curl からでも付けられるので、「付いていたら信じる」は危ない。
//     信じてよいのは、つないできた相手(TCP の相手なので偽れない)が、自分の置いたプロキシのときだけ
//   - 右から読む。X-Forwarded-For はプロキシを通るたびに右へ足される(元の相手, プロキシ1, プロキシ2)。左は元の相手が好きに書ける所なので、
//     右から信頼するプロキシを飛ばし、最初に出てきた信頼しないアドレスを元の相手とする
//   - 見るヘッダーは1つ。どのヘッダーをプロキシが書き直すかはプロキシの設定で決まるので、それに合わせて1つだけ見る

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"

	"github.com/secondarykey/aibow/api"
	"github.com/secondarykey/aibow/log"
)

// loopbackProxies は、書かなくてもいつも信頼する「サーバーの PC の上」(0044 の決定事項 2)。
// サーバーの PC の上から来た呼び出しは、もともとサーバーの PC の上からだけの操作までできる相手なので、そのヘッダーを信じても信頼の範囲は広がらない。
var loopbackProxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("::1/128")}

// proxyTrust は、信頼するプロキシの一覧と、元の相手を読むヘッダー。
type proxyTrust struct {
	nets   []netip.Prefix // サーバーの PC の上 + server.json の trustedProxies
	header string         // X-Forwarded-For・Forwarded・X-Real-IP のどれか(config.ProxyHeaderName でそろえた名前)
}

func newProxyTrust(extra []netip.Prefix, header string) proxyTrust {
	return proxyTrust{nets: append(append([]netip.Prefix{}, loopbackProxies...), extra...), header: header}
}

// trusted は、a が信頼するプロキシの一覧に入っているか。
func (p proxyTrust) trusted(a netip.Addr) bool {
	for _, n := range p.nets {
		if n.Contains(a) {
			return true
		}
	}
	return false
}

// errBadForward は、転送のヘッダーに読めない値があったとき(0044 の決定事項 8)。
var errBadForward = errors.New("転送のヘッダーに読めない値があります")

// origin は、リクエストの元の相手を決める(0044 の決定事項 6〜9)。
//   - 接続の相手が一覧に入っていない → 接続の相手(ヘッダーは見ない)
//   - 入っていて、ヘッダーが無い → 接続の相手(プロキシ自身か、同じ PC からプロキシを通らずにつないだ人)
//   - 入っていて、ヘッダーがある → ヘッダーから読んだ元の相手(forwarded が true)
//
// 読めない値に行き当たったら errBadForward。接続の相手(= 信頼するプロキシ)に戻すと、外の人が「サーバーの PC の上から」になってしまうので戻さない。
func (p proxyTrust) origin(r *http.Request) (addr netip.Addr, forwarded bool, err error) {
	peer, err := peerAddr(r.RemoteAddr)
	if err != nil {
		return netip.Addr{}, false, fmt.Errorf("接続の相手のアドレスが読めません: %q", r.RemoteAddr)
	}
	values := r.Header.Values(p.header)
	if !p.trusted(peer) || len(values) == 0 {
		return peer, false, nil
	}
	if p.header == "X-Real-IP" {
		// X-Real-IP は1つのアドレス(並びにはしない)。2行あるのは、プロキシが書き直さずに元の相手のものを残しているとき
		if len(values) != 1 {
			return netip.Addr{}, false, fmt.Errorf("%w(X-Real-IP が %d 行あります)", errBadForward, len(values))
		}
		a, ok := parseForwardedAddr(values[0])
		if !ok {
			return netip.Addr{}, false, errBadForward
		}
		return a, true, nil
	}
	// 同じ名前のヘッダーが複数行あれば、上から順につなげて1つの並びとして読む(RFC 9110 の 5.3。プロキシによっては行を分けて足す)
	var hops []string
	for _, v := range values {
		for item := range strings.SplitSeq(v, ",") {
			if p.header == "Forwarded" {
				item = forwardedFor(item)
			}
			hops = append(hops, item)
		}
	}
	// 右から読み、信頼するプロキシを飛ばす。全部が一覧に入っていたら、いちばん左を元の相手とする
	var a netip.Addr
	for i := len(hops) - 1; i >= 0; i-- {
		var ok bool
		if a, ok = parseForwardedAddr(hops[i]); !ok {
			return netip.Addr{}, false, fmt.Errorf("%w(%q)", errBadForward, strings.TrimSpace(hops[i]))
		}
		if !p.trusted(a) {
			break
		}
	}
	return a, true, nil
}

// forwardedFor は、Forwarded の1つの要素(for=192.0.2.60;proto=http;by=203.0.113.43)から for= の値を取り出す。
// by=・proto=・host= は見ない(0044 の決定事項 7)。for= が無ければ空(読めない値になる)。
func forwardedFor(elem string) string {
	for pair := range strings.SplitSeq(elem, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if ok && strings.EqualFold(strings.TrimSpace(k), "for") {
			return v
		}
	}
	return ""
}

// parseForwardedAddr は、ヘッダーに書かれた1つの相手を IP アドレスとして読む。
// Forwarded の引用符・IPv6 の角かっこ・ポートは外す("[2001:db8::1]:4711"・192.0.2.1:51234。IIS の ARR は X-Forwarded-For にポートも書く)。
// unknown・_hidden のような伏せた名前や空は読めない(ok が false)。
func parseForwardedAddr(s string) (netip.Addr, bool) {
	s = strings.Trim(strings.TrimSpace(s), `"`)
	if strings.HasPrefix(s, "[") {
		end := strings.Index(s, "]")
		if end < 0 {
			return netip.Addr{}, false
		}
		if rest := s[end+1:]; rest != "" && !strings.HasPrefix(rest, ":") {
			return netip.Addr{}, false
		}
		s = s[1:end]
	} else if strings.Count(s, ":") == 1 {
		s, _, _ = strings.Cut(s, ":") // IPv4 とポート
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}, false
	}
	return a.Unmap().WithZone(""), true
}

// peerAddr は、接続の相手(r.RemoteAddr。"192.0.2.1:51234"・"[::1]:51234")の IP アドレス。
func peerAddr(remote string) (netip.Addr, error) {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		return netip.Addr{}, err
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, err
	}
	return a.Unmap().WithZone(""), nil
}

// fromProxy は、mount の外側で元の相手を1回だけ決め、r.RemoteAddr を元の相手に書き換えて中に渡す(0044 の決定事項 10)。
// 中の api.IsLocal・auth のログインの制限・ログは、変えずに元の相手を見る(http.StripPrefix で頭を外すのと同じ考え方)。
// ポートは分からないので 0 にする(192.0.2.1:0)。
//
// login が false(ログインなしの形)のときは、元の相手がサーバーの PC の上でなければ断る(決定事項 12)。
// login: false は「この PC の人だけが使う」形なので、信頼するプロキシ越しに外から来た人には使わせない。
func fromProxy(p proxyTrust, login bool, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a, forwarded, err := p.origin(r)
		if err != nil {
			log.Warnf("元の相手が決められないので断りました: %s %s(接続の相手 %s・%s: %q): %v",
				r.Method, r.URL.Path, r.RemoteAddr, p.header, strings.Join(r.Header.Values(p.header), ", "), err)
			http.Error(w, "転送のヘッダー("+p.header+")に読めない値があります。リバースプロキシの設定を確かめてください", http.StatusBadRequest)
			return
		}
		if forwarded {
			log.Debugf("プロキシ %s から、元の相手 %s(%s)", api.RemoteHost(r), a, p.header)
			r2 := r.WithContext(r.Context())
			r2.RemoteAddr = netip.AddrPortFrom(a, 0).String()
			r = r2
		}
		if !login && !a.IsLoopback() {
			log.Warnf("ログインなし(login: false)の形で、サーバーの PC の外(%s)から来たので断りました: %s %s", a, r.Method, r.URL.Path)
			http.Error(w, "この aibow-server はログインなし(login: false)で動いているので、サーバーの PC の上からだけ使えます", http.StatusForbidden)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// describe は、起動のときに出す信頼するプロキシの一覧(0044 の決定事項 13)。サーバーの PC の上は「この PC の上」とまとめる。
func (p proxyTrust) describe() string {
	names := []string{"この PC の上"}
	for _, n := range p.nets[len(loopbackProxies):] {
		if n.IsSingleIP() {
			names = append(names, n.Addr().String())
		} else {
			names = append(names, n.String())
		}
	}
	return strings.Join(names, ", ")
}

// all は、全部のアドレスを含む範囲(0.0.0.0/0・::/0)が一覧にあるか(決定事項 5。誰でもヘッダーで元の相手を偽れる)。
func (p proxyTrust) all() []string {
	var out []string
	for _, n := range p.nets {
		if n.Bits() == 0 {
			out = append(out, n.String())
		}
	}
	return out
}
