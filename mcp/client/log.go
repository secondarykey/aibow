package client

// log.go: MCP のやり取りとサーバーのエラー出力を、1行ずつログに出す部分(学習用)。

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/secondarykey/aibow/log"
)

// logMaxChars はログの1行の長さの上限。ツールの定義の一覧やファイルの中身は長いので、先頭だけを出す
const logMaxChars = 400

// lineLogger は、書かれたものを1行ずつに分けてログ(aibow/log)に出す io.Writer。
//
// rpc が true のときは、SDK の LoggingTransport が書いてくる「write: {JSON}」(aibow → サーバー)と
// 「read: {JSON}」(サーバー → aibow)を受け取る。1つのメッセージを2段で出す:
//
//   - Info:  id と何を呼んだか(`→ #3 tools/call read_file`・`← #3 tools/call read_file の結果`)。流れを追うにはこれで足りる
//   - Debug: JSON そのもの(`→ {"jsonrpc":"2.0","id":3,…}`)。中身まで見たいとき
//
// JSON を毎回 Info に出すと、ツールの定義の一覧やファイルの中身でログが埋まり、ほかの行が読めなくなるため(2026-11-03、ユーザーの指示)。
// サーバーのエラー出力(stderr。rpc が false)は、そのまま Info で出す。
type lineLogger struct {
	prefix string
	rpc    bool

	mu      sync.Mutex
	buf     []byte
	tail    string            // 最後に出した行(起動に失敗したときに、理由として画面のお知らせに添える)
	pending map[string]string // 答えを待っているリクエスト(向き+id → 何を呼んだか)。答えの行に、何への答えかを添えるため
}

// last は最後の行を返す。改行で終わっていない書きかけの行があれば、そちらを返す。
func (l *lineLogger) last() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if s := strings.TrimSpace(string(l.buf)); s != "" {
		return shorten(s)
	}
	return l.tail
}

func (l *lineLogger) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf = append(l.buf, p...)
	for {
		i := bytes.IndexByte(l.buf, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimRight(string(l.buf[:i]), "\r")
		l.buf = l.buf[i+1:]
		if line != "" {
			l.output(line)
			l.tail = shorten(strings.TrimSpace(line))
		}
	}
	return len(p), nil
}

// output は1行をログに出す。JSON-RPC のメッセージなら要約を Info に、JSON を Debug に出す。
func (l *lineLogger) output(line string) {
	if !l.rpc {
		log.Info(l.prefix + shorten(line))
		return
	}
	dir, body, ok := direction(line)
	if !ok {
		log.Info(l.prefix + shorten(line))
		return
	}
	summary, ok := l.summarize(dir, body)
	if !ok {
		// 読めない形(JSON-RPC のバッチなど)は、要約できないので JSON のまま Info で出す
		log.Info(l.prefix + dir + " " + shorten(body))
		return
	}
	log.Info(l.prefix + dir + " " + summary)
	log.Debug(l.prefix + dir + " " + shorten(body))
}

// direction は LoggingTransport の書き方を、向きの矢印と JSON に分ける。
func direction(line string) (dir, body string, ok bool) {
	if s, ok := strings.CutPrefix(line, "write: "); ok {
		return "→", s, true
	}
	if s, ok := strings.CutPrefix(line, "read: "); ok {
		return "←", s, true
	}
	return "", line, false
}

// rpcMessage は、要約に使うところだけを読む JSON-RPC のメッセージ。
//
// JSON-RPC のメッセージは3種類で、欄の有無で見分ける:
//   - リクエスト:id と method がある(答えが返ってくる)
//   - 通知:method だけ(答えは返ってこない。notifications/… など)
//   - 答え:id と、result か error のどちらか(method は無い。何への答えかは id で対応させる)
type rpcMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params struct {
		Name string `json:"name"` // tools/call・prompts/get の、呼ぶものの名前
		URI  string `json:"uri"`  // resources/read の、読むものの URI
	} `json:"params"`
	Result *struct {
		IsError    bool   `json:"isError"`    // tools/call の結果が「ツールの失敗」か(JSON-RPC のエラーとは別)
		ResultType string `json:"resultType"` // input_required なら、サーバーがユーザーへの確認を求めている(MRTR。2026-07-28 版)
	} `json:"result"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// summarize は、メッセージを「id と何を呼んだか」の1行にする。
func (l *lineLogger) summarize(dir, body string) (string, bool) {
	var m rpcMessage
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		return "", false
	}
	id := ""
	if len(m.ID) > 0 && string(m.ID) != "null" {
		id = "#" + strings.Trim(string(m.ID), `"`)
	}
	switch {
	case m.Method != "" && id == "": // 通知
		return what(m), true
	case m.Method != "": // リクエスト。答えが逆向きで返ってくるので覚えておく
		if l.pending == nil {
			l.pending = map[string]string{}
		}
		l.pending[dir+id] = what(m)
		return id + " " + what(m), true
	case id != "": // 答え
		req := opposite(dir) + id
		called, found := l.pending[req]
		delete(l.pending, req)
		if found {
			return id + " " + called + " の" + answer(m), true
		}
		return id + " " + answer(m), true
	}
	return "", false
}

// what は、リクエストと通知が何を呼んだか(method と、ツールなどの名前)。
func what(m rpcMessage) string {
	if name := cmp.Or(m.Params.Name, m.Params.URI); name != "" {
		return m.Method + " " + name
	}
	return m.Method
}

// answer は、答えの中身を一言にする。
func answer(m rpcMessage) string {
	switch {
	case m.Error != nil:
		return fmt.Sprintf("エラー %d %s", m.Error.Code, shorten(m.Error.Message))
	case m.Result != nil && m.Result.IsError:
		return "結果(ツールが失敗した)"
	case m.Result != nil && m.Result.ResultType == "input_required":
		return "結果(input_required。ユーザーへの確認を求めている)"
	}
	return "結果"
}

// opposite は逆向きの矢印。リクエストが → なら、答えは ← で来る。
func opposite(dir string) string {
	if dir == "→" {
		return "←"
	}
	return "→"
}

func shorten(s string) string {
	if utf8.RuneCountInString(s) <= logMaxChars {
		return s
	}
	return string([]rune(s)[:logMaxChars]) + "…"
}
