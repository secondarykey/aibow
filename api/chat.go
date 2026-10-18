package api

// chat.go: POST /v1/chat/completions。履歴を受け取り、続きの返事を生成して返す。
//
// 流れ:
//
//	1. JSON を読み、書式の形(format.Prompt)にする(types.go の toPrompt)
//	2. 書式を当てたトークン数を数え、作業領域に入らなければエラーを返す(compact はしない。履歴を持つのはクライアントなので)
//	3. 生成の順番を待つ(生成は1つずつ)
//	4. 生成する。ストリーミングなら、文字が出るたびに Server-Sent Events の塊を送る
//	5. モデルが書いたツール呼び出しを tool_calls にする(tools.go)
//	6. 学習用のログ(KV キャッシュを使い回せた数・速さ・終了の理由・呼び出しの数)を出す

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
	"uuid"

	"github.com/secondarykey/aibow/engine"
	"github.com/secondarykey/aibow/format"
	"github.com/secondarykey/aibow/log"
	"github.com/secondarykey/aibow/tool"
)

// maxBody はリクエストの本文の上限。履歴の全部が毎回来るので大きめにする(作業領域 32K トークンでも数百 KB)
const maxBody = 32 << 20

// handleChat は POST /v1/chat/completions。
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	var req chatRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "", "リクエストの JSON が読めません: "+err.Error())
		return
	}
	think := s.defaults.Think
	if v := req.ChatTemplateKwargs.EnableThinking; v != nil {
		think = *v
	}
	p, err := toPrompt(req, think)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "", err.Error())
		return
	}
	opt, err := s.options(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "", err.Error())
		return
	}

	info := s.gen.Info()
	if info == nil {
		writeError(w, http.StatusServiceUnavailable, "server_error", "", engine.ErrNoModel.Error())
		return
	}
	if req.Model != "" && req.Model != info.ID {
		log.Infof("リクエストのモデル %q ではなく、読み込み中の %s を使います(モデルは起動の引数で決めます)", req.Model, info.ID)
	}

	// 作業領域に入るかを、生成の前に確かめる。トークン数を数えるのは生成中でもできる(engine の vocabMu)ので、順番を待つ前に断れる。
	// 返事の分の空きまでは求めない(足りなくなったら、そこで止めて finish_reason を length にする)
	n, err := s.gen.CountTokens(p)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server_error", "", err.Error())
		return
	}
	if nCtx := int(info.NCtx); n >= nCtx {
		log.Warnf("リクエストを断りました: プロンプト %d トークンが作業領域 %d に入りません(発言 %d 件)", n, nCtx, len(p.Messages))
		writeError(w, http.StatusBadRequest, "invalid_request_error", "context_length_exceeded",
			fmt.Sprintf("履歴が長すぎます。プロンプトが %d トークンあり、作業領域(%d トークン)に入りません。"+
				"履歴を短くするか、aibow-api を -ctx で大きくして起動し直してください", n, nCtx))
		return
	}

	// 順番を待つ。待っている間につながりが切れたら、何も返さずに抜ける(列から外れる)
	waitStart := time.Now()
	select {
	case s.turn <- struct{}{}:
	case <-r.Context().Done():
		log.Infof("順番を待っている間につながりが切れました(%.2f 秒待った)", time.Since(waitStart).Seconds())
		return
	}
	defer func() { <-s.turn }()
	wait := time.Since(waitStart)

	c := call{
		id:      "chatcmpl-" + uuid.NewV7().String(),
		created: s.now().Unix(),
		model:   info.ID,
		msgs:    len(p.Messages),
		tools:   p.Tools,
		wait:    wait,
	}
	if req.Stream {
		s.stream(w, r, c, p, opt, req.StreamOptions != nil && req.StreamOptions.IncludeUsage)
		return
	}
	s.complete(w, r, c, p, opt)
}

