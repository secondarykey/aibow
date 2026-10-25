// aibow: ローカルLLMを自分で動かして、仕組みを学ぶためのチャットアプリ(デスクトップ版)
//
//	ウィンドウ(frontend の React) ⇄ Wails ⇄ ChatService ⇄ aibow パッケージ ⇄ llama.cpp(DLL) ⇄ GPU
//	                                                         ⇅                  └─(つなぎ先を書いたとき)HTTP ⇄ aibow-server
//	                                                   store パッケージ(SQLite)
//
// Wails はデスクトップアプリの枠組み。OS に入っている WebView(Windows なら WebView2)で
// HTML の画面を表示し、画面の JavaScript から Go のメソッドを呼べるようにする。
// ブラウザ版との違いは「画面と Go の間が HTTP ではなく Wails になった」ことだけで、
// LLM を動かす部分(リポジトリのルートの aibow パッケージ)は同じものを使っている。
//
// Wails に依存するコードはこのフォルダ(_cmd/aibow)だけに置く。
//
// ファイルの役割:
//
//	main.go    … 起動(このファイル)
//	config.go  … 環境変数から設定を読む(settings.json は aibow.LoadSettingsFile、MCP のサーバーは startMCP。aibow-server のつなぎ先は loadServer)
//	connection.go … 画面から aibow-server につなぐ・ログインする・戻す(connection.json。0019)と、起動し直す
//	service.go … 画面から呼ばれるメソッドを Wails に渡す(中身はルートの ui パッケージ。モデル・会話・設定・送信と、モデルを扱う係)
//	frontend/  … 画面(React + TypeScript)。Vite でビルドした結果は、共有のパッケージ web(リポジトリのルートの web/dist/app)に出て、実行ファイルに埋め込まれる
//	build/     … wails3 build / wails3 dev の設定(アプリ名、アイコン、OS ごとの手順)
package main

