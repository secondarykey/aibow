package tool

// command.go: コマンドを実行するツール(run_command)。実行前に確認する。
//
// コーディングエージェントの「動かして結果を見る」部分。テストを実行し、失敗の出力を読んで直す、ができるようになる。
// コマンドは何でもできる(ファイルを消す、ネットワークにつなぐ、作業フォルダの外を触る)ので、
// ファイルのツールの os.Root のように仕組みで縛ることはできない。実行前の確認が唯一の守りになる。
//
// どのシェルで動かすかは OS ごとに違うので、shell_windows.go / shell_unix.go に分けている。
// 子プロセスまでまとめて止める仕組みは internal/proc(MCP のサーバーでも使う)。
// ここは OS に関係ない部分(時間の上限・出力の切り方・結果の書き方)。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"aibow/internal/proc"
)

const (
	// commandTimeLimit はコマンドの時間の上限。go test の初回のコンパイルも収まるように、ほかのツール(10 秒)より長くする
	commandTimeLimit = 120 * time.Second

	// 出力のうち残す先頭と末尾のバイト数。合わせて 8,000 文字程度(英数字なら 8,000 文字、日本語なら 2,700 文字ほど)。
	// エラーは末尾に出やすいので、末尾を多めに残す
	outputHead = 3000
	outputTail = 5000

	// waitDelay は、止めた後に出力のパイプが閉じるのを待つ時間。
	// 子プロセスが止めきれずにパイプを持ったままだと、Wait がいつまでも返らないので、これを過ぎたら待つのをやめる
	waitDelay = 3 * time.Second
)

// RunCommand は、作業フォルダ dir をカレントにしてコマンドを実行するツール。dir は絶対パス。
func RunCommand(dir string) Tool {
	return runCommand{dir: dir}
}

type runCommand struct {
	dir   string
	limit time.Duration // 時間の上限。0 なら commandTimeLimit(テストで短くするため)
}

func (runCommand) Def() Def {
	return Def{
		Name: "run_command",
		Description: "Run a command line with the shell named in the Environment section, and return its output and exit code. " +
			"It already runs in the working directory; do not cd into it. " +
			"Use it to build, test or run programs. Commands cannot read input and are stopped after 120 seconds. The user is asked to approve before it runs.",
		Parameters: json.RawMessage(`{"type": "object", "properties": {"command": {"type": "string", ` +
			`"description": "The command line to run."}}, "required": ["command"]}`),
	}
}

func (t runCommand) TimeLimit() time.Duration {
	if t.limit > 0 {
		return t.limit
	}
	return commandTimeLimit
}

func (t runCommand) Preview(args Args) (Preview, error) {
	command, err := t.check(args)
	if err != nil {
		return Preview{}, err
	}
	return Preview{Note: "コマンド", Command: command}, nil
}

// check はコマンドと作業フォルダを確かめる。
func (t runCommand) check(args Args) (string, error) {
	command := strings.TrimSpace(args.String("command"))
	if command == "" {
		return "", errors.New("command が空です")
	}
	if st, err := os.Stat(t.dir); err != nil || !st.IsDir() {
		return "", fmt.Errorf("作業フォルダ %s がありません(消えたか、名前が変わった可能性があります)", t.dir)
	}
	return command, nil
}

func (t runCommand) Run(ctx context.Context, args Args) (string, error) {
	command, err := t.check(args)
	if err != nil {
		return "", err
	}
	var out outputBuffer
	cmd := shellCommand(t.dir, command)
	cmd.Stdin = nil                     // 標準入力は空(NUL / /dev/null)。入力待ちで止まらないように
	cmd.Stdout, cmd.Stderr = &out, &out // 同じ書き先にすると、標準出力とエラー出力が出た順に1つにまとまる
	cmd.WaitDelay = waitDelay
	tree, err := proc.Start(cmd)
	if err != nil {
		return "", fmt.Errorf("コマンドを起動できません: %w", err)
	}
	defer tree.Close()

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err = <-done:
	case <-ctx.Done():
		// 時間切れか停止。シェルだけでなく、そこから起動された子プロセス(go test が起動したものなど)もまとめて止める
		tree.Kill()
		<-done
		why := "停止されたため"
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			why = fmt.Sprintf("時間の上限(%d 秒)を超えたため", int(t.TimeLimit().Seconds()))
		}
		return "", fmt.Errorf("%s止めました。ここまでの出力:\n%s", why, out.String())
	}

	code := 0
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		code = exit.ExitCode() // 0 以外の終了コードは失敗ではなく結果(テストの失敗など)。モデルに読ませる
	case err != nil && !errors.Is(err, exec.ErrWaitDelay):
		return "", fmt.Errorf("コマンドを実行できません: %w", err)
	}
	text := out.String()
	if text == "" {
		text = "(出力なし)"
	}
	return fmt.Sprintf("%s\n(終了コード %d)", text, code), nil
}

// outputBuffer はコマンドの出力を受け取る。長い出力は先頭と末尾だけを残す(全部をメモリに溜めない)。
type outputBuffer struct {
	mu    sync.Mutex
	head  []byte
	tail  []byte // 末尾。outputTail の2倍を超えたら前を捨てる
	total int    // 受け取ったバイト数
}

func (o *outputBuffer) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.total += len(p)
	rest := p
	if n := outputHead - len(o.head); n > 0 {
		n = min(n, len(rest))
		o.head = append(o.head, rest[:n]...)
		rest = rest[n:]
	}
	o.tail = append(o.tail, rest...)
	if len(o.tail) > 2*outputTail {
		o.tail = append(o.tail[:0], o.tail[len(o.tail)-outputTail:]...)
	}
	return len(p), nil
}

// String は残した出力を文字列にする。間を切ったときは、切ったことと量を書く。
// 改行は LF にそろえ(cmd は CRLF で出す。CR はトークンの無駄)、UTF-8 でないバイトは置き換える(プロンプトを壊さないため)。
func (o *outputBuffer) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	var s string
	if skipped := o.total - len(o.head) - min(len(o.tail), outputTail); skipped <= 0 {
		s = string(o.head) + string(o.tail)
	} else {
		head := trimRuneEnd(o.head)
		tail := trimRuneStart(o.tail[len(o.tail)-outputTail:])
		s = fmt.Sprintf("%s\n…(長いため、間の約 %d バイトを省略しました)…\n%s", head, skipped, tail)
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.TrimRight(strings.ToValidUTF8(s, "?"), "\n")
}

// trimRuneEnd / trimRuneStart は、バイト数で切ったせいで途中になった文字(日本語は3バイト)を、端から落とす。
func trimRuneEnd(b []byte) string {
	for i := 0; i < utf8.UTFMax && len(b) > 0; i++ {
		if r, size := utf8.DecodeLastRune(b); r != utf8.RuneError || size != 1 {
			break
		}
		b = b[:len(b)-1]
	}
	return string(b)
}

func trimRuneStart(b []byte) string {
	for i := 0; i < utf8.UTFMax && len(b) > 0 && !utf8.RuneStart(b[0]); i++ {
		b = b[1:]
	}
	return string(b)
}
