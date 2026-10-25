package auth

// http.go: ログインの HTTP の口と、cookie からログインした人を決める係。
//
//	POST /aibow/auth/login   本文 {"name", "password"}。合っていれば cookie を渡して {"name"}、違えば 401。続けて間違えると 429(limiter.go)
//	POST /aibow/auth/logout  セッションを消して、cookie を消す
//	GET  /aibow/auth/me      {"login": ログインが要るか, "name": ログインしている人の名前(していなければ空)}。画面が最初に聞く
//	POST /aibow/auth/token   本文 {"name", "password", "device"}。アプリのログイン(0019)。合っていればアプリ用のトークン(AppLogin)。間違いの数え方はログインと同じ
//	POST /aibow/auth/token/revoke  Authorization: Bearer <トークン> のトークンを消す(アプリのログアウト)
//	POST /aibow/auth/link/check    本文 {"token"}。パスワードを決めるリンクの持ち主の名前(0022)。使えなければ 400
//	POST /aibow/auth/link          本文 {"token", "password"}。リンクでパスワードを決め、そのままログインする(cookie を渡す)
//	POST /aibow/auth/password      本文 {"current", "password"}。ログインしている本人がパスワードを変える(新しい cookie を渡す)
//
// cookie は次の性質を付ける(どれも、ほかの人に合言葉を使われないため):
//
//	HttpOnly        … 画面の JavaScript から読めない(画面に紛れ込んだスクリプトに盗まれない)
//	SameSite=Strict … ほかのサイトから aibow-server へのリクエストには付かない(ほかのサイトに、ログインした人として勝手に送信させない)
//	Secure          … HTTPS で待ち受けているときだけ付ける(HTTP では送らない。HTTPS は 10-2)

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/secondarykey/aibow/log"
)

// CookieName はセッションの cookie の名前。
const CookieName = "aibow_session"

type userKey struct{}

// UserFrom は、Middleware がリクエストの ctx に入れたログインした人。ログインしていなければ ok=false。
func UserFrom(ctx context.Context) (User, bool) {
	u, ok := ctx.Value(userKey{}).(User)
	return u, ok
}

// IsAdmin は、リクエストの ctx に入っている利用者(ログインした人か API キーの持ち主)が管理者か(0018 の 11-0)。
func IsAdmin(r *http.Request) bool {
	u, ok := UserFrom(r.Context())
	return ok && u.Admin
}

