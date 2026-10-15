package client

// local.go: aibow の中で自作の MCP サーバー(mcp/server)を動かし、組み込みのツールを MCP 越しに渡す部分(0007 の 5-4)。
//
//	aibow [ 会話の仕組み ] ⇄ MCP(メモリ内のつなぎ方) ⇄ aibow の中の自作サーバー [ tool のツール ]
//
// サーバーを別のプロセスにせず、同じプロセスの中で SDK のメモリ内のトランスポート(NewInMemoryTransports)でつなぐ。
// exe を別に用意しなくても、組み込みのツールが MCP を通る(ロードマップの「ステップ3のツールを MCP サーバー経由に置き換える」)。
// やり取りは、settings.json のサーバーと同じ JSON-RPC のメッセージで、ログにも出る。
//
// モデルと会話の仕組みからは、今までの組み込みのツールと同じに見せる(名前に aibow__ を付けない・グループは組み込み・扱いの決まりの既定も同じ)。
// 違うのは確認のしかただけ: 書き換えるツールは、呼ぶ前に確認せず、サーバーが差分を送ってきたときに1回だけ聞く(tool.SelfConfirming)。
// aibow が自分で立てたサーバーなので、送ってきた差分を信頼してよい。

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/secondarykey/aibow/log"
	mcpserver "github.com/secondarykey/aibow/mcp/server" // このパッケージには、動かしているサーバーを表す型 server があるので別名にする
	"github.com/secondarykey/aibow/tool"
)

// localName は、aibow の中のサーバーの、ログでの名前(MCP 組み込み → {...})。
const localName = "組み込み"

// localGrace は、aibow の中のサーバーのツールの時間の上限に足す時間。
// サーバーもツールの上限(10 秒・run_command は 120 秒)で止めるので、クライアントはそれより少し長く待ち、サーバーの「上限を超えた」の結果を受け取る
const localGrace = 5 * time.Second

// Local は、aibow の中で自作の MCP サーバーを動かし、組み込みのツールを渡す(会話の仕組みの LocalTools)。
//
// サーバーは作業フォルダごとに1つ(作業フォルダは起動の引数で渡すため。settings.json の ${workdir} と同じ)。
// 要る最初のときにつなぎ、Close まで残す。メモリ内なので、つなぐのは一瞬(0.1 秒もかからない)。
type Local struct {
	opt Options

	mu       sync.Mutex
	sessions map[string]*localSession // 作業フォルダごと(作業フォルダの無い会話は "")
	closed   bool
}

type localSession struct {
	server *mcp.ServerSession
	client *mcp.ClientSession
	tools  []tool.Tool
}

// NewLocal は、aibow の中のサーバーの係を作る。サーバーはまだ動かさない。
func NewLocal(opt Options) *Local {
	return &Local{opt: opt, sessions: map[string]*localSession{}}
}

// Tools は、作業フォルダ workDir の会話で使う組み込みのツール(aibow の中のサーバー越しに呼ぶもの)。
// サーバーにつなげなかったときは、ログに出して、tool パッケージのツールを直接呼ぶものを返す(予備。0007 の決定事項 C)。
func (l *Local) Tools(workDir string) []tool.Tool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if s, ok := l.sessions[workDir]; ok {
		return s.tools
	}
	if l.closed {
		return mcpserver.Tools(workDir)
	}
	s, err := l.connect(workDir)
	if err != nil {
		log.Errorf("MCP %s: aibow の中のサーバーにつなげないので、組み込みのツールを直接呼びます: %v", localName, err)
		return mcpserver.Tools(workDir)
	}
	l.sessions[workDir] = s
	return s.tools
}

