package main

// usage.go: 記録付きの engine(使用量。0033 の段1)。
//
//	OpenAI 互換の口(api)──────────┐
//	                                 ├→ recorder.Generate → engine.Generate
//	ブラウザの会話(ui.ServerHost)──┘        └→ 終わったら server.db の generation_log に1行
//
// 生成が通る2本の道の両方に書き足さず、aibow-server が engine を渡すところで1か所だけ包む(デコレータの形)。
// 包む側は、だれが・どの道で・何のために呼んだかを ctx から読む:
//   - だれ:API キーかログインの人(auth.UserFrom)、なければ会話の持ち主(aibow.UserOf。ログインなしの形の local)
//   - 道・用途・会話・順番待ち:呼ぶ側が付けた付箋(genlog)
//
// 記録は使えるかどうかには関わらないので、書けなくても生成は止めない(Warn のログだけ。決定事項 11。最後に使った日と同じ考え方)。

import (
	"context"
	"net/http"
	"time"
	"uuid"

	"github.com/secondarykey/aibow"
	"github.com/secondarykey/aibow/api"
	"github.com/secondarykey/aibow/auth"
	"github.com/secondarykey/aibow/engine"
	"github.com/secondarykey/aibow/format"
	"github.com/secondarykey/aibow/genlog"
	"github.com/secondarykey/aibow/log"
)

// generationLog は、記録を書く先(本物は *store.SQLite)。名前を写すために、利用者も読む。
type generationLog interface {
	AddGeneration(ctx context.Context, r genlog.Record) error
	UserByID(ctx context.Context, id uuid.UUID) (auth.User, error)
}

// recorder は、生成1回ごとに使用量を記録する engine。生成以外(トークン数・書式・読み込み中のモデル)はそのまま通す。
// api.Generator を満たすので、api.New にも ui.NewServerHost(ui.ServerEngine)にも渡せる。
type recorder struct {
	api.Generator
	log generationLog
	now func() time.Time // テストで時刻を決めるため
}

func newRecorder(gen api.Generator, l generationLog) *recorder {
	return &recorder{Generator: gen, log: l, now: time.Now}
}

// Generate は生成して、終わったら1行書く。失敗した生成も、秒数が使われていれば書く。止めた生成(cancel)も書く。
func (r *recorder) Generate(ctx context.Context, p format.Prompt, opt engine.GenerateOptions,
	onPrompt func(prompt string), onThinking, onContent func(text string)) (format.Output, engine.GenerateStats, error) {
	at := r.now()
	model := ""
	if i := r.Info(); i != nil {
		model = i.ID // 生成は順番待ちの席の中なので、終わるまでほかの人に入れ替えられない
	}
	out, st, err := r.Generator.Generate(ctx, p, opt, onPrompt, onThinking, onContent)
	r.record(ctx, at, model, st, err)
	return out, st, err
}

func (r *recorder) record(ctx context.Context, at time.Time, model string, st engine.GenerateStats, genErr error) {
	j := genlog.JobOf(ctx)
	rec := genlog.Record{
		ID: uuid.NewV7(), At: at, Route: j.Route, Purpose: j.Purpose, Conversation: j.Conversation, Model: model,
		PromptTokens: st.PromptTokens, ReusedTokens: st.ReusedTokens, GenTokens: st.GenTokens, ImageTokens: st.ImageTokens,
		PromptSec: st.PromptSec, GenSec: st.GenSec, EncodeSec: st.EncodeSec, StopReason: st.StopReason,
	}
	if genErr != nil {
		if rec.GPUSec() == 0 {
			return // 生成まで行かなかった(モデルが無い・ほかの生成の途中など)。GPU を使っていない
		}
		rec.StopReason = "error"
	}
	if rec.Purpose == "" {
		rec.Purpose = genlog.PurposeReply
	}
	if d, ok := genlog.TakeWait(ctx); ok {
		sec := d.Seconds()
		rec.WaitSec = &sec
	}
	// 止めた生成でも書けるように、止める合図だけを外す(利用者などの値は残す)
	ctx = context.WithoutCancel(ctx)
	rec.User, rec.UserName = r.who(ctx, rec.Route)
	if err := r.log.AddGeneration(ctx, rec); err != nil {
		log.Warnf("使用量を記録できませんでした(生成は続けます): %v", err)
	}
}

// who は、生成を呼んだ人と、その名前(記録に写す)。分からなければゼロと空で、Warn のログを出す(どの道で抜けているかを見つけるため。決定事項 12)。
func (r *recorder) who(ctx context.Context, route string) (uuid.UUID, string) {
	if u, ok := auth.UserFrom(ctx); ok {
		return u.ID, u.Name
	}
	if id, ok := aibow.UserOf(ctx); ok {
		u, err := r.log.UserByID(ctx, id)
		if err != nil {
			log.Warnf("使用量の記録で、利用者 %s の名前を読めません: %v", id, err)
			return id, ""
		}
		return u.ID, u.Name
	}
	log.Warnf("生成を呼んだ人が分かりません(道: %s)。使用量は利用者を空にして記録します", route)
	return uuid.UUID{}, ""
}

// asLocal は、API キーの要らない形(127.0.0.1 だけでログインなし)の OpenAI 互換の口を、ログインなしの形の利用者(local)の呼び出しにする。
// 保存の口(/aibow/store/)がこの形で local を使うのと同じ線(使用量の記録で、だれでもない生成にしないため)。
func asLocal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(aibow.WithUser(r.Context(), aibow.LocalUser)))
	})
}
