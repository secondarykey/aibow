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
//	config.go  … 環境変数から設定を読む
//	service.go … 画面から呼ばれるメソッド(モデル・会話・設定・送信)
//	frontend/  … 画面(React + TypeScript)。Vite でビルドした frontend/dist が実行ファイルに埋め込まれる
//	build/     … wails3 build / wails3 dev の設定(アプリ名、アイコン、OS ごとの手順)
package main

import (
	"embed"
	"io/fs"
	"log"
	"strings"

	"aibow"
	"aibow/store"

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
	// 起動に失敗しても(lib が無いなど)ウィンドウは出し、理由を画面に表示する。
	// リリース版(wails3 build)はコンソールが無く、ログだけでは何も見えずに終わってしまうため。
	cfg, err := loadConfig()
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
		log.Printf("※ 起動できません: %v", err)
	} else {
		defer engine.Close()
	}

	assets, _ := fs.Sub(frontendFiles, "frontend/dist")
	app := application.New(application.Options{
		Name:        "aibow",
		Description: "ローカルLLMを自分で動かして仕組みを学ぶチャット",
		Services: []application.Service{
			application.NewService(NewChatService(engine, st, settings, cfg.catalog, initial, err)),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "aibow " + strings.TrimSpace(version),
		Width:  1280,
		Height: 820,
		URL:    "/",
	})

	// Windows の WebView2 は、別のウィンドウから戻ってきたときに IME(日本語入力)の状態が
	// 戻らず、入力欄で変換できなくなることがある。戻ってきたことを画面に知らせ、
	// 画面側(frontend/src/imeFocus.ts)で入力欄のフォーカスを付け直して直す。
	// events.Common.WindowFocus は Windows では発火しないので、Windows 用のイベントを使う。
	window.OnWindowEvent(events.Windows.WindowSetFocus, func(*application.WindowEvent) {
		app.Event.Emit(windowFocusEventName)
	})

	log.Printf("aibow %s 起動", strings.TrimSpace(version))
	// Run はウィンドウが閉じられるまで返らない。返ったら defer で VRAM を解放する。
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

// openStore は会話を保存する DB を開き、アプリ全体の設定を読む。
func openStore(cfg appConfig) (*store.SQLite, aibow.AppSettings, error) {
	st, err := store.Open(cfg.dbPath)
	if err != nil {
		return nil, cfg.app, err
	}
	log.Printf("会話の保存先: %s", absPath(cfg.dbPath))
	app, err := aibow.LoadAppSettings(st, cfg.app)
	if err != nil {
		st.Close()
		return nil, cfg.app, err
	}
	return st, app, nil
}

// startEngine は llama.cpp を読み込み、起動時に読み込むモデルを決める。
// モデルの読み込み自体は、ウィンドウを出してから ChatService が裏で行う。
func startEngine(cfg appConfig, app aibow.AppSettings) (*aibow.Engine, *aibow.ModelEntry, error) {
	ec := cfg.engine
	ec.NCtx, ec.GPULayers = app.NCtx, app.GPULayers // 画面で変えた値(DB)か、環境変数の値
	engine, err := aibow.NewEngine(ec)
	if err != nil {
		return nil, nil, err
	}
	for _, d := range engine.Devices {
		log.Printf("計算装置: %s", d)
	}
	if !engine.HasGPU {
		log.Printf("※ GPU が見つかりません。CPU だけで計算するので遅くなります(README の「llama.cpp の取得」を確認)")
	}

	// 使えるモデルの一覧
	models := cfg.catalog.List()
	log.Printf("モデルの置き場所: %s / Ollama: %s", absPath(cfg.catalog.ModelsDir), absPath(cfg.catalog.OllamaDir))
	for _, m := range models {
		log.Printf("  %-8s %-40s %.1f GB", m.Source, m.Name, m.SizeGB)
	}

	// 起動時のモデル。見つからなくても起動はして、画面で選べるようにする。
	m, err := pickInitialModel(cfg, app, models)
	if err != nil {
		log.Printf("※ %v", err)
		return engine, nil, nil
	}
	return engine, &m, nil
}
