package main

// version.go: 画面のアプリとバージョンを受け渡す(0054)。
//
// MCP は最初の initialize で clientInfo / serverInfo を1回だけ交換する(つながりがずっと続くので)。
// HTTP はリクエストが1つずつ独立していて「つないだ瞬間」が無いので、User-Agent や Server と同じく、
// 毎回のリクエストと返事の見出し(ヘッダー)に載せる。
//
//	アプリ → サーバー(全部のリクエスト)  X-Aibow-App-Version     … アプリのバージョン
//	サーバー → (全部の返事)            X-Aibow-Server-Version  … aibow-server のバージョン
//	サーバー → (全部の返事)            X-Aibow-Min-App-Version … 対応できるアプリの最低バージョン(appversion.MinApp)
//
// 今は受け渡しと記録だけで、版を比べて止めることはしない(判定のルールは将来決める)。

import (
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/secondarykey/aibow/internal/appversion"
	"github.com/secondarykey/aibow/log"
)

// maxAppSeen は、覚えておく(相手, 版)の組の数の上限。見出しは誰でも好きな値を送れるので、
// 値を変えながら送り続けられてもメモリが増え続けないようにする(超えたら、新しい組はもう記録しない)
const maxAppSeen = 1024

// appSeen は、起動してから見た(相手のアドレス, アプリのバージョン)の組(メモリの中だけ。起動し直したら忘れる)。
type appSeen struct {
	mu   sync.Mutex
	seen map[[2]string]bool
}

func newAppSeen() *appSeen { return &appSeen{seen: map[[2]string]bool{}} }

// first は、その組を初めて見たか(見たことにする)。上限を超えたら false(記録しない)。
func (s *appSeen) first(host, version string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := [2]string{host, version}
	if s.seen[k] || len(s.seen) >= maxAppSeen {
		return false
	}
	s.seen[k] = true
	return true
}

// withVersion は、全部の返事に自分の版と対応できるアプリの最低バージョンを付け、
// 画面のアプリからのリクエストなら、(相手, 版)を初めて見たときだけ Notice のログを出す(0054 の決定事項 8・13)。
// 置くのは信頼するプロキシの書き換え(fromProxy)のすぐ内側で、mount の外(r.RemoteAddr は元の相手になっている。道によらず1か所で付ける)。
// X-Aibow-App-Version を送ってこないリクエスト(Cline・ブラウザ・この機能より古いアプリ)は、画面のアプリではないとして記録しない(決定事項 9)。
func withVersion(seen *appSeen, h http.Handler) http.Handler {
	server := appversion.Version()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(appversion.ServerHeader, server)
		w.Header().Set(appversion.MinAppHeader, appversion.MinApp)
		if v := cleanVersion(r.Header.Get(appversion.AppHeader)); v != "" {
			host := remoteHost(r.RemoteAddr)
			if seen.first(host, v) {
				// 今は版を比べない(最低バージョンより古くても、同じ文。決定事項 14)
				log.Noticef("画面のアプリ(%s)が %s からつながりました(この aibow-server は %s、対応するアプリは %s から)", v, host, server, appversion.MinApp)
			}
		}
		h.ServeHTTP(w, r)
	})
}

// remoteHost は、r.RemoteAddr のホストの部分(192.0.2.1:51234 → 192.0.2.1)。ポートが無ければそのまま。
func remoteHost(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}

// cleanVersion は、送られてきた版をログに出せる形にする(SemVer に使う文字だけ・64 文字まで)。
// 見出しは誰でも好きな値を送れるので、ログを読みにくくする文字や長すぎる値をそのまま出さない。使える文字が無ければ空(記録しない)。
func cleanVersion(v string) string {
	v = strings.TrimSpace(v)
	if len(v) > 64 {
		v = v[:64]
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '.', r == '-', r == '+':
			return r
		}
		return -1
	}, v)
}
