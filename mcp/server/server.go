// Package server は、aibow のツール(tool パッケージ)を MCP のサーバーとして出す(ステップ5。仕様は _docs/features/0007-mcp-server.md)。
//
// ステップ4の MCP クライアント(mcp/client)の反対側。クライアントから届く tools/list に定義の一覧を、
// tools/call に実行の結果を返す。やり取りの決まりごと(JSON-RPC・版の取り決め・server/discover など)は公式の Go SDK に任せ、
// ここは「tool のツールを MCP のツールの形にする」ことだけを書く。
//
// このパッケージは tool と SDK だけを使い、LLM・会話の仕組み・画面(ルートの aibow や Wails)を import しない。
// 標準入出力で動かす exe(_cmd/aibow-mcp)はここを起動するだけで、中身はここにある。
// main に書かないのは、テストでプロセスを起動せずにつなぐため・aibow の中でメモリ内のつなぎ方で動かすため(5-4)。
//
// ほかのアプリも、自分のツール(tool.Tool)を Options.Custom で渡せば、同じ仕組み(引数の確かめ・時間の上限・
// 失敗を isError で返す・書き換えるツールの確認)で MCP のサーバーになれる(0045。使い方は _docs/mcp.md の「ほかのアプリから使う」)。
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

// Name は、Options.Name を決めないときのサーバーの名前(MCP の serverInfo.name)。クライアントのログに出る。
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
	// Folders は、フォルダの組(ワークスペース。0035 の段2)。決めたら WorkDir の代わりに使う。
	// ファイルのツールは引数 folder でフォルダを選び、run_command はメインのフォルダで動く
	Folders []tool.Folder
	// Version はサーバーの版(MCP の serverInfo.version)。空なら "(devel)"
	Version string
	// Fetch は、URL のページを読むツール(fetch_url。0036)の設定。nil なら出さない。
	// aibow の中で動かすときは出し、aibow-mcp は引数 -fetch を付けたときだけ出す(Claude Code などは自分の fetch を持っていて重なるため。決定事項 15)
	Fetch *tool.FetchOptions

	// Name はサーバーの名前(MCP の serverInfo.name)。空なら "aibow-mcp"(0045)
	Name string
	// Instructions は、つないだクライアントに渡す説明(MCP の instructions)。クライアントはふつう、これをモデルに見せる。
	// 空なら、Custom を決めないときはフォルダから作った説明(作業フォルダ・シェル)、Custom を決めたときは説明を出さない(0045 の決定事項 9)
	Instructions string
	// Custom は、ほかのアプリが出す自分のツール(0045)。決めたら、組み込み・フォルダのツールの代わりに、これだけを出す。
	// 組み込みも出したいときは append(tool.Builtin(), …) で渡す(足すのでなく代わりにするのは、メモ帳のようなアプリに
	// read_file や run_command が混ざると、モデルがアプリの外のファイルを触ろうとするため)。
	// WorkDir・Folders・Fetch と一緒には使えない(Check がエラーにする)。
	// 書き換えるツールに「読むだけ」の印を付けないため、印は tool.Hinted で読むだけと宣言したツールにだけ付ける
	Custom []tool.Tool
}

// folders は、ツールが扱うフォルダの組(Folders か、WorkDir 1つ。どちらも無ければ空)。
func (o Options) folders() []tool.Folder {
	if len(o.Folders) > 0 {
		return o.Folders
	}
	return tool.Single(o.WorkDir)
}

// New は、tool のツールを出す MCP のサーバーを作る。動かすのは呼ぶ側(Run に標準入出力やメモリ内のトランスポートを渡す)。
//
// opts に誤りがあれば(Check がエラーを返せば)、何が悪いかを書いて panic する。
// SDK の AddTool も引数の定義がおかしいと panic するが、どのツールのどこが悪いかが分かりにくく、名前が重なっても黙って置き換えるため、
// その前に止める(regexp.MustCompile と同じ考え方。ツールの一覧はふつうプログラムに書いて決まるので、誤りは作った人の直すもの)。
// 起動の前に Check を呼べば、panic を避けてエラーとして扱える。組み込みのツールだけのとき(Custom が空)は誤りにならない
func New(opts Options) *mcp.Server {
	if err := opts.Check(); err != nil {
		panic("mcp/server.New: " + err.Error())
	}
	name := opts.Name
	if name == "" {
		name = Name
	}
	version := opts.Version
	if version == "" {
		version = "(devel)"
	}
	s := mcp.NewServer(&mcp.Implementation{Name: name, Version: version}, &mcp.ServerOptions{
		Instructions: opts.instructions(),
	})
	custom := len(opts.Custom) > 0
	for _, t := range opts.Tools() {
		s.AddTool(mcpTool(t, custom), handler(t))
	}
	return s
}

