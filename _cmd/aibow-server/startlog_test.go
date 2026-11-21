package main

import (
	"net/netip"
	"strings"
	"testing"
)

// 起動のログは、その人に関係あるときだけ出す(0048 の決定事項 3・4)。
//   - base を付けて 127.0.0.1 で待ち受けても、プロキシのヘッダーの案内は出さない(プロキシを使っているか見分けられないため)
//   - 「信頼するプロキシ」は、trustedProxies を書いたときだけ Notice。書かなければ Info
func TestStartupLogOnlyWhenRelevant(t *testing.T) {
	line := func(out, text string) string {
		for _, l := range strings.Split(out, "\n") {
			if strings.Contains(l, text) {
				return l
			}
		}
		return ""
	}

	buf := captureLog(t)
	c := serverConfig{Host: "127.0.0.1", Base: "/aibow", ProxyHeader: "X-Forwarded-For", Login: true}
	logAddresses(c, "http://127.0.0.1:8089/aibow", newProxyTrust(nil, c.ProxyHeader))
	out := buf.String()
	if strings.Contains(out, "リバースプロキシは") {
		t.Errorf("プロキシの案内は出さない: %s", out)
	}
	if l := line(out, "信頼するプロキシ"); !strings.Contains(l, "level=INFO ") || !strings.Contains(l, "この PC(") {
		t.Errorf("trustedProxies を書かなければ Info で、「この PC」: %q", l)
	}

	buf = captureLog(t)
	c.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("10.0.0.5/32")}
	logAddresses(c, "http://127.0.0.1:8089/aibow", newProxyTrust(c.TrustedProxies, c.ProxyHeader))
	if l := line(buf.String(), "信頼するプロキシ"); l == "" || strings.Contains(l, "level=INFO ") || !strings.Contains(l, "この PC, 10.0.0.5") {
		t.Errorf("trustedProxies を書いたら Notice: %q", l)
	}
}
