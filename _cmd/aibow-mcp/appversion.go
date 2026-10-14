package main

// appversion.go: ログ・MCP のサーバーとして名乗るとき(serverInfo.version)・-version で出すバージョン。
// 形はアプリ(_cmd/aibow/appversion.go)にならう。バージョンもアプリと同じ。
//
// go run で起動したものは、まだデプロイしていない「次のバージョン」を作っている途中なので、
// patch を1つ上げて +dev を付ける(version が 0.3.1 なら 0.3.2+dev)。exe(go build・go install で作ったもの)は version のまま。
// + の後ろは SemVer のビルドの情報で、版の大小の比べ方には関係しない。
//
// アプリはビルドタグ(production)で見分けるが、aibow-mcp は Taskfile を通さず go build・go install で作るので、起動のしかたで見分ける。

import (
	_ "embed"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// version はバージョン。_cmd/aibow/version(唯一の正)の写しで、go run _cmd/version.go が書き写す。手で書き換えない
//
//go:embed version
var version string

// appVersion は表示するバージョン。
func appVersion() string {
	v := strings.TrimSpace(version)
	if !isGoRun() {
		return v
	}
	return nextPatch(v) + "+dev"
}

// isGoRun は go run で起動したか。
// go run は一時フォルダ(GOTMPDIR か OS の一時フォルダ)の go-build… の下に exe を作って実行するので、自分の exe の場所で見分ける
// (go-build123/b001/exe/aibow-mcp.exe)。
func isGoRun() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	for _, part := range strings.Split(filepath.ToSlash(exe), "/") {
		if strings.HasPrefix(part, "go-build") {
			return true
		}
	}
	return false
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
