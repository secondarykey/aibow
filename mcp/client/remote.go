package client

// remote.go: MCP のサーバーのツールを tool.Tool として見せる部分。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/secondarykey/aibow/log"
	"github.com/secondarykey/aibow/mcp/internal/meta"
	"github.com/secondarykey/aibow/tool"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// resultMaxChars はモデルに渡す結果の長さの上限(read_file と同じ)。
	// 組み込みのツールは自分で切るが、MCP のサーバーは切らずに返してくることがある(filesystem の directory_tree など)
	resultMaxChars = 8000
	// maxRounds は、1回の呼び出しで確認(MRTR)を往復する回数の上限。サーバーがいつまでも聞いてくるときに止める(SDK の自動の送り直しと同じ 10 回)
	maxRounds = 10
)

// remoteTool は MCP のサーバーのツール1つ。
//
// 実行する前に、いつも確認する(tool.Confirmable)。中身が見えないので、読むだけのツールかどうかは分からない。
// サーバーは「読むだけ」(readOnlyHint)などの印を付けられるが、MCP の仕様でも「信じてはいけないヒント」とされている。
// 信頼して確認を省く仕組みは、あとで作る(TODO.md のロードマップの外)。
type remoteTool struct {
	server  string
	session *mcp.ClientSession
	def     tool.Def      // モデルに見せる定義。名前は「サーバーの名前__ツールの名前」(ToolName)
	name    string        // サーバーが付けたツールの名前(tools/call にはこちらを送る)
	limit   time.Duration // 1回の実行の時間の上限(settings.json の callTimeout。サーバーごと)
	broken  func(error)   // つながりが切れたと分かったときに呼ぶ(Manager が一覧から外し、次の送信でつなぎ直す)
}

var (
	_ tool.Remote      = (*remoteTool)(nil)
	_ tool.Confirmable = (*remoteTool)(nil)
	_ tool.TimeLimiter = (*remoteTool)(nil)
	_ tool.Interactive = (*remoteTool)(nil)
)

// ToolName は、MCP のサーバーのツールをモデルに見せるときの名前。サーバーの名前を前に付ける(filesystem__read_file)。
//
// MCP の決まりごとには名前空間が無く、ツールの名前は「1つのサーバーの中で重ならない」ことしか決まっていない。
// モデルに見えるのはツールの名前の平らな一覧で、呼び出しも名前だけなので、サーバーが違っても名前が同じだと区別できない
// (filesystem サーバーの read_file と、組み込みの read_file)。そこで、クライアントの側でサーバーの名前を前に付けて、名前空間の代わりにする
// (Claude Code の mcp__<サーバー>__<ツール> と同じ考え方)。組み込みのツールには付けない。
//
// 区切りは __。ステップ6の OpenAI 互換 API では、ツールの名前に使える文字が英数字・_・- に限られるので、. は使わない。
// サーバーの名前のそれ以外の文字は _ に置き換える(ServerPrefix)。
// 名前が重なったときだけでなく、いつも付ける(サーバーを足したら別のツールの名前が変わる、ということが起きないように)。
func ToolName(server, name string) string {
	return ServerPrefix(server) + "__" + name
}

// ServerPrefix は、ツールの名前の前に付けるサーバーの名前(英数字・_・- 以外を _ にしたもの)。
func ServerPrefix(server string) string {
	return strings.Map(func(c rune) rune {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' {
			return c
		}
		return '_' // 日本語の名前なども1文字ずつ _ にする(重なったら settings.json を読むときにエラーにする)
	}, server)
}

// newRemoteTool は、tools/list で受け取ったツールの定義を tool.Def にする。
//
// inputSchema(引数の JSON Schema)は、0004 の tool.Def の引数と同じ形なので、そのまま使える。
// ただし SDK は inputSchema を map[string]any で渡してくるので、サーバーが書いた順(type → properties → required など)は残らず、
// JSON にし直すとキーの名前の順になる。モデルが読むには困らない。
func newRemoteTool(server string, limit time.Duration, session *mcp.ClientSession, t *mcp.Tool, broken func(error)) (*remoteTool, error) {
	schema := t.InputSchema
	if schema == nil {
		schema = map[string]any{"type": "object", "properties": map[string]any{}}
	}
	params, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("ツール %s の inputSchema を読めません: %w", t.Name, err)
	}
	return &remoteTool{
		server: server, session: session, broken: broken, limit: limit,
		def:  tool.Def{Name: ToolName(server, t.Name), Description: t.Description, Parameters: params},
		name: t.Name,
	}, nil
}

