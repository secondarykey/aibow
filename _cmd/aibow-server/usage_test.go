package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/secondarykey/aibow"
	"github.com/secondarykey/aibow/api"
	"github.com/secondarykey/aibow/auth"
	"github.com/secondarykey/aibow/config"
	"github.com/secondarykey/aibow/engine"
	"github.com/secondarykey/aibow/format"
	"github.com/secondarykey/aibow/log"
	"github.com/secondarykey/aibow/store"
	"github.com/secondarykey/aibow/usage"
)

// usageGen は使用量のテストの偽物の生成。決まった記録を返す。
type usageGen struct {
	st  engine.GenerateStats
	err error
}

func (g *usageGen) Generate(ctx context.Context, p format.Prompt, opt engine.GenerateOptions,
	onPrompt func(string), onThinking, onContent func(string)) (format.Output, engine.GenerateStats, error) {
	if g.err != nil {
		return format.Output{}, g.st, g.err
	}
	onContent("はい")
	return format.Output{Content: "はい"}, g.st, nil
}
func (g *usageGen) CountTokens(format.Prompt) (int, error)      { return 10, nil }
func (g *usageGen) ApplyTemplate(format.Prompt) (string, error) { return "", nil }
func (g *usageGen) Info() *engine.ModelInfo {
	return &engine.ModelInfo{ID: "file:qwen.gguf", NCtx: 4096}
}
func (g *usageGen) Loading() string { return "" }

// failingLog は書けない記録先(ディスクがいっぱいなど)。
type failingLog struct{ generationLog }

func (failingLog) AddGeneration(context.Context, usage.Record) error { return errors.New("disk full") }

