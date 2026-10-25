// dev: aibow-server を開発で動かす・exe を作るスクリプト(0021)。
//
//	go run ./_cmd/dev server [aibow-server の引数...]   画面は vite の開発用サーバーから配り(保存するとブラウザにすぐ出る)、aibow-server は go run で動かす
//	go run ./_cmd/dev build [-o 出力先]                 画面をビルドしてから aibow-server の exe を作る
//
// aibow-server は画面(_cmd/aibow/frontend のビルド結果 web/dist/app)を exe に埋め込む。そのため「先に画面をビルドしてから go run / go build」の2段になり、
// 画面のビルドを忘れると古い画面が配られる(2026-10-26、「利用者」のタブが出なかった)。この2つのコマンドは、それを1つにまとめる。
//
//	server … vite の開発用サーバー(127.0.0.1:9246)── /aibow/ の呼び出し ──→ aibow-server(go run。既定 127.0.0.1:8089)
//	         ブラウザは vite のほうを開く。vite がブラウザの印を index.html に足し、/aibow/ を aibow-server に渡す(vite.config.ts。aibow-server は何も変えない)
//	build  … npm run build(web/dist/app に出る)→ go build ./_cmd/aibow-server
//
// リポジトリのルートで実行する。要るのは Go と Node.js(npm)。node_modules が無ければ先に npm install する。
// _cmd/version.go と同じく、ルートのモジュールに属し、標準ライブラリ(と aibow の internal/proc)だけを使う。
package main

import (
	"cmp"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/secondarykey/aibow/internal/proc"
)

// パスはリポジトリのルートからの相対パス
const (
	frontendDir = "_cmd/aibow/frontend"
	serverPkg   = "./_cmd/aibow-server"
	webIndex    = "web/dist/app/index.html"
	// vitePort は開発用サーバーのポート。画面のアプリの wails3 dev(9245)と同時に動かせるよう、別にする
	vitePort = 9246
)

const usage = `使い方(リポジトリのルートで):
  go run ./_cmd/dev server [aibow-server の引数...]   画面を vite の開発用サーバーから配って aibow-server を動かす
  go run ./_cmd/dev build [-o 出力先]                 画面をビルドしてから aibow-server の exe を作る`

func main() {
	if len(os.Args) < 2 {
		fail(errors.New(usage))
	}
	if _, err := os.Stat(filepath.Join(frontendDir, "package.json")); err != nil {
		fail(fmt.Errorf("%s が見つかりません。リポジトリのルートで実行してください", frontendDir))
	}
	var err error
	switch os.Args[1] {
	case "server":
		err = runServer(os.Args[2:])
	case "build":
		err = runBuild(os.Args[2:])
	default:
		err = fmt.Errorf("知らないコマンドです: %s\n%s", os.Args[1], usage)
	}
	if err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "dev:", err)
	os.Exit(1)
}

// ---- server -----------------------------------------------------------------------------

// runServer は vite の開発用サーバーと aibow-server を一緒に動かし、どちらかが終わったらもう片方も止める。
func runServer(args []string) error {
	if err := ensureDeps(); err != nil {
		return err
	}
	target := serverAddress(args)
	setDefaultPaths()

	// vite:AIBOW_BROWSER_DEV があるときだけ、ブラウザの印と /aibow/ の受け渡しが効く(vite.config.ts)
	vite := npm("run", "dev", "--", "--port", strconv.Itoa(vitePort), "--strictPort")
	vite.Env = append(os.Environ(), "AIBOW_BROWSER_DEV="+target)
	viteTree, err := proc.Start(vite)
	if err != nil {
		return fmt.Errorf("vite の開発用サーバーを起動できません: %w", err)
	}
	defer viteTree.Close()

	server := exec.Command("go", append([]string{"run", serverPkg}, args...)...)
	server.Stdout, server.Stderr, server.Stdin = os.Stdout, os.Stderr, os.Stdin
	serverTree, err := proc.Start(server)
	if err != nil {
		viteTree.Kill()
		return fmt.Errorf("aibow-server を起動できません: %w", err)
	}
	defer serverTree.Close()

	fmt.Printf("dev: 画面は vite の開発用サーバーから配ります。ブラウザで http://127.0.0.1:%d/ を開いてください(/aibow/ の呼び出しは %s に渡します)\n", vitePort, target)
	fmt.Printf("dev: OpenAI 互換の口(Cline など)と画面のアプリは、今までどおり %s につなぎます。止めるときは Ctrl+C\n", target)

	// Ctrl+C は同じコンソールの子にも届く(aibow-server は自分で後片付けして終わる)。ここでは受け取って、子が終わるのを待つ
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	done := make(chan string, 2)
	go func() { server.Wait(); done <- "aibow-server" }()
	go func() { vite.Wait(); done <- "vite" }()

	select {
	case who := <-done:
		fmt.Printf("dev: %s が終わったので、もう片方も止めます\n", who)
	case <-sig:
		// aibow-server の後片付け(生成を止める・DB を閉じる)を少し待つ
		select {
		case <-done:
		case <-time.After(15 * time.Second):
		}
	}
	serverTree.Kill()
	viteTree.Kill()
	return nil
}

