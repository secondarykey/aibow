package main

// appversion.go: 画面・ウィンドウの名前・ログ・MCP のサーバーに名乗るときのバージョン。
//
// wails3 dev で起動したものは、まだデプロイしていない「次のバージョン」を作っている途中なので、
// patch を1つ上げて +dev を付ける(version が 0.3.1 なら 0.3.2+dev)。リリースのビルド(wails3 build)は version のまま。
// + の後ろは SemVer のビルドの情報で、版の大小の比べ方には関係しない。

import (
	"strconv"
	"strings"
)

// appVersion は表示するバージョン。
func appVersion() string {
	v := strings.TrimSpace(version)
	if !isDev {
		return v
	}
	return nextPatch(v) + "+dev"
}

// nextPatch は patch を1つ上げた版(0.3.1 → 0.3.2)。major.minor.patch の形でなければ、そのまま返す。
func nextPatch(v string) string {
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return v
	}
	n, err := strconv.Atoi(parts[2])
	if err != nil {
		return v
	}
	parts[2] = strconv.Itoa(n + 1)
	return strings.Join(parts, ".")
}