// captureLog は aibow のログを buf に集める(Warn を確かめるため)。
func captureLog(t *testing.T) *syncBuffer {
	buf := &syncBuffer{}
	log.SetLogger(slog.New(slog.NewTextHandler(buf, nil)))
	t.Cleanup(func() { log.SetLogger(nil) })
	return buf
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// 記録付きの engine(0033 の段1):道ごとに1行・だれのものか・書けなくても生成は止まらない
func TestRecorder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	var env config.Env
	if _, err := createUser(ctx, st, initialSettings(&env, 0), "alice", ""); err != nil {
		t.Fatal(err)
	}
	alice, err := st.UserByName(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	key, err := renewKey(ctx, st, "alice", "")
	if err != nil {
		t.Fatal(err)
	}

	gen := &usageGen{st: engine.GenerateStats{PromptTokens: 50, ReusedTokens: 20, PromptSec: 0.5, GenTokens: 8, GenSec: 1.5, StopReason: "eos"}}
	rec := newRecorder(gen, st)
	day := time.Date(2026, 11, 1, 0, 0, 0, 0, time.Local)
	rec.now = func() time.Time { return day.Add(time.Hour) }
	srv := api.New(rec, nil, 4096, api.Defaults{MaxTokens: 100})
	keys := auth.NewKeys(st)

	post := func(h http.Handler, auth, body string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		if auth != "" {
			req.Header.Set("Authorization", "Bearer "+auth)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
	}
	msgs := `"messages":[{"role":"user","content":"こんにちは"}]`
	conv := uuid.NewV7()

	// OpenAI 互換の口:Cline の形(aibow 欄なし)と、アプリの形(aibow 欄で要約と会話を知らせる)。だれかは API キーの持ち主
	post(keys.RequireKey(srv.Handler()), key, `{`+msgs+`}`)
	post(keys.RequireKey(srv.Handler()), key, `{`+msgs+`,"aibow":{"purpose":"summary","conversation":"`+conv.String()+`"}}`)
	// キーの要らない形は local の分
	post(asLocal(srv.Handler()), "", `{`+msgs+`}`)
	// ブラウザの会話:画面用の口が道を、会話の仕組みが用途と会話を、席を待った時間を付ける
	bctx := usage.WithWait(usage.WithPurpose(usage.WithRoute(aibow.WithUser(ctx, alice.ID), usage.RouteBrowser), usage.PurposeReply, conv), 3*time.Second)
	for range 2 { // ツールのループの2回目には、送信の待ち時間を付けない
		if _, _, err := rec.Generate(bctx, format.Prompt{}, engine.GenerateOptions{}, func(string) {}, func(string) {}, func(string) {}); err != nil {
			t.Fatal(err)
		}
	}

	rows := readLog(t, path)
	type want struct {
		user, name, route, purpose string
		conv                       uuid.UUID
		wait                       *float64
	}
	three := 3.0
	wants := []want{
		{alice.ID.String(), "alice", usage.RouteAPI, usage.PurposeReply, uuid.UUID{}, ptr(0)},
		{alice.ID.String(), "alice", usage.RouteApp, usage.PurposeSummary, conv, ptr(0)},
		{aibow.LocalUser.String(), "local", usage.RouteAPI, usage.PurposeReply, uuid.UUID{}, ptr(0)},
		{alice.ID.String(), "alice", usage.RouteBrowser, usage.PurposeReply, conv, &three},
		{alice.ID.String(), "alice", usage.RouteBrowser, usage.PurposeReply, conv, nil},
	}
	if len(rows) != len(wants) {
		t.Fatalf("%d 行(期待: %d): %+v", len(rows), len(wants), rows)
	}
	for i, w := range wants {
		r := rows[i]
		if r.user != w.user || r.name != w.name || r.route != w.route || r.purpose != w.purpose || r.conv != idOr(w.conv) || r.model != "file:qwen.gguf" ||
			r.gen != 8 || r.reused != 20 || r.stop != "eos" || !sameWait(r.wait, w.wait) {
			t.Errorf("%d 行目: %+v(期待: %+v)", i, r, w)
		}
	}

	t.Run("書けなくても止まらない", func(t *testing.T) {
		buf := captureLog(t)
		r := newRecorder(gen, failingLog{st})
		out, _, err := r.Generate(bctx, format.Prompt{}, engine.GenerateOptions{}, func(string) {}, func(string) {}, func(string) {})
		if err != nil || out.Content != "はい" {
			t.Fatalf("生成が止まった: %v %+v", err, out)
		}
		if !strings.Contains(buf.String(), "level=WARN") || !strings.Contains(buf.String(), "使用量を記録できませんでした") {
			t.Errorf("Warn が出ていない: %s", buf)
		}
	})

	t.Run("だれでもない・失敗した生成", func(t *testing.T) {
		buf := captureLog(t)
		before := len(readLog(t, path))
		// 呼んだ人が ctx に無い:利用者を空にして記録し、Warn を出す
		if _, _, err := rec.Generate(usage.WithRoute(ctx, usage.RouteAPI), format.Prompt{}, engine.GenerateOptions{}, func(string) {}, func(string) {}, func(string) {}); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(buf.String(), "生成を呼んだ人が分かりません") {
			t.Errorf("Warn が出ていない: %s", buf)
		}
		// 生成まで行かなかった失敗(秒数 0)は書かず、途中で失敗した生成は error として書く
		failed := newRecorder(&usageGen{err: engine.ErrBusy}, st)
		failed.Generate(bctx, format.Prompt{}, engine.GenerateOptions{}, nil, nil, nil)
		failed = newRecorder(&usageGen{err: errors.New("decode failed"), st: engine.GenerateStats{PromptTokens: 9, PromptSec: 0.2}}, st)
		failed.Generate(bctx, format.Prompt{}, engine.GenerateOptions{}, nil, nil, nil)
		rows := readLog(t, path)[before:]
		if len(rows) != 2 || rows[0].user != "" || rows[0].name != "" || rows[1].stop != "error" || rows[1].user != alice.ID.String() {
			t.Errorf("%+v", rows)
		}
	})
}

func ptr(f float64) *float64 { return &f }

func idOr(id uuid.UUID) string {
	if id == (uuid.UUID{}) {
		return ""
	}
	return id.String()
}

// sameWait は、順番待ちの秒数が同じか(テストの中の待ちはほぼ 0 秒なので、0.5 秒より短ければ 0 とみなす)。
func sameWait(got, want *float64) bool {
	if got == nil || want == nil {
		return got == nil && want == nil
	}
	return *got-*want < 0.5 && *want-*got < 0.5
}

type logRow struct {
	user, name, route, purpose, conv, model, stop string
	gen, reused                                   int
	wait                                          *float64
}

// readLog は記録を書いた順に読む(ID は uuid の v7 なので、並びが書いた順)。
// 1行ずつの中身を読む口は store に無い(画面は集計だけを見る)ので、同じファイルを別に開く(ドライバーは store が登録したもの)。
func readLog(t *testing.T, path string) []logRow {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT user_id, user_name, route, purpose, conversation_id, model, stop_reason, gen_tokens, reused_tokens, wait_sec
		FROM generation_log ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []logRow
	for rows.Next() {
		var r logRow
		if err := rows.Scan(&r.user, &r.name, &r.route, &r.purpose, &r.conv, &r.model, &r.stop, &r.gen, &r.reused, &r.wait); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

// 使用量を見る口(0033 の段2・段3):全員の分は管理者だけ、自分の分は本人。ほかの人の分は口で断る
func TestUsageHandlers(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	var env config.Env
	initial := initialSettings(&env, 0)
	sessions := auth.NewSessions(st)
	users := map[string]auth.User{}
	tokens := map[string]string{}
	for _, name := range []string{"alice", "bob"} {
		l, err := createUser(ctx, st, initial, name, "")
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := sessions.UseLink(ctx, l.Token, name+"-password"); err != nil {
			t.Fatal(err)
		}
		if tokens[name], _, err = sessions.Login(ctx, name, name+"-password"); err != nil {
			t.Fatal(err)
		}
		users[name], _ = st.UserByName(ctx, name)
	}
	if err := setAdmin(ctx, st, "alice", true); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for i, name := range []string{"alice", "bob", "bob"} {
		u := users[name]
		at := now.Add(-time.Duration(i) * time.Minute)
		if i == 2 {
			at = now.Add(-3 * 24 * time.Hour) // bob の2回目は今日の外(7日の中)
		}
		if err := st.AddGeneration(ctx, usage.Record{ID: uuid.NewV7(), At: at, User: u.ID, UserName: u.Name, Route: usage.RouteBrowser,
			Purpose: usage.PurposeReply, Model: "file:qwen.gguf", GenTokens: 10, GenSec: 2, StopReason: "eos"}); err != nil {
			t.Fatal(err)
		}
	}
	clock := func() time.Time { return now }
	mux := http.NewServeMux()
	mux.Handle("/_aibow/admin/usage", adminUsageHandler(st, true, clock))
	mux.Handle("/_aibow/admin/usage/", adminUsageHandler(st, true, clock))
	mux.Handle("GET /_aibow/auth/usage", myUsageHandler(st, true, clock))
	srv := httptest.NewServer(sessions.Middleware(mux))
	defer srv.Close()

	get := func(name, path string, v any) int {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		if name != "" {
			req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: tokens[name]})
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if v != nil && res.StatusCode == http.StatusOK {
			if err := json.NewDecoder(res.Body).Decode(v); err != nil {
				t.Fatal(err)
			}
		}
		return res.StatusCode
	}

	var all []usage.Total
	if code := get("alice", "/_aibow/admin/usage?days=7", &all); code != http.StatusOK || len(all) != 2 || all[0].Name != "bob" || all[0].Count != 2 || all[0].GPUSec != 4 {
		t.Errorf("管理者が見る全員の分: %d %+v", code, all)
	}
	if code := get("alice", "/_aibow/admin/usage?days=1", &all); code != http.StatusOK || len(all) != 2 || all[0].Count != 1 {
		t.Errorf("今日の分: %d %+v", code, all)
	}
	var d usageDetail
	if code := get("alice", "/_aibow/admin/usage/user?days=7&id="+users["bob"].ID.String(), &d); code != http.StatusOK ||
		len(d.Models) != 1 || d.Models[0].Count != 2 || len(d.Days) != 2 {
		t.Errorf("bob の内訳: %d %+v", code, d)
	}
	if code := get("alice", "/_aibow/admin/usage?days=0", nil); code != http.StatusBadRequest {
		t.Errorf("days=0: %d", code)
	}
	// 管理者でない人・ログインしていない人には、全員の分を見せない
	if code := get("bob", "/_aibow/admin/usage", nil); code != http.StatusForbidden {
		t.Errorf("管理者でない人: %d", code)
	}
	if code := get("bob", "/_aibow/admin/usage/user?id="+users["alice"].ID.String(), nil); code != http.StatusForbidden {
		t.Errorf("管理者でない人がほかの人の内訳: %d", code)
	}
	if code := get("", "/_aibow/admin/usage", nil); code != http.StatusUnauthorized {
		t.Errorf("ログインしていない人: %d", code)
	}
	// 自分の分は本人のものだけ
	var mine usage.Mine
	if code := get("bob", "/_aibow/auth/usage", &mine); code != http.StatusOK || mine.Today.Count != 1 || mine.Week.Count != 2 || mine.Month.Count != 2 || mine.Week.Key != users["bob"].ID.String() {
		t.Errorf("bob の自分の分: %d %+v", code, mine)
	}
	if code := get("", "/_aibow/auth/usage", nil); code != http.StatusUnauthorized {
		t.Errorf("ログインしていない人の自分の分: %d", code)
	}

	// ログインなしの形:全員の分はサーバーの PC の上からだけ。自分の分は local
	local := adminUsageHandler(st, false, clock)
	for addr, want := range map[string]int{"127.0.0.1:5000": http.StatusOK, "192.168.0.10:5000": http.StatusForbidden} {
		req := httptest.NewRequest(http.MethodGet, "/_aibow/admin/usage", nil)
		req.RemoteAddr = addr
		w := httptest.NewRecorder()
		local.ServeHTTP(w, req)
		if w.Code != want {
			t.Errorf("ログインなしの形で %s から: %d(期待: %d)", addr, w.Code, want)
		}
	}
}