// options はリクエストのサンプリングと長さを、書かれていなければ既定の値にする。
func (s *Server) options(req chatRequest) (engine.GenerateOptions, error) {
	opt := engine.GenerateOptions{
		Temperature:     s.defaults.Temperature,
		MaxTokens:       s.defaults.MaxTokens,
		PresencePenalty: s.defaults.PresencePenalty,
	}
	if req.Temperature != nil {
		opt.Temperature = *req.Temperature
	}
	if req.PresencePenalty != nil {
		opt.PresencePenalty = *req.PresencePenalty
	}
	for _, v := range []*int{req.MaxTokens, req.MaxCompletionTokens} { // 後ろ(新しい名前)が勝つ
		if v != nil {
			opt.MaxTokens = *v
		}
	}
	if opt.MaxTokens <= 0 {
		return opt, fmt.Errorf("max_tokens(max_completion_tokens)は 1 以上にしてください: %d", opt.MaxTokens)
	}
	return opt, nil
}

// call は1回のリクエストの、返事に書く値とログに出す値。
type call struct {
	id      string
	created int64
	model   string
	msgs    int           // 発言の数(ログ用)
	tools   []tool.Def    // モデルに知らせたツール(呼び出しの型を決めるのに使う)
	wait    time.Duration // 順番を待った時間(ログ用)
}

// complete はストリーミングなしで返す。生成し終えてから、1つの JSON で返す。
func (s *Server) complete(w http.ResponseWriter, r *http.Request, c call, p format.Prompt, opt engine.GenerateOptions) {
	nop := func(string) {}
	out, st, err := s.gen.Generate(r.Context(), p, opt, nop, nop, nop)
	if err != nil {
		logResult(c, false, st, 0, err)
		writeError(w, http.StatusInternalServerError, "server_error", "", err.Error())
		return
	}
	calls, extra := toResponseCalls(out.ToolCalls, c.tools)
	logResult(c, false, st, len(calls), nil)
	if st.StopReason == "cancel" {
		return // つながりが切れた。返す相手がいない
	}
	fr := finishReason(st.StopReason, len(calls))
	writeJSON(w, http.StatusOK, completion{
		ID: c.id, Object: "chat.completion", Created: c.created, Model: c.model,
		Choices: []choice{{
			Message:      &message{Role: "assistant", Content: joinContent(out.Content, extra), ReasoningContent: out.Thinking, ToolCalls: calls},
			FinishReason: &fr,
		}},
		Usage: usageOf(st),
	})
}

// stream は Server-Sent Events(SSE)で少しずつ返す。
//
// SSE は「data: <JSON>」の行と空行を1つの塊として、つながりを開いたまま何回も書く形。
// 最初の塊で役(assistant)を、続く塊で増えた文字(delta)を、最後の塊で終了の理由を送り、
// 終わりの印として「data: [DONE]」を書く。塊を書くたびに Flush して、すぐに相手に届ける
// (Flush しないと、net/http が溜めてからまとめて送るので、少しずつ見えない)。
//
// ツール呼び出しは、生成が終わってから1つにつき1つの塊で、引数まで全部を送る(0012 の 6-2 で Code が決めた)。
// 書式は <tool_call> から後ろを持っておいて生成の後で読み取る(途中では呼び出しが閉じているか分からない)ので、少しずつは送れない。
// OpenAI の形では arguments を何回かに分けて送ってもよいことになっていて、クライアントはつなげて読むので、1回で全部でも読める。
func (s *Server) stream(w http.ResponseWriter, r *http.Request, c call, p format.Prompt, opt engine.GenerateOptions, includeUsage bool) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)

	// 書けなかったとき(つながりが切れたとき)は気にしない。r.Context() が終わり、生成もそこで止まる
	send := func(v any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(w, "data: %s\n\n", b)
		rc.Flush()
	}
	chunk := func(d delta, finish *string) completion {
		return completion{
			ID: c.id, Object: "chat.completion.chunk", Created: c.created, Model: c.model,
			Choices: []choice{{Delta: &d, FinishReason: finish}},
		}
	}

	send(chunk(delta{Role: "assistant"}, nil))
	out, st, err := s.gen.Generate(r.Context(), p, opt, func(string) {},
		func(t string) { send(chunk(delta{ReasoningContent: t}, nil)) },
		func(t string) { send(chunk(delta{Content: t}, nil)) })
	if err != nil {
		logResult(c, true, st, 0, err)
		// ステータスはもう 200 で送ってしまったので、エラーは塊の1つとして送る(OpenAI も途中のエラーはこの形)
		send(errorBody{Error: errorObject{Message: err.Error(), Type: "server_error"}})
		return
	}
	calls, extra := toResponseCalls(out.ToolCalls, c.tools)
	logResult(c, true, st, len(calls), nil)
	if st.StopReason == "cancel" {
		return
	}
	if extra != "" {
		// 書式が崩れた呼び出しは、答えの続きとして送る(答えの後ろには、ストリーミングなしと同じく空行を挟む)
		send(chunk(delta{Content: joinContent(out.Content, extra)[len(out.Content):]}, nil))
	}
	for i := range calls {
		calls[i].Index = &i
		send(chunk(delta{ToolCalls: calls[i : i+1]}, nil))
	}
	fr := finishReason(st.StopReason, len(calls))
	send(chunk(delta{}, &fr))
	if includeUsage {
		send(completion{
			ID: c.id, Object: "chat.completion.chunk", Created: c.created, Model: c.model,
			Choices: []choice{}, Usage: usageOf(st),
		})
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
	rc.Flush()
}

