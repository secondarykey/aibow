// aibow: ローカルLLMを自分で動かして、仕組みを学ぶためのチャットアプリ(デスクトップ版)
//
//	ウィンドウ(frontend の React) ⇄ Wails ⇄ ChatService ⇄ aibow パッケージ ⇄ llama.cpp(DLL) ⇄ GPU
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
//	service.go … 画面から呼ばれるメソッド(モデルの一覧・切り替え・チャット)
//	frontend/  … 画面(React + TypeScript)。Vite でビルドした frontend/dist が実行ファイルに埋め込まれる
//	build/     … wails3 build / wails3 dev の設定(アプリ名、アイコン、OS ごとの手順)
package main

import (
	"embed"
	"io/fs"
	"log"

	"aibow"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Vite でビルドした画面(frontend/dist)を実行ファイルに埋め込む。
// all: を付けないと、_ や . で始まるファイルが埋め込まれない。
//
//go:embed all:frontend/dist
var frontendFiles embed.FS

func init() {
	// chat イベントで送るデータの型を Wails に教えておく。
	// バインディング生成のときに TypeScript の型(ChatEvent)が作られ、画面側で型付きで受け取れる。
	application.RegisterEvent[ChatEvent](chatEventName)
}

func main() {
	// 起動に失敗しても(lib が無いなど)ウィンドウは出し、理由を画面に表示する。
	// リリース版(wails3 build)はコンソールが無く、ログだけでは何も見えずに終わってしまうため。
	cfg, err := loadConfig()
	var engine *aibow.Engine
	var initial *aibow.ModelEntry
	if err == nil {
		engine, initial, err = startEngine(cfg)
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
			application.NewService(NewChatService(engine, cfg.catalog, initial, err)),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "aibow",
		Width:  1280,
		Height: 820,
		URL:    "/",
	})

	log.Printf("aibow 起動")
	// Run はウィンドウが閉じられるまで返らない。返ったら defer で VRAM を解放する。
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

// startEngine は llama.cpp を読み込み、起動時に読み込むモデルを決める。
// モデルの読み込み自体は、ウィンドウを出してから ChatService が裏で行う。
func startEngine(cfg appConfig) (*aibow.Engine, *aibow.ModelEntry, error) {
	engine, err := aibow.NewEngine(cfg.engine)
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
	m, err := pickInitialModel(cfg, models)
	if err != nil {
		log.Printf("※ %v", err)
		return engine, nil, nil
	}
	return engine, &m, nil
}
