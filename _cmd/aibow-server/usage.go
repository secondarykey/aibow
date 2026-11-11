package main

// usage.go: 記録付きの engine(使用量。0033 の段1)と、使用量を見る口(段2・段3)。
//
//	OpenAI 互換の口(api)──────────┐
//	                                 ├→ recorder.Generate → engine.Generate
//	ブラウザの会話(ui.ServerHost)──┘        └→ 終わったら server.db の generation_log に1行
//
// 生成が通る2本の道の両方に書き足さず、aibow-server が engine を渡すところで1か所だけ包む(デコレータの形)。
// 包む側は、だれが・どの道で・何のために呼んだかを ctx から読む:
//   - だれ:API キーかログインの人(auth.UserFrom)、なければ会話の持ち主(aibow.UserOf。ログインなしの形の local)
//   - 道・用途・会話・順番待ち:呼ぶ側が付けた付箋(usage パッケージ)
//
// 記録は使えるかどうかには関わらないので、書けなくても生成は止めない(Warn のログだけ。決定事項 11。最後に使った日と同じ考え方)。

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
	"uuid"

	"github.com/secondarykey/aibow"
	"github.com/secondarykey/aibow/api"
	"github.com/secondarykey/aibow/auth"
	"github.com/secondarykey/aibow/engine"
	"github.com/secondarykey/aibow/format"
	"github.com/secondarykey/aibow/log"
	"github.com/secondarykey/aibow/usage"
)

// generationLog は、記録を書く先(本物は *store.SQLite)。名前を写すために、利用者も読む。
type generationLog interface {
	AddGeneration(ctx context.Context, r usage.Record) error
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
	logSequence(st)
	return out, st, err
}

// logSequence は、シーケンスが2本以上のとき、どのシーケンスを使ったかを出す(学習用。0023 の決定事項 8)。
// 生成は2本の道(OpenAI 互換の口・ブラウザの会話)を通るので、どちらの道のログにも書き足さず、ここで1か所だけ出す。
// 混ざった数は、この生成の decode に一番多く入っていた生成の数(自分を含む。2 なら、ほかの人と1回の decode にまとめて進めた)。
func logSequence(st engine.GenerateStats) {
	if st.Mixed == 0 {
		return // シーケンスが1本(今までと同じ)か、decode まで行かなかった
	}
	log.Infof("シーケンス %d:プロンプト %d トークン(KV キャッシュの使い回し %d)%.2f 秒 / 生成 %d トークン %.2f 秒 / 同じ decode に混ざった生成 最大 %d 件 / 終了理由: %s",
		st.Seq, st.PromptTokens, st.ReusedTokens, st.PromptSec, st.GenTokens, st.GenSec, st.Mixed, st.StopReason)
}

func (r *recorder) record(ctx context.Context, at time.Time, model string, st engine.GenerateStats, genErr error) {
	j := usage.JobOf(ctx)
	rec := usage.Record{
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
		rec.Purpose = usage.PurposeReply
	}
	if d, ok := usage.TakeWait(ctx); ok {
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
// 保存の口(/_aibow/store/)がこの形で local を使うのと同じ線(使用量の記録で、だれでもない生成にしないため)。
func asLocal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(aibow.WithUser(r.Context(), aibow.LocalUser)))
	})
}

// ---- 使用量を見る口(0033 の段2・段3) ----------------------------------------------------
//
//	GET /_aibow/admin/usage?days=7                  利用者ごと([]usage.Total。GPU の秒数の多い順)
//	GET /_aibow/admin/usage/user?id=<ID>&days=7     1人のモデルごと・日ごと({"models", "days"}。id が空ならだれでもない生成の分)
//	GET /_aibow/auth/usage                          自分の今日・7日・30日(usage.Mine)
//
// days は今日を含めた日数(1〜366。書かなければ 7)。日の区切りはサーバーの PC の時刻。
// 全員の分を見られるのは、ログインした管理者だけ(利用者のタブと同じ線)。ログインなしの形(利用者は local だけ)では、
// 管理の画面を開けるサーバーの PC の上の画面に見せる(0033 の決定事項 13)。
// 自分の分は、ブラウザ(cookie)と画面のアプリ(アプリ用のトークン。auth.Keys.WithBearer)のどちらからも呼べる。

