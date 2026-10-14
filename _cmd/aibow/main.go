// aibow: ローカルLLMを自分で動かして、仕組みを学ぶためのチャットアプリ(デスクトップ版)
//
//	ウィンドウ(frontend の React) ⇄ Wails ⇄ ChatService ⇄ aibow パッケージ ⇄ llama.cpp(DLL) ⇄ GPU
//	                                                         ⇅
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
//	config.go  … 環境変数から設定を読む(settings.json は aibow.LoadSettingsFile、MCP のサーバーは startMCP)
//	service.go … 画面から呼ばれるメソッド(モデル・会話・設定・送信)
//	frontend/  … 画面(React + TypeScript)。Vite でビルドした frontend/dist が実行ファイルに埋め込まれる
//	build/     … wails3 build / wails3 dev の設定(アプリ名、アイコン、OS ごとの手順)
package main

import (
	"embed"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/secondarykey/aibow"
	"github.com/secondarykey/aibow/log"
	"github.com/secondarykey/aibow/mcp/client"
	"github.com/secondarykey/aibow/store"

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

// Vite でビルドした画面(frontend/dist)を実行ファイルに埋め込む。
// all: を付けないと、_ や . で始まるファイルが埋め込まれない。
//
//go:embed all:frontend/dist
var frontendFiles embed.FS

func init() {
	// chat イベントで送るデータの型を Wails に教えておく。
	// バインディング生成のときに TypeScript の型(ChatEvent)が作られ、画面側で型付きで受け取れる。
	application.RegisterEvent[aibow.ChatEvent](chatEventName)
}

func main() {
	// ログの出口を最初に組む(設定の読み込みのログから残すため)。aibow のライブラリのログも、アプリのログもここに集まる
	logs, logNotices := startLog()
	defer logs.Close()

	// 起動に失敗しても(lib が無いなど)ウィンドウは出し、理由を画面に表示する。
	// リリース版(wails3 build)はコンソールが無く、ログだけでは何も見えずに終わってしまうため。
	cfg, err := loadConfig()
	// ログが書けない・前回は異常終了した、などは起動を止めず、画面のお知らせで伝える
	cfg.notices = append(cfg.notices, logNotices...)
	var mcpm *client.Manager
	var prefs *aibow.ToolPrefs
	if err == nil {
		if mcpm = startMCP(&cfg); mcpm != nil {
			defer mcpm.Close() // アプリを閉じたら、起動した MCP のサーバーも止める(子プロセスまで)
		}
		prefs = loadToolPrefs(&cfg)
	}
	for _, n := range cfg.notices {
		log.Warn(n)
	}
	var st *store.SQLite
	var settings aibow.AppSettings
	var engine *aibow.Engine
	var initial *aibow.ModelEntry
	if err == nil {
		st, settings, err = openStore(cfg)
	}
	if err == nil {
		defer st.Close()
		engine, initial, err = startEngine(cfg, settings)
	}
	if err != nil {
		log.Errorf("起動できません: %v", err)
	} else {
		defer engine.Close()
	}

	assets, _ := fs.Sub(frontendFiles, "frontend/dist")
	app := application.New(application.Options{
		Name:        "aibow",
		Description: "ローカルLLMを自分で動かして仕組みを学ぶチャット",
		// Wails 自身のログも aibow と同じ出口(startLog が組んだ slog.Default())に出す。
		// 渡さないと、Wails は dev では自分の書式で標準エラー出力に出し、リリース版では捨てる(exe でエラーが残らない)。
		// なお WebView2 の行([WebView2] …)は標準の log で来るが、slog.SetDefault のあとは標準の log も同じ出口に流れる
		Logger: slog.Default(),
		Services: []application.Service{
			application.NewService(NewChatService(engine, st, settings, cfg.catalog, initial, mcpm, prefs, appVersion(), cfg.notices, err)),
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
	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     "aibow " + appVersion(),
		Width:     720, // 起動時の大きさ(広げたいときは最大化かリサイズ)
		Height:    820,
		MinWidth:  480, // 最小の幅。画面の端に細く置いておけるよう、起動時の幅より狭くできる
		MinHeight: 480, // 最小の高さ。これより低いと、入力欄とモデルの行で会話がほとんど見えなくなる
		Frameless: true,
		URL:       "/",
	})

	// Windows の WebView2 は、別のウィンドウから戻ってきたときに IME(日本語入力)の状態が
	// 戻らず、入力欄で変換できなくなることがある。戻ってきたことを画面に知らせ、
	// 画面側(frontend/src/imeFocus.ts)で入力欄のフォーカスを付け直して直す。
	// events.Common.WindowFocus は Windows では発火しないので、Windows 用のイベントを使う。
	window.OnWindowEvent(events.Windows.WindowSetFocus, func(*application.WindowEvent) {
		app.Event.Emit(windowFocusEventName)
	})

	log.Noticef("aibow %s 起動", appVersion())
	// Run はウィンドウが閉じられるまで返らない。返ったら defer で VRAM を解放する。
	if err := app.Run(); err != nil {
		log.Error(err.Error())
		os.Exit(1)
	}
}

// logKeepDays は、exe がログファイルを残す日数(今日を含む)。起動のたびに、それより古いものを消す。
const logKeepDays = 10

// startLog はログの出口を組む(aibow/log の Init)。
//
//   - wails3 dev(isDev):ターミナルに出す(標準エラー出力)。AIBOW_LOG_DIR を指定したときだけ、ファイルにも書く
//   - リリース版の exe:ファイルに書く(%APPDATA%\aibow\logs\aibow_<日付>.log。AIBOW_LOG_DIR で変えられる)。
//     リリース版(-H windowsgui)には標準エラー出力の行き先が無く、ファイルにしか残らないため
//     (0006 で、起動できない理由がどこにも残らず困った)。Notice 以上だけを書き、今日を含めて logKeepDays 日分を残して古いものは起動時に消す
//
// 異常終了(Go の panic・ランタイムの致命的なエラー)の内容も、ファイルに書くときは同じフォルダの aibow_crash.log に残す
// (log.CatchCrash)。前回の記録が残っていれば改名して残し、「前回は異常終了しました」を画面とログで伝える(main がお知らせとして出す)。
//
// ログが書けなくてもアプリは止めない。理由や前回の異常終了は、画面のお知らせに出す文として返す。
func startLog() (io.Closer, []string) {
	dir := os.Getenv("AIBOW_LOG_DIR")
	if dir == "" && !isDev {
		if dir = configFile("logs"); dir == "" {
			c, _ := log.Init(log.Options{})
			return c, []string{"ログを書くフォルダが決められないので、ログはファイルに残りません。AIBOW_LOG_DIR でフォルダを指定してください"}
		}
	}
	opts := log.Options{Dir: dir, Console: isDev, Root: log.LevelInfo}
	if !isDev {
		// exe は常に残したいもの(Notice)以上だけ。生成の統計などの Info は、学習・開発中に wails3 dev で見る
		opts.Root = log.LevelNotice
		opts.KeepDays = logKeepDays // exe は古いログを消す(dev で AIBOW_LOG_DIR に書いたものは消さない)
	}
	c, err := log.Init(opts)
	if err != nil {
		return c, []string{err.Error()}
	}
	if dir == "" {
		return c, nil // dev でファイルに書かないとき(異常終了はターミナルに出る)
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
	sf, err := aibow.LoadSettingsFile(cfg.settingsPath)
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

// loadToolPrefs は、ツールの扱いの PC の既定(tools.json)を読む。
// 壊れていたら画面のお知らせで伝え、決まりの既定で動く(壊れたファイルは書き換えない)。
func loadToolPrefs(cfg *appConfig) *aibow.ToolPrefs {
	prefs, err := aibow.LoadToolPrefs(cfg.toolsPath)
	if err != nil {
		cfg.notices = append(cfg.notices, err.Error())
	}
	return prefs
}

// openStore は会話を保存する DB を開き、アプリ全体の設定を読む。
func openStore(cfg appConfig) (*store.SQLite, aibow.AppSettings, error) {
	st, err := store.Open(cfg.dbPath)
	if err != nil {
		return nil, cfg.app, err
	}
	log.Noticef("会話の保存先: %s", absPath(cfg.dbPath))
	app, notes, err := aibow.LoadAppSettings(st, cfg.app)
	if err != nil {
		st.Close()
		return nil, cfg.app, err
	}
	for _, n := range notes {
		log.Notice(n)
	}
	d := app.Defaults
	log.Infof("新しい会話の既定: 会話の上限 %d + 返事の上限 %d = 作業領域 %d トークン", d.HistoryTokens, d.ReplyTokens, d.ContextSize())
	return st, app, nil
}

// startEngine は llama.cpp を読み込み、起動時に読み込むモデルを決める。
// モデルの読み込み自体は、ウィンドウを出してから ChatService が裏で行う。
func startEngine(cfg appConfig, app aibow.AppSettings) (*aibow.Engine, *aibow.ModelEntry, error) {
	ec := cfg.engine
	// 画面で変えた値(DB)か、環境変数の値。
	// 作業領域は会話ごとに違う(会話の上限 + 返事の上限)。起動時は新しい会話の画面から始まるので、新しい会話の既定の大きさで作り、
	// 大きさの違う会話に送信するときに作り直す(Conversations.ensureWorkspace)
	ec.NCtx, ec.GPULayers = app.Defaults.ContextSize(), app.Workspace.GPULayers
	engine, err := aibow.NewEngine(ec)
	if err != nil {
		return nil, nil, err
	}
	for _, d := range engine.Devices {
		log.Noticef("計算装置: %s", d)
	}
	if !engine.HasGPU {
		log.Warn("GPU が見つかりません。CPU だけで計算するので遅くなります(README の「llama.cpp の取得」を確認)")
	}

	// 使えるモデルの一覧
	models := cfg.catalog.List()
	log.Infof("モデルの置き場所: %s / Ollama: %s", absPath(cfg.catalog.ModelsDir), absPath(cfg.catalog.OllamaDir))
	for _, m := range models {
		log.Infof("  %-8s %-40s %.1f GB", m.Source, m.Name, m.SizeGB)
	}

	// 起動時のモデル。見つからなくても起動はして、画面で選べるようにする。
	m, err := pickInitialModel(cfg, app, models)
	if err != nil {
		log.Warn(err.Error())
		return engine, nil, nil
	}
	return engine, &m, nil
}
