package main

// service.go: 画面(frontend)から呼ばれる Go のメソッドを Wails に渡す。
//
// 中身(モデル・会話・設定・送信)はルートの ui パッケージの Service にあり、ChatService はそれを埋め込むだけ。
// 埋め込んだ型のメソッドは ChatService のメソッドとして見えるので、Wails のバインディングは今までどおり ChatService.Send などになる。
// ui は Wails を import しない(aibow-server がブラウザ向けに同じ Service を使うため。0016 の 9)。
// ここに残すのは Wails が要るものだけ:起動と終了の合図・ダイアログ・Wails のイベントで画面に知らせる係(wailsNotifier)。

import (
	"context"
	"fmt"

	"github.com/secondarykey/aibow"
	"github.com/secondarykey/aibow/ui"

	"github.com/wailsapp/wails/v3/pkg/application"
)

type ChatService struct {
	*ui.Service

	// life は起動と終了のときに呼ぶ係。フィールドなので画面からは呼べない
	life ui.Lifecycle
}

// NewChatService は ChatService を作る。画面に知らせる係は、ここで Wails のイベントにする。
func NewChatService(o ui.Options) *ChatService {
	o.Notify = wailsNotifier{}
	s, life := ui.New(o)
	return &ChatService{Service: s, life: life}
}

// ServiceStartup はアプリの起動時に Wails から呼ばれる(画面からは呼べない特別な名前)。起動時のモデルを裏で読み込む。
func (s *ChatService) ServiceStartup(ctx context.Context, options application.ServiceOptions) error {
	s.life.Start()
	return nil
}

// ServiceShutdown はアプリの終了時に Wails から呼ばれる(画面からは呼べない特別な名前)。
func (s *ChatService) ServiceShutdown() error {
	s.life.Stop()
	return nil
}

// wailsNotifier は、Go から画面に Wails のイベントで知らせる係(ui.Notifier)。
// chat イベントの型は main.go の RegisterEvent で登録している。
type wailsNotifier struct{}

func (wailsNotifier) Chat(ev aibow.ChatEvent) { application.Get().Event.Emit(ui.ChatEventName, ev) }
func (wailsNotifier) ModelLoaded()            { application.Get().Event.Emit(ui.ModelsEventName) }

// ChooseModelPlace は、置き場所を選ぶダイアログを出し(アプリの中で動かすときだけ使う。置き場所はモデルを読み込む PC のパスなので)、選んだフォルダ(kind が "dir")か GGUF のファイル(kind が "file")の
// 絶対パスを返す。取り消したら空。足すのは AddModelPlace(選んだだけでは足さない)。
func (s *ChatService) ChooseModelPlace(kind string) (string, error) {
	app := application.Get()
	d := app.Dialog.OpenFile()
	if kind == "file" {
		d.SetTitle("モデルのファイル(GGUF)を選ぶ").CanChooseFiles(true).CanChooseDirectories(false).AddFilter("GGUF", "*.gguf")
	} else {
		d.SetTitle("モデルを置いたフォルダを選ぶ").CanChooseFiles(false).CanChooseDirectories(true)
	}
	if w := app.Window.Current(); w != nil {
		d.AttachToWindow(w) // ウィンドウの前に出す(ChooseWorkDir と同じ)
	}
	p, err := d.PromptForSingleSelection()
	if err != nil {
		return "", fmt.Errorf("置き場所を選ぶダイアログを出せません: %w", err)
	}
	return p, nil
}

// ChooseWorkDir は作業フォルダを選ぶダイアログを出し、選んだフォルダの絶対パスを返す。取り消したら空。
// current が空でなければ、そのフォルダから開く。選んだフォルダを会話に入れるのは画面の側(会話の設定として保存する)。
func (s *ChatService) ChooseWorkDir(current string) (string, error) {
	app := application.Get()
	d := app.Dialog.OpenFile().
		SetTitle("作業フォルダを選ぶ").
		CanChooseFiles(false).
		CanChooseDirectories(true).
		CanCreateDirectories(true)
	if current != "" {
		d.SetDirectory(current)
	}
	if w := app.Window.Current(); w != nil {
		d.AttachToWindow(w) // ウィンドウの前に出す(後ろに隠れて、操作できなくなったように見えるのを防ぐ)
	}
	dir, err := d.PromptForSingleSelection()
	if err != nil {
		return "", fmt.Errorf("フォルダを選ぶダイアログを出せません: %w", err)
	}
	return dir, nil
}