// finishReason は、エンジンの終了の理由を OpenAI の finish_reason にする。
//
//	ツール呼び出しを返すとき                                       → tool_calls(クライアントはツールを実行して、結果を足して送り直す)
//	eos(発言の終わりのトークン)・stop(書式が決めた位置で止めた) → stop
//	length(返事の上限)・context(作業領域がいっぱい)            → length
func finishReason(stop string, calls int) string {
	if calls > 0 {
		return "tool_calls"
	}
	switch stop {
	case "length", "context":
		return "length"
	}
	return "stop"
}

func usageOf(st engine.GenerateStats) *usage {
	u := &usage{PromptTokens: st.PromptTokens, CompletionTokens: st.GenTokens, TotalTokens: st.PromptTokens + st.GenTokens}
	u.PromptTokensDetails.CachedTokens = st.ReusedTokens
	return u
}

// joinContent は、答えの後ろに、書式が崩れて読み取れなかった呼び出し(モデルが書いたまま)を空行を挟んで足す。
func joinContent(content, extra string) string {
	switch {
	case extra == "":
		return content
	case content == "":
		return extra
	}
	return content + "\n\n" + extra
}

// logResult は学習用のログ。リクエストごとに1行。
// 「KV キャッシュの再利用」が、続けて送ったリクエストで前回の分を使い回せたかを表す(API はステートレスなので、ここが速さを決める)。
func logResult(c call, stream bool, st engine.GenerateStats, calls int, err error) {
	mode := "まとめて"
	if stream {
		mode = "ストリーミング"
	}
	if err != nil {
		log.Errorf("生成に失敗しました(%s・発言 %d 件・プロンプト %d トークン): %v", mode, c.msgs, st.PromptTokens, err)
		return
	}
	speed := 0.0
	if st.GenSec > 0 {
		speed = float64(st.GenTokens) / st.GenSec
	}
	log.Infof("%s・発言 %d 件・ツール %d 個 / 順番待ち %.2f 秒 / プロンプト %d トークン(KV キャッシュの再利用 %d)%.2f 秒 / 生成 %d トークン %.2f 秒(%.1f tok/s)/ 終了理由: %s / ツール呼び出し %d 件",
		mode, c.msgs, len(c.tools), c.wait.Seconds(), st.PromptTokens, st.ReusedTokens, st.PromptSec, st.GenTokens, st.GenSec, speed, st.StopReason, calls)
}
