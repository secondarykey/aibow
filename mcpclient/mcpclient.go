// Package mcpclient は、aibow を MCP(Model Context Protocol)のクライアントにする部分。
//
// MCP は、ツールやデータを AI アプリにつなぐための決まりごと。中身は JSON-RPC 2.0 のメッセージのやり取りで、
// ローカルでは「クライアント(aibow)がサーバーを子プロセスとして起動し、標準入出力で1行ずつ JSON を送り合う」(stdio のトランスポート)。
// やり取りは公式の Go SDK(github.com/modelcontextprotocol/go-sdk)に任せ、送受信したメッセージはログに出して見えるようにする。
//
//	aibow ── 起動する(npx …)──→ MCP のサーバー(別のプロセス)
//	aibow ── tools/list ──────→ ツールの定義(名前・説明・inputSchema)が返る → tool.Tool にしてモデルに見せる
//	aibow ── tools/call ──────→ 結果(content)が返る → ツールの結果としてモデルに渡す
//
// ツールの定義の形は、0004 の tool.Def(名前・説明・引数の JSON Schema)とほぼ同じ。
// なので、遠くのツールも tool.Tool として見せれば、エージェントループ(ルートの agent.go)は何も変えずに使える。
//
// このパッケージは tool を import し、ルートの aibow(LLM・会話の仕組み)は import しない。
// SDK の mcp パッケージと名前が重ならないよう、mcpclient にしている。
package mcpclient

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"aibow/internal/proc"
	"aibow/tool"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ServerConfig は MCP のサーバー1つの起動のしかた(settings.json の mcpServers の1つ)。
// 形は、多くのツールで使われている mcpServers に合わせている(ほかのツールの設定をそのまま貼れるように)。
type ServerConfig struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	// Type はトランスポートの種類。aibow は stdio だけなので、空か "stdio" だけを受け付ける
	// (ほかのツールの設定には "type": "http" や "url" を書いたものがあるため、読んで分かりやすいエラーにする)
	Type string `json:"type,omitempty"`
	URL  string `json:"url,omitempty"`
}

// workDirVar は、args と env の値の中で会話の作業フォルダに置き換える文字列。
//
// 作業フォルダはサーバーの起動の引数で渡す。MCP の Roots(クライアントがフォルダを伝える仕組み)は
// 2026-07-28 版で非推奨になり、「設定かツールの引数で渡す」のが新しいやり方のため。
// そのため、これを使うサーバーは作業フォルダごとに1つ起動する。
const workDirVar = "${workdir}"

// Validate は設定の形を確かめる。
func (c ServerConfig) Validate() error {
	if c.URL != "" || (c.Type != "" && c.Type != "stdio") {
		return errors.New("aibow は stdio のサーバー(command で起動するもの)だけに対応しています。url や type: http のサーバーは使えません")
	}
	if strings.TrimSpace(c.Command) == "" {
		return errors.New("command(起動するプログラム)がありません")
	}
	return nil
}

// UsesWorkDir は、作業フォルダごとに起動するサーバーか(args か env に ${workdir} があるか)。
func (c ServerConfig) UsesWorkDir() bool {
	for _, a := range c.Args {
		if strings.Contains(a, workDirVar) {
			return true
		}
	}
	for _, v := range c.Env {
		if strings.Contains(v, workDirVar) {
			return true
		}
	}
	return false
}

// command は、サーバーを起動する exec.Cmd を作る。${workdir} を作業フォルダに置き換え、カレントフォルダも作業フォルダにする。
func (c ServerConfig) command(workDir string) *exec.Cmd {
	expand := func(s string) string { return strings.ReplaceAll(s, workDirVar, workDir) }
	args := make([]string, len(c.Args))
	for i, a := range c.Args {
		args[i] = expand(a)
	}
	cmd := exec.Command(c.Command, args...)
	cmd.Env = os.Environ()
	for k, v := range c.Env {
		cmd.Env = append(cmd.Env, k+"="+expand(v))
	}
	if c.UsesWorkDir() {
		cmd.Dir = workDir
	}
	return cmd
}

// commandLine はログに出すための起動のコマンド。
func (c ServerConfig) commandLine(workDir string) string {
	cmd := c.command(workDir)
	return strings.Join(append([]string{c.Command}, cmd.Args[1:]...), " ")
}

const (
	// startTimeout はサーバーの起動(つないでツールの一覧を受け取るまで)の時間の上限。
	// 初回の npx はパッケージをダウンロードするので長めにしている
	startTimeout = 60 * time.Second
	// closeWait は、閉じるときに標準入力を閉じてから、サーバーが自分で終わるのを待つ時間。過ぎたら止める
	closeWait = 2 * time.Second
)

// Options は Manager の設定。
type Options struct {
	// Version は、サーバーに名乗る aibow のバージョン(clientInfo)
	Version string
	// Logf はログの出し先。nil なら log.Printf
	Logf func(format string, args ...any)
}

