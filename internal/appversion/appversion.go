// Package appversion は、aibow-mcp と aibow-server が名乗るバージョン(ログ・起動の行・-version・MCP の serverInfo.version)。
// 形はアプリ(_cmd/aibow/appversion.go)にならう。バージョンもアプリと同じ。
//
// go run で起動したものは、まだデプロイしていない「次のバージョン」を作っている途中なので、
// patch を1つ上げて +dev を付ける(version が 0.3.1 なら 0.3.2+dev)。exe(go build・go install で作ったもの)は version のまま。
// + の後ろは SemVer のビルドの情報で、版の大小の比べ方には関係しない。
//
// アプリはビルドタグ(production)で見分けるが、aibow-mcp と aibow-server は Taskfile を通さず go build・go install で作るので、
// 起動のしかたで見分ける(IsGoRun)。
//
// 前は2つの exe のフォルダに同じ appversion.go と version の写しを1つずつ置いていた(//go:embed は自分のフォルダの下しか埋め込めないため)。
// どちらもルートのモジュールの exe なので、ここに1つだけ置く。画面のアプリ(_cmd/aibow)は別のモジュールで internal を import できず、
// 見分け方も違うので、自分の appversion.go を持つ。
package appversion

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

// Version は表示するバージョン。
func Version() string {
	v := strings.TrimSpace(version)
	if !IsGoRun() {
		return v
	}
	return nextPatch(v) + "+dev"
}

// IsGoRun は go run で起動したか。
// go run は一時フォルダ(GOTMPDIR か OS の一時フォルダ)の go-build… の下に exe を作って実行するので、自分の exe の場所で見分ける
// (go-build123/b001/exe/aibow-server.exe)。aibow-server はログのレベルの既定を決めるのにも使う。
func IsGoRun() bool {
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

// MinApp は、この aibow-server が対応できる画面のアプリの最低バージョン(0054)。返事の見出し X-Aibow-Min-App-Version で知らせる。
// Version と違って _cmd/aibow/version の写しではなく、手で書く(go run _cmd/version.go は書き換えない。go run でも +dev を付けない)。
// アプリとサーバーのあいだのやり取り(api/client と aibow-server の口)を、古いアプリが使えない形に変えたときに、手で上げる。
// 初めの値は、この見出しを送り始めた版(それより古いアプリは自分の版を送ってこないので、比べる相手にならない)。
// 今は値を受け渡すだけで、比べて止めることはしない(判定のルールは将来決める)。
const MinApp = "0.9.3"

// 画面のアプリと aibow-server がバージョンを受け渡す見出し(0054。今ある X-Aibow-User にそろえた)。
// HTTP には「つないだ瞬間」が無いので、毎回のリクエストと返事に載せる。aibow-server(付ける側)と api/client(読む側)で使う。
const (
	AppHeader    = "X-Aibow-App-Version"     // アプリ → サーバー(全部のリクエスト): アプリのバージョン
	ServerHeader = "X-Aibow-Server-Version"  // サーバー → (全部の返事): aibow-server のバージョン
	MinAppHeader = "X-Aibow-Min-App-Version" // サーバー → (全部の返事): 対応できるアプリの最低バージョン(MinApp)
)
