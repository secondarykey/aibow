//go:build ignore

// install.go: aibow の exe を全部ビルドするスクリプト。
//
//	go run _cmd/install.go
//
// 画面のアプリ(aibow)・aibow-server・aibow-mcp の3つを、画面から順に作って _cmd/aibow/bin に置く。
// 順番に意味がある:
//
//  1. 画面のパッケージ(node_modules)が無ければ npm install する
//  2. wails3 build … バインディングの生成 → 画面のビルド(web/dist/app)→ 画面のアプリの exe
//  3. aibow-server … 2 の画面(web/dist/app)を埋め込むので、必ず 2 の後に作る(先に作ると古い画面が入る)
//  4. aibow-mcp
//
// 3 と 4 は、リリース(.github/workflows/release.yml)と同じく C コンパイラなしで -trimpath -ldflags="-w -s" を付ける
// (コンソールのアプリなので -H windowsgui は付けない)。go build なので、名乗る版は version のまま(+dev は付かない)。
//
// バージョンの反映・コミット・インストール先へのコピーはしない。それは各自の Taskfile.local.yml(local:deploy・local:install)が受け持つ。
// macOS でも wails3 build(.app ではなく実行ファイル)にする。.app が要るときは _cmd/aibow で wails3 package を使う。
//
// リポジトリのルートで実行する。要るのは Go・Node.js(npm)・wails3。
// _cmd/version.go と同じフォルダに置く、このファイルだけで動くプログラム。
// 2つとも package main なので、ツール(go vet・gopls)が main の重複を言わないよう、go:build ignore で普段のビルドから外している(go run に直接渡せば動く)。
// 標準ライブラリだけを使う。
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// パスはリポジトリのルートからの相対パス
const (
	appDir      = "_cmd/aibow"
	frontendDir = "_cmd/aibow/frontend"
	binDir      = "_cmd/aibow/bin"
	webIndex    = "web/dist/app/index.html"
)

// tools は go build で作るコンソールのアプリ(パッケージと exe の名前)。画面を埋め込む aibow-server を先にする
var tools = []struct{ pkg, name string }{
	{"./_cmd/aibow-server", "aibow-server"},
	{"./_cmd/aibow-mcp", "aibow-mcp"},
}

func main() {
	if len(os.Args) > 1 {
		fail(errors.New("引数は取りません。リポジトリのルートで go run _cmd/install.go と打ってください"))
	}
	start := time.Now()
	if err := install(); err != nil {
		fail(err)
	}
	fmt.Printf("install: できました(%.0f 秒)\n", time.Since(start).Seconds())
}

func install() error {
	if err := checkRoot(); err != nil {
		return err
	}
	if err := checkTools(); err != nil {
		return err
	}
	if err := ensureDeps(); err != nil {
		return err
	}
	if err := buildApp(); err != nil {
		return err
	}
	exe, err := goExe()
	if err != nil {
		return err
	}
	var outs []string
	for _, t := range tools {
		out, err := buildTool(t.pkg, t.name+exe)
		if err != nil {
			return err
		}
		outs = append(outs, out)
	}
	report(append([]string{filepath.Join(binDir, "aibow"+exe)}, outs...))
	return nil
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "install:", err)
	os.Exit(1)
}

// checkRoot は、リポジトリのルートで動いているかを確かめる(パスを全部ルートからの相対で書いているため)。
func checkRoot() error {
	for _, p := range []string{"go.mod", filepath.Join(appDir, "Taskfile.yml"), filepath.Join(frontendDir, "package.json")} {
		if _, err := os.Stat(p); err != nil {
			return fmt.Errorf("%s が見つかりません。リポジトリのルートで go run _cmd/install.go と打ってください", p)
		}
	}
	return nil
}

// checkTools は、途中で止まらないよう、要るコマンドが揃っているかを先に確かめる。
func checkTools() error {
	if _, err := exec.LookPath("npm"); err != nil {
		return errors.New("npm が見つかりません。Node.js(20.19 以上)を入れてください")
	}
	if _, err := exec.LookPath("wails3"); err != nil {
		return errors.New("wails3 が見つかりません。README の「使ってみる」にある go install で Wails の CLI を入れてください")
	}
	return nil
}

// ensureDeps は、画面のビルドに要る npm のパッケージが無ければ入れる。
// wails3 build も node_modules が無ければ npm install するが、何が起きているか見えるよう、ここで先に行う
// (worktree のジャンクションのように、node_modules があれば何もしない。パッケージを増減したときは frontend で手で npm install する)。
func ensureDeps() error {
	if _, err := os.Stat(filepath.Join(frontendDir, "node_modules")); err == nil {
		return nil
	}
	step("画面のパッケージを入れます(npm install)")
	cmd := exec.Command("npm", "install")
	cmd.Dir = frontendDir
	if err := run(cmd); err != nil {
		return fmt.Errorf("npm install に失敗しました: %w", err)
	}
	return nil
}

// buildApp は画面のアプリを作る。wails3 build が、バインディングの生成・画面のビルド(web/dist/app)・exe までを行う。
func buildApp() error {
	step("画面と画面のアプリをビルドします(wails3 build)")
	cmd := exec.Command("wails3", "build")
	cmd.Dir = appDir
	if err := run(cmd); err != nil {
		return fmt.Errorf("画面のアプリのビルドに失敗しました: %w", err)
	}
	// aibow-server は web/dist/app を埋め込む。無ければ「画面がビルドされていません」のページが入ってしまうので、ここで止める
	if _, err := os.Stat(webIndex); err != nil {
		return fmt.Errorf("画面のビルド結果 %s がありません(wails3 build が画面を作っていません): %w", webIndex, err)
	}
	return nil
}

// buildTool はコンソールのアプリを bin に作る。リリースと同じ付け方にする(release.yml の「Build aibow-server and aibow-mcp」)。
func buildTool(pkg, file string) (string, error) {
	out := filepath.Join(binDir, file)
	step(fmt.Sprintf("%s をビルドします(go build)", strings.TrimPrefix(pkg, "./_cmd/")))
	cmd := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-ldflags=-w -s", "-o", out, pkg)
	// C コンパイラなしでビルドできる状態を保つ(AGENTS.md の技術方針)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if err := run(cmd); err != nil {
		return "", fmt.Errorf("%s のビルドに失敗しました: %w", file, err)
	}
	return out, nil
}

// goExe は exe の拡張子(Windows なら .exe)。GOOS を変えてビルドしたときに合わせるため、go env に聞く。
func goExe() (string, error) {
	out, err := exec.Command("go", "env", "GOEXE").Output()
	if err != nil {
		return "", fmt.Errorf("go env GOEXE を読めません: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// report は、できた exe とその大きさを並べる。
func report(files []string) {
	step("できた exe")
	for _, f := range files {
		st, err := os.Stat(f)
		if err != nil {
			fmt.Printf("  %s(見つかりません: %v)\n", f, err)
			continue
		}
		fmt.Printf("  %s(%.1f MB)\n", filepath.ToSlash(f), float64(st.Size())/1024/1024)
	}
}

func step(msg string) {
	fmt.Println("install:", msg)
}

// run はコマンドを動かし、出力をそのまま見せて、終わるまで待つ。
func run(cmd *exec.Cmd) error {
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	fmt.Println("install: >", strings.Join(cmd.Args, " "))
	return cmd.Run()
}
