package mcpclient

// remote.go: MCP のサーバーのツールを tool.Tool として見せる部分。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"aibow/tool"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// callTimeout はツール1回の実行の時間の上限(組み込みのツールの 10 秒より長め。サーバーの中で何をするか分からないため)
	callTimeout = 60 * time.Second
	// resultMaxChars はモデルに渡す結果の長さの上限(read_file と同じ)。
	// 組み込みのツールは自分で切るが、MCP のサーバーは切らずに返してくることがある(filesystem の directory_tree など)
	resultMaxChars = 8000
)

// remoteTool は MCP のサーバーのツール1つ。
//
// 実行する前に、いつも確認する(tool.Confirmable)。中身が見えないので、読むだけのツールかどうかは分からない。
// サーバーは「読むだけ」(readOnlyHint)などの印を付けられるが、MCP の仕様でも「信じてはいけないヒント」とされている。
// 信頼して確認を省く仕組みは、あとで作る(TODO.md のロードマップの外)。
type remoteTool struct {
	server  string
	session *mcp.ClientSession
	def     tool.Def
}

var (
	_ tool.Remote      = (*remoteTool)(nil)
	_ tool.Confirmable = (*remoteTool)(nil)
	_ tool.TimeLimiter = (*remoteTool)(nil)
)

// newRemoteTool は、tools/list で受け取ったツールの定義を tool.Def にする。
//
// inputSchema(引数の JSON Schema)は、0004 の tool.Def の引数と同じ形なので、そのまま使える。
// ただし SDK は inputSchema を map[string]any で渡してくるので、サーバーが書いた順(type → properties → required など)は残らず、
// JSON にし直すとキーの名前の順になる。モデルが読むには困らない。
func newRemoteTool(server string, session *mcp.ClientSession, t *mcp.Tool) (*remoteTool, error) {
	schema := t.InputSchema
	if schema == nil {
		schema = map[string]any{"type": "object", "properties": map[string]any{}}
	}
	params, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("ツール %s の inputSchema を読めません: %w", t.Name, err)
	}
	return &remoteTool{
		server: server, session: session,
		def: tool.Def{Name: t.Name, Description: t.Description, Parameters: params},
	}, nil
}

func (t *remoteTool) Def() tool.Def            { return t.def }
func (t *remoteTool) Server() string           { return t.server }
func (t *remoteTool) TimeLimit() time.Duration { return callTimeout }
func (t *remoteTool) Preview(args tool.Args) (tool.Preview, error) {
	b, err := json.MarshalIndent(args, "", "  ")
	if err != nil {
		return tool.Preview{}, err
	}
	return tool.Preview{Note: "MCP", Server: t.server, Args: string(b)}, nil
}

// Run は tools/call でサーバーにツールを実行させる。
//
// 時間切れ・停止のときは、SDK が取り消しの知らせ(notifications/cancelled)をサーバーに送って、すぐに返ってくる。
func (t *remoteTool) Run(ctx context.Context, args tool.Args) (string, error) {
	if args == nil {
		args = tool.Args{}
	}
	res, err := t.session.CallTool(ctx, &mcp.CallToolParams{Name: t.def.Name, Arguments: map[string]any(args)})
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return "", fmt.Errorf("時間の上限(%d 秒)を超えたため止めました", int(callTimeout.Seconds()))
	case ctx.Err() != nil:
		return "", errors.New("停止されました")
	case err != nil:
		// プロトコルのエラー(知らないツール・引数の形が違う、など)や、サーバーが落ちていたとき
		return "", fmt.Errorf("MCP のサーバー %s で実行できません: %w", t.server, err)
	}
	text := truncate(resultText(res.Content))
	if res.IsError {
		// ツールの中のエラー(ファイルが無い、など)。モデルに読ませて立て直させる
		return "", errors.New(text)
	}
	return text, nil
}

// resultText は、結果の content(文章・画像など、いくつかの部分)を、モデルに渡す1つの文章にする。
// aibow のモデルは画像を読めない(ロードマップの外の「画像を読む」)ので、文章以外は種類だけを書く。
func resultText(content []mcp.Content) string {
	parts := make([]string, 0, len(content))
	for _, c := range content {
		switch c := c.(type) {
		case *mcp.TextContent:
			parts = append(parts, c.Text)
		case *mcp.ImageContent:
			parts = append(parts, fmt.Sprintf("(画像 %s は渡せません)", c.MIMEType))
		case *mcp.AudioContent:
			parts = append(parts, fmt.Sprintf("(音声 %s は渡せません)", c.MIMEType))
		case *mcp.ResourceLink:
			parts = append(parts, fmt.Sprintf("(リンク: %s)", c.URI))
		case *mcp.EmbeddedResource:
			if c.Resource != nil && c.Resource.Text != "" {
				parts = append(parts, c.Resource.Text)
			} else if c.Resource != nil {
				parts = append(parts, fmt.Sprintf("(リソース %s は渡せません)", c.Resource.URI))
			}
		default:
			parts = append(parts, fmt.Sprintf("(%T は渡せません)", c))
		}
	}
	return strings.Join(parts, "\n")
}

// truncate は長すぎる結果を切る。切ったことを書き添えて、モデルが範囲を絞って呼び直せるようにする。
func truncate(s string) string {
	n := utf8.RuneCountInString(s)
	if n <= resultMaxChars {
		return s
	}
	r := []rune(s)
	return string(r[:resultMaxChars]) + fmt.Sprintf("\n…(結果が長いため、%d 文字のうち先頭の %d 文字だけを渡しています。範囲を絞って呼び直してください)", n, resultMaxChars)
}