// Middleware は、cookie の合言葉からログインした人を決めて、リクエストの ctx に入れる(UserFrom で読む)。
// ログインしていなくても断らずに次へ渡す(断るかは、受け取った側が決める。ログインの画面のファイルは誰にでも配るため)。
// 期限の切れた cookie は消す。期限を延ばしたら、cookie の期限も延ばす。
func (s *Sessions) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(CookieName); err == nil {
			u, extended, err := s.User(r.Context(), c.Value)
			switch {
			case err == nil:
				if extended {
					setCookie(w, r, c.Value)
				}
				r = r.WithContext(context.WithValue(r.Context(), userKey{}, u))
			case errors.Is(err, ErrNoSession):
				clearCookie(w, r)
			default:
				log.Errorf("ログインを確かめられません: %v", err)
				writeError(w, http.StatusInternalServerError, "ログインを確かめられません: "+err.Error())
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// Me は GET /aibow/auth/me の返事。
type Me struct {
	Login bool   `json:"login"` // ログインが要るか(要らない形なら、画面はログインもログアウトも出さない)
	Name  string `json:"name"`  // ログインしている人の名前。していなければ空
	// Admin は、ログインしている人が管理者か(0018 の 11-0。画面が、管理者だけのものを出すかを決める)
	Admin bool `json:"admin,omitempty"`
	// Setup は、ログインが要るのに、ログインできる管理者がまだいないか(初めての起動など。0020 の決定事項 5)。
	// ログインの画面が、サーバーの PC で管理者を作るよう出す。いるかどうかだけで、誰がいるかは出さない
	Setup bool `json:"setup,omitempty"`
}

// Handler は /aibow/auth/ の下の口を返す。required はログインが要るか(aibow-server の設定)。
// Middleware の内側に置く(me と logout は、ログインしている人を使う)。
func Handler(s *Sessions, required bool) http.Handler {
	lim := newLimiter(func() time.Time { return s.now() })
	mux := http.NewServeMux()
	mux.HandleFunc("GET /aibow/auth/me", func(w http.ResponseWriter, r *http.Request) {
		me := Me{Login: required}
		if u, ok := UserFrom(r.Context()); ok && required {
			me.Name, me.Admin = u.Name, u.Admin
		} else if required {
			has, err := HasAdmin(r.Context(), s.store)
			if err != nil {
				log.Errorf("管理者がいるかを確かめられません: %v", err)
			}
			me.Setup = err == nil && !has
		}
		writeJSON(w, http.StatusOK, me)
	})
	mux.HandleFunc("POST /aibow/auth/login", func(w http.ResponseWriter, r *http.Request) {
		if !required {
			writeError(w, http.StatusBadRequest, "この aibow-server はログインなしで使う設定です")
			return
		}
		var req struct {
			Name     string `json:"name"`
			Password string `json:"password"`
		}
		if !readJSON(w, r, &req) {
			return
		}
		addr := remote(r)
		if left := lim.wait(addr); left > 0 {
			log.Warnf("ログインを続けて間違えたので、受け付けません: 名前 %q(%s。あと %s)", req.Name, addr, left.Round(time.Second))
			writeError(w, http.StatusTooManyRequests, fmt.Sprintf("ログインを続けて間違えたので、しばらく受け付けません。%d 分ほど待ってから、もう一度ログインしてください", int(left.Minutes())+1))
			return
		}
		token, u, err := s.Login(r.Context(), req.Name, req.Password)
		if errors.Is(err, ErrLogin) {
			lim.fail(addr)
			log.Warnf("ログインできませんでした: 名前 %q(%s)", req.Name, addr)
			writeError(w, http.StatusUnauthorized, err.Error())
			return
		}
		if err != nil {
			log.Errorf("ログインの途中で失敗しました: %v", err)
			writeError(w, http.StatusInternalServerError, "ログインの途中で失敗しました: "+err.Error())
			return
		}
		lim.ok(addr)
		setCookie(w, r, token)
		log.Noticef("ログインしました: %s(%s)", u.Name, addr)
		writeJSON(w, http.StatusOK, Me{Login: true, Name: u.Name, Admin: u.Admin})
	})
	mux.HandleFunc("POST /aibow/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(CookieName); err == nil {
			if err := s.Logout(r.Context(), c.Value); err != nil {
				log.Errorf("ログアウトできません: %v", err)
				writeError(w, http.StatusInternalServerError, "ログアウトできません: "+err.Error())
				return
			}
		}
		clearCookie(w, r)
		if u, ok := UserFrom(r.Context()); ok {
			log.Noticef("ログアウトしました: %s", u.Name)
		}
		writeJSON(w, http.StatusOK, Me{Login: required})
	})
	// アプリのログイン(0019)。名前とパスワードを確かめて、アプリ用のトークンを返す。間違いの回数はブラウザのログインと一緒に数える
	mux.HandleFunc("POST /aibow/auth/token", func(w http.ResponseWriter, r *http.Request) {
		if !required {
			writeError(w, http.StatusBadRequest, "この aibow-server はログインなしで使う設定です(つなぐのに API キーもトークンも要りません)")
			return
		}
		var req struct {
			Name     string `json:"name"`
			Password string `json:"password"`
			Device   string `json:"device"`
		}
		if !readJSON(w, r, &req) {
			return
		}
		addr := remote(r)
		if left := lim.wait(addr); left > 0 {
			log.Warnf("ログインを続けて間違えたので、アプリのログインを受け付けません: 名前 %q(%s。あと %s)", req.Name, addr, left.Round(time.Second))
			writeError(w, http.StatusTooManyRequests, fmt.Sprintf("ログインを続けて間違えたので、しばらく受け付けません。%d 分ほど待ってから、もう一度ログインしてください", int(left.Minutes())+1))
			return
		}
		login, err := s.IssueAppToken(r.Context(), req.Name, req.Password, req.Device)
		if errors.Is(err, ErrLogin) {
			lim.fail(addr)
			log.Warnf("アプリのログインができませんでした: 名前 %q(%s)", req.Name, addr)
			writeError(w, http.StatusUnauthorized, err.Error())
			return
		}
		if err != nil {
			log.Errorf("アプリのログインの途中で失敗しました: %v", err)
			writeError(w, http.StatusInternalServerError, "アプリのログインの途中で失敗しました: "+err.Error())
			return
		}
		lim.ok(addr)
		log.Noticef("アプリでログインしました: %s(PC: %s・%s。期限 %s)", login.Name, strings.TrimSpace(req.Device), addr, login.ExpiresAt.Local().Format("2006-01-02 15:04"))
		writeJSON(w, http.StatusOK, login)
	})
	// アプリのログアウト。Authorization: Bearer のトークンを消す(期限が切れていても、無くても 200。消えていればよいので)
	mux.HandleFunc("POST /aibow/auth/token/revoke", func(w http.ResponseWriter, r *http.Request) {
		token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token = strings.TrimSpace(token); !IsAppToken(token) {
			writeError(w, http.StatusBadRequest, "消すアプリ用のトークンを Authorization: Bearer <トークン> で送ってください")
			return
		}
		if err := s.RevokeAppToken(r.Context(), token); err != nil {
			log.Errorf("アプリのログアウトができません: %v", err)
			writeError(w, http.StatusInternalServerError, "アプリのログアウトができません: "+err.Error())
			return
		}
		log.Noticef("アプリでログアウトしました(%s)", remote(r))
		writeJSON(w, http.StatusOK, map[string]any{})
	})
	// パスワードを決めるリンク(0022)。合言葉は画面が URL の # の後ろから読んで、本文で送ってくる
	mux.HandleFunc("POST /aibow/auth/link/check", func(w http.ResponseWriter, r *http.Request) {
		if !required {
			writeError(w, http.StatusBadRequest, "この aibow-server はログインなしで使う設定なので、パスワードは使いません")
			return
		}
		var req struct {
			Token string `json:"token"`
		}
		if !readJSON(w, r, &req) {
			return
		}
		u, err := s.LinkUser(r.Context(), req.Token)
		if linkError(w, err) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"name": u.Name})
	})
	mux.HandleFunc("POST /aibow/auth/link", func(w http.ResponseWriter, r *http.Request) {
		if !required {
			writeError(w, http.StatusBadRequest, "この aibow-server はログインなしで使う設定なので、パスワードは使いません")
			return
		}
		var req struct {
			Token    string `json:"token"`
			Password string `json:"password"`
		}
		if !readJSON(w, r, &req) {
			return
		}
		session, u, err := s.UseLink(r.Context(), req.Token, req.Password)
		if linkError(w, err) {
			return
		}
		setCookie(w, r, session)
		log.Noticef("リンクでパスワードを決めました: %s(%s)", u.Name, remote(r))
		writeJSON(w, http.StatusOK, Me{Login: true, Name: u.Name, Admin: u.Admin})
	})
	mux.HandleFunc("POST /aibow/auth/password", func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFrom(r.Context())
		if !required || !ok {
			writeError(w, http.StatusUnauthorized, "ログインしてください")
			return
		}
		var req struct {
			Current  string `json:"current"`
			Password string `json:"password"`
		}
		if !readJSON(w, r, &req) {
			return
		}
		// 今のパスワードを当てようとするのを防ぐため、間違いはログインと一緒に数える
		addr := remote(r)
		if left := lim.wait(addr); left > 0 {
			writeError(w, http.StatusTooManyRequests, fmt.Sprintf("パスワードを続けて間違えたので、しばらく受け付けません。%d 分ほど待ってください", int(left.Minutes())+1))
			return
		}
		session, err := s.ChangePassword(r.Context(), u, req.Current, req.Password)
		if errors.Is(err, ErrWrongPassword) {
			lim.fail(addr)
			log.Warnf("パスワードを変えられませんでした(今のパスワードが違う): %s(%s)", u.Name, addr)
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if linkError(w, err) {
			return
		}
		lim.ok(addr)
		setCookie(w, r, session)
		log.Noticef("パスワードを変えました: %s(%s。ほかのログイン中の画面とアプリはログインし直しになります)", u.Name, addr)
		writeJSON(w, http.StatusOK, Me{Login: true, Name: u.Name, Admin: u.Admin})
	})
	mux.HandleFunc("/aibow/auth/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "このパスはありません: "+r.Method+" "+r.URL.Path)
	})
	return mux
}

