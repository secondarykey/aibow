package auth

// http.go: ログインの HTTP の口と、cookie からログインした人を決める係。
//
//	POST /aibow/auth/login   本文 {"name", "password"}。合っていれば cookie を渡して {"name"}、違えば 401
//	POST /aibow/auth/logout  セッションを消して、cookie を消す
//	GET  /aibow/auth/me      {"login": ログインが要るか, "name": ログインしている人の名前(していなければ空)}。画面が最初に聞く
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
	"mime"
	"net/http"
	"strings"

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
}

// Handler は /aibow/auth/ の下の口を返す。required はログインが要るか(aibow-server の設定)。
// Middleware の内側に置く(me と logout は、ログインしている人を使う)。
func Handler(s *Sessions, required bool) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /aibow/auth/me", func(w http.ResponseWriter, r *http.Request) {
		me := Me{Login: required}
		if u, ok := UserFrom(r.Context()); ok && required {
			me.Name = u.Name
		}
		writeJSON(w, http.StatusOK, me)
	})
	mux.HandleFunc("POST /aibow/auth/login", func(w http.ResponseWriter, r *http.Request) {
		if !required {
			writeError(w, http.StatusBadRequest, "この aibow-server はログインなしで使う設定です")
			return
		}
		// 本文は JSON だけ受け付ける。ほかのサイトのフォームからは JSON を送れない(送るには、ブラウザがこのサーバーに先に聞く)ので、
		// 知らないうちにほかの人の名前でログインさせられることを防げる
		if ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); ct != "application/json" {
			writeError(w, http.StatusUnsupportedMediaType, "本文は JSON で送ってください(Content-Type: application/json)")
			return
		}
		var req struct {
			Name     string `json:"name"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "本文が読めません: "+err.Error())
			return
		}
		token, u, err := s.Login(r.Context(), req.Name, req.Password)
		if errors.Is(err, ErrLogin) {
			log.Warnf("ログインできませんでした: 名前 %q(%s)", req.Name, remote(r))
			writeError(w, http.StatusUnauthorized, err.Error())
			return
		}
		if err != nil {
			log.Errorf("ログインの途中で失敗しました: %v", err)
			writeError(w, http.StatusInternalServerError, "ログインの途中で失敗しました: "+err.Error())
			return
		}
		setCookie(w, r, token)
		log.Noticef("ログインしました: %s(%s)", u.Name, remote(r))
		writeJSON(w, http.StatusOK, Me{Login: true, Name: u.Name})
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
	mux.HandleFunc("/aibow/auth/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "このパスはありません: "+r.Method+" "+r.URL.Path)
	})
	return mux
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
