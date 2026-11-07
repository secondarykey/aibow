package main

// admin.go: 管理者の画面の口(利用者の管理。0018 の決定事項 7。11-0)。
//
//	GET    /_aibow/admin/users                  利用者の一覧([]adminUser)
//	POST   /_aibow/admin/users                  本文 {"name"}。作って {"name", "token", "expiresAt"} を返す(招待のリンクの合言葉。0022)
//	POST   /_aibow/admin/users/{name}/password  今のパスワードを使えなくし、{"name", "token", "expiresAt"} を返す(再設定のリンク。その人のログインは切れる)。自分には使えない
//	POST   /_aibow/admin/users/{name}/key       API キーを作り直して {"name", "key"} を返す(前のキーは使えなくなる)
//	POST   /_aibow/admin/users/{name}/display   本文 {"display_name"}。表示名を変える(空なら無し。0031)
//	POST   /_aibow/admin/users/{name}/email     本文 {"email"}。メールアドレスを変える(空なら無し。重なれば 409。0031)
//	DELETE /_aibow/admin/users/{name}           利用者と、その人の会話・プロファイル・設定を消す
//
// 呼べるのは、ブラウザでログインした管理者だけ(cookie のセッション。auth.Middleware がリクエストの ctx に入れた人)。
// ログインが要らない形(127.0.0.1 だけ)では、利用者の管理はコマンドで行う(画面からはだれも呼べない)。
// 処理はコマンド(users.go)と同じものを使う。リンクの合言葉と API キーは返事で一度だけ渡し、ログには残さない(コマンドと同じ考え方)。
// リンクの URL は画面が作る(管理者が開いている画面のアドレス + /#reset=<合言葉>)。
// 管理者の印を付ける / 外すのはコマンドだけ(0018 の決定事項 6。画面からは決めない)。

import (
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"time"

	"github.com/secondarykey/aibow"
	"github.com/secondarykey/aibow/auth"
	"github.com/secondarykey/aibow/log"
	"github.com/secondarykey/aibow/store"
)

// adminUser は一覧の1人(パスワードと API キーそのものは渡さない。あるかだけ)。
type adminUser struct {
	Name      string    `json:"name"`
	Admin     bool      `json:"admin"`
	Password  bool      `json:"password"` // パスワードがあるか(無ければログインできない)
	Key       bool      `json:"key"`      // API キーがあるか
	Local     bool      `json:"local"`    // ログインなしの形の利用者(消せない)
	CreatedAt time.Time `json:"created_at"`
	// DisplayName は表示名(0031。空ならログインの名前を出す)
	DisplayName string `json:"display_name,omitempty"`
	// Email はメールアドレス(0031。管理者と本人にだけ見せる)
	Email string `json:"email,omitempty"`
	// LastUsedAt は最後に使った時刻(1日に1回だけ書く。一度も使っていなければ省く。0031 の決定事項 21・22)
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	// Conversations はその人の会話の数(利用者のタブの「会話」の列と、消すときの確かめ。0031 の決定事項 14・15)
	Conversations int `json:"conversations"`
	// LinkExpiresAt は、まだ使っていないパスワードを決めるリンクの期限(無ければ省く)。招待中の人のリンクが切れていたら、画面が出し直しを出す
	LinkExpiresAt *time.Time `json:"link_expires_at,omitempty"`
}

// adminHandler は /_aibow/admin/ の下の口。initial は新しい人のプロファイル「既定」を作るときの値(コマンドと同じ)。
func adminHandler(st *store.SQLite, initial aibow.AppSettings) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /_aibow/admin/users", func(w http.ResponseWriter, r *http.Request) {
		out, err := adminUsers(r.Context(), st)
		if err != nil {
			writeAdminError(w, http.StatusInternalServerError, err)
			return
		}
		writeAdminJSON(w, out)
	})
	mux.HandleFunc("POST /_aibow/admin/users", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Name string `json:"name"`
		}
		if !readAdminJSON(w, r, &req) {
			return
		}
		if err := checkName(req.Name); err != nil {
			writeAdminError(w, http.StatusBadRequest, err)
			return
		}
		l, err := createUser(r.Context(), st, initial, req.Name, by(r))
		if err != nil {
			writeAdminError(w, statusOf(err), err)
			return
		}
		writeAdminJSON(w, map[string]any{"name": req.Name, "token": l.Token, "expiresAt": l.ExpiresAt})
	})
	mux.HandleFunc("POST /_aibow/admin/users/{name}/password", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		// 自分には使えない(使うと自分を締め出す。自分のパスワードは基本設定の「パスワードを変える」で変える。0022 の決定事項 6)
		if me, _ := auth.UserFrom(r.Context()); me.Name == name {
			writeAdminError(w, http.StatusBadRequest, errors.New("自分のパスワードは、設定の画面の「基本設定」の「パスワードを変える」で変えます"))
			return
		}
		l, err := resetPassword(r.Context(), st, name, by(r))
		if err != nil {
			writeAdminError(w, statusOf(err), err)
			return
		}
		writeAdminJSON(w, map[string]any{"name": name, "token": l.Token, "expiresAt": l.ExpiresAt})
	})
	mux.HandleFunc("POST /_aibow/admin/users/{name}/key", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		key, err := renewKey(r.Context(), st, name, by(r))
		if err != nil {
			writeAdminError(w, statusOf(err), err)
			return
		}
		writeAdminJSON(w, map[string]string{"name": name, "key": key})
	})
	mux.HandleFunc("POST /_aibow/admin/users/{name}/display", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			DisplayName string `json:"display_name"`
		}
		if !readAdminJSON(w, r, &req) {
			return
		}
		if _, err := auth.CheckDisplayName(req.DisplayName); err != nil {
			writeAdminError(w, http.StatusBadRequest, err)
			return
		}
		if err := setDisplayName(r.Context(), st, r.PathValue("name"), req.DisplayName, by(r)); err != nil {
			writeAdminError(w, statusOf(err), err)
			return
		}
		writeAdminJSON(w, struct{}{})
	})
	mux.HandleFunc("POST /_aibow/admin/users/{name}/email", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Email string `json:"email"`
		}
		if !readAdminJSON(w, r, &req) {
			return
		}
		if _, err := auth.CheckEmail(req.Email); err != nil {
			writeAdminError(w, http.StatusBadRequest, err)
			return
		}
		if err := setEmail(r.Context(), st, r.PathValue("name"), req.Email, by(r)); err != nil {
			writeAdminError(w, statusOf(err), err)
			return
		}
		writeAdminJSON(w, struct{}{})
	})
	mux.HandleFunc("DELETE /_aibow/admin/users/{name}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		// 自分は消せない(画面から自分を消すと、そのまま管理者が使えなくなるため。消すならほかの管理者かコマンドで)
		if me, _ := auth.UserFrom(r.Context()); me.Name == name {
			writeAdminError(w, http.StatusBadRequest, errors.New("自分は消せません"))
			return
		}
		if u, err := st.UserByName(r.Context(), name); err == nil && u.ID == aibow.LocalUser {
			writeAdminError(w, http.StatusBadRequest, errors.New(name+" はログインなしの形の利用者なので消せません(ログインなしで使うときの会話の持ち主です)"))
			return
		}
		if err := deleteUser(r.Context(), st, name, by(r)); err != nil {
			writeAdminError(w, statusOf(err), err)
			return
		}
		writeAdminJSON(w, struct{}{})
	})
	mux.HandleFunc("/_aibow/admin/", func(w http.ResponseWriter, r *http.Request) {
		writeAdminError(w, http.StatusNotFound, errors.New("このパスはありません: "+r.Method+" "+r.URL.Path))
	})
	return onlyAdmin(mux)
}

