package ui

// send.go: 画面から呼ぶ、送信のメソッド(送る・作り直す・送り直す・今すぐ要約・ツールの確認)と、生成を動かす run・学習用のログ。

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
	"uuid"

	"github.com/secondarykey/aibow"
	"github.com/secondarykey/aibow/config"
	"github.com/secondarykey/aibow/engine"
	"github.com/secondarykey/aibow/log"
	"github.com/secondarykey/aibow/usage"
)

// ---- 送信 -------------------------------------------------------------------------------

// Send は会話にユーザーの発言を足し、返事を生成する。
// 返事の中身は戻り値ではなく、chat イベントで少しずつ送る。
//
// 終わりの合図も戻り値ではなく done イベントにしている。
// Wails ではメソッドの戻り値とイベントは別の経路で画面に届くので、
// 戻り値が先に着くと、最後のほうの文字がまだ届いていないことがあるため。
//
// 画面で「停止」を押すと、JavaScript 側で Promise の cancel() が呼ばれ、
// Wails がこの ctx をキャンセルする。エンジンはそれを見て生成を止める。
//
// files は入力欄で読み込んだファイル(0030。CheckFile が返したもの)。無ければ空の配列か null。
func (s *Service) Send(ctx context.Context, id uuid.UUID, text string, files []aibow.File) {
	s.run(ctx, id, func(ctx context.Context, emit func(aibow.ChatEvent)) {
		s.convs.Send(ctx, id, text, files, emit)
	})
}

// DiscardFile は、送った発言に付けたファイル(画像・テキスト)を捨てる(0030 の決定事項 26〜28)。中身は戻せない。
// 画面は、捨てたら札を「消去済み」にし、メーターを取り直す。
func (s *Service) DiscardFile(ctx context.Context, id, fileID uuid.UUID) error {
	if s.startupErr != nil {
		return s.startupErr
	}
	return s.convs.DiscardFile(ctx, id, fileID)
}

// CheckFile は、入力欄で読み込んだファイル(選ぶ・ドロップ・貼り付け)を確かめ、会話の上限に入るかを数える(0030)。
// 画面は、返ってきたもの(札に出すトークン数と、Send に渡すファイル)を、送信するまで持っておく。
// テキストでも画像でもない・UTF-8 / UTF-16(BOM あり)でない・大きすぎる・会話の上限に入らない・画像を読めないモデルのときはエラー(理由と数字を書く)。
//
// id は送り先の会話(新しい会話の画面ではゼロ。そのときは新しい会話の画面の設定 draft で数える)。
// pending と pendingTokens は、この発言にもう付けているファイルの数とトークン数の合計(一緒に数えるため。中身は送らせない)。
// data は、Wails でもブラウザでも JSON の base64 の文字列で届く([]byte の JSON の形)。
func (s *Service) CheckFile(ctx context.Context, id uuid.UUID, draft *aibow.ConversationSettings, name string, data []byte, pending, pendingTokens int) (aibow.FileCheck, error) {
	if s.startupErr != nil {
		return aibow.FileCheck{}, s.startupErr
	}
	c, err := s.convs.CheckFile(ctx, id, draft, name, data, pending, pendingTokens)
	if errors.Is(err, engine.ErrNoModel) {
		return c, errors.New("モデルを読み込んでいないため、ファイルのトークン数を数えられません。モデルを読み込んでから、もう一度読み込んでください")
	}
	return c, err
}

// Regenerate は最後の返事を作り直す(0011)。途中経過と終わり(done)は Send と同じく chat イベントで送る。
func (s *Service) Regenerate(ctx context.Context, id uuid.UUID) {
	s.run(ctx, id, func(ctx context.Context, emit func(aibow.ChatEvent)) {
		s.convs.Regenerate(ctx, id, emit)
	})
}

// Resend は最後の発言を text に直して送り直す(0011)。途中経過と終わり(done)は Send と同じく chat イベントで送る。
func (s *Service) Resend(ctx context.Context, id uuid.UUID, text string) {
	s.run(ctx, id, func(ctx context.Context, emit func(aibow.ChatEvent)) {
		s.convs.Resend(ctx, id, text, emit)
	})
}