// Manager は settings.json に書いた MCP のサーバーを起動し、そのツールを渡す。
//
// サーバーは、要る最初の送信のときに起動し(アプリの起動を遅くしないため)、アプリを閉じるまで残す。
// 落ちていたら、次の Start で起動し直す。
type Manager struct {
	configs map[string]ServerConfig
	names   []string // 名前の順(ツールを並べる順を毎回同じにする。プロンプトが変わると KV キャッシュが効かなくなるため)
	opt     Options

	// transport はサーバーへのつなぎ方。本物は子プロセスを起動する(commandTransport)。テストではプロセスを起動しないものに差し替える
	transport func(name string, cfg ServerConfig, workDir string) (mcp.Transport, error)

	mu      sync.Mutex
	running map[serverKey]*server
	closed  bool
}

// serverKey は起動したサーバーの見分け方。${workdir} を使わないサーバーは workDir が空(全部の会話で1つを共有する)。
type serverKey struct {
	name    string
	workDir string
}

// server は起動したサーバー1つ。
type server struct {
	key     serverKey
	session *mcp.ClientSession
	tools   []tool.Tool
}

// New は Manager を作る(サーバーはまだ起動しない)。configs は Validate 済みのもの。
func New(configs map[string]ServerConfig, opt Options) *Manager {
	if opt.Logf == nil {
		opt.Logf = log.Printf
	}
	m := &Manager{configs: configs, opt: opt, running: map[serverKey]*server{}}
	for name := range configs {
		m.names = append(m.names, name)
	}
	slices.Sort(m.names)
	m.transport = m.commandTransport
	return m
}

// Start は、作業フォルダ workDir の会話で使うサーバーのうち、まだ動いていないものを起動する。
// 起動したサーバーの名前と、起動できなかったサーバーのエラーを返す(起動できなくても、ほかのサーバーと会話は続けられる)。
// workDir が空なら、${workdir} を使うサーバーは起動しない。
func (m *Manager) Start(ctx context.Context, workDir string) (started []string, errs []error) {
	for _, name := range m.names {
		cfg := m.configs[name]
		k := serverKey{name: name}
		if cfg.UsesWorkDir() {
			if workDir == "" {
				continue
			}
			k.workDir = workDir
		}
		m.mu.Lock()
		_, ok := m.running[k]
		closed := m.closed
		m.mu.Unlock()
		if ok || closed {
			continue
		}
		if err := m.start(ctx, k, cfg); err != nil {
			errs = append(errs, fmt.Errorf("MCP のサーバー %s を起動できません: %w", name, err))
			continue
		}
		started = append(started, name)
	}
	return started, errs
}

// start はサーバーを1つ起動し、つないで、ツールの一覧を受け取る。
func (m *Manager) start(ctx context.Context, k serverKey, cfg ServerConfig) error {
	ctx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	begin := time.Now()
	m.opt.Logf("MCP %s: 起動します: %s", k.name, cfg.commandLine(k.workDir))

	t, err := m.transport(k.name, cfg, k.workDir)
	if err != nil {
		return err
	}
	// 送受信した JSON-RPC のメッセージを、そのままログに出す(SDK の LoggingTransport)。MCP の正体が見える
	t = &mcp.LoggingTransport{Transport: t, Writer: &lineLogger{logf: m.opt.Logf, prefix: "MCP " + k.name + " "}}

	// できることは何も名乗らない(空の ClientCapabilities)。SDK の既定は、昔からの名残で「Roots に対応している」と名乗るが、
	// aibow は Roots を使わず、作業フォルダは起動の引数で渡す。filesystem サーバーは、クライアントが Roots に対応していると
	// Roots で受け取ったフォルダを引数のフォルダの代わりに使うので、名乗ると引数の作業フォルダが使われなくなるおそれがある
	client := mcp.NewClient(&mcp.Implementation{Name: "aibow", Version: m.opt.Version},
		&mcp.ClientOptions{Capabilities: &mcp.ClientCapabilities{}})
	session, err := client.Connect(ctx, t, nil)
	if err != nil {
		return fmt.Errorf("つなげません(command・args を確かめてください。サーバーのエラー出力はログにあります): %w", err)
	}
	tools, err := listTools(ctx, k.name, session)
	if err != nil {
		session.Close()
		return fmt.Errorf("ツールの一覧を受け取れません: %w", err)
	}

	// どの版の MCP で話すことになったか。2026-07-28 版からは最初の挨拶(initialize)が無くなり、
	// 古い版のサーバーとは、SDK が版を取り決めて挨拶をしてから話す
	ver, info := "?", "?"
	if r := session.InitializeResult(); r != nil {
		ver = r.ProtocolVersion
		if r.ServerInfo != nil {
			info = r.ServerInfo.Name + " " + r.ServerInfo.Version
		}
	}
	m.opt.Logf("MCP %s: つながりました(%.1f 秒。MCP の版 %s・サーバー %s・ツール %d 個: %s)",
		k.name, time.Since(begin).Seconds(), ver, info, len(tools), tool.Names(tools))

	s := &server{key: k, session: session, tools: tools}
	m.mu.Lock()
	if m.closed { // 起動している間にアプリが閉じられた
		m.mu.Unlock()
		session.Close()
		return errors.New("アプリを閉じるところなので、起動をやめました")
	}
	m.running[k] = s
	m.mu.Unlock()

	// サーバーが落ちたら(つながりが切れたら)、一覧から外す。次の Start で起動し直す
	go func() {
		err := session.Wait()
		m.mu.Lock()
		gone := m.running[k] == s
		if gone {
			delete(m.running, k)
		}
		closed := m.closed
		m.mu.Unlock()
		if gone && !closed {
			m.opt.Logf("※ MCP %s: サーバーとのつながりが切れました(%v)。次の送信で起動し直します", k.name, err)
		}
	}()
	return nil
}