// serverAddress は、vite が /aibow/ を渡す aibow-server のアドレス。引数 > 設定ファイル > 既定(aibow-server の決め方と同じ順)。
// 環境変数(AIBOW_SERVER_PORT など)も見る。全部のネットワークで待ち受けるとき(0.0.0.0 など)も、渡すのはこの PC の 127.0.0.1
func serverAddress(args []string) string {
	fs := flag.NewFlagSet("aibow-server", flag.ContinueOnError)
	fs.SetOutput(nopWriter{})
	config := fs.String("config", "", "")
	port := fs.Int("port", 0, "")
	host := fs.String("host", "", "")
	tlsCert := fs.String("tls-cert", "", "")
	// aibow-server のほかの引数は読み飛ばす(値を取るものも、取らないものもある)
	for _, name := range []string{"model", "lib", "models", "tls-key"} {
		fs.String(name, "", "")
	}
	for _, name := range []string{"ctx"} {
		fs.Int(name, 0, "")
	}
	for _, name := range []string{"login", "debug", "version"} {
		fs.Bool(name, false, "")
	}
	fs.Parse(args)

	var file struct {
		Port    int    `json:"port"`
		Host    string `json:"host"`
		TLSCert string `json:"tlsCert"`
	}
	path := *config
	if path == "" {
		if dir, err := os.UserConfigDir(); err == nil {
			path = filepath.Join(dir, "aibow", "server.json")
		}
	}
	if data, err := os.ReadFile(path); err == nil {
		json.Unmarshal(data, &file) // 読めなければ aibow-server が理由を出して止まる
	}
	envPort, _ := strconv.Atoi(os.Getenv("AIBOW_SERVER_PORT"))
	p := cmp.Or(*port, file.Port, envPort, 8089)
	h := cmp.Or(*host, file.Host, os.Getenv("AIBOW_SERVER_HOST"), "127.0.0.1")
	if ip := net.ParseIP(h); ip != nil && ip.IsUnspecified() {
		h = "127.0.0.1"
	}
	scheme := "http"
	if cmp.Or(*tlsCert, file.TLSCert, os.Getenv("AIBOW_SERVER_TLS_CERT")) != "" {
		scheme = "https"
	}
	return scheme + "://" + net.JoinHostPort(h, strconv.Itoa(p))
}

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }

// setDefaultPaths は、llama.cpp とモデルの場所をどこにも書いていなければ、画面のアプリと同じ _cmd/aibow/lib・_cmd/aibow/models にする。
// 環境変数で渡す(aibow-server では引数 > 設定ファイル > 環境変数 なので、引数や server.json に書いた値が勝つ)。
func setDefaultPaths() {
	for key, dir := range map[string]string{"AIBOW_LIB": "_cmd/aibow/lib", "AIBOW_MODELS_DIR": "_cmd/aibow/models"} {
		if os.Getenv(key) == "" {
			os.Setenv(key, dir)
		}
	}
}

// ---- build ------------------------------------------------------------------------------

// runBuild は画面をビルドしてから aibow-server の exe を作る。
func runBuild(args []string) error {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	def := "aibow-server"
	if runtime.GOOS == "windows" {
		def += ".exe"
	}
	out := fs.String("o", def, "作る exe のパス")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := ensureDeps(); err != nil {
		return err
	}
	fmt.Println("dev: 画面をビルドします(npm run build → web/dist/app)")
	if err := run(npm("run", "build")); err != nil {
		return fmt.Errorf("画面のビルドに失敗しました: %w", err)
	}
	st, err := os.Stat(webIndex)
	if err != nil {
		return fmt.Errorf("画面のビルド結果 %s がありません: %w", webIndex, err)
	}
	fmt.Printf("dev: aibow-server をビルドします(画面: %s)\n", st.ModTime().Format("2006-01-02 15:04:05"))
	if err := run(exec.Command("go", "build", "-o", *out, serverPkg)); err != nil {
		return fmt.Errorf("aibow-server のビルドに失敗しました: %w", err)
	}
	abs, _ := filepath.Abs(*out)
	fmt.Println("dev: できました:", abs)
	return nil
}

// ---- 共通 -------------------------------------------------------------------------------

// ensureDeps は、画面のビルドに要る npm のパッケージが無ければ入れる。
func ensureDeps() error {
	if _, err := os.Stat(filepath.Join(frontendDir, "node_modules")); err == nil {
		return nil
	}
	if _, err := exec.LookPath("npm"); err != nil {
		return errors.New("npm が見つかりません。Node.js(20.19 以上)を入れてください")
	}
	fmt.Println("dev: 画面のパッケージを入れます(npm install)")
	if err := run(npm("install")); err != nil {
		return fmt.Errorf("npm install に失敗しました: %w", err)
	}
	return nil
}

// npm は frontend で npm を動かすコマンド。
func npm(args ...string) *exec.Cmd {
	cmd := exec.Command("npm", args...)
	cmd.Dir = frontendDir
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd
}

// run はコマンドを動かし、終わるまで待つ。
func run(cmd *exec.Cmd) error {
	if cmd.Stdout == nil {
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	}
	fmt.Println("dev: >", strings.Join(cmd.Args, " "))
	return cmd.Run()
}
