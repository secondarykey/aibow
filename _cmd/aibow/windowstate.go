package main

// windowstate.go: ウィンドウの位置・大きさ・最大化を覚えておき、次の起動で同じところに開く。
//
// 覚える先は %APPDATA%\aibow\window.json(AIBOW_WINDOW)。会話の DB には入れない
// (その PC の画面の都合で、会話をサーバーに置く形にしても手元に残るもののため。AGENTS.md の技術方針)。
//
// Wails で位置と大きさを戻すときの約束(wails3 スキルの window-state.md):
//   - SetPosition / SetSize は Run の前に呼んでも無視されるので、ウィンドウを作るときのオプション(X・Y・Width・Height)で渡す。
//     InitialPosition に WindowXY を指定しないと X・Y は無視されて真ん中に出る
//   - 閉じるとき(WindowClosing)の Position / Size は壊れた値を返すことがある(特に Frameless)。
//     そこで、動かした・大きさを変えたときに値を覚えておき、終わるとき(app.Run が返ったあと)にその値を書く。
//     × のほか、Alt+F4 やタスクバーから閉じても同じように残る
//   - どの画面(モニター)にあるかは Run の後でしか分からないので、画面の外に出ていないかはウィンドウの準備ができてから確かめる

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/secondarykey/aibow/log"
)

// windowState はファイルに書く形。位置と大きさは DIP(画面の拡大率で割った値)で、最大化する前の大きさを持つ。
// 左や上にモニターがあると X・Y は負になるので、「まだ覚えていない」はファイルが無いことで表す(0 や負の値で判断しない)
type windowState struct {
	X         int  `json:"x"`
	Y         int  `json:"y"`
	Width     int  `json:"width"`
	Height    int  `json:"height"`
	Maximised bool `json:"maximised"`
}

// 覚えた大きさがこれを超えていたら壊れているとみなす(画面の情報は Run の前には取れないので、ここでは大まかに確かめる)
const maxWindowSize = 10000

// loadWindowState は覚えておいた状態を読む。無い・読めない・おかしな値なら false(既定の大きさで真ん中に開く)
func loadWindowState(path string) (windowState, bool) {
	if path == "" {
		return windowState{}, false // 保存先が決まらない(%APPDATA% が分からない)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Warnf("ウィンドウの位置と大きさを読めません。既定の大きさで開きます(%s): %v", path, err)
		}
		return windowState{}, false
	}
	var s windowState
	if err := json.Unmarshal(b, &s); err != nil {
		log.Warnf("ウィンドウの位置と大きさのファイルが壊れています。既定の大きさで開きます(%s): %v", path, err)
		return windowState{}, false
	}
	if s.Width < windowMinWidth || s.Height < windowMinHeight || s.Width > maxWindowSize || s.Height > maxWindowSize {
		log.Warnf("ウィンドウの大きさ(%d×%d)がおかしいので、既定の大きさで開きます", s.Width, s.Height)
		return windowState{}, false
	}
	return s, true
}

// windowTracker は、今のウィンドウの位置と大きさを覚えておき、終わるときにファイルに書く。
type windowTracker struct {
	path   string
	window *application.WebviewWindow

	mu    sync.Mutex
	state windowState
	moved bool // 起動してから動かした・大きさを変えたか(変えていなければ書かない)
}

// applyWindowState は、覚えておいた状態をウィンドウを作るときのオプションに入れる。最大化はウィンドウの準備ができてから行う(restore)。
func applyWindowState(opts *application.WebviewWindowOptions, s windowState) {
	opts.X, opts.Y = s.X, s.Y
	opts.Width, opts.Height = s.Width, s.Height
	opts.InitialPosition = application.WindowXY
}

// trackWindow は、ウィンドウの位置と大きさを見張り始める。saved は起動時に読んだ状態(無ければ ok=false)
func trackWindow(app *application.App, window *application.WebviewWindow, path string, saved windowState, ok bool) *windowTracker {
	t := &windowTracker{path: path, window: window, state: saved}
	window.OnWindowEvent(events.Windows.WindowDidMove, func(*application.WindowEvent) { t.record() })
	window.OnWindowEvent(events.Windows.WindowDidResize, func(*application.WindowEvent) { t.record() })
	window.OnWindowEvent(events.Common.WindowRuntimeReady, func(*application.WindowEvent) {
		if ok {
			t.restore(app, saved)
		}
	})
	return t
}

// restore は、Run の後でしか分からないこと(どの画面にあるか・最大化)を戻す。
//
// 覚えた位置がどの画面の作業領域(タスクバーを除いた所)にも掛かっていなければ、真ん中に出す
// (モニターを外した・解像度を変えたなど。タイトルバーをつかめないとウィンドウを動かせなくなるため)。
// 最大化はここで行う。オプションの StartState で最大化すると、そのあとで X・Y の位置に動かされてしまう
// (Wails は最大化してから位置を設定する)。
func (t *windowTracker) restore(app *application.App, s windowState) {
	if !onSomeScreen(app.Screen.GetAll(), s) {
		log.Warnf("覚えていた位置(%d, %d)がどの画面にも入っていないので、真ん中に出します", s.X, s.Y)
		t.window.Center()
	}
	if s.Maximised {
		t.window.Maximise()
	}
}

// onSomeScreen は、ウィンドウの上の端(タイトルバーのあたり)がどれかの画面の作業領域に入っているかを返す。
// 画面の情報が取れなければ、確かめずに入っていることにする。
func onSomeScreen(screens []*application.Screen, s windowState) bool {
	if len(screens) == 0 {
		return true
	}
	// 上の端の真ん中あたりの点。ここがどこかの画面にあれば、つかんで動かせる
	x, y := s.X+s.Width/2, s.Y+titleBarHeight/2
	for _, sc := range screens {
		wa := sc.WorkArea
		if x >= wa.X && x < wa.X+wa.Width && y >= wa.Y && y < wa.Y+wa.Height {
			return true
		}
	}
	return false
}

// titleBarHeight は画面のタイトルバーの高さ(TitleBar.tsx と同じ)。
const titleBarHeight = 36

// record は、今の位置と大きさを覚える。最大化・最小化しているときは、元に戻したときの大きさを残したいので位置と大きさは覚えない
// (最大化しているかどうかだけ覚える。最小化したまま閉じたときは、最小化する前の状態のまま)
func (t *windowTracker) record() {
	if t.window.IsMinimised() {
		return
	}
	maximised := t.window.IsMaximised()
	var b application.Rect
	if !maximised {
		b = t.window.Bounds()
		if b.Width <= 0 || b.Height <= 0 {
			return // 作っている途中・壊している途中の値
		}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.state.Maximised = maximised
	if !maximised {
		t.state.X, t.state.Y, t.state.Width, t.state.Height = b.X, b.Y, b.Width, b.Height
	}
	t.moved = true
}

// save は覚えている状態をファイルに書く。アプリが終わるとき(app.Run が返ったあと)に呼ぶ。
// 書けなくても終わるのは止めない(次の起動が既定の大きさになるだけ)
func (t *windowTracker) save() {
	t.mu.Lock()
	s, moved := t.state, t.moved
	t.mu.Unlock()
	if !moved || t.path == "" || s.Width <= 0 || s.Height <= 0 {
		return
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err == nil {
		err = os.MkdirAll(filepath.Dir(t.path), 0o755)
	}
	if err == nil {
		err = os.WriteFile(t.path, b, 0o644)
	}
	if err != nil {
		log.Warnf("ウィンドウの位置と大きさを覚えておけません(%s): %v", t.path, err)
	}
}
