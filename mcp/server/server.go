// Package server は、aibow のツール(tool パッケージ)を MCP のサーバーとして出す(ステップ5。仕様は _docs/features/0007-mcp-server.md)。
//
// ステップ4の MCP クライアント(mcp/client)の反対側。クライアントから届く tools/list に定義の一覧を、
// tools/call に実行の結果を返す。やり取りの決まりごと(JSON-RPC・版の取り決め・server/discover など)は公式の Go SDK に任せ、
// ここは「tool のツールを MCP のツールの形にする」ことだけを書く。
//
// このパッケージは tool と SDK だけを使い、LLM・会話の仕組み・画面(ルートの aibow や Wails)を import しない。
// 標準入出力で動かす exe(_cmd/aibow-mcp)はここを起動するだけで、中身はここにある。
// main に書かないのは、テストでプロセスを起動せずにつなぐため・aibow の中でメモリ内のつなぎ方で動かすため(5-4)。
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/secondarykey/aibow/log"
	"github.com/secondarykey/aibow/tool"
)

// Name はサーバーの名前(MCP の serverInfo.name)。クライアントのログに出る。
const Name = "aibow-mcp"

// defaultTimeLimit は、上限を自分で決めないツール(tool.TimeLimiter でないもの)の時間の上限。
// aibow の会話の仕組み(agent.go の toolTimeout)と同じ 10 秒にする。
// クライアントの側にも上限はあるが、Claude Code などほかのクライアントがどうするかは分からないので、サーバーの側でも止める
const defaultTimeLimit = 10 * time.Second

// Options はサーバーの設定。
type Options struct {
	// WorkDir は作業フォルダ(絶対パス)。ファイルのツールとコマンドのツールは、この中だけを扱う。
	// 空なら作業フォルダを使うツールを出さず、組み込みのツール(時刻・計算)だけを出す
	WorkDir string
	// Version はサーバーの版(MCP の serverInfo.version)。空なら "(devel)"
	Version string
}

// New は、tool のツールを出す MCP のサーバーを作る。動かすのは呼ぶ側(Run に標準入出力やメモリ内のトランスポートを渡す)。
func New(opts Options) *mcp.Server {
	version := opts.Version
	if version == "" {
		version = "(devel)"
	}
	s := mcp.NewServer(&mcp.Implementation{Name: Name, Version: version}, &mcp.ServerOptions{
		Instructions: instructions(opts.WorkDir),
	})
	for _, t := range Tools(opts.WorkDir) {
		s.AddTool(mcpTool(t), handler(t))
	}
	return s
}

// Tools はサーバーが出すツール。tools/list の並びは SDK が名前の順にする(ここで並べた順ではない)。
func Tools(workDir string) []tool.Tool {
	tools := tool.Builtin()
	if workDir != "" {
		tools = append(tools, tool.Files(workDir)...)
		tools = append(tools, tool.RunCommand(workDir))
	}
	return tools
}

// instructions は、つないだクライアントに渡す説明(MCP の instructions)。
//
// run_command の説明は「Environment の節に書いたシェルで動かす」と言っている。aibow はシステムプロンプトにその節を書くが、
// ほかのクライアント(Claude Code など)は書かないので、同じことをここで伝える。クライアントはふつう、これをモデルに見せる
func instructions(workDir string) string {
	if workDir == "" {
		return ""
	}
	return "# Environment\n" +
		"- Working directory: " + workDir + " (file paths are relative to it)\n" +
		"- Shell for run_command: " + tool.ShellNote + "\n"
}

// mcpTool は tool の定義を MCP のツールの定義にする。
//
// 名前・説明・引数の JSON Schema は、aibow の組み込みとまったく同じ(4B が書きやすい形のまま出せるのが、自分で作るサーバーの良いところ)。
// 確認の要らないツール(読むだけのツール)には「読むだけ」の印(readOnlyHint)を付ける。
// これはクライアントへのヒントで、MCP の仕様でも「信じてはいけない」とされている(aibow はまだ信じない。0007 の決定事項 9)
func mcpTool(t tool.Tool) *mcp.Tool {
	def := t.Def()
	m := &mcp.Tool{
		Name:        def.Name,
		Description: def.Description,
		InputSchema: def.Parameters,
	}
	if _, confirmable := t.(tool.Confirmable); !confirmable {
		m.Annotations = &mcp.ToolAnnotations{ReadOnlyHint: true}
	}
	return m
}