// usageStore は、使用量を読む先(本物は *store.SQLite)。
type usageStore interface {
	UsageByUser(ctx context.Context, since time.Time) ([]usage.Total, error)
	UsageOfUser(ctx context.Context, user string, since time.Time) (usage.Total, error)
	UsageByModel(ctx context.Context, user string, since time.Time) ([]usage.Total, error)
	UsageByDay(ctx context.Context, user string, since time.Time) ([]usage.Total, error)
}

// usageDetail は、1人の使用量の内訳。
type usageDetail struct {
	Models []usage.Total `json:"models"`
	Days   []usage.Total `json:"days"`
}

// adminUsageHandler は、全員の使用量の口(/_aibow/admin/usage の下)。now はテストで時刻を決めるため。
func adminUsageHandler(st usageStore, login bool, now func() time.Time) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /_aibow/admin/usage", func(w http.ResponseWriter, r *http.Request) {
		since, ok := usageSince(w, r, now())
		if !ok {
			return
		}
		out, err := st.UsageByUser(r.Context(), since)
		if err != nil {
			writeAdminError(w, http.StatusInternalServerError, err)
			return
		}
		writeAdminJSON(w, out)
	})
	mux.HandleFunc("GET /_aibow/admin/usage/user", func(w http.ResponseWriter, r *http.Request) {
		since, ok := usageSince(w, r, now())
		if !ok {
			return
		}
		id := r.URL.Query().Get("id")
		var d usageDetail
		var err error
		if d.Models, err = st.UsageByModel(r.Context(), id, since); err == nil {
			d.Days, err = st.UsageByDay(r.Context(), id, since)
		}
		if err != nil {
			writeAdminError(w, http.StatusInternalServerError, err)
			return
		}
		writeAdminJSON(w, d)
	})
	return usageViewer(login, mux)
}

// usageViewer は、全員の使用量を見てよい人だけを通す。ログインが要る形はログインした管理者、要らない形はサーバーの PC の上からの呼び出し。
func usageViewer(login bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if login {
			u, ok := auth.UserFrom(r.Context())
			if !ok {
				writeAdminError(w, http.StatusUnauthorized, errNotLoggedIn)
				return
			}
			if !u.Admin {
				log.Warnf("管理者でない人が全員の使用量を呼んだので断りました: %s %s(%s)", r.Method, r.URL.Path, u.Name)
				writeAdminError(w, http.StatusForbidden, errors.New("全員の使用量は、管理者だけが見られます(自分の分は、設定の画面の「アカウント」で見られます)"))
				return
			}
		} else if !api.IsLocal(r) {
			log.Warnf("サーバーの PC の外から使用量を呼んだので断りました: %s %s(%s)", r.Method, r.URL.Path, api.RemoteHost(r))
			writeAdminError(w, http.StatusForbidden, errors.New("全員の使用量は、aibow-server を動かしている PC からだけ見られます"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// myUsageHandler は、自分の使用量の口(GET /_aibow/auth/usage)。ログインなしの形では local の分。
func myUsageHandler(st usageStore, login bool, now func() time.Time) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := aibow.LocalUser
		if login {
			u, ok := auth.UserFrom(r.Context())
			if !ok {
				writeAdminError(w, http.StatusUnauthorized, errNotLoggedIn)
				return
			}
			id = u.ID
		}
		t := now()
		var out usage.Mine
		for _, p := range []struct {
			total *usage.Total
			days  int
		}{{&out.Today, 1}, {&out.Week, 7}, {&out.Month, 30}} {
			v, err := st.UsageOfUser(r.Context(), id.String(), usage.Since(t, p.days))
			if err != nil {
				writeAdminError(w, http.StatusInternalServerError, err)
				return
			}
			*p.total = v
		}
		writeAdminJSON(w, out)
	})
}

// usageSince は、days(今日を含めた日数)から範囲の始まりを決める。読めなければ 400 を返して false。
func usageSince(w http.ResponseWriter, r *http.Request, now time.Time) (time.Time, bool) {
	days := 7
	if v := r.URL.Query().Get("days"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 366 {
			writeAdminError(w, http.StatusBadRequest, fmt.Errorf("days は 1〜366 の日数にしてください: %q", v))
			return time.Time{}, false
		}
		days = n
	}
	return usage.Since(now, days), true
}