func (t *remoteTool) Def() tool.Def            { return t.def }
func (t *remoteTool) Server() string           { return t.server }
func (t *remoteTool) TimeLimit() time.Duration { return t.limit }
func (t *remoteTool) Preview(args tool.Args) (tool.Preview, error) {
	b, err := json.MarshalIndent(args, "", "  ")
	if err != nil {
		return tool.Preview{}, err
	}
	return tool.Preview{Note: "MCP", Server: t.server, Args: string(b)}, nil
}

// Run は tools/call でサーバーにツールを実行させる。サーバーが確認を求めてきたら、エラーにする(聞く手段が無いため)。
// 会話の仕組みは RunInteractive を使う。
func (t *remoteTool) Run(ctx context.Context, args tool.Args) (string, error) {
	return t.RunInteractive(ctx, args, nil)
}

// RunInteractive は tools/call でサーバーにツールを実行させる。サーバーが確認を求めてきたら(MRTR)、ask でユーザーに聞いて送り直す。
//
//	aibow → tools/call(引数)
//	aibow ← resultType: "input_required"・inputRequests(聞きたいこと)・requestState
//	        ask でユーザーに聞く(確認のカード)
//	aibow → tools/call(同じ引数 + inputResponses(答え)・requestState)
//	aibow ← 結果
//
// SDK はこの送り直しを自動で行えるが、止めて(MultiRoundTrip.Disabled)ここで書いている。
// 自動だと1つの ctx の中で回るので、ユーザーが確認のカードを読んでいる時間まで時間の上限(callTimeout)に数えてしまうため。
// ここでは、サーバーとの1往復ごとに上限をかける。往復がコードに見えるのも、学ぶ題材として良い。
//
// 時間切れ・停止のときは、SDK が取り消しの知らせ(notifications/cancelled)をサーバーに送って、すぐに返ってくる。
func (t *remoteTool) RunInteractive(ctx context.Context, args tool.Args, ask tool.Ask) (string, error) {
	if args == nil {
		args = tool.Args{}
	}
	params := &mcp.CallToolParams{Name: t.name, Arguments: map[string]any(args)}
	for round := 1; ; round++ {
		res, err := t.call(ctx, params)
		if err != nil {
			return "", err
		}
		if !res.NeedsInput() {
			text := truncate(resultText(res.Content))
			if res.IsError {
				// ツールの中のエラー(ファイルが無い、など)。モデルに読ませて立て直させる
				return "", errors.New(text)
			}
			return text, nil
		}
		if round >= maxRounds {
			return "", fmt.Errorf("MCP のサーバー %s が確認を %d 回求めてきたため、止めました", t.server, round)
		}
		answers, err := t.answer(ctx, res.InputRequests, ask, round)
		if err != nil {
			return "", err
		}
		// 送り直しは、同じ引数に答えと合言葉(requestState。サーバーが前の回のことを思い出すためのもの)を付ける
		params = &mcp.CallToolParams{Name: t.name, Arguments: map[string]any(args), InputResponses: answers, RequestState: res.RequestState}
	}
}

// call は tools/call を1回送る。時間の上限はこの1往復だけにかける。
func (t *remoteTool) call(ctx context.Context, params *mcp.CallToolParams) (*mcp.CallToolResult, error) {
	rctx, cancel := context.WithTimeout(ctx, t.limit)
	defer cancel()
	res, err := t.session.CallTool(rctx, params)
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded) || (ctx.Err() == nil && errors.Is(rctx.Err(), context.DeadlineExceeded)):
		return nil, fmt.Errorf("時間の上限(%d 秒)を超えたため止めました(settings.json の callTimeout に秒数を書くと延ばせます)", int(t.limit.Seconds()))
	case ctx.Err() != nil:
		return nil, errors.New("停止されました")
	case err != nil:
		// サーバーが JSON-RPC のエラーで答えた(知らないツール・引数の形が違う、など)なら、つながりは生きている。
		// それ以外(HTTP でつなげない・サーバーが落ちた)は、つながりが切れたとみなして、次の送信でつなぎ直す
		if !answeredByServer(err) && t.broken != nil {
			t.broken(err)
			return nil, fmt.Errorf("MCP のサーバー %s とのつながりが切れました。次の送信でつなぎ直します: %w", t.server, err)
		}
		return nil, fmt.Errorf("MCP のサーバー %s で実行できません: %w", t.server, err)
	}
	return res, nil
}

