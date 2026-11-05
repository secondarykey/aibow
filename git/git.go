// Package git は、aibow のアプリが行う Git の操作(0039)。
//
// Git の操作は、`git` の exe を `os/exec` で呼ぶだけで行う(外部パッケージは足さない。go-git は使わない。0039 の決定事項 2)。
// コーディングエージェントを使う PC には、たいてい `git` が入っているため。
//
// 分け方:
//
//	git  … アプリが決まった手順で行う Git の操作(worktree を作る・差分を取る・本体に当てる・消す)
//	tool … モデルに見せるツール。モデルには Git の操作をさせない(commit・push・branch もしない)
//
// ルートの aibow・tool・Wails を import しない(aibow-server からもそのまま使えるように。tool が LLM を知らないのと同じ分け方)。
// 段3で読むだけの Git のツールを作るなら、tool の中に作り、中でここを呼ぶ(tool → git の向き)。
//
// ここで学べること:Git の操作は「コマンドの組み合わせ」でできている。呼んだコマンドは Info のログに残すので、
// アプリが裏で何をしているか(worktree add・add -A・diff・apply)をそのまま観察できる。
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/secondarykey/aibow/internal/proc"
	"github.com/secondarykey/aibow/log"
)

// ErrNotFound は、`git` が PATH に無いときのエラー。
var ErrNotFound = errors.New("git が見つかりません(PATH に git の exe がありません。Git for Windows などを入れてから、aibow を起動し直してください)")

// found は、一度見つかった git の場所(見つかったら覚える。見つからないうちは、呼ぶたびに探し直す)。
// 探すのは PATH のフォルダを見て回るだけだが、ツールの扱いを決めるたびに呼ぶので、見つかったら覚えておく。
var found struct {
	mu   sync.Mutex
	path string
}

// Available は、`git` が使えるか(PATH にあるか)。使えなければ ErrNotFound。
func Available() error {
	_, err := gitPath()
	return err
}

func gitPath() (string, error) {
	found.mu.Lock()
	defer found.mu.Unlock()
	if found.path != "" {
		return found.path, nil
	}
	p, err := exec.LookPath("git")
	if err != nil {
		return "", ErrNotFound
	}
	found.path = p
	return p, nil
}

// Error は、git のコマンドが失敗したときのエラー。git が出したエラーの文(標準エラー出力)を持つ。
type Error struct {
	Args   []string // git に渡した引数(-C のフォルダの後ろ)
	Stderr string
	Err    error
}

func (e *Error) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		msg = e.Err.Error()
	}
	return fmt.Sprintf("git %s が失敗しました: %s", strings.Join(e.Args, " "), msg)
}

func (e *Error) Unwrap() error { return e.Err }

// run は、フォルダ dir で git を動かし、標準出力を返す。stdin が nil でなければ標準入力に渡す。
//
// git のメッセージは英語に固定する(LC_ALL=C)。当たらないファイルをエラーの文から読み取るため(Apply)。
// パスの日本語を "\346\227..." のように書かせない(core.quotepath=false)。
// 資格情報を聞いて止まらないよう、端末での入力も止める(GIT_TERMINAL_PROMPT=0。アプリには入力する端末が無い)。
func run(ctx context.Context, dir string, stdin []byte, args ...string) (string, error) {
	exe, err := gitPath()
	if err != nil {
		return "", err
	}
	full := append([]string{"-C", dir, "-c", "core.quotepath=false"}, args...)
	log.Infof("git %s(%s)", strings.Join(args, " "), dir)
	cmd := exec.Command(exe, full...)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANGUAGE=C", "GIT_TERMINAL_PROMPT=0")
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	proc.Prepare(cmd) // Windows では黒い窓を出さない
	tree, err := proc.Start(cmd)
	if err != nil {
		return "", fmt.Errorf("git を起動できません: %w", err)
	}
	defer tree.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err = <-done:
	case <-ctx.Done():
		tree.Kill()
		<-done
		return "", fmt.Errorf("git %s を止めました: %w", strings.Join(args, " "), ctx.Err())
	}
	if err != nil {
		return out.String(), &Error{Args: args, Stderr: errOut.String(), Err: err}
	}
	return out.String(), nil
}