// listTools はサーバーのツールの一覧を受け取り、tool.Tool にする。
func listTools(ctx context.Context, name string, session *mcp.ClientSession) ([]tool.Tool, error) {
	var tools []tool.Tool
	for t, err := range session.Tools(ctx, nil) { // 一覧が何ページかに分かれていても、全部たどる
		if err != nil {
			return nil, err
		}
		rt, err := newRemoteTool(name, session, t)
		if err != nil {
			return nil, err
		}
		tools = append(tools, rt)
	}
	return tools, nil
}

// Tools は、作業フォルダ workDir の会話で使える、起動済みのサーバーのツール(サーバーの名前の順)。
// サーバーを起動はしない(画面のメーターの計算などからも呼ばれるため。起動は送信のときの Start)。
func (m *Manager) Tools(workDir string) []tool.Tool {
	m.mu.Lock()
	defer m.mu.Unlock()
	var tools []tool.Tool
	for _, name := range m.names {
		k := serverKey{name: name}
		if m.configs[name].UsesWorkDir() {
			if workDir == "" {
				continue
			}
			k.workDir = workDir
		}
		if s, ok := m.running[k]; ok {
			tools = append(tools, s.tools...)
		}
	}
	return tools
}

// Close は起動したサーバーを全部止める(アプリを閉じるとき)。
// サーバーの標準入力を閉じて自分で終わるのを待ち(MCP の stdio の決まりごと)、終わらなければ止める。
// npx から起動された node のような子プロセスも残さない(commandTransport)。
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	servers := make([]*server, 0, len(m.running))
	for _, s := range m.running {
		servers = append(servers, s)
	}
	m.running = map[serverKey]*server{}
	m.mu.Unlock()

	// 1つずつ待つと、サーバーの数だけアプリの終わりが遅くなるので、まとめて止める
	var wg sync.WaitGroup
	for _, s := range servers {
		wg.Go(func() {
			if err := s.session.Close(); err != nil {
				m.opt.Logf("MCP %s: 止めました(%v)", s.key.name, err)
				return
			}
			m.opt.Logf("MCP %s: 止めました", s.key.name)
		})
	}
	wg.Wait()
}

// commandTransport は、サーバーを子プロセスとして起動する本物のつなぎ方。
//
// SDK の CommandTransport は、閉じるときに「標準入力を閉じる → 待つ → SIGTERM → 止める」(MCP の決まりごと)を行うが、
// Windows では起動したプロセス(npx.cmd を動かす cmd.exe)しか止めないので、その子の node が残ってしまう。
// そこで起動した直後にジョブオブジェクトに入れ(internal/proc)、閉じた後にジョブごと止める。
func (m *Manager) commandTransport(name string, cfg ServerConfig, workDir string) (mcp.Transport, error) {
	cmd := cfg.command(workDir)
	// サーバーのエラー出力は、そのサーバーのログ(stdio では、標準出力は MCP のメッセージ専用。ログはエラー出力に出す決まり)
	cmd.Stderr = &lineLogger{logf: m.opt.Logf, prefix: "MCP " + name + " (stderr) "}
	proc.Prepare(cmd)
	return &treeTransport{inner: &mcp.CommandTransport{Command: cmd, TerminateDuration: closeWait}}, nil
}

// treeTransport は、起動したサーバーを子プロセスごとまとめる(proc.Tree)CommandTransport。
type treeTransport struct {
	inner *mcp.CommandTransport
}

func (t *treeTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	conn, err := t.inner.Connect(ctx) // ここで子プロセスが起動する
	if err != nil {
		return nil, err
	}
	tree, err := proc.Attach(t.inner.Command)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return &treeConn{Connection: conn, tree: tree}, nil
}

// treeConn は、閉じた後に、残った子プロセスもまとめて止める。
type treeConn struct {
	mcp.Connection
	tree *proc.Tree
}

func (c *treeConn) Close() error {
	err := c.Connection.Close()
	c.tree.Close()
	return err
}
