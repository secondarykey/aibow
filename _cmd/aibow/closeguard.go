package main

// closeguard.go: ウィンドウを閉じるときに、保存していない変更があれば止めて、画面に聞く(0053 の未決事項 5・決定事項 17)。
//
// 画面の設定の画面は、編集して「保存」で確定する(下書きは画面の中だけに持つ)。保存しないままウィンドウを閉じると下書きが消えるので、
// ほかの画面に移るときと同じ「保存していない変更があります。保存しますか?」を出したい。
// ブラウザなら beforeunload に任せられるが、Wails のウィンドウを閉じても beforeunload は来ないので、Go で閉じるのを止める。
//
//   - 画面は、保存していない変更があるかを SetUnsaved で知らせておく(変わるたび)
//   - 閉じようとしたら(タイトルバーの ×・Alt+F4・タスクバーの「閉じる」)、Wails の WindowClosing のフック(RegisterHook)が先に呼ばれる。
//     変更があれば event.Cancel() で止め、画面に closeRequestEventName を送る
//   - 画面は知らせを出し、「保存」(できたら)か「保存しない」なら CloseWindow を呼ぶ。CloseWindow は止めずに閉じる印を立ててから閉じる
//
// フック(RegisterHook)は、ふつうの受け取り(OnWindowEvent)より先に呼ばれ、Cancel するとあとの受け取り(ウィンドウを壊す処理)まで届かない。

import (
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// closeRequestEventName は、閉じるのを止めたことを画面に知らせるイベントの名前(画面の backendWails.ts と同じ)。
const closeRequestEventName = "aibow:window:close-request"

// closeGuard は、閉じるのを止めるかを決める係。画面から呼ぶメソッドは ChatService に置く(SetUnsaved・CloseWindow)。
type closeGuard struct {
	unsaved atomic.Bool // 画面に保存していない変更がある
	force   atomic.Bool // 画面が「閉じてよい」と決めた(次の WindowClosing は止めない)
	window  *application.WebviewWindow
}

// attach は、ウィンドウを閉じるときのフックを付ける。ウィンドウを作ったあとに1回呼ぶ。
func (g *closeGuard) attach(app *application.App, w *application.WebviewWindow) {
	g.window = w
	w.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if g.force.Load() || !g.unsaved.Load() {
			return
		}
		e.Cancel()
		app.Event.Emit(closeRequestEventName)
	})
}

// SetUnsaved は、画面に保存していない変更があるかを知らせる(変わるたびに画面が呼ぶ)。
func (s *ChatService) SetUnsaved(unsaved bool) {
	s.closer.unsaved.Store(unsaved)
}

// CloseWindow は、保存していない変更の知らせで「保存」か「保存しない」を選んだあとに、止めずにウィンドウを閉じる。
func (s *ChatService) CloseWindow() {
	s.closer.force.Store(true)
	if s.closer.window != nil {
		s.closer.window.Close()
	}
}