// adminUsers は利用者の一覧(作った順。並べ替えと絞り込みは画面がする。0031 の決定事項 13・14)。
func adminUsers(ctx context.Context, st *store.SQLite) ([]adminUser, error) {
	list, err := st.Users(ctx)
	if err != nil {
		return nil, err
	}
	counts, err := st.ConversationCounts(ctx)
	if err != nil {
		return nil, err
	}
	links, err := st.PasswordLinkExpiries(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]adminUser, 0, len(list))
	for _, u := range list {
		has, err := st.HasAPIKey(ctx, u.ID)
		if err != nil {
			return nil, err
		}
		a := adminUser{Name: u.Name, Admin: u.Admin, Password: u.Password != "", Key: has, Local: u.ID == aibow.LocalUser, CreatedAt: u.CreatedAt,
			DisplayName: u.DisplayName, Email: u.Email, Conversations: counts[u.ID]}
		if !u.LastUsedAt.IsZero() {
			a.LastUsedAt = &u.LastUsedAt
		}
		if t, ok := links[u.ID]; ok {
			a.LinkExpiresAt = &t
		}
		out = append(out, a)
	}
	return out, nil
}

// onlyAdmin は、ブラウザでログインした管理者の呼び出しだけを通す。ログインしていなければ 401、管理者でなければ 403。
func onlyAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := auth.UserFrom(r.Context())
		if !ok {
			writeAdminError(w, http.StatusUnauthorized, errNotLoggedIn)
			return
		}
		if !u.Admin {
			log.Warnf("管理者でない人が利用者の管理を呼んだので断りました: %s %s(%s)", r.Method, r.URL.Path, u.Name)
			writeAdminError(w, http.StatusForbidden, errors.New("利用者の管理は、管理者だけができます"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// by は、ログに足す「誰が」(画面から変えたことと、した管理者)。
func by(r *http.Request) string {
	u, _ := auth.UserFrom(r.Context())
	return "(管理者 " + u.Name + " が画面から)"
}

// readAdminJSON は本文の JSON を読む。JSON だけ受け付ける(ほかのサイトのフォームから送らせないため。ログインの口と同じ)。
func readAdminJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); ct != "application/json" {
		writeAdminError(w, http.StatusUnsupportedMediaType, errors.New("本文は JSON で送ってください(Content-Type: application/json)"))
		return false
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(v); err != nil {
		writeAdminError(w, http.StatusBadRequest, errors.New("本文が読めません: "+err.Error()))
		return false
	}
	return true
}

// statusOf は、利用者の処理のエラーの HTTP の状態。いない人は 404、名前の重なりは 409、ほかは 500
// (使えない名前・消せない人は、処理の前に 400 で断っている)。
func statusOf(err error) int {
	switch {
	case errors.Is(err, auth.ErrUserNotFound):
		return http.StatusNotFound
	case errors.Is(err, auth.ErrUserExists), errors.Is(err, auth.ErrEmailExists):
		return http.StatusConflict
	}
	return http.StatusInternalServerError
}

func writeAdminJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	// パスワードと API キーを含む返事は、ブラウザに残さない
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}

// writeAdminError は、aibow-server のほかの口と同じ形のエラー({"error": {"message", "type"}})を返す。
func writeAdminError(w http.ResponseWriter, status int, err error) {
	typ := "server_error"
	if status < 500 {
		typ = "invalid_request_error"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": err.Error(), "type": typ}})
}
