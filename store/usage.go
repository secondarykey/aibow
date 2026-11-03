package store

// usage.go: aibow-server の生成1回ごとの記録(使用量。0033)の保存と集計。
//
// 書くのは aibow-server の「記録付きの engine」だけ、読むのは使用量の口(管理者は全員の分、本人は自分の分)だけ。
// 会話の持ち主(ctx の利用者)では絞らない:だれの分を読んでよいかは口の側で決める(管理者か・本人か)。
// 集計は SQL で行う(日ごとの合計の表は作らない。決定事項 14)。期間は at の文字列(UTC・桁数固定)の比べ方で絞る。

import (
	"context"
	"database/sql"
	"fmt"
	"time"
	"uuid"

	"github.com/secondarykey/aibow/genlog"
)

// AddGeneration は生成1回の記録を足す。
func (s *SQLite) AddGeneration(ctx context.Context, r genlog.Record) error {
	var wait sql.NullFloat64
	if r.WaitSec != nil {
		wait = sql.NullFloat64{Float64: *r.WaitSec, Valid: true}
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO generation_log
		(id, at, user_id, user_name, route, purpose, conversation_id, model,
		 prompt_tokens, reused_tokens, gen_tokens, image_tokens, prompt_sec, gen_sec, encode_sec, wait_sec, stop_reason)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID.String(), formatTime(r.At), idText(r.User), r.UserName, r.Route, r.Purpose, idText(r.Conversation), r.Model,
		r.PromptTokens, r.ReusedTokens, r.GenTokens, r.ImageTokens, r.PromptSec, r.GenSec, r.EncodeSec, wait, r.StopReason)
	if err != nil {
		return fmt.Errorf("生成の記録を書けません: %w", err)
	}
	return nil
}

// idText は、分からない(ゼロの)ID を空の文字列にする(記録の ID は外部キーではないので、空の値で持つ)。
func idText(id uuid.UUID) string {
	if id == (uuid.UUID{}) {
		return ""
	}
	return id.String()
}

// totalColumns は、まとめた行の数字(genlog.Total の Count から後ろ)。GPU の秒数はモデルの読み込みを入れない(genlog.Record.GPUSec と同じ)
const totalColumns = `count(*), total(purpose = 'summary'), total(prompt_sec + gen_sec + encode_sec), total(gen_tokens),
	total(prompt_tokens), total(reused_tokens), total(image_tokens), total(wait_sec)`

func scanTotal(r rowScanner, t *genlog.Total, head ...any) error {
	// total() は REAL を返し、行が無くても NULL ではなく 0 になる(sum() は NULL)
	var summaries, gen, prompt, reused, image float64
	if err := r.Scan(append(head, &t.Count, &summaries, &t.GPUSec, &gen, &prompt, &reused, &image, &t.WaitSec)...); err != nil {
		return err
	}
	t.Summaries, t.GenTokens, t.PromptTokens, t.ReusedTokens, t.ImageTokens = int(summaries), int(gen), int(prompt), int(reused), int(image)
	return nil
}

// UsageByUser は、since から後の使用量を利用者ごとにまとめる(管理の画面の「使用量」。GPU の秒数の多い順)。
// 名前は記録に写したもの(同じ人で名前が違う行があれば新しいほう)。今もいる人には表示名を付け、いない人は Deleted にする。
func (s *SQLite) UsageByUser(ctx context.Context, since time.Time) ([]genlog.Total, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT g.user_id,
		(SELECT user_name FROM generation_log WHERE user_id = g.user_id ORDER BY at DESC LIMIT 1),
		coalesce(u.display_name, ''), u.id IS NULL AND g.user_id <> '', `+totalColumns+`
		FROM generation_log g LEFT JOIN users u ON u.id = g.user_id
		WHERE g.at >= ? GROUP BY g.user_id ORDER BY 7 DESC, g.user_id`, formatTime(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []genlog.Total{}
	for rows.Next() {
		var t genlog.Total
		if err := scanTotal(rows, &t, &t.Key, &t.Name, &t.DisplayName, &t.Deleted); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// UsageOfUser は、1人(user。分からない人の分は空の文字列)の since から後の合計。
func (s *SQLite) UsageOfUser(ctx context.Context, user string, since time.Time) (genlog.Total, error) {
	var t genlog.Total
	err := scanTotal(s.db.QueryRowContext(ctx, `SELECT `+totalColumns+` FROM generation_log WHERE user_id = ? AND at >= ?`,
		user, formatTime(since)), &t)
	t.Key = user
	return t, err
}

// UsageByModel は、1人の since から後の使用量をモデルごとにまとめる(GPU の秒数の多い順)。
func (s *SQLite) UsageByModel(ctx context.Context, user string, since time.Time) ([]genlog.Total, error) {
	return s.usageGrouped(ctx, "model", "4 DESC, 1", user, since)
}

// UsageByDay は、1人の since から後の使用量を日ごとにまとめる(新しい日から)。日の区切りはサーバーの PC の時刻(now の場所)で数える。
// 記録の時刻は UTC なので、SQLite の date() に場所のずれ(分)を足してから日付にする。
// 'localtime' を使わないのは、SQLite の思う場所と Go の time.Local が同じとは限らないため(Since と区切りをそろえる)。
func (s *SQLite) UsageByDay(ctx context.Context, user string, since time.Time) ([]genlog.Total, error) {
	_, offset := since.Zone()
	return s.usageGrouped(ctx, fmt.Sprintf("date(at, '%+d minutes')", offset/60), "1 DESC", user, since)
}

// usageGrouped は、1人の使用量を key(列か式)でまとめる。order は ORDER BY(列の番号)。どちらもこのファイルの中の決まった値だけを渡す。
func (s *SQLite) usageGrouped(ctx context.Context, key, order, user string, since time.Time) ([]genlog.Total, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+key+`, `+totalColumns+` FROM generation_log
		WHERE user_id = ? AND at >= ? GROUP BY 1 ORDER BY `+order, user, formatTime(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []genlog.Total{}
	for rows.Next() {
		var t genlog.Total
		if err := scanTotal(rows, &t, &t.Key); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
