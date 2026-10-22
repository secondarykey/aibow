package remote

// handler.go: aibow-server の保存の口(POST /aibow/store/{メソッド})。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"uuid"

	"github.com/secondarykey/aibow"
	"github.com/secondarykey/aibow/log"
)

// Who は、保存の口を呼んだ利用者を決める係(aibow-server が渡す。API キーの持ち主か、ログインなしの形の利用者)。
type Who func(r *http.Request) (uuid.UUID, error)

// Handler は保存の口(/aibow/store/ の下)を返す。呼べるのは Backend のメソッドだけ。
func Handler(st Backend, who Who) http.Handler {
	v := reflect.ValueOf(&appAsUser{st})
	methods := map[string]reflect.Value{}
	t := reflect.TypeFor[Backend]()
	for i := range t.NumMethod() {
		name := t.Method(i).Name
		methods[name] = v.MethodByName(name)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /aibow/store/{method}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("method")
		m, ok := methods[name]
		if !ok {
			writeError(w, http.StatusNotFound, "", "保存の口に無いメソッドです: "+name)
			return
		}
		user, err := who(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "", err.Error())
			return
		}
		call(w, r.WithContext(aibow.WithUser(r.Context(), user)), name, m)
	})
	mux.HandleFunc("/aibow/store/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "", "このパスはありません: "+r.Method+" "+r.URL.Path+"(あるのは POST /aibow/store/{メソッド} です)")
	})
	return mux
}

var typeError = reflect.TypeFor[error]()

// call は、本文の JSON の配列を引数にしてメソッドを呼び、error 以外の戻り値を JSON の配列で返す(ui/web.go の call と同じ考え方)。
// 最初の引数(context.Context)には、利用者を入れたリクエストの ctx を渡す。
func call(w http.ResponseWriter, r *http.Request, name string, m reflect.Value) {
	var raw []json.RawMessage
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<20)).Decode(&raw); err != nil {
		writeError(w, http.StatusBadRequest, "", name+" の引数が読めません(本文は JSON の配列で書いてください): "+err.Error())
		return
	}
	t := m.Type()
	if len(raw) != t.NumIn()-1 {
		writeError(w, http.StatusBadRequest, "", fmt.Sprintf("%s の引数は %d 個です(%d 個ありました)", name, t.NumIn()-1, len(raw)))
		return
	}
	args := []reflect.Value{reflect.ValueOf(r.Context())}
	for i, a := range raw {
		p := reflect.New(t.In(i + 1))
		if err := json.Unmarshal(a, p.Interface()); err != nil {
			writeError(w, http.StatusBadRequest, "", fmt.Sprintf("%s の %d 番目の引数が読めません: %v", name, i+1, err))
			return
		}
		args = append(args, p.Elem())
	}
	results := []any{}
	for _, o := range m.Call(args) {
		if o.Type() == typeError {
			if !o.IsNil() {
				err := o.Interface().(error)
				code, status := codeOf(err)
				if status == http.StatusInternalServerError {
					log.Errorf("保存の口 %s: %v", name, err)
				}
				writeError(w, status, code, err.Error())
				return
			}
			continue
		}
		results = append(results, o.Interface())
	}
	writeJSON(w, http.StatusOK, results)
}

// appAsUser は、アプリが書く「アプリ全体の設定」を、その人の設定として server.db に置く(0017 の 10-3。ユーザーと決めた)。
// アプリの「最後に使ったモデル」などは、その PC のアプリのもの。サーバー全体の設定に書くと、ほかの人のアプリやブラウザの値を書き換えてしまう。
// 人ごとの設定と名前が重ならないよう、頭に app. を付ける。
type appAsUser struct{ Backend }

const appPrefix = "app."

func (a *appAsUser) AppSetting(ctx context.Context, key string) (string, bool, error) {
	return a.Backend.UserSetting(ctx, appPrefix+key)
}

func (a *appAsUser) SetAppSetting(ctx context.Context, key, value string) error {
	return a.Backend.SetUserSetting(ctx, appPrefix+key, value)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false) // 会話にはコードが多いので、< > & をそのまま送る(api/client と同じ)
	enc.Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	typ := "server_error"
	if status < 500 {
		typ = "invalid_request_error"
	}
	writeJSON(w, status, map[string]any{"error": map[string]string{"message": msg, "type": typ, "code": code}})
}