// Check は opts の誤りを確かめる(0045 の決定事項 3・9)。
//
//   - Custom と WorkDir・Folders・Fetch を一緒に決めた(黙って片方を無視すると、どちらが効いているか分からなくなる)
//   - ツールの名前が空・重なる
//   - 引数の定義(Def.Parameters)が JSON のオブジェクトとして読めない・type が "object" でない(MCP のツールの引数はオブジェクトの決まり)
func (o Options) Check() error {
	if len(o.Custom) > 0 {
		var both []string
		if o.WorkDir != "" {
			both = append(both, "WorkDir")
		}
		if len(o.Folders) > 0 {
			both = append(both, "Folders")
		}
		if o.Fetch != nil {
			both = append(both, "Fetch")
		}
		if len(both) > 0 {
			return fmt.Errorf("Custom と %s は一緒に使えません(Custom を決めたら、出すツールは Custom だけです。ファイルのツールも出すなら tool.FilesIn などを Custom に入れてください)", strings.Join(both, "・"))
		}
	}
	seen := map[string]bool{}
	for i, t := range o.Tools() {
		def := t.Def()
		if def.Name == "" {
			return fmt.Errorf("%d 番目のツールの名前が空です", i+1)
		}
		if seen[def.Name] {
			return fmt.Errorf("ツール %s が2つあります(名前は1つずつにしてください)", def.Name)
		}
		seen[def.Name] = true
		if err := checkParameters(def.Parameters); err != nil {
			return fmt.Errorf("ツール %s の引数の定義(Def.Parameters): %w", def.Name, err)
		}
	}
	return nil
}

// checkParameters は、引数の定義が JSON のオブジェクトで、type が "object" かを確かめる(SDK の AddTool が panic する条件と同じ)。
func checkParameters(raw json.RawMessage) error {
	if len(raw) == 0 {
		return errors.New(`空です。引数が無いツールでも {"type": "object", "properties": {}} を書いてください`)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return fmt.Errorf("JSON のオブジェクトとして読めません: %w", err)
	}
	if m["type"] != "object" {
		return fmt.Errorf(`type を "object" にしてください(今は %v)`, m["type"])
	}
	return nil
}

// Tools はサーバーが出すツール。tools/list の並びは SDK が名前の順にする(ここで並べた順ではない)。
// Custom を決めたら、それだけ。作業フォルダが無ければ、組み込みのツール(時刻・計算と、Fetch を決めたら fetch_url)だけ。
func (o Options) Tools() []tool.Tool {
	if len(o.Custom) > 0 {
		return slices.Clone(o.Custom)
	}
	tools := tool.Builtin()
	if o.Fetch != nil {
		tools = append(tools, tool.FetchURL(*o.Fetch))
	}
	folders := o.folders()
	if len(folders) > 0 {
		tools = append(tools, tool.FilesIn(folders)...)
		tools = append(tools, tool.UpdatePlan()) // 作業の予定(0037)。フォルダのある会話だけで出す(決定事項 14)
		tools = append(tools, tool.RunCommandIn(folders))
		tools = append(tools, tool.GitIn(folders)...) // 読むだけの Git のツール(0041)。リポジトリで git があるときだけ
	}
	return tools
}

// instructions は、つないだクライアントに渡す説明。Instructions を決めたらそれ、Custom を決めたら無し、ほかはフォルダの説明。
func (o Options) instructions() string {
	switch {
	case o.Instructions != "":
		return o.Instructions
	case len(o.Custom) > 0:
		return "" // フォルダの説明は Custom のツールに合わない(0045 の決定事項 9)
	}
	return instructions(o.folders())
}

