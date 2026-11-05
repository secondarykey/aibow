package aibow

// elide.go: 今の組の中の compact(0040)。1回の送信の中で、古い一歩のツールの結果と思考を省略して続ける。
//
// compact(compact.go)は組(ユーザーの発言 + その返事)ごとに外す・要約し、最新の組は必ずそのまま渡す。
// ツールを使う送信では、1回の送信(= 最新の組)の中でツールの結果と思考が積み重なるので、
// 長い作業ほど最新の組だけで会話の上限を使い切り、結果を切るか止めるしかなかった。
//
// そこで、今の組の中の「一歩」(返事1つ = 思考とツールの呼び出し + その呼び出しの結果)のうち古いものを、
//
//   - ツールの結果は、短い札(elidedNote)に置き換える
//   - その一歩の返事の思考は、空にする
//
// の形にして渡す。ツールの呼び出し(名前と引数)はそのまま残す。実際のエージェントが長い作業で使う
// 「コンテキストの編集(tool result clearing)」と同じ考え方で、「何をしたか(呼び出し)」は残し、
// 「その時に読んだ中身(結果)」は要ればもう一度読めるので捨てる。
//
// 縮めるのは、今の組が会話の上限の 3/4 を超えたときに、1/2 まで(水位)。少しずつ縮めると、
// そのたびに縮めた一歩から後ろの KV キャッシュが使えなくなる(プロンプトの途中が変わるため。Qwen3.5 は途中まで巻き戻せないので全部を読み直す)ので、まとめて縮めて回数を減らす。
//
// 縮めたことは発言の印(StoredMessage.Elided)として保存する。DB の結果の文と思考は消さない(画面には全部残す)。
// モデルに渡す形にするところ(promptMessages)で札に置き換えるので、送信・メーター・truncate の位置探し・要約の前後の数え方が、
// どれも縮めた形で数える(0011 の Excluded・0037 の予定と同じ置き方)。

import (
	"context"
	"fmt"
	"slices"
	"time"
	"unicode/utf8"
	"uuid"

	"github.com/secondarykey/aibow/format"
	"github.com/secondarykey/aibow/log"
)

// CompactElide は、今の組の古い一歩を省略したときの compact イベントの方式(CompactInfo.Method)。
// 会話の設定の compact の値ではない(truncate / summary の会話で、送信の途中に起きる)
const CompactElide = "elide"

const (
	// elideKeepSteps は、縮めずにそのまま渡す新しい一歩の数(今まさに使っている結果と、その一つ前)
	elideKeepSteps = 2
	// elideMinRunes は、これ以下の長さの結果は札にしない(札のほうが長くなる。エラーや update_plan の結果など)
	elideMinRunes = 200
)

// elideHigh は、縮め始める水位(会話の上限の 3/4)。elideLow は、縮めて下げる先(1/2)。
func elideHigh(limit int) int { return limit * 3 / 4 }
func elideLow(limit int) int  { return limit / 2 }

// elidedNote は、省略した結果の代わりにモデルに渡す札(runes は元の結果の文字数)。
// 何が起きたか・要るときにどうすればよいかを書く(cutResult の書き添えと同じ考え方)
func elidedNote(runes int) string {
	return fmt.Sprintf("(省略:会話の上限を空けるため、この結果はもう渡していません。元は約 %d 文字。要るときは、もう一度呼んでください)", runes)
}

// elideForModel は、印の付いた発言 m をモデルに渡す形 fm に当てる(promptMessages から呼ぶ)。
// tool は札に(札より短い結果はそのまま)、assistant は思考を空にする。呼び出し(ToolCalls)は変えない。
func elideForModel(m StoredMessage, fm *format.Message) {
	if !m.Elided {
		return
	}
	switch m.Role {
	case "tool":
		if n := utf8.RuneCountInString(m.Content); n > elideMinRunes {
			fm.Content = elidedNote(n)
		}
	case "assistant":
		fm.Thinking = ""
	}
}

// step は、今の組の中の一歩。history[start:end] が、返事(assistant)1つとその呼び出しの結果(tool)。
type step struct{ start, end int }

// turnSteps は、今の組(最後のユーザーの発言から後ろ)の一歩を古い順に返す。
func turnSteps(history []StoredMessage) []step {
	var out []step
	for i := lastUser(history) + 1; i < len(history); i++ {
		switch {
		case history[i].Role == "assistant":
			out = append(out, step{i, i + 1})
		case len(out) > 0:
			out[len(out)-1].end = i + 1 // 結果は、その前の返事の一歩に入る
		}
	}
	return out
}