import (
	"context"
	_ "embed" // version を埋め込む
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/secondarykey/aibow"
	apiclient "github.com/secondarykey/aibow/api/client"
	"github.com/secondarykey/aibow/engine"
	"github.com/secondarykey/aibow/log"
	"github.com/secondarykey/aibow/mcp/client"
	"github.com/secondarykey/aibow/models"
	"github.com/secondarykey/aibow/store"
	"github.com/secondarykey/aibow/store/remote"
	"github.com/secondarykey/aibow/ui"
	"github.com/secondarykey/aibow/web"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// windowFocusEventName は、ウィンドウがアクティブに戻ったことを画面に知らせるイベントの名前。
const windowFocusEventName = "aibow:window:focus"

// version はアプリのバージョン。_cmd/aibow/version(唯一の正)を埋め込む。
// 上げ方は _cmd/version.go を参照。
//
//go:embed version
var version string

func init() {
	// chat イベントで送るデータの型を Wails に教えておく。
	// バインディング生成のときに TypeScript の型(ChatEvent)が作られ、画面側で型付きで受け取れる。
	application.RegisterEvent[aibow.ChatEvent](ui.ChatEventName)
}

func main() {
	// ログの出口を最初に組む(設定の読み込みのログから残すため)。aibow のライブラリのログも、アプリのログもここに集まる
	logs, logNotices := startLog()
	defer logs.Close()
	// 画面でつなぎ先を変えたら、ほかの後片付け(モデルの解放・DB を閉じる)が済んでから起動し直す(0019 の決定事項 1)。
	// defer は後に書いたものから動くので、ログを閉じる直前に動く
	var conn *connection
	defer func() {
		if conn != nil && conn.restart.Load() {
			relaunch()
		}
	}()

	// 起動に失敗しても(lib が無いなど)ウィンドウは出し、理由を画面に表示する。
	// リリース版(wails3 build)はコンソールが無く、ログだけでは何も見えずに終わってしまうため。
	cfg, err := loadConfig()
	conn = newConnection(cfg)
	// ログが書けない・前回は異常終了した、などは起動を止めず、画面のお知らせで伝える
	cfg.notices = append(cfg.notices, logNotices...)
	var mcpm *client.Manager
	var prefs *aibow.ToolPrefs
	// 組み込みのツールは、aibow の中で自作の MCP サーバーを動かし、MCP 越しに呼ぶ(0007 の 5-4)。サーバーは要る最初のときに動かす
	local := client.NewLocal(client.Options{Version: appVersion()})
	defer local.Close()
	if err == nil {
		if mcpm = startMCP(&cfg); mcpm != nil {
			defer mcpm.Close() // アプリを閉じたら、起動した MCP のサーバーも止める(子プロセスまで)
		}
		prefs = loadToolPrefs(&cfg)
	}
	for _, n := range cfg.notices {
		log.Warn(n)
	}
	if err == nil {
		// settings.json に足したばかりの MCP のサーバーは、新しい会話で最初から使わないにする(画面のお知らせでも伝える)
		if n := noteNewServers(mcpm, prefs); n != "" {
			cfg.notices = append(cfg.notices, n)
		}
	}
	var st aibow.Store
	var settings aibow.AppSettings
	var host ui.ModelHost
	var eng *engine.Engine
	var initial *models.Entry
	var server *apiclient.Client // aibow-server につなぐとき(serverUrl)。生成も保存も、ここに頼む(0017 の 10-3)
	if err == nil && cfg.serverURL != "" {
		server, err = newServerClient(cfg)
	}
	if err == nil {
		var closeStore func()
		st, settings, closeStore, err = openStore(cfg, server)
		if closeStore != nil {
			defer closeStore()
		}
	}
	if err == nil {
		host, eng, initial, err = startModels(&cfg, settings, server)
	}
	if err != nil {
		log.Errorf("起動できません: %v", err)
	} else if eng != nil {
		defer eng.Close()
	}

	// 画面は共有のパッケージ web に埋め込んである(aibow-server がブラウザに配るものと同じ。0016)
	assets, built := web.Files()
	if !built {
		log.Warn("画面がビルドされていません(web/dist/app が無い)。wails3 build で画面をビルドしてください")
	}
	app := application.New(application.Options{
		Name:        "aibow",
		Description: "ローカルLLMを自分で動かして仕組みを学ぶチャット",
		// Wails 自身のログも aibow と同じ出口(startLog が組んだ slog.Default())に出す。
		// 渡さないと、Wails は dev では自分の書式で標準エラー出力に出し、リリース版では捨てる(exe でエラーが残らない)。
		// なお WebView2 の行([WebView2] …)は標準の log で来るが、slog.SetDefault のあとは標準の log も同じ出口に流れる
		Logger: slog.Default(),
		Services: []application.Service{
			application.NewService(NewChatService(ui.Options{
				Host: host, Store: st, App: settings, Initial: initial, Local: local, MCP: mcpm, Prefs: prefs,
				Version: appVersion(), Notices: cfg.notices, StartupErr: err,
			}, conn)),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	// Frameless: OS のタイトルバーと枠を消し、画面(frontend/src/components/TitleBar.tsx)で作る。
	// Title は画面には出ないが、タスクバーや Alt+Tab に出る。
	// 前に閉じたときの位置と大きさがあれば、そこに開く(windowstate.go)
	opts := application.WebviewWindowOptions{
		Title:     "aibow " + appVersion(),
		Width:     720, // 初めて起動したときの大きさ(広げたいときは最大化かリサイズ)
		Height:    820,
		MinWidth:  windowMinWidth,
		MinHeight: windowMinHeight,
		Frameless: true,
		URL:       "/",
	}
	saved, hasSaved := loadWindowState(cfg.windowPath)
	if hasSaved {
		applyWindowState(&opts, saved)
	}
	window := app.Window.NewWithOptions(opts)
	tracker := trackWindow(app, window, cfg.windowPath, saved, hasSaved)

	// Windows の WebView2 は、別のウィンドウから戻ってきたときに IME(日本語入力)の状態が
	// 戻らず、入力欄で変換できなくなることがある。戻ってきたことを画面に知らせ、
	// 画面側(frontend/src/imeFocus.ts)で入力欄のフォーカスを付け直して直す。
	// events.Common.WindowFocus は Windows では発火しないので、Windows 用のイベントを使う。
	window.OnWindowEvent(events.Windows.WindowSetFocus, func(*application.WindowEvent) {
		app.Event.Emit(windowFocusEventName)
	})

	log.Noticef("aibow %s 起動", appVersion())
	// Run はウィンドウが閉じられるまで返らない。返ったら defer で VRAM を解放する。
	runErr := app.Run()
	// 終わったら、覚えておいたウィンドウの位置と大きさを書く。
	// OnShutdown は使わない:Windows では最後のウィンドウを閉じる(× や Alt+F4)と、OnShutdown を呼ばずに終わるため(beta.26)
	tracker.save()
	if runErr != nil {
		log.Error(runErr.Error())
		os.Exit(1)
	}
}

// ウィンドウの最小の大きさ。幅は画面の端に細く置いておけるよう、初めの幅(720)より狭くできる。
// 高さはこれより低いと、入力欄とモデルの行で会話がほとんど見えなくなる
const (
	windowMinWidth  = 480
	windowMinHeight = 480
)

// logKeepDays は、ログファイルを残す日数(今日を含む)。起動のたびに、それより古いものを消す。
const logKeepDays = 10

// devLogPrefix は、wails3 dev で動かしたときのログファイルの名前の頭(aibow-dev_<日付>.log・aibow-dev_crash.log)。
// ふだん使う exe と同時に動かすことがあるので、exe のファイル(aibow_…)と分ける
// (同じファイルに2つのプロセスが書くと行が入り混じり、異常終了の記録も取り合いになる。aibow-mcp と同じ考え方)
const devLogPrefix = "aibow-dev"

// startLog はログの出口を組む(aibow/log の Init)。
//
//   - wails3 dev(isDev):ターミナル(標準エラー出力)と、ファイル(%APPDATA%\aibow\logs\aibow-dev_<日付>.log)の両方に Info 以上を書く。
//     ターミナルは流れて消えるので、あとから不具合を調べられるようにファイルにも残す(2026-10-16、ユーザーの指示。前は AIBOW_LOG_DIR を指定したときだけ)
//   - リリース版の exe:ファイルに書く(%APPDATA%\aibow\logs\aibow_<日付>.log)。
//     リリース版(-H windowsgui)には標準エラー出力の行き先が無く、ファイルにしか残らないため
//     (0006 で、起動できない理由がどこにも残らず困った)。Notice 以上だけを書く
//
// フォルダは AIBOW_LOG_DIR で変えられる。どちらも今日を含めて logKeepDays 日分を残し、古いものは起動時に消す(頭の違うファイルには触らない)
//
// 異常終了(Go の panic・ランタイムの致命的なエラー)の内容も、ファイルに書くときは同じフォルダの aibow_crash.log に残す
// (log.CatchCrash)。前回の記録が残っていれば改名して残し、「前回は異常終了しました」を画面とログで伝える(main がお知らせとして出す)。
//
// ログが書けなくてもアプリは止めない。理由や前回の異常終了は、画面のお知らせに出す文として返す。
func startLog() (io.Closer, []string) {
	dir := os.Getenv("AIBOW_LOG_DIR")
	if dir == "" {
		if dir = configFile("logs"); dir == "" {
			c, _ := log.Init(log.Options{Console: isDev})
			return c, []string{"ログを書くフォルダが決められないので、ログはファイルに残りません。AIBOW_LOG_DIR でフォルダを指定してください"}
		}
	}
	opts := log.Options{Dir: dir, KeepDays: logKeepDays}
	if isDev {
		// dev は生成の統計などの Info も出す(学習・開発中に観察するもの)
		opts.Console, opts.Root, opts.Prefix = true, log.LevelInfo, devLogPrefix
	} else {
		// exe は常に残したいもの(Notice)以上だけ
		opts.Root = log.LevelNotice
	}
	c, err := log.Init(opts)
	if err != nil {
		return c, []string{err.Error()}
	}
	log.Noticef("ログの書き出し先: %s", absPath(log.FilePath(dir)))

	// お知らせは main が画面に出し、ログにも Warn で書く(ここでは書かない。二重になるため)
	var notices []string
	previous, err := log.CatchCrash(dir)
	if previous != "" {
		notices = append(notices, fmt.Sprintf("前回は異常終了しました。記録: %s", absPath(previous)))
	}
	if err != nil {
		notices = append(notices, fmt.Sprintf("異常終了の記録を残す準備ができません(起動は続けます): %v", err))
	}
	return c, notices
}

// startMCP は設定ファイル(settings.json)を読み、MCP のサーバーの係(client.Manager)を作る。
// サーバーはまだ起動しない(要る最初の送信のときに起動する)。サーバーが書いてなければ nil。
// 設定ファイルの書き間違いは、アプリを止めずに画面のお知らせで伝え、MCP を使わずに起動する。
func startMCP(cfg *appConfig) *client.Manager {
	sf, err := cfg.settings, cfg.settingsErr
	if err != nil {
		cfg.notices = append(cfg.notices, err.Error()+"(MCP のサーバーは使わずに起動しました)")
		return nil
	}
	if len(sf.MCPServers) == 0 {
		log.Infof("MCP のサーバー: なし(設定ファイル: %s)", absPath(cfg.settingsPath))
		return nil
	}
	names := slices.Sorted(maps.Keys(sf.MCPServers))
	log.Infof("MCP のサーバー: %s(設定ファイル: %s。要る最初の送信で起動します)", strings.Join(names, ", "), absPath(cfg.settingsPath))
	return client.New(sf.MCPServers, client.Options{Version: appVersion()})
}

// noteNewServers は、settings.json に足したばかりの MCP のサーバーを、新しい会話で最初から使わないにする(tools.json に書く)。
// 画面のお知らせに出す文を返す(足したサーバーが無ければ空)。
func noteNewServers(mcpm *client.Manager, prefs *aibow.ToolPrefs) string {
	var servers []string
	if mcpm != nil {
		servers = mcpm.Servers()
	}
	added, err := prefs.NoteServers(servers)
	if err != nil {
		log.Error(err.Error())
		return ""
	}
	if len(added) == 0 {
		return ""
	}
	n := fmt.Sprintf("MCP のサーバー %s を見つけました。新しい会話では最初から使わないにしています(設定の画面の「ツール」でチェックすると、最初から使います)",
		strings.Join(added, ", "))
	log.Notice(n)
	return n
}

// loadToolPrefs は、ツールの扱いの PC の既定(tools.json)を読む。
// 壊れていたら画面のお知らせで伝え、決まりの既定で動く(壊れたファイルは書き換えない)。
func loadToolPrefs(cfg *appConfig) *aibow.ToolPrefs {
	prefs, err := aibow.LoadToolPrefs(cfg.toolsPath)
	if err != nil {
		cfg.notices = append(cfg.notices, err.Error())
	}
	return prefs
}

// newServerClient は aibow-server につなぐクライアントを作る(API キーと、自己署名の証明書を信頼するか。0017 の 10-2)。ここではまだつながない。
func newServerClient(cfg appConfig) (*apiclient.Client, error) {
	c := apiclient.New(cfg.serverURL)
	c.SetKey(cfg.serverKey)
	if cfg.serverCert != "" {
		if err := c.TrustCert(cfg.serverCert); err != nil {
			return nil, err
		}
		log.Noticef("aibow-server の証明書を信頼します: %s", cfg.serverCert)
	}
	// 画面でつないだときは、初めてつないだときに人が指紋で確かめた証明書を信頼する(0019 の決定事項 6)
	if cfg.serverPin != "" {
		c.PinCert(cfg.serverPin)
		log.Noticef("aibow-server の証明書を、信頼した指紋で確かめます: %s", cfg.serverPin)
	}
	return c, nil
}

// openStore は会話を保存する先を開き、アプリ全体の設定を読む。close は終了のときに呼ぶ。
//
//   - aibow-server につなぐとき(server が nil でない):保存も aibow-server の server.db に頼む(store/remote。0017 の 10-3)。
//     手元の aibow.db は開かない(手元の会話はサーバーに移さない。直接の形に戻せば見える)。
//     サーバーがまだ起動していなくても、アプリは起動する(設定は初期値で始め、つながってから使う。0015 の 13)
//   - つながないとき:手元の DB(aibow.db)
func openStore(cfg appConfig, server *apiclient.Client) (st aibow.Store, app aibow.AppSettings, close func(), err error) {
	if server != nil {
		rs := remote.New(server)
		log.Noticef("会話の保存先: aibow-server(%s)。手元の %s は使いません", server.URL(), absPath(cfg.dbPath))
		app, notes, err := aibow.LoadAppSettings(context.Background(), rs, cfg.app)
		if err != nil {
			log.Warnf("aibow-server から設定を読めません(初期値で始めます): %v", err)
			return rs, cfg.app, nil, nil
		}
		for _, n := range notes {
			log.Notice(n)
		}
		return rs, app, nil, nil
	}
	sq, err := openLocalStore(cfg)
	if err != nil {
		return nil, cfg.app, nil, err
	}
	app, notes, err := aibow.LoadAppSettings(context.Background(), sq, cfg.app)
	if err != nil {
		sq.Close()
		return nil, cfg.app, nil, err
	}
	for _, n := range notes {
		log.Notice(n)
	}
	d := app.Defaults
	log.Infof("新しい会話の既定: 会話の上限 %d + 返事の上限 %d = 作業領域 %d トークン", d.HistoryTokens, d.ReplyTokens, d.ContextSize())
	return sq, app, func() { sq.Close() }, nil
}

// openLocalStore は手元の DB(aibow.db)を開く。
func openLocalStore(cfg appConfig) (*store.SQLite, error) {
	st, err := store.Open(cfg.dbPath)
	if err != nil {
		return nil, err
	}
	// 手元の DB を使うのはこの PC の1人だけ。利用者の印の無い呼び出しを、ログインなしの形の利用者のものとして扱う(0017)
	st.OneUser()
	log.Noticef("会話の保存先: %s", absPath(cfg.dbPath))
	return st, nil
}

// startModels はモデルを扱う係を作る(0015 の 11)。
//
//   - つなぎ先(settings.json の serverUrl か AIBOW_SERVER_URL)が無ければ、今までどおり llama.cpp を読み込む(startEngine)
//   - あれば aibow-server につなぐ係にする。llama.cpp は読み込まず、GPU も使わない。起動のときにモデルも読ませない
//     (サーバーが起動のときに読み込んでいる。会話のモデルが違えば、送信の前に読み直す。0015 の 12)。
//     ここではまだつながない(サーバーがまだ起動していなくても、アプリは起動する。0015 の 13)
//
// 直接の形のときは、画面で足したモデルの置き場所(model-places.json)も読み、手で決めた置き場所に足す(0015 の 24)。
// 読めなければ画面のお知らせで伝え、足した置き場所なしで起動する。
func startModels(cfg *appConfig, app aibow.AppSettings, server *apiclient.Client) (ui.ModelHost, *engine.Engine, *models.Entry, error) {
	if server != nil {
		log.Noticef("生成は aibow-server に頼みます: %s(つなぎ先を決めた場所: %s。llama.cpp はこのアプリでは読み込みません。API キー: %s)",
			server.URL(), sourceLabel(*cfg), map[bool]string{true: "あり", false: "なし"}[cfg.serverKey != ""])
		return ui.NewRemoteHost(server), nil, nil, nil
	}
	added, err := ui.LoadPlacesFile(cfg.placesPath)
	if err != nil {
		log.Warn(err.Error())
		cfg.notices = append(cfg.notices, err.Error())
	}
	// 手で決めた置き場所に、画面で足した置き場所を合わせた一覧から、起動時のモデルを選ぶ
	catalog := cfg.catalog
	catalog.Added = added.List()
	eng, initial, err := startEngine(*cfg, catalog, app)
	if err != nil {
		return nil, nil, nil, err
	}
	return ui.NewDirectHost(eng, cfg.catalog, added, cfg.engine.LibPath, cfg.initialModel), eng, initial, nil
}

// startEngine は llama.cpp を読み込み、起動時に読み込むモデルを決める。
// モデルの読み込み自体は、ウィンドウを出してから ui.Service が裏で行う。
// catalog は、手で決めた置き場所に画面で足した置き場所を合わせたもの。
func startEngine(cfg appConfig, catalog models.Catalog, app aibow.AppSettings) (*engine.Engine, *models.Entry, error) {
	ec := cfg.engine
	// 画面で変えた値(DB)か、環境変数の値。
	// 作業領域は会話ごとに違う(会話の上限 + 返事の上限)。起動時は新しい会話の画面から始まるので、新しい会話の既定の大きさで作り、
	// 大きさの違う会話に送信するときに作り直す(Conversations.ensureWorkspace)
	ec.NCtx, ec.GPULayers = app.Defaults.ContextSize(), app.Workspace.GPULayers
	eng, err := engine.New(ec)
	if err != nil {
		return nil, nil, err
	}
	for _, d := range eng.Devices {
		log.Noticef("計算装置: %s", d)
	}
	if !eng.HasGPU {
		log.Warn("GPU が見つかりません。CPU だけで計算するので遅くなります(README の「llama.cpp の取得」を確認)")
	}

	// 使えるモデルの一覧
	list := catalog.List()
	log.Infof("モデルの置き場所: %s / Ollama: %s", absPath(catalog.ModelsDir), absPath(catalog.OllamaDir))
	for _, p := range catalog.Places() {
		if p.Origin == models.OriginAdded {
			log.Infof("  足した置き場所: %s(%d 個)%s", p.Path, p.Models, p.Missing)
		}
	}
	for _, m := range list {
		log.Infof("  %-8s %-40s %.1f GB", m.Source, m.Name, m.SizeGB)
	}

	// 起動時のモデル。見つからなくても起動はして、画面で選べるようにする。
	cfg.catalog = catalog
	m, err := pickInitialModel(cfg, app, list)
	if err != nil {
		log.Warn(err.Error())
		return eng, nil, nil
	}
	return eng, &m, nil
}
