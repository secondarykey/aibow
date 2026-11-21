package main

// loglevel.go: アプリのログのレベルを、起動したあとに設定の画面(基本設定)から変える(0052)。
//
// 変わるのはアプリのプロセスのログ(aibow_<日付>.log・dev は aibow-dev_<日付>.log)。サーバーモードでも同じで、aibow-server のログには効かない
// (aibow-server のログはブラウザの管理の画面で変える。ui の SetServerLogLevel)。
//
// ⚠️ ui.Service ではなくここ(ChatService)に置く。ui.Service のメソッドはブラウザからも呼べる(/_aibow/ui/call/)ので、
// そこに置くと、ブラウザから aibow-server のプロセスのレベルを変えられてしまう(0052 の決定事項 15)。
//
// 保存はしない。起動し直すと、起動したときのレベル(dev は Info、exe は Notice)に戻る(0052 の決定事項 2)。

import (
	"errors"
	"fmt"
	"strings"

	"github.com/secondarykey/aibow/log"
)

// LogLevel は、アプリの今のログのレベル(log.LevelNames の名前。trace・debug・info・notice・warn・error)。
func (s *ChatService) LogLevel() (string, error) {
	l, ok := log.Level()
	if !ok {
		return "", errors.New("ログのレベルを読めません(ログの出口が組まれていません)")
	}
	return log.LevelName(l), nil
}

// SetLogLevel は、アプリのログのレベルを変えて、変えたあとのレベルを返す。変えたことは Notice で残る。
func (s *ChatService) SetLogLevel(name string) (string, error) {
	l, ok := log.ParseLevel(name)
	if !ok {
		return "", fmt.Errorf("ログのレベルは %s のどれかにしてください: %q", strings.Join(log.LevelNames, "・"), name)
	}
	if !log.SetLevel(l, "") {
		return "", errors.New("ログのレベルを変えられません(ログの出口が組まれていません)")
	}
	return log.LevelName(l), nil
}
