package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/secondarykey/aibow/mcp/server"
	"github.com/secondarykey/aibow/tool"
)

// shout は、ほかのアプリが自分で書くツールの見本(tool.Tool を満たすだけ)。
type shout struct{}

func (shout) Def() tool.Def {
	return tool.Def{
		Name:        "shout",
		Description: "Return the text in upper case.",
		Parameters:  json.RawMessage(`{"type": "object", "properties": {"text": {"type": "string"}}, "required": ["text"]}`),
	}
}

func (shout) Run(_ context.Context, args tool.Args) (string, error) {
	return strings.ToUpper(args.String("text")), nil
}

// ReadOnly は「読むだけ」の印(readOnlyHint)を付けるための宣言(tool.Hinted)。Custom のツールは、宣言しなければ印が付かない。
func (shout) ReadOnly() bool { return true }

// 自分のツールだけを出す MCP のサーバーを作り、SDK のクライアントからつなぐ。
//
// ここではテストで出力を確かめるため、メモリ内のつなぎ方(mcp.NewInMemoryTransports)でつなぐ。
// exe として動かすときは s.Run(ctx, &mcp.StdioTransport{})、画面のアプリなら mcp.NewStreamableHTTPHandler で包む(_docs/mcp.md の「ほかのアプリから使う」)。
func ExampleNew() {
	opts := server.Options{
		Name:         "shouter",
		Version:      "v0.1.0",
		Instructions: "Use shout to make text loud.",
		Custom:       []tool.Tool{shout{}},
	}
	if err := opts.Check(); err != nil { // New は誤りがあると panic するので、先に確かめておく
		fmt.Println(err)
		return
	}
	s := server.New(opts)

	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "client", Version: "v0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer cs.Close()

	init := cs.InitializeResult()
	fmt.Println("server:", init.ServerInfo.Name, init.ServerInfo.Version)
	fmt.Println("instructions:", init.Instructions)

	list, err := cs.ListTools(ctx, nil)
	if err != nil {
		fmt.Println(err)
		return
	}
	for _, t := range list.Tools {
		fmt.Printf("tool: %s (readOnly %v)\n", t.Name, t.Annotations != nil && t.Annotations.ReadOnlyHint)
	}

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "shout", Arguments: map[string]any{"text": "hello"}})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("result:", res.Content[0].(*mcp.TextContent).Text, "isError", res.IsError)

	// 引数が足りないなどのツールの失敗は、呼び出しのエラーでなく isError の結果で返る(モデルに読ませて立て直させる)
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "shout", Arguments: map[string]any{}})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("result:", res.Content[0].(*mcp.TextContent).Text, "isError", res.IsError)

	// Output:
	// server: shouter v0.1.0
	// instructions: Use shout to make text loud.
	// tool: shout (readOnly true)
	// result: HELLO isError false
	// result: 必須の引数 text がありません isError true
}