// Compact は「今すぐ要約」。会話の上限を超えていなくても、古い発言を要約する。
// Send と同じく、途中経過(要約の文)と終わり(done)は chat イベントで送る。
func (s *Service) Compact(ctx context.Context, id uuid.UUID) {
	s.run(ctx, id, func(ctx context.Context, emit func(aibow.ChatEvent)) {
		s.convs.Compact(ctx, id, emit)
	})
}

// ConfirmTool は、実行前の確認を待っているツールに答える(カードの「実行する」「拒否」)。
// reqID は confirm イベントの ID。拒否するときの reason は空でもよい(モデルには「理由: (書かれていません)」と渡る)。
// remember は、このツールのこの会話でのこれから先の扱い(空ならこの1回だけ。allow = この会話では今後聞かない、off = 今後許可しない。0010)。
func (s *Service) ConfirmTool(ctx context.Context, id uuid.UUID, reqID string, approved bool, reason string, remember config.ToolPolicy) error {
	if s.startupErr != nil {
		return s.startupErr
	}
	if s.browser {
		// ほかの人の会話の確認には答えさせない(会話が ctx の利用者のものかを、保存先で確かめる)
		if _, _, err := s.convs.Get(ctx, id); err != nil {
			return err
		}
	}
	if err := s.convs.Answer(ctx, id, reqID, aibow.Confirmation{Approved: approved, Reason: reason, Remember: remember}); err != nil {
		return err
	}
	s.stopWait(reqID)
	return nil
}

// PendingConfirm は、会話 id で答えを待っている確認(待っていなければ null)。
// ブラウザの画面は、知らせ(SSE)をつなぎ直したときにこれを呼び、切れている間に届かなかった確認のカードを出し直す(0024 の決定事項 15)。
func (s *Service) PendingConfirm(ctx context.Context, id uuid.UUID) (*aibow.ConfirmRequest, error) {
	if s.startupErr != nil {
		return nil, s.startupErr
	}
	return s.convs.Pending(ctx, id)
}

// confirmLimit は、ブラウザの会話で確認を待つ時間の上限(0024 の決定事項 16)。テストで短くするので変数にしている。
// 確認を待つ間も生成の席を持っている(送信の全体で持つ。takeSeat)ので、超えたら送信を止めて席を返す
var confirmLimit = 5 * time.Minute

// waitConfirm は、確認を待ち始めた送信を、confirmLimit を過ぎたら止めるタイマーを仕掛ける(ブラウザだけ)。
// 止め方は停止ボタンと同じ(送信の ctx をキャンセルする)。待っている確認は「停止されたため実行していません」になる。
func (s *Service) waitConfirm(id uuid.UUID, req *aibow.ConfirmRequest, emit func(aibow.ChatEvent), cancel context.CancelFunc) {
	if !s.browser {
		return
	}
	s.waitMu.Lock()
	defer s.waitMu.Unlock()
	s.waits[req.ID] = time.AfterFunc(confirmLimit, func() {
		s.waitMu.Lock()
		_, waiting := s.waits[req.ID]
		delete(s.waits, req.ID)
		s.waitMu.Unlock()
		if !waiting {
			return // 止める直前に答えた
		}
		log.Warnf("会話 %s: ツール %s の確認に %s 答えがないので、送信を止めます(待っている間は、ほかの人の生成が待たされるため)", id, req.Call.Name, confirmLimit)
		emit(aibow.ChatEvent{ConversationID: id, Type: "error", Caution: true,
			Error: fmt.Sprintf("確認に %s 答えがなかったので、送信を止めました(待っている間は、ほかの人の生成が待たされるため)。続けるときは、もう一度送信してください", confirmLimit)})
		cancel()
	})
}

// stopWait は、確認の ID reqID の打ち切りのタイマーを止める(答えた・送信が終わった)。
func (s *Service) stopWait(reqID string) {
	s.waitMu.Lock()
	defer s.waitMu.Unlock()
	if t, ok := s.waits[reqID]; ok {
		t.Stop()
		delete(s.waits, reqID)
	}
}

// StopSummary は生成中の要約だけを止める(要約中に出る停止ボタン)。
// 送信の途中の要約なら、その回は古い発言を外して(truncate)返事の生成に進む。
// Promise の cancel() だと送信全体が止まってしまうので、別のメソッドにしている。
func (s *Service) StopSummary(ctx context.Context) {
	if s.startupErr == nil {
		s.convs.StopSummary(ctx)
	}
}

