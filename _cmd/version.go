// version.go: アプリのバージョンを管理するスクリプト。
//
// _cmd/aibow/version(テキスト1行)が唯一の正。ここから build/config.yml と frontend/package.json に書き写す。
// config.yml の値は wails3 update build-assets で exe のバージョン情報(build/windows/info.json など)になる。
// MCP のサーバー(_cmd/aibow-mcp)と OpenAI 互換の API(_cmd/aibow-api)も同じバージョンを名乗るので、
// _cmd/aibow-mcp/version と _cmd/aibow-api/version にも書き写す
// (//go:embed は自分のフォルダの下のファイルしか埋め込めず、_cmd/aibow/version を直接は使えないため)。
//
//	go run _cmd/version.go          version の値で config.yml / package.json をそろえる
//	go run _cmd/version.go 1.2.3    バージョンを指定して書き換える
//	go run _cmd/version.go -bump    patch / minor / major を選んで上げる
//	go run _cmd/version.go -print   今のバージョンを表示するだけ
//	go run _cmd/version.go -auto    v+今のバージョンのタグがあれば(デプロイ済みなら)patch を上げる。local:deploy から使う
//
// _cmd/aibow とは別の、このファイルだけで動くプログラム(リポジトリのルートのモジュールに属し、標準ライブラリだけを使う)。
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// パスはリポジトリのルートからの相対パス。ルートで go run _cmd/version.go として実行する
const (
	versionFile = "./_cmd/aibow/version"
	configYml   = "./_cmd/aibow/build/config.yml"
	packJsn     = "./_cmd/aibow/frontend/package.json"
)

// subVersionFiles は、ほかの exe(aibow-mcp・aibow-api)が埋め込むバージョン。version の写しで、手で書き換えない
var subVersionFiles = []string{
	"./_cmd/aibow-mcp/version",
	"./_cmd/aibow-api/version",
}

// 値の部分だけを置き換え、インデントや行末コメントは残す。
// ⚠️ 行頭を空白のみに限定すること（後述「config.yml のコメント行を書き換えない」参照）
var (
	configRg = regexp.MustCompile(`^(\s*version:\s*")[0-9]+\.[0-9]+\.[0-9]+(".*)$`)
	packRg   = regexp.MustCompile(`^(\s*"version":\s*")[0-9]+\.[0-9]+\.[0-9]+(".*)$`)
)

const inquiry = `
Now Version: %s

  Enter   -> Patch Version
  1:Patch -> %s
  2:Minor -> %s
  3:Major -> %s
  Other   -> Cancel

Please select the upgrade version(1-3)[1]:`

type ver struct {
	major int
	minor int
	patch int
}

func parseVer(v string) *ver {
	rtn := ver{-1, -1, -1}
	vals := strings.Split(v, ".")
	if len(vals) == 3 {
		rtn.major = parseInt(vals[0])
		rtn.minor = parseInt(vals[1])
		rtn.patch = parseInt(vals[2])
	}
	return &rtn
}

func parseInt(v string) int {
	val, err := strconv.Atoi(v)
	if err != nil || val < 0 {
		return -1
	}
	return val
}

func (v ver) addMajor() *ver { return &ver{v.major + 1, 0, 0} }
func (v ver) addMinor() *ver { return &ver{v.major, v.minor + 1, 0} }
func (v ver) addPatch() *ver { return &ver{v.major, v.minor, v.patch + 1} }
func (v ver) String() string { return fmt.Sprintf("%d.%d.%d", v.major, v.minor, v.patch) }
func (v *ver) isError() bool {
	return v == nil || v.major < 0 || v.minor < 0 || v.patch < 0
}

var (
	bump     bool
	auto     bool
	printVer bool
)

func main() {
	flag.BoolVar(&bump, "bump", false, "対話的にバージョンを選択して更新")
	flag.BoolVar(&auto, "auto", false, "現在のバージョンのタグがあれば patch を上げる")
	flag.BoolVar(&printVer, "print", false, "現在のバージョンを表示")
	flag.Parse()

	// -print: 他ファイルには触れず現在値を出力するだけ（CIやシェルスクリプトからの参照用）
	if printVer {
		v, err := parseVersion()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %+v\n", err)
			os.Exit(1)
		}
		fmt.Println(v)
		return
	}

	if err := run(flag.Args()); err != nil {
		fmt.Fprintf(os.Stderr, "run() error: %+v\n", err)
		os.Exit(1)
	}
	fmt.Println("Success")
}

