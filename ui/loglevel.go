package ui

// loglevel.go: aibow-server のログのレベルを、起動したあとに管理の画面から変える(0052)。
//
// ここで変わるのは、このメソッドが動いているプロセス(aibow-server)のログ。デスクトップのアプリのログは
// _cmd/aibow の ChatService の LogLevel / SetLogLevel で変える(ブラウザからは呼べない)。
// ChatService は Service を埋め込んでいるので、デスクトップのアプリからもこのメソッドは見えるが、
// そこで動くとアプリのログが変わってしまうので、ブラウザ(aibow-server)のとき以外は断る(0052 の決定事項 18)。
//
// 保存はしない。aibow-server を起動し直すと、起動したときのレベル(-debug > server.json の logLevel > 既定)に戻る。

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/secondarykey/aibow/log"
)

// ServerLogLevel は、aibow-server の今のログのレベル(log.LevelNames の名前)。管理者だけ(画面で隠すだけにしない)。
func (s *Service) ServerLogLevel(ctx context.Context) (string, error) {
	if err := s.checkServerLog(ctx); err != nil {
		return "", err
	}
	l, ok := log.Level()
	if !ok {
		return "", errors.New("aibow-server のログのレベルを読めません(ログの出口が組まれていません)")
	}
	return log.LevelName(l), nil
}

// SetServerLogLevel は、aibow-server のログのレベルを変えて、変えたあとのレベルを返す。変えたことは Notice で残り、誰が変えたかも添える。
func (s *Service) SetServerLogLevel(ctx context.Context, name string) (string, error) {
	if err := s.checkServerLog(ctx); err != nil {
		return "", err
	}
	l, ok := log.ParseLevel(name)
	if !ok {
		return "", fmt.Errorf("ログのレベルは %s のどれかにしてください: %q", strings.Join(log.LevelNames, "・"), name)
	}
	by := ""
	if c, ok := callerOf(ctx); ok {
		by = c.Name
	}
	if !log.SetLevel(l, by) {
		return "", errors.New("aibow-server のログのレベルを変えられません(ログの出口が組まれていません)")
	}
	return log.LevelName(l), nil
}

// ServerLogFile は、aibow-server の今日のログファイルの絶対パス(サーバーの PC のパス。管理の画面に出してコピーする。0052 の決定事項 21)。
// ファイルに書いていなければ空。サーバーの PC のパスなので、レベルと同じく管理者だけに見せる。
func (s *Service) ServerLogFile(ctx context.Context) (string, error) {
	if err := s.checkServerLog(ctx); err != nil {
		return "", err
	}
	p, _ := log.CurrentFile()
	return p, nil
}

// checkServerLog は、aibow-server のログのレベルを見たり変えたりしてよいか。ブラウザ(aibow-server)の管理者だけ(ログインなしで使うときはいつも。0046)。
func (s *Service) checkServerLog(ctx context.Context) error {
	if !s.browser {
		return errors.New("aibow-server のログのレベルは、ブラウザの管理の画面で変えてください")
	}
	if !managesPC(ctx) {
		return errors.New(serverLogOnly)
	}
	return nil
}

// serverLogOnly は、管理者でない人に断るときの文(web.go の adminOnly と同じもの)。
const serverLogOnly = "aibow-server のログのレベルは、管理者だけが変えられます"