// answer は、サーバーが聞いてきたこと(inputRequests)をユーザーに聞き、答え(inputResponses)にする。
//
// aibow が答えられるのは、ユーザーへの確認(elicitation)だけ。ほかの問い合わせ(sampling: クライアントの LLM で生成させる、など)には対応していない。
// 答えは、実行する = accept、拒否 = decline。拒否の理由は content の reason に入れる
// (MCP の決まりでは content は accept のときのものだが、aibow-mcp は decline でも理由を読んでモデルに渡す。ほかのサーバーは読まないだけ)。
func (t *remoteTool) answer(ctx context.Context, reqs mcp.InputRequestMap, ask tool.Ask, round int) (mcp.InputResponseMap, error) {
	if len(reqs) == 0 {
		// 空の inputRequests は「今は混んでいるので、あとで送り直して」の意味(MRTR の決まり)。aibow は送り直さずに止める
		return nil, fmt.Errorf("MCP のサーバー %s が混んでいるため、実行できませんでした", t.server)
	}
	answers := mcp.InputResponseMap{}
	for id, ir := range reqs {
		ep, ok := ir.(*mcp.ElicitParams)
		if !ok {
			return nil, fmt.Errorf("MCP のサーバー %s が、aibow の対応していない問い合わせ(%T)を求めました", t.server, ir)
		}
		if ask == nil {
			return nil, fmt.Errorf("MCP のサーバー %s が確認を求めましたが、ユーザーに聞く手段がありません", t.server)
		}
		log.Infof("MCP %s: %s がユーザーの確認を求めています(%d 回目の往復。%s)", t.server, t.name, round, firstLine(ep.Message))
		a, err := ask(ctx, previewOf(t.server, ep))
		if err != nil {
			return nil, err
		}
		r := &mcp.ElicitResult{Action: "decline", Content: map[string]any{}}
		if a.Approved {
			r.Action = "accept"
		} else if a.Reason != "" {
			r.Content["reason"] = a.Reason
		}
		log.Infof("MCP %s: %s の確認の答え: %s", t.server, t.name, r.Action)
		answers[id] = r
	}
	return answers, nil
}

// previewOf は、サーバーが聞いてきたことを、確認のカードの中身(tool.Preview)にする。
// aibow-mcp は _meta にカードの中身(差分など)を入れてくるので、それをそのまま使う。ほかのサーバーは、聞いてきた文(message)を出す。
//
// どちらもサーバーが書いたもので、本当にそのとおりのことをするかは分からない(信頼していないサーバーの差分を、そのまま信じないこと)。
// settings.json のサーバーは、呼ぶ前の確認(引数)も今までどおり出す(0007)
func previewOf(server string, ep *mcp.ElicitParams) tool.Preview {
	if raw, ok := ep.Meta[meta.Preview]; ok {
		var pv tool.Preview
		if b, err := json.Marshal(raw); err == nil && json.Unmarshal(b, &pv) == nil && pv.Note != "" {
			pv.Server = server
			return pv
		}
	}
	return tool.Preview{Note: "サーバーからの確認", Server: server, Args: ep.Message}
}

// firstLine は文の最初の行(ログに出す用)。
func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// answeredByServer は、エラーがサーバーからの答え(JSON-RPC のエラー)か。
//
// SDK は、つながりの問題も JSON-RPC のエラーの形で返すことがある(HTTP で送れなかったときの -32005 rejected by transport など)。
// それらの番号はサーバーの答えではないので、つながりが切れたとみなす。
func answeredByServer(err error) bool {
	var e *jsonrpc.Error
	if !errors.As(err, &e) {
		return false
	}
	switch e.Code {
	case -32003, -32004, -32005: // SDK が付ける番号: client is closing / server is closing / rejected by transport
		return false
	}
	return true
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