// handler は、ツール1つの tools/call を受けて実行する。
//
// ツールが失敗したとき(ファイルが無い、引数が違う、など)は、Go の error ではなく isError の結果で返す。
// MCP では、error は「呼び出しそのものが成り立たなかった」(知らないツールなど)ときに使い、
// ツールの失敗はモデルに読ませて立て直させるため、結果の中身として返す決まり(aibow の「エラー: …」と同じ考え方)
func handler(t tool.Tool) mcp.ToolHandler {
	def := t.Def()
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, err := toArgs(def, req.Params.Arguments)
		if err != nil {
			log.Infof("ツール %s: 引数が違います: %v", def.Name, err)
			return errorResult(err), nil
		}
		if ct, ok := t.(tool.Confirmable); ok {
			// 書き換えるツールは、ユーザーに確かめてから実行する(confirm.go)
			run, res := confirmed(req, ct, args)
			if !run {
				logConfirm(def.Name, res)
				return res, nil
			}
			log.Infof("ツール %s: ユーザーが実行を認めました", def.Name)
		}
		start := time.Now()
		text, err := run(ctx, t, args)
		secs := time.Since(start).Seconds()
		if err != nil {
			log.Infof("ツール %s: 失敗しました(%.2f 秒): %v", def.Name, secs, err)
			return errorResult(err), nil
		}
		log.Infof("ツール %s: 実行しました(%.2f 秒・結果 %d 文字)", def.Name, secs, len([]rune(text)))
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil
	}
}

// run はツールを時間の上限つきで実行する。
//
// ctx は SDK がリクエストごとに作るもので、クライアントが止める知らせ(notifications/cancelled)を送ると止まる。
// ツールは ctx が止まったら後片付けをして返す約束(run_command は子プロセスまでまとめて止める)なので、それを待って返す
func run(ctx context.Context, t tool.Tool, args tool.Args) (string, error) {
	limit := defaultTimeLimit
	if l, ok := t.(tool.TimeLimiter); ok {
		limit = l.TimeLimit()
	}
	tctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	text, err := t.Run(tctx, args)
	if err != nil && ctx.Err() != nil {
		return "", errors.New("停止されました")
	}
	if err != nil && errors.Is(tctx.Err(), context.DeadlineExceeded) {
		return "", fmt.Errorf("時間の上限(%d 秒)を超えたため止めました", int(limit.Seconds()))
	}
	return text, err
}

// toArgs は MCP で届いた引数(JSON のオブジェクト)を tool.Args にする。
//
// 確かめるのは tool.CheckArgs に任せる(知らない引数・足りない引数・型の違いを、小さいモデルが直しやすい文で返す。組み込みと同じ文になる)。
// CheckArgs は Qwen3.5 の書式に合わせて「値は文字列で届く」前提なので、JSON の値をいったん文字列に戻して渡す:
// 文字列はそのまま、それ以外(数・真偽値・配列・オブジェクト)は JSON の書き方のまま。CheckArgs が JSON Schema の型を見て元に戻す。
// 値は読み直さず、届いた JSON の書き方のまま渡す(数を float64 で読むと、大きな整数の桁が落ちたり 1e+06 の形になったりするため)
func toArgs(def tool.Def, raw json.RawMessage) (tool.Args, error) {
	values := map[string]json.RawMessage{}
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &values); err != nil {
			return nil, fmt.Errorf("引数は JSON のオブジェクトで渡してください: %w", err)
		}
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	slices.Sort(names) // いつも同じ順にする(エラーの文が毎回変わらないように)

	var args []tool.Arg
	for _, name := range names {
		v := values[name]
		if strings.TrimSpace(string(v)) == "null" {
			continue // null は「書かなかった」として扱う(省略できる引数に null を入れてくるクライアントがある)
		}
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			s = string(v) // 文字列ではない値は、JSON の書き方のまま
		}
		args = append(args, tool.Arg{Name: name, Value: s})
	}
	return tool.CheckArgs(def, args)
}

// errorResult はツールの失敗を、モデルに読ませる結果(isError)にする。
func errorResult(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
	}
}
