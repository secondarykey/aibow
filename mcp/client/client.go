// Package client は、aibow を MCP(Model Context Protocol)のクライアントにする部分(mcp/client)。
//
// MCP は、ツールやデータを AI アプリにつなぐための決まりごと。中身は JSON-RPC 2.0 のメッセージのやり取りで、
// ローカルでは「クライアント(aibow)がサーバーを子プロセスとして起動し、標準入出力で1行ずつ JSON を送り合う」(stdio のトランスポート)。
// どこかで動いているサーバーには、同じ JSON を HTTP の POST で送る(Streamable HTTP のトランスポート)。
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
// SDK のパッケージ名が mcp なので、こちらは mcp/ の下に置いて client にしている(ステップ5で作るサーバーは mcp/server に並べる)。
package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/secondarykey/aibow/internal/proc"
	"github.com/secondarykey/aibow/log"
	"github.com/secondarykey/aibow/tool"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ServerConfig は MCP のサーバー1つのつなぎ方(settings.json の mcpServers の1つ)。
// 形は、多くのツールで使われている mcpServers に合わせている(ほかのツールの設定をそのまま貼れるように)。
//
// つなぎ方(トランスポート)は2つ。どちらか一方を書く:
//
//	stdio           … command(と args・env)。aibow がサーバーを子プロセスとして起動し、標準入出力で話す
//	Streamable HTTP … url(と headers)。どこかで動いているサーバーに、HTTP で話しかける(イントラの中のサーバーなど)
type ServerConfig struct {
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`

	URL string `json:"url,omitempty"`
	// Headers は、url のサーバーへのリクエストに毎回付ける HTTP ヘッダー(API キーなど)。
	// OAuth(ブラウザでのログイン)には、まだ対応していない(TODO.md のロードマップの外)
	Headers map[string]string `json:"headers,omitempty"`

	// Type はトランスポートの種類。書かなくてよい(command か url で決まる)。
	// ほかのツールの設定には "type": "stdio" / "http" と書いたものがあるので、読んで確かめる
	Type string `json:"type,omitempty"`

	// StartupTimeout は、起動してつながる(ツールの一覧を受け取る)までの上限の秒数。書かなければ 60 秒。
	// 初回の npx・uv は、パッケージのダウンロードや環境の組み立てで 60 秒を超えることがあるので、サーバーごとに延ばせるようにしている
	StartupTimeout int `json:"startupTimeout,omitempty"`
}

// workDirVar は、args と env の値の中で会話の作業フォルダに置き換える文字列。
//
// 作業フォルダはサーバーの起動の引数で渡す。MCP の Roots(クライアントがフォルダを伝える仕組み)は
// 2026-07-28 版で非推奨になり、「設定かツールの引数で渡す」のが新しいやり方のため。
// そのため、これを使うサーバーは作業フォルダごとに1つ起動する。
const workDirVar = "${workdir}"

// Validate は設定の形を確かめる。
func (c ServerConfig) Validate() error {
	if c.StartupTimeout < 0 {
		return fmt.Errorf("startupTimeout は秒数(1 以上)で書いてください: %d", c.StartupTimeout)
	}
	switch {
	case c.Type == "sse":
		return errors.New("古い HTTP+SSE のトランスポート(type: sse)には対応していません(MCP の 2025-03-26 版から非推奨)。" +
			"Streamable HTTP(type: http)のサーバーなら、url だけを書けばつなげます")
	case c.Type != "" && c.Type != "stdio" && c.Type != "http" && c.Type != "streamable-http":
		return fmt.Errorf("type %q は知らない種類です(stdio か http。書かなくても command か url で決まります)", c.Type)
	case c.URL != "" && strings.TrimSpace(c.Command) != "":
		return errors.New("command と url の両方があります。どちらか1つにしてください(command はサーバーを起動する、url は動いているサーバーにつなぐ)")
	case c.URL != "":
		if c.Type == "stdio" {
			return errors.New("type が stdio なのに url があります。stdio なら command を書いてください")
		}
		u, err := url.Parse(c.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("url %q は http:// か https:// で始まる URL で書いてください", c.URL)
		}
		if strings.Contains(c.URL, workDirVar) || slices.ContainsFunc(slices.Collect(maps.Values(c.Headers)), func(v string) bool { return strings.Contains(v, workDirVar) }) {
			return errors.New("url のサーバーには作業フォルダ(${workdir})を渡せません(別の PC で動いているかもしれず、こちらのフォルダは見えないため)")
		}
		return nil
	case strings.TrimSpace(c.Command) == "":
		return errors.New("command(起動するプログラム)か url(つなぐ先)がありません")
	case c.Type == "http" || c.Type == "streamable-http":
		return errors.New("type が http なのに url がありません")
	}
	return nil
}

// startupTimeout は起動の上限。
func (c ServerConfig) startupTimeout() time.Duration {
	if c.StartupTimeout > 0 {
		return time.Duration(c.StartupTimeout) * time.Second
	}
	return defaultStartupTimeout
}

// isHTTP は url のサーバー(Streamable HTTP)か。
func (c ServerConfig) isHTTP() bool { return c.URL != "" }

// UsesWorkDir は、作業フォルダごとに起動するサーバーか(args か env に ${workdir} があるか)。
// url のサーバーには作業フォルダを渡せないので、いつも false。
func (c ServerConfig) UsesWorkDir() bool {
	if c.isHTTP() {
		return false
	}
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

// describe はログに出すための、つなぎ方の説明(起動のコマンドか、つなぐ先の url)。
// ヘッダー(API キーなど)はログに出さない。
func (c ServerConfig) describe(workDir string) string {
	if c.isHTTP() {
		return "つなぎます: " + c.URL
	}
	cmd := c.command(workDir)
	return "起動します: " + strings.Join(append([]string{c.Command}, cmd.Args[1:]...), " ")
}

const (
	// defaultStartupTimeout はサーバーの起動(つないでツールの一覧を受け取るまで)の時間の上限の既定。
	// 初回の npx はパッケージをダウンロードするので長めにしている。サーバーごとに startupTimeout で変えられる
	defaultStartupTimeout = 60 * time.Second
	// closeWait は、閉じるときに標準入力を閉じてから、サーバーが自分で終わるのを待つ時間。過ぎたら止める
	closeWait = 2 * time.Second
)

// Options は Manager の設定。
type Options struct {
	// Version は、サーバーに名乗る aibow のバージョン(clientInfo)
	Version string
}

// Manager は settings.json に書いた MCP のサーバーを起動し、そのツールを渡す。
//
// サーバーは、要る最初の送信のときに起動し(アプリの起動を遅くしないため)、アプリを閉じるまで残す。
// 落ちていたら、次の Start で起動し直す。
type Manager struct {
	configs map[string]ServerConfig
	names   []string // 名前の順(ツールを並べる順を毎回同じにする。プロンプトが変わると KV キャッシュが効かなくなるため)
	opt     Options

	// transport はサーバーへのつなぎ方。本物は子プロセスを起動するか HTTP で話す(realTransport)。テストではプロセスを起動しないものに差し替える。
	// stderr は、子プロセスのサーバーのエラー出力の書き先
	transport func(name string, cfg ServerConfig, workDir string, stderr io.Writer) (mcp.Transport, error)

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
	m := &Manager{configs: configs, opt: opt, running: map[serverKey]*server{}}
	for name := range configs {
		m.names = append(m.names, name)
	}
	slices.Sort(m.names)
	m.transport = m.realTransport
	return m
}

// Servers は settings.json に書いたサーバーの名前(名前の順)。
func (m *Manager) Servers() []string {
	return slices.Clone(m.names)
}

// ServerInfo は、画面で会話ごとにサーバーを選ぶときに出す情報。
type ServerInfo struct {
	Name        string `json:"name"`          // settings.json の名前
	HTTP        bool   `json:"http"`          // url のサーバー(そうでなければ command で起動する stdio のサーバー)
	UsesWorkDir bool   `json:"uses_work_dir"` // ${workdir} を使う(作業フォルダを決めた会話でしか使えない)
}

// Infos は settings.json に書いたサーバーの情報(名前の順)。
func (m *Manager) Infos() []ServerInfo {
	out := make([]ServerInfo, 0, len(m.names))
	for _, name := range m.names {
		c := m.configs[name]
		out = append(out, ServerInfo{Name: name, HTTP: c.isHTTP(), UsesWorkDir: c.UsesWorkDir()})
	}
	return out
}

// selected は、names のうち use に入っているもの(名前の順)。知らない名前(settings.json から消したサーバー)は無視する。
func (m *Manager) selected(use []string) []string {
	var out []string
	for _, name := range m.names {
		if slices.Contains(use, name) {
			out = append(out, name)
		}
	}
	return out
}

// Start は、作業フォルダ workDir の会話で使うサーバー(use)のうち、まだ動いていないものを起動する。
// 起動したサーバーの名前と、起動できなかったサーバーのエラーを返す(起動できなくても、ほかのサーバーと会話は続けられる)。
// workDir が空なら、${workdir} を使うサーバーは起動しない。会話で選んでいないサーバーも起動しない。
func (m *Manager) Start(ctx context.Context, workDir string, use []string) (started []string, errs []error) {
	for _, name := range m.selected(use) {
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
		if err := m.startAndKeep(ctx, k, cfg); err != nil {
			errs = append(errs, fmt.Errorf("MCP のサーバー %s を起動できません: %w", name, err))
			continue
		}
		started = append(started, name)
	}
	return started, errs
}

// startAndKeep はサーバーを1つ起動してつなぎ、動いているサーバーの一覧に入れる(アプリを閉じるまで残す)。
func (m *Manager) startAndKeep(ctx context.Context, k serverKey, cfg ServerConfig) error {
	s, err := m.connect(ctx, k, cfg)
	if err != nil {
		return err
	}
	m.mu.Lock()
	if m.closed { // 起動している間にアプリが閉じられた
		m.mu.Unlock()
		s.session.Close()
		return errors.New("アプリを閉じるところなので、起動をやめました")
	}
	m.running[k] = s
	m.mu.Unlock()

	// stdio のサーバーが落ちたら、つながりが閉じて Wait が返る。
	// url のサーバーは、リクエストのたびに HTTP でつなぐので、落ちたことはツールの呼び出しが失敗して分かる(broken)
	go func() { m.drop(s, s.session.Wait()) }()
	return nil
}

// ListTools は、サーバーを一時的に起動してツールの一覧を受け取り、すぐ止める。
//
// 設定の画面で、会話で使う前に「このツールは使わない」を決められるようにするため
// (使うツールを決めてから会話で使いたいのに、一度使わないと一覧が分からない、では順番が逆になる)。
// ${workdir} を使うサーバーには、空の一時フォルダを渡す(ツールの一覧はフォルダによらないため。一覧を受け取ったら消す)。
// 動いているサーバーの一覧には入れない(会話のための起動とは別)。
func (m *Manager) ListTools(ctx context.Context, name string) ([]tool.Tool, error) {
	cfg, ok := m.configs[name]
	if !ok {
		return nil, fmt.Errorf("MCP のサーバー %s は settings.json にありません", name)
	}
	k := serverKey{name: name}
	if cfg.UsesWorkDir() {
		dir, err := os.MkdirTemp("", "aibow-mcp-list-")
		if err != nil {
			return nil, fmt.Errorf("一時フォルダを作れません: %w", err)
		}
		defer os.RemoveAll(dir) // サーバーを止めてから消す(defer は後に書いたものから動く)
		k.workDir = dir
	}
	log.Infof("MCP %s: ツールの一覧を受け取るために、一時的につなぎます", name)
	s, err := m.connect(ctx, k, cfg)
	if err != nil {
		return nil, fmt.Errorf("MCP のサーバー %s を起動できません: %w", name, err)
	}
	defer func() {
		s.session.Close()
		log.Infof("MCP %s: ツールの一覧を受け取ったので止めました", name)
	}()
	return s.tools, nil
}

// connect はサーバーを1つ起動し(url のサーバーなら、つなぎ)、ツールの一覧を受け取る。
func (m *Manager) connect(ctx context.Context, k serverKey, cfg ServerConfig) (*server, error) {
	limit := cfg.startupTimeout()
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	begin := time.Now()
	log.Infof("MCP %s: %s", k.name, cfg.describe(k.workDir))

	// サーバーのエラー出力(stderr)はログに出し、最後の行を覚えておく。起動に失敗したとき、理由はたいていそこに書いてある
	stderr := &lineLogger{prefix: "MCP " + k.name + " (stderr) "}
	t, err := m.transport(k.name, cfg, k.workDir, stderr)
	if err != nil {
		return nil, err
	}
	hint := "command・args を確かめてください"
	if cfg.isHTTP() {
		hint = "url と、サーバーが動いているかを確かめてください"
	}
	fail := func(what string, err error) error {
		return startFailure(ctx, what, hint, limit, stderr, err)
	}
	// 送受信した JSON-RPC のメッセージを、そのままログに出す(SDK の LoggingTransport)。MCP の正体が見える
	t = &mcp.LoggingTransport{Transport: t, Writer: &lineLogger{prefix: "MCP " + k.name + " "}}

	// できることは何も名乗らない(空の ClientCapabilities)。SDK の既定は、昔からの名残で「Roots に対応している」と名乗るが、
	// aibow は Roots を使わず、作業フォルダは起動の引数で渡す。filesystem サーバーは、クライアントが Roots に対応していると
	// Roots で受け取ったフォルダを引数のフォルダの代わりに使うので、名乗ると引数の作業フォルダが使われなくなるおそれがある
	client := mcp.NewClient(&mcp.Implementation{Name: "aibow", Version: m.opt.Version},
		&mcp.ClientOptions{Capabilities: &mcp.ClientCapabilities{}})
	session, err := client.Connect(ctx, t, nil)
	if err != nil {
		return nil, fail("つなげません", err)
	}
	s := &server{key: k, session: session}
	// つながりが切れた(サーバーが落ちた・再起動した)と分かったら、一覧から外す。次の Start でつなぎ直す
	broken := func(err error) { m.drop(s, err) }
	tools, err := listTools(ctx, k.name, session, broken)
	if err != nil {
		session.Close()
		return nil, fail("ツールの一覧を受け取れません", err)
	}
	s.tools = tools

	// どの版の MCP で話すことになったか。2026-07-28 版からは最初の挨拶(initialize)が無くなり、
	// 古い版のサーバーとは、SDK が版を取り決めて挨拶をしてから話す
	ver, info := "?", "?"
	if r := session.InitializeResult(); r != nil {
		ver = r.ProtocolVersion
		if r.ServerInfo != nil {
			info = r.ServerInfo.Name + " " + r.ServerInfo.Version
		}
	}
	log.Infof("MCP %s: つながりました(%.1f 秒。MCP の版 %s・サーバー %s・ツール %d 個: %s)",
		k.name, time.Since(begin).Seconds(), ver, info, len(tools), tool.Names(tools))

	return s, nil
}

// startFailure は、起動に失敗した理由の文を作る(画面のお知らせに出る)。
// 時間切れなら上限の延ばし方を、サーバーがエラー出力に何か書いていれば、その最後の行を添える
// (ログを見なくても、たいていの原因が分かるように。例: uv の「アクセスが拒否されました」)。
func startFailure(ctx context.Context, what, hint string, limit time.Duration, stderr *lineLogger, err error) error {
	msg := fmt.Sprintf("%s(%s)", what, hint)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		msg = fmt.Sprintf("起動の上限(%d 秒)までにつながりませんでした。初回はダウンロードや環境の組み立てで時間がかかることがあります"+
			"(settings.json の startupTimeout に秒数を書くと延ばせます)", int(limit.Seconds()))
	}
	if last := stderr.last(); last != "" {
		msg += "。サーバーのエラー出力の最後の行: " + last
	}
	return fmt.Errorf("%s: %w", msg, err)
}

// drop は、つながりが切れたサーバーを一覧から外す(次の Start で起動し直す・つなぎ直す)。
// すでに外していたら(閉じた・つなぎ直した後なら)何もしない。
func (m *Manager) drop(s *server, err error) {
	m.mu.Lock()
	gone := m.running[s.key] == s
	if gone {
		delete(m.running, s.key)
	}
	closed := m.closed
	m.mu.Unlock()
	if !gone || closed {
		return
	}
	log.Warnf("MCP %s: サーバーとのつながりが切れました(%v)。次の送信でつなぎ直します", s.key.name, err)
	go s.session.Close() // 子プロセスが残っていれば止める(Wait から呼ばれたときは、もう閉じている)
}

// listTools はサーバーのツールの一覧を受け取り、tool.Tool にする。
func listTools(ctx context.Context, name string, session *mcp.ClientSession, broken func(error)) ([]tool.Tool, error) {
	var tools []tool.Tool
	for t, err := range session.Tools(ctx, nil) { // 一覧が何ページかに分かれていても、全部たどる
		if err != nil {
			return nil, err
		}
		rt, err := newRemoteTool(name, session, t, broken)
		if err != nil {
			return nil, err
		}
		tools = append(tools, rt)
	}
	return tools, nil
}

// Tools は、作業フォルダ workDir の会話で使う(use)、起動済みのサーバーのツール(サーバーの名前の順)。
// サーバーを起動はしない(画面のメーターの計算などからも呼ばれるため。起動は送信のときの Start)。
func (m *Manager) Tools(workDir string, use []string) []tool.Tool {
	m.mu.Lock()
	defer m.mu.Unlock()
	var tools []tool.Tool
	for _, name := range m.selected(use) {
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
				log.Infof("MCP %s: 止めました(%v)", s.key.name, err)
				return
			}
			log.Infof("MCP %s: 止めました", s.key.name)
		})
	}
	wg.Wait()
}

// realTransport は本物のつなぎ方。command のサーバーは子プロセスとして起動し、url のサーバーには HTTP で話しかける。
func (m *Manager) realTransport(name string, cfg ServerConfig, workDir string, stderr io.Writer) (mcp.Transport, error) {
	if cfg.isHTTP() {
		return httpTransport(cfg), nil
	}
	return commandTransport(cfg, workDir, stderr)
}

// httpTransport は url のサーバーへのつなぎ方(Streamable HTTP)。
//
// aibow → サーバーのメッセージは、1つずつ HTTP の POST で送る。返事は、その POST の応答(JSON か、SSE で何回かに分けて)で返ってくる。
// サーバーから好きなときに知らせを送るための、つなぎっぱなしの GET(standalone SSE)は開かない
// (ツールの一覧が変わった知らせなどは使わないため。つなぎっぱなしの接続を持たないほうが、イントラのプロキシなどで困りにくい)。
func httpTransport(cfg ServerConfig) mcp.Transport {
	client := &http.Client{Transport: headerTransport{headers: cfg.Headers, base: http.DefaultTransport}}
	return &mcp.StreamableClientTransport{Endpoint: cfg.URL, HTTPClient: client, DisableStandaloneSSE: true}
}

// headerTransport は、リクエストに settings.json の headers(API キーなど)を付ける。
type headerTransport struct {
	headers map[string]string
	base    http.RoundTripper
}

func (t headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if len(t.headers) > 0 {
		r = r.Clone(r.Context()) // RoundTrip は渡されたリクエストを書き換えてはいけない決まり
		for k, v := range t.headers {
			r.Header.Set(k, v)
		}
	}
	return t.base.RoundTrip(r)
}

// commandTransport は、サーバーを子プロセスとして起動するつなぎ方(stdio)。
//
// SDK の CommandTransport は、閉じるときに「標準入力を閉じる → 待つ → SIGTERM → 止める」(MCP の決まりごと)を行うが、
// Windows では起動したプロセス(npx.cmd を動かす cmd.exe)しか止めないので、その子の node が残ってしまう。
// そこで起動した直後にジョブオブジェクトに入れ(internal/proc)、閉じた後にジョブごと止める。
func commandTransport(cfg ServerConfig, workDir string, stderr io.Writer) (mcp.Transport, error) {
	cmd := cfg.command(workDir)
	// サーバーのエラー出力は、そのサーバーのログ(stdio では、標準出力は MCP のメッセージ専用。ログはエラー出力に出す決まり)
	cmd.Stderr = stderr
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