// gains は、一歩 s を縮めると何か減るか(思考がある・札より長い結果がある)。まだ縮めていない一歩だけを見る。
func (s step) gains(history []StoredMessage) bool {
	if history[s.start].Elided {
		return false
	}
	for _, m := range history[s.start:s.end] {
		if (m.Role == "assistant" && m.Thinking != "") || (m.Role == "tool" && utf8.RuneCountInString(m.Content) > elideMinRunes) {
			return true
		}
	}
	return false
}

// elideOld は、今の組が水位(会話の上限の 3/4)を超えていたら、古い一歩から順に縮めて 1/2 まで下げ、印を保存する。
// next は、まだ history に足していないが、これから足す発言(新しいツールの結果)。数えるときに入れるが、縮めない。
// 縮めたら compact イベント(Method: elide)で知らせ、印を付けた history(写し)を返す。縮めなければ history をそのまま返す。
//
// 縮めるのは truncate / summary の会話だけ(none は「何もしない」なので、今までどおり上限で止める)。
// 新しい 2 歩(elideKeepSteps)は縮めない。全部縮めても 1/2 まで下がらなければ、縮められるだけ縮める
// (それでも入らない結果は、呼ぶ側が今までどおり切る(fitResult)・止める)。
func (cs *Conversations) elideOld(ctx context.Context, conv Conversation, history []StoredMessage, next []StoredMessage, emit func(ChatEvent)) ([]StoredMessage, error) {
	set := conv.Settings
	if set.Compact != CompactTruncate && set.Compact != CompactSummary {
		return history, nil
	}
	limit := cs.limitsOf(set).history
	count := func(h []StoredMessage) (int, error) {
		return cs.turnTokens(set, append(slices.Clip(h), next...))
	}
	before, err := count(history)
	if err != nil || before <= elideHigh(limit) {
		return history, err
	}
	steps := turnSteps(history)
	if len(steps) <= elideKeepSteps {
		return history, nil
	}
	start := time.Now()

	// 古い一歩から1つずつ縮め、1/2 を下回ったらやめる(縮める数を最小にして、KV キャッシュの使えなくなる範囲を狭くする)
	out := slices.Clone(history)
	var done []step
	after := before
	for _, s := range steps[:len(steps)-elideKeepSteps] {
		if !s.gains(out) {
			continue
		}
		for i := s.start; i < s.end; i++ {
			out[i].Elided = true // 印は一歩の単位で付ける(返事と、その呼び出しの結果の全部)
		}
		done = append(done, s)
		if after, err = count(out); err != nil {
			return history, err
		}
		if after <= elideLow(limit) {
			break
		}
	}
	if len(done) == 0 {
		return history, nil
	}

	// 印を保存する(上限で止まったあとに「続けて」と送っても、前の組が元の長さに戻らないように。メーターも同じ形で数える)。
	// 止められていても保存する(save と同じ)
	var ids []uuid.UUID
	results := 0
	for _, s := range done {
		for _, m := range out[s.start:s.end] {
			ids = append(ids, m.ID)
			if m.Role == "tool" && utf8.RuneCountInString(m.Content) > elideMinRunes {
				results++
			}
		}
	}
	if err := cs.store.SetMessagesElided(context.WithoutCancel(ctx), ids); err != nil {
		return history, fmt.Errorf("省略した印を保存できません: %w", err)
	}
	cs.logElide(set, history, next, done, before, after, limit)
	emit(ChatEvent{ConversationID: conv.ID, Type: "compact", Compact: &CompactInfo{
		Method: CompactElide, BeforeTokens: before, AfterTokens: after, Dropped: results, Sec: time.Since(start).Seconds(),
	}})
	return out, nil
}

// logElide は、縮めたことをログに出す(学習用)。思考と結果のどちらがどれだけ効いたかを分けて出す
// (思考だけを空にした形をもう1回数え、その差を思考の分、残りを結果の分にする)。
func (cs *Conversations) logElide(set ConversationSettings, history, next []StoredMessage, done []step, before, after, limit int) {
	thinkOnly := slices.Clone(history)
	for _, s := range done {
		thinkOnly[s.start].Thinking = ""
	}
	mid, err := cs.turnTokens(set, append(thinkOnly, next...))
	if err != nil {
		log.Infof("今の組の古い一歩 %d 個を省略しました(%d → %d トークン。水位 %d を超えたので %d まで)", len(done), before, after, elideHigh(limit), elideLow(limit))
		return
	}
	log.Infof("今の組の古い一歩 %d 個を省略しました(%d → %d トークン。うち思考で %d・結果で %d 減った。水位 %d を超えたので %d まで)",
		len(done), before, after, before-mid, mid-after, elideHigh(limit), elideLow(limit))
}