func run(args []string) error {
	now, err := parseVersion()
	if err != nil {
		return err
	}

	var rtn *ver
	if auto {
		// タグがある = そのバージョンはデプロイ済みなのに version が更新されていない
		rtn, err = autoVersion(now)
		if err != nil {
			return err
		}
		if rtn == nil {
			fmt.Println("Version:", now)
			return write(now)
		}
	} else if len(args) == 0 && !bump {
		// 引数なし・フラグなし: 現在値で同期
		fmt.Println("Version:", now)
		return write(now)
	} else if bump {
		rtn = inquiryVersion(now)
	} else {
		rtn = parseVer(args[0])
	}
	if rtn.isError() {
		return fmt.Errorf("input version error")
	}

	fmt.Println("Version:", rtn)

	// 他ファイルを先に書き換え、全て成功してから正である version ファイルを更新する
	if err := write(rtn); err != nil {
		return err
	}
	// ★ 複数コマンドがある場合はここに追加
	for _, f := range []string{versionFile} {
		if err := os.WriteFile(f, []byte(rtn.String()+"\n"), 0644); err != nil {
			return err
		}
		fmt.Println("Write:", f)
	}
	return nil
}

func inquiryVersion(now *ver) *ver {
	major := now.addMajor()
	minor := now.addMinor()
	patch := now.addPatch()
	fmt.Fprintf(os.Stdout, inquiry, now, patch, minor, major)
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	switch strings.TrimSpace(scanner.Text()) {
	case "", "1":
		return patch
	case "2":
		return minor
	case "3":
		return major
	}
	return &ver{-1, -1, -1}
}

// autoVersion は現在のバージョンのタグがあれば patch を上げたバージョンを返す。
// タグが無ければ nil（そのまま使う）。
// 上げた先のタグもすでにあるときは、version とタグがずれているので止める
// （そのまま進めると古いコミットを指すタグが残り、新しいビルドにタグが付かない）。
func autoVersion(now *ver) (*ver, error) {
	exists, err := tagExists(now)
	if err != nil || !exists {
		return nil, err
	}
	next := now.addPatch()
	fmt.Printf("Tag v%s exists: bump to %s\n", now, next)

	exists, err = tagExists(next)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, fmt.Errorf("tag v%s already exists: fix %s by hand", next, versionFile)
	}
	return next, nil
}

func tagExists(v *ver) (bool, error) {
	tag := "v" + v.String()
	out, err := exec.Command("git", "tag", "--list", tag).Output()
	if err != nil {
		return false, fmt.Errorf("git tag --list %s: %w", tag, err)
	}
	return strings.TrimSpace(string(out)) == tag, nil
}

func parseVersion() (*ver, error) {
	data, err := os.ReadFile(versionFile)
	if err != nil {
		return nil, err
	}
	v := parseVer(strings.TrimSpace(string(data)))
	if v.isError() {
		return nil, fmt.Errorf("invalid version in %s: %q", versionFile, strings.TrimSpace(string(data)))
	}
	return v, nil
}

type op struct {
	path string
	rg   *regexp.Regexp
}

func write(v *ver) error {
	// ★ 複数コマンドがある場合は ops にエントリを追加する
	ops := []op{
		{configYml, configRg},
		{packJsn, packRg},
		// {subConfigYml, configRg},
		// {subPackJsn, packRg},
	}

	// 全ファイルの置換結果を用意してから書き込む（途中で失敗しても中途半端に残さない）
	outs := make([][]byte, len(ops))
	for i, o := range ops {
		out, err := replaceVersion(o, v)
		if err != nil {
			return err
		}
		outs[i] = out
	}
	for i, o := range ops {
		if err := os.WriteFile(o.path, outs[i], 0644); err != nil {
			return err
		}
		fmt.Println("Write:", o.path)
	}
	// 写しのバージョンのファイルは丸ごと書く(引数なしで同期したときにも、ずれていればそろう)
	for _, f := range subVersionFiles {
		if err := os.WriteFile(f, []byte(v.String()+"\n"), 0644); err != nil {
			return err
		}
		fmt.Println("Write:", f)
	}
	return nil
}

// replaceVersion は最初にマッチした1行だけを置き換える。
// 改行コード（CRLF/LF）は元ファイルのまま保つ。
func replaceVersion(o op, v *ver) ([]byte, error) {
	data, err := os.ReadFile(o.path)
	if err != nil {
		return nil, err
	}
	lines := strings.SplitAfter(string(data), "\n")
	for i, line := range lines {
		body := strings.TrimRight(line, "\r\n")
		if !o.rg.MatchString(body) {
			continue
		}
		eol := line[len(body):]
		lines[i] = o.rg.ReplaceAllString(body, "${1}"+v.String()+"${2}") + eol
		return []byte(strings.Join(lines, "")), nil
	}
	return nil, fmt.Errorf("version line not found: %s", o.path)
}