// instructions は、フォルダから作る説明(MCP の instructions)。
//
// run_command の説明は「Environment の節に書いたシェルで動かす」と言っている。aibow はシステムプロンプトにその節を書くが、
// ほかのクライアント(Claude Code など)は書かないので、同じことをここで伝える。クライアントはふつう、これをモデルに見せる
func instructions(folders []tool.Folder) string {
	if len(folders) == 0 {
		return ""
	}
	if len(folders) == 1 {
		return "# Environment\n" +
			"- Working directory: " + folders[0].Path + " (file paths are relative to it)\n" +
			"- Shell for run_command: " + tool.ShellNoteFor(true) + "\n"
	}
	var b strings.Builder
	b.WriteString("# Environment\n- Folders (file tools and run_command take a folder argument; omit it for the main folder. File paths are relative to the folder):\n")
	for _, f := range folders {
		b.WriteString("  - " + tool.FolderLine(f, false) + "\n")
	}
	b.WriteString("- Shell for run_command: " + tool.ShellNoteFor(true) + " (each call starts a new shell in the chosen folder; a cd does not carry over. Pass folder instead of cd to use another folder)\n")
	return b.String()
}

// mcpTool は tool の定義を MCP のツールの定義にする。
//
// 名前・説明・引数の JSON Schema は、aibow の組み込みとまったく同じ(4B が書きやすい形のまま出せるのが、自分で作るサーバーの良いところ)。
// 読むだけのツールには「読むだけ」の印(readOnlyHint)を付ける。
// これはクライアントへのヒントで、MCP の仕様でも「信じてはいけない」とされている(aibow はまだ信じない。0007 の決定事項 9)
func mcpTool(t tool.Tool, custom bool) *mcp.Tool {
	def := t.Def()
	m := &mcp.Tool{
		Name:        def.Name,
		Description: def.Description,
		InputSchema: def.Parameters,
	}
	if readOnly(t, custom) {
		m.Annotations = &mcp.ToolAnnotations{ReadOnlyHint: true}
	}
	return m
}

// readOnly は、ツールに「読むだけ」の印を付けるか(0045 の決定事項 5)。
//
// tool.Hinted で宣言していれば、それに従う(update_plan は確認が要らないが、予定を書き換えるので読むだけではない)。
// 宣言が無いとき、組み込みのツールは「確認が要らなければ読むだけ」(aibow の書き換えるツールは、どれも確認する)。
// 外から渡したツール(custom)は印を付けない:確認を自分の画面で行うアプリでは、書き換えるツールも Confirmable にならないため
func readOnly(t tool.Tool, custom bool) bool {
	if h, ok := t.(tool.Hinted); ok {
		return h.ReadOnly()
	}
	if custom {
		return false
	}
	_, confirmable := t.(tool.Confirmable)
	return !confirmable
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

// TimeLimit は、ツール1回の実行の時間の上限(上限を自分で決めるツールはその値、ほかは 10 秒)。
// aibow の中でこのサーバーを動かすとき(mcp/client の Local)、クライアントはこれより少し長く待つ
func TimeLimit(t tool.Tool) time.Duration {
	if l, ok := t.(tool.TimeLimiter); ok {
		return l.TimeLimit()
	}
	return defaultTimeLimit
}

// run はツールを時間の上限つきで実行する。
//
// ctx は SDK がリクエストごとに作るもので、クライアントが止める知らせ(notifications/cancelled)を送ると止まる。
// ツールは ctx が止まったら後片付けをして返す約束(run_command は子プロセスまでまとめて止める)なので、それを待って返す
func run(ctx context.Context, t tool.Tool, args tool.Args) (string, error) {
	limit := TimeLimit(t)
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
// CheckArgs は書式によらない tool.Arg の形で値を受け取るので、JSON の値を定義の型を見てその形にして渡す(tool.ArgFromJSON):
// 文字列の引数は中身、それ以外(数・真偽値・配列・オブジェクト)は JSON の書き方のまま。CheckArgs が JSON Schema の型を見て Go の値にする。
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

	types := tool.ArgTypes(def)
	var args []tool.Arg
	for _, name := range names {
		v := values[name]
		if strings.TrimSpace(string(v)) == "null" {
			continue // null は「書かなかった」として扱う(省略できる引数に null を入れてくるクライアントがある)
		}
		args = append(args, tool.Arg{Name: name, Value: tool.ArgFromJSON(types[name], v)})
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
