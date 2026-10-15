package server

// confirm.go: 書き換えるツール(tool.Confirmable)を実行する前に、サーバーの側からユーザーに確かめる(0007 の 5-3)。
//
// aibow の中では、書き換える前に会話の仕組みが差分を見せて確かめている。サーバーに移すと、クライアントからは中身が見えず、
// 差分を作れない。そこでサーバーが差分を作り、「これでよいか」をクライアントに聞く。仕組みは MCP の MRTR(Multi Round-Trip Requests。2026-07-28 版):
//
//	クライアント → tools/call(引数)
//	サーバー     ← 結果: resultType "input_required"・inputRequests {"confirm": 聞きたいこと(elicitation)}・requestState(合言葉)
//	クライアント → tools/call(同じ引数 + inputResponses {"confirm": 答え}・requestState)   ← ユーザーに聞いてから送り直す
//	サーバー     ← 結果: 実行した結果(拒否されたら「拒否しました」)
//
// サーバーは1回目と2回目の間で何も覚えない(ステートレス)。2回目は同じ引数でもう一度ハンドラが呼ばれるので、
// 差分を作り直し、1回目に見せたものと同じか(requestState)を確かめてから実行する。
// 古い版のクライアント(2026-07-28 版より前)には、SDK がサーバーからのリクエスト(elicitation/create)で聞き、同じハンドラを答え付きで呼び直す。

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/secondarykey/aibow/log"
	"github.com/secondarykey/aibow/mcp/internal/meta"
	"github.com/secondarykey/aibow/tool"
)

const (
	// confirmKey は inputRequests / inputResponses の中で、この確認に付ける名前(サーバーが決める。クライアントは同じ名前で答える)
	confirmKey = "confirm"
	// reasonField は、確認のフォームの中の「拒否の理由」の欄の名前
	reasonField = "reason"
)

// errNoElicitation は、確認(elicitation)に対応していないクライアントから、確認の要るツールを呼ばれたときのエラー(0007 の決定事項 6)。
// 確かめずに書き換えることはしない(クライアント自身の確認に任せて実行する案より安全なほう)
var errNoElicitation = errors.New("このクライアントは実行前の確認(elicitation)に対応していないため、書き換えません")

// errChanged は、確認のあとで内容(ファイルの中身など)が変わったときのエラー。見せた差分と違うことをしないため
var errChanged = errors.New("確認のあとでファイルが変わったため書き換えませんでした。もう一度呼んで、新しい差分で確かめてください")

// confirmed は、確認の要るツールについて、実行してよいかを決める。
//
//   - 実行してよい: run = true
//   - まだ聞いていない: ユーザーに聞くための結果(input_required)を返す
//   - 拒否された・確認できない: モデルに返す結果を返す
func confirmed(req *mcp.CallToolRequest, t tool.Confirmable, args tool.Args) (run bool, res *mcp.CallToolResult) {
	if caps := req.ClientCapabilities(); caps == nil || caps.Elicitation == nil {
		return false, errorResult(errNoElicitation)
	}
	pv, err := t.Preview(args)
	if err != nil {
		return false, errorResult(err) // 置き換える場所が見つからない、など。ユーザーに聞くまでもない
	}
	state := previewState(pv)

	answer, ok := req.Params.InputResponses[confirmKey]
	if !ok {
		// 1回目: 聞きたいことを返す。実行はしない
		return false, &mcp.CallToolResult{
			InputRequests: mcp.InputRequestMap{confirmKey: elicitation(t.Def().Name, pv)},
			RequestState:  state,
		}
	}
	er, ok := answer.(*mcp.ElicitResult)
	if !ok {
		return false, errorResult(fmt.Errorf("確認の答えの形が違います(%T)", answer))
	}
	if er.Action != "accept" {
		// 拒否(decline)・取り消し(cancel)。実行せず、拒否されたことと理由をモデルに返す(エラーではない。モデルに別のやり方を考えさせる)
		reason, _ := er.Content[reasonField].(string)
		return false, &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: tool.RejectedText(reason)}}}
	}
	if req.Params.RequestState != state {
		return false, errorResult(errChanged)
	}
	return true, nil
}

// previewState は、確認で見せた内容の合言葉(requestState)。内容(差分・コマンド)の SHA-256。
// 送り直しで作り直した内容と比べ、見せたものと同じかを確かめる(サーバーは何も覚えていないので、クライアントに持たせて送り返させる)
func previewState(pv tool.Preview) string {
	b, _ := json.Marshal(pv)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// elicitation は、ユーザーに確かめる内容(MCP の elicitation のフォーム)を作る。
//
// message は人が読む文(aibow 以外のクライアントはこれを出す)。_meta の aibow/preview に、今の確認のカードの中身(tool.Preview)を入れる。
// フォームの欄は「拒否の理由」だけ(省略できる)。実行するかどうかは、フォームの答えの action(accept / decline / cancel)で表す
func elicitation(name string, pv tool.Preview) *mcp.ElicitParams {
	return &mcp.ElicitParams{
		Meta:    mcp.Meta{meta.Preview: pv},
		Mode:    "form",
		Message: message(name, pv),
		RequestedSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				reasonField: map[string]any{
					"type":        "string",
					"title":       "拒否の理由",
					"description": "拒否するときの理由(省略できます)。モデルに渡します",
				},
			},
		},
	}
}

// message は、確認の内容を人が読む文にする。
func message(name string, pv tool.Preview) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s を実行してよいですか?", name)
	if pv.Path != "" {
		fmt.Fprintf(&b, "\n対象: %s(%s)", pv.Path, pv.Note)
	}
	switch {
	case pv.Command != "":
		fmt.Fprintf(&b, "\nコマンド:\n%s", pv.Command)
	case pv.Diff != "":
		fmt.Fprintf(&b, "\n差分:\n%s", strings.TrimRight(pv.Diff, "\n"))
	case pv.Path != "":
		b.WriteString("\n(大きいため差分は出していません)")
	}
	return b.String()
}

// logConfirm は、実行しなかったときの結果をログに出す(学習用。MRTR の往復が見える)。
func logConfirm(name string, res *mcp.CallToolResult) {
	var text string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text += tc.Text
		}
	}
	switch {
	case res.InputRequests != nil:
		log.Infof("ツール %s: ユーザーに確認を求めます(input_required で返し、答え付きの送り直しを待つ)", name)
	case res.IsError:
		log.Infof("ツール %s: 実行しません: %s", name, text)
	default:
		log.Infof("ツール %s: %s", name, text)
	}
}