// connect は、作業フォルダ workDir のサーバーを aibow の中で動かしてつなぎ、ツールの一覧を受け取る。
func (l *Local) connect(workDir string) (*localSession, error) {
	ctx := context.Background()
	begin := time.Now()
	ct, st := mcp.NewInMemoryTransports()
	ss, err := mcpserver.New(mcpserver.Options{WorkDir: workDir, Version: l.opt.Version}).Connect(ctx, st, nil)
	if err != nil {
		return nil, err
	}
	// 送受信した JSON-RPC のメッセージを、settings.json のサーバーと同じくログに出す
	t := &mcp.LoggingTransport{Transport: ct, Writer: &lineLogger{prefix: "MCP " + localName + " "}}
	session, err := newClient(l.opt.Version).Connect(ctx, t, nil)
	if err != nil {
		ss.Close()
		return nil, err
	}
	s := &localSession{server: ss, client: session}
	originals := mcpserver.Tools(workDir) // サーバーが出しているツールの中身(扱いの決まりの既定を決めるため)
	for mt, err := range session.Tools(ctx, nil) {
		if err != nil {
			s.close()
			return nil, err
		}
		orig := tool.Find(originals, mt.Name)
		if orig == nil {
			s.close()
			return nil, fmt.Errorf("サーバーが知らないツール %s を出しました", mt.Name)
		}
		s.tools = append(s.tools, newLocalTool(orig, session))
	}
	where := workDir
	if where == "" {
		where = "なし"
	}
	log.Infof("MCP %s: aibow の中のサーバーにつなぎました(%.3f 秒。作業フォルダ: %s・ツール %d 個)", localName, time.Since(begin).Seconds(), where, len(s.tools))
	return s, nil
}

// Close は、aibow の中のサーバーを全部止める(アプリを閉じるとき)。
func (l *Local) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closed = true
	for k, s := range l.sessions {
		s.close()
		delete(l.sessions, k)
	}
}

func (s *localSession) close() {
	s.client.Close()
	s.server.Close()
}

// newLocalTool は、aibow の中のサーバーのツール1つを、組み込みのツールと同じに見せる形にする。
// 書き換えるツール(Confirmable)は、確認をサーバーに任せる印(tool.SelfConfirming)を付ける。
func newLocalTool(orig tool.Tool, session *mcp.ClientSession) tool.Tool {
	limit := mcpserver.TimeLimit(orig) + localGrace
	lt := localTool{
		orig: orig,
		rt:   &remoteTool{server: localName, session: session, name: orig.Def().Name, limit: limit},
	}
	if ct, ok := orig.(tool.Confirmable); ok {
		return &localConfirmTool{localTool: lt, preview: ct}
	}
	return &lt
}

// localTool は aibow の中のサーバーのツール1つ。
//
// 定義はサーバーが出しているものと同じ(tool のツールそのもの)なので、元のツールの定義を使う(名前に aibow__ を付けない)。
// tool.Remote にはしない(会話の仕組みから見て、MCP のサーバーのツールではなく組み込みのツールとして扱うため)。
type localTool struct {
	orig tool.Tool
	rt   *remoteTool // 実行は MCP 越し(remoteTool と同じ送り直しの仕組みを使う)
}

var (
	_ tool.Interactive    = (*localTool)(nil)
	_ tool.TimeLimiter    = (*localTool)(nil)
	_ tool.SelfConfirming = (*localConfirmTool)(nil)
)

func (t *localTool) Def() tool.Def            { return t.orig.Def() }
func (t *localTool) TimeLimit() time.Duration { return t.rt.limit }

func (t *localTool) Run(ctx context.Context, args tool.Args) (string, error) {
	return t.RunInteractive(ctx, args, nil)
}

// RunInteractive は、aibow の中のサーバーにツールを実行させる。サーバーが差分を送ってきたら ask で聞く。
// 確認のカードにサーバーの名前は出さない(組み込みのツールと同じ見た目にする)。
func (t *localTool) RunInteractive(ctx context.Context, args tool.Args, ask tool.Ask) (string, error) {
	var asking tool.Ask
	if ask != nil {
		asking = func(ctx context.Context, pv tool.Preview) (tool.Answer, error) {
			pv.Server = ""
			return ask(ctx, pv)
		}
	}
	return t.rt.RunInteractive(ctx, args, asking)
}

// localConfirmTool は、aibow の中のサーバーの書き換えるツール。確認はサーバーが求めてくるので、会話の仕組みは呼ぶ前に確認しない。
type localConfirmTool struct {
	localTool
	preview tool.Confirmable
}

// Preview は、確認が要るツールであることを示すためのもの(扱いの決まりの既定に使う)。tool.SelfConfirming なので、会話の仕組みは呼ばない。
func (t *localConfirmTool) Preview(args tool.Args) (tool.Preview, error) {
	return t.preview.Preview(args)
}

// SelfConfirming は、確認をサーバーに任せる印(tool.SelfConfirming)。
func (t *localConfirmTool) SelfConfirming() {}