// run は Send / Compact の共通部分: 停止とアプリの終了で止める ctx を作り、chat イベントを画面に送りながらログに出す。
func (s *Service) run(ctx context.Context, id uuid.UUID, f func(ctx context.Context, emit func(aibow.ChatEvent))) {
	emit := func(ev aibow.ChatEvent) { s.notify.Chat(ctx, ev) }
	if s.startupErr != nil {
		emit(aibow.ChatEvent{ConversationID: id, Type: "error", Error: s.startupErr.Error()})
		emit(aibow.ChatEvent{ConversationID: id, Type: "done"})
		return
	}

	// 画面の停止ボタンか、アプリの終了のどちらかで止める
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(s.quit, cancel)
	defer stop()

	// 順番待ちの席があれば(aibow-server の中)、送信の全体(モデルの読み直し・要約・返事)の間、席を持つ。
	// 生成1回ごとに取り直すと、その間に Cline などがモデルを入れ替えてしまうため
	waitStart := time.Now()
	if !s.takeSeat(ctx, id, emit) {
		return
	}
	if s.seat != nil {
		defer s.seat.Release()
		// 待った時間は、使用量の記録で、この送信の最初の生成に付ける(0033)
		ctx = usage.WithWait(ctx, time.Since(waitStart))
	}

	who := whoOf(ctx)
	var asked []string // この送信で待った確認の ID(停止で終わったときも、打ち切りのタイマーを残さない)
	defer func() {
		for _, id := range asked {
			s.stopWait(id)
		}
	}()
	f(ctx, func(ev aibow.ChatEvent) {
		switch ev.Type {
		case "summarizing":
			log.Infof("要約を作っています…")
		case "compact":
			logCompact(ev.Compact)
		case "error":
			log.Error(ev.Error)
		case "confirm":
			if c := ev.Confirm; c != nil {
				log.Infof("確認を待っています: %s %s(%s)", c.Call.Name, c.Preview.Path, c.Preview.Note)
				asked = append(asked, c.ID)
				s.waitConfirm(id, c, emit, cancel)
			}
		case "reply":
			// ツールを使うと1回の送信で何回も生成する。回ごとに出すと、KV キャッシュの再利用数の変化が見える
			if r := ev.Reply; r != nil && r.Stats != nil {
				st := r.Stats
				log.Infof("プロンプト %d トークン(再利用 %d)%.2f 秒 / 生成 %d トークン %.2f 秒(%.1f tok/s)/ 終了理由: %s / ツール呼び出し %d 件",
					st.PromptTokens, st.ReusedTokens, st.PromptSec, st.GenTokens, st.GenSec, tokPerSec(st.GenerateStats), st.StopReason, len(r.ToolCalls))
				// 画像(0030)。画像トークン数と、今回エンコードした秒数(KV キャッシュを使い回した画像は 0 秒)
				if st.ImageTokens > 0 {
					log.Infof("画像: %d トークン / エンコード %.2f 秒", st.ImageTokens, st.EncodeSec)
				}
				if st.ProjectorSec > 0 {
					where := "GPU"
					if st.ProjectorCPU {
						where = "CPU(GPU に載らなかった)"
					}
					log.Noticef("画像を読む部品(mmproj)を %s に読み込みました(%.1f 秒)", where, st.ProjectorSec)
				}
			}
		case "tool_result":
			logTool(ev.Message, who)
			s.logWrite(ev.Message, who)
		}
		emit(ev)
	})
}

// logWrite は、ブラウザの会話が資料のフォルダに書いたことを、誰が・どの会話で・どのファイルに書いたかと一緒に Notice で残す(0024 の決定事項 34)。
// 同じファイルを2人が書くことがあるので、あとから追えるように。
func (s *Service) logWrite(m *aibow.StoredMessage, who string) {
	r := m.ToolResult
	if !s.browser || r == nil || r.Error || r.Call.Name != "write_file" && r.Call.Name != "edit_file" || r.Confirm != nil && !r.Confirm.Approved {
		return
	}
	path := ""
	for _, a := range r.Call.Args {
		if a.Name == "path" {
			path = a.Value
		}
	}
	log.Noticef("資料のフォルダに書きました: %s %s(%s の会話 %s)", r.Call.Name, path, who, m.ConversationID)
}

