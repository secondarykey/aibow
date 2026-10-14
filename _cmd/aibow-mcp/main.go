// aibow-mcp は、aibow のツール(ファイルの読み書き・コマンドなど)を MCP のサーバーとして出すプログラム(ステップ5)。
//
//	aibow-mcp [作業フォルダ]
//
// クライアント(aibow・Claude Code など)が子プロセスとして起動し、標準入出力で1行ずつ JSON を送り合う(stdio のトランスポート)。
// 標準出力は MCP のメッセージ専用なので、ログは標準エラー出力に書く(クライアントがそれを自分のログに取り込む)。
//
// 中身は mcp/server にあり、ここは引数を読んで起動するだけ(_cmd/aibow と、中身のルートの aibow の関係と同じ)。
// 画面のアプリ(Wails)とは別の exe にしているのは、Windows の画面のアプリは標準入出力を持たない GUI のプログラムで、
// 標準入出力で話すサーバーに向かないため。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/secondarykey/aibow/log"
	"github.com/secondarykey/aibow/mcp/server"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "使い方: %s [作業フォルダ]\n\n", filepath.Base(os.Args[0]))
		fmt.Fprintln(flag.CommandLine.Output(), "aibow のツールを MCP のサーバー(標準入出力)として出します。")
		fmt.Fprintln(flag.CommandLine.Output(), "作業フォルダを渡すと、その中のファイルを扱うツールとコマンドのツールも出します。")
	}
	flag.Parse()

	// ログは標準エラー出力だけに書く(標準出力に何か書くと、クライアントが MCP のメッセージとして読もうとして壊れる)
	closer, err := log.Init(log.Options{Console: true, Root: log.LevelInfo})
	defer closer.Close()
	if err != nil {
		log.Warn(err.Error())
	}

	if err := run(); err != nil {
		log.Error(err.Error())
		closer.Close()
		os.Exit(1)
	}
}

func run() error {
	if flag.NArg() > 1 {
		flag.Usage()
		return fmt.Errorf("引数は作業フォルダ1つだけです(%d 個渡されました)", flag.NArg())
	}
	dir, err := workDir(flag.Arg(0))
	if err != nil {
		return err
	}

	s := server.New(server.Options{WorkDir: dir, Version: version()})
	names := make([]string, 0)
	for _, t := range server.Tools(dir) {
		names = append(names, t.Def().Name)
	}
	where := dir
	if where == "" {
		where = "なし(作業フォルダのツールは出しません)"
	}
	log.Noticef("%s %s を起動しました(作業フォルダ: %s・ツール %d 個: %v)", server.Name, version(), where, len(names), names)

	// Ctrl+C で止める(ターミナルで試すとき用)。ふだんはクライアントが標準入力を閉じると Run が返る
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := s.Run(ctx, &mcp.StdioTransport{}); err != nil && ctx.Err() == nil {
		return fmt.Errorf("MCP のやり取りが止まりました: %w", err)
	}
	log.Noticef("%s を終了します", server.Name)
	return nil
}

// workDir は、引数の作業フォルダを確かめて絶対パスにする。空なら空のまま(作業フォルダを使わない)。
// 相対パスは、起動したときのカレントフォルダからの相対(クライアントの多くはサーバーをプロジェクトのフォルダで起動する)
func workDir(arg string) (string, error) {
	if arg == "" {
		return "", nil
	}
	abs, err := filepath.Abs(arg)
	if err != nil {
		return "", fmt.Errorf("作業フォルダ %s の場所が分かりません: %w", arg, err)
	}
	st, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("作業フォルダ %s が開けません: %w", abs, err)
	}
	if !st.IsDir() {
		return "", fmt.Errorf("作業フォルダ %s はフォルダではありません", abs)
	}
	return abs, nil
}

// version は、この exe の版。go install …@v1.2.3 で入れたならその版、手元でビルドしたなら Go が付けたコミットの版(無ければ "(devel)")。
func version() string {
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
		return bi.Main.Version
	}
	return "(devel)"
}