// linkError は、リンクのエラーを返事に書く(書いたら true)。リンクが使えない・パスワードの決まりに合わないは 400、それ以外は 500。
func linkError(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, ErrBadLink), errors.Is(err, ErrLinkExpired):
		writeError(w, http.StatusBadRequest, err.Error())
	case isPasswordRule(err):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		log.Errorf("パスワードを決められません: %v", err)
		writeError(w, http.StatusInternalServerError, "パスワードを決められません: "+err.Error())
	}
	return true
}

// readJSON はログインの本文を読む。読めなければエラーを返事に書いて false。
// 本文は JSON だけ受け付ける。ほかのサイトのフォームからは JSON を送れない(送るには、ブラウザがこのサーバーに先に聞く)ので、
// 知らないうちにほかの人の名前でログインさせられることを防げる
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); ct != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "本文は JSON で送ってください(Content-Type: application/json)")
		return false
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "本文が読めません: "+err.Error())
		return false
	}
	return true
}

func setCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: token, Path: "/",
		MaxAge:   int(SessionTTL.Seconds()), // ブラウザを閉じても残す(期限はサーバーが決める。ここは同じ長さにそろえるだけ)
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil,
	})
}

func clearCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil,
	})
}

// remote は、ログに書く相手のアドレス(ポートは外す)。
func remote(r *http.Request) string {
	if i := strings.LastIndex(r.RemoteAddr, ":"); i > 0 {
		return r.RemoteAddr[:i]
	}
	return r.RemoteAddr
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// writeError は、aibow-server のほかの口と同じ形のエラー({"error": {"message", "type"}})を返す。
func writeError(w http.ResponseWriter, status int, msg string) {
	typ := "server_error"
	if status < 500 {
		typ = "invalid_request_error"
	}
	writeJSON(w, status, map[string]any{"error": map[string]string{"message": msg, "type": typ}})
}