// whoOf は、ログに出す「誰の会話か」(ブラウザの会話なら利用者の名前と会話。0024 の決定事項 11)。
// デスクトップのアプリは利用者が1人なので空(会話の ID だけを出す)。
func whoOf(ctx context.Context) string {
	if c, ok := callerOf(ctx); ok {
		return cmp.Or(c.Name, c.ID.String())
	}
	return ""
}

// takeSeat は順番待ちの席を取る(席が無ければ何もしない)。空いていなければ「待っています」と知らせてから待つ。
// 待っている間に止められたら(停止ボタン・ブラウザを閉じた)、done を送って false。
func (s *Service) takeSeat(ctx context.Context, id uuid.UUID, emit func(aibow.ChatEvent)) bool {
	if s.seat == nil || s.seat.TryTake() {
		return true
	}
	log.Infof("会話 %s: ほかの生成(Cline など)が終わるのを待っています", id)
	emit(aibow.ChatEvent{ConversationID: id, Type: "notice", Text: "ほかの生成が終わるのを待っています…"})
	if s.seat.Take(ctx) {
		return true
	}
	log.Infof("会話 %s: 順番を待っている間に止められました", id)
	emit(aibow.ChatEvent{ConversationID: id, Type: "done"})
	return false
}

func tokPerSec(st engine.GenerateStats) float64 {
	if st.GenSec == 0 {
		return 0
	}
	return float64(st.GenTokens) / st.GenSec
}

// logTool はツールの実行をログに出す(学習用: 名前・引数・結果の長さ・秒数・エラー)。
// 書式が崩れていた呼び出しは、モデルが書いたままの文字列も出す(どう崩れたかを見るため)。
// who は誰の会話か(ブラウザの会話。aibow-server では、誰が・どの会話でツールを動かしたかを残す。0024 の決定事項 11)。
func logTool(m *aibow.StoredMessage, who string) {
	r := m.ToolResult
	if r == nil {
		return
	}
	var args []string
	for _, a := range r.Call.Args {
		args = append(args, fmt.Sprintf("%s=%q", a.Name, a.Value))
	}
	status := "成功"
	if r.Error {
		status = m.Content
	}
	where := ""
	if who != "" {
		where = fmt.Sprintf("(%s の会話 %s)", who, m.ConversationID)
	}
	log.Infof("ツール %s(%s)%s: 結果 %d 文字 / %.3f 秒 / %s", cmp.Or(r.Call.Name, "?"), strings.Join(args, ", "), where,
		utf8.RuneCountInString(m.Content), r.Sec, status)
	if c := r.Confirm; c != nil {
		if c.Approved {
			log.Infof("  確認: 実行した")
		} else {
			log.Infof("  確認: 拒否した(理由: %q)", c.Reason)
		}
	}
	if r.Call.Raw != "" {
		log.Infof("  モデルが書いた呼び出し: %q", r.Call.Raw)
	}
}

// logCompact は compact したことをログに出す(学習用: 方式・前後のトークン数・かかった秒数)。
func logCompact(c *aibow.CompactInfo) {
	switch c.Method {
	case aibow.CompactTruncate:
		log.Infof("compact(truncate): %d → %d トークン(古い発言 %d 件をモデルに渡しません)",
			c.BeforeTokens, c.AfterTokens, c.Dropped)
	case aibow.CompactElide:
		// 縮めた一歩の数と、思考と結果のどちらが効いたかは、会話の仕組み(elide.go)がログに出している
	case aibow.CompactSummary:
		if c.Error != "" {
			log.Warnf("compact(summary): %s", c.Error)
			return
		}
		st := c.Compaction.Stats
		log.Infof("compact(summary): %d → %d トークン(発言 %d 件を要約)/ 要約: 読ませた %d トークン(再利用 %d)・生成 %d トークン / %.1f 秒",
			c.BeforeTokens, c.AfterTokens, c.Dropped, st.PromptTokens, st.ReusedTokens, st.GenTokens, c.Sec)
	}
}
