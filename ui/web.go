package ui

// web.go: 画面用の HTTP の口(ステップ9。0016 の 3・10・14)。aibow-server が、ブラウザの画面のためにこの口を出す。
//
// デスクトップのアプリでは、Wails が画面と Go の間を仲立ちしている。ここでは同じことを自分で HTTP に書く。
// 何が起きているかを見比べられるように、形を Wails にそろえた:
//
//	                       デスクトップ(Wails)                       ブラウザ(この口)
//	メソッドの呼び出し    ChatService.Send(id, text)                  POST /aibow/ui/call/Send   本文 ["<id>", "<text>"]
//	                      (Wails がメソッドの名前と引数の配列を送る)   返事は戻り値の JSON。error なら 500 と {"error": {"message"}}
//	Go からの知らせ       Wails のイベント(chat・models)              GET /aibow/ui/events(SSE。event: chat / data: ChatEvent の JSON)
//	停止                  Promise の cancel() → ctx がキャンセル       fetch を止める(AbortController)→ つながりが切れて r.Context() が終わる
//
// 呼べるメソッドは、ブラウザで使うものだけ(browserMethods)。ツールと、サーバーの PC の事情を変えるものは出さない(0016 の 6・7)。
// 呼び出しは reflect で Service のメソッドを名前から探す(Wails のバインディングも、実行のときは同じことをしている)。
//
// SSE(Server-Sent Events)は、HTTP のつながりを開いたままにして、サーバーから「event: 名前\ndata: 中身\n\n」を送り続ける仕組み。
// ブラウザは EventSource で受け取り、切れたら自分でつなぎ直す。ステップ9では、つないでいるブラウザ全部に送る(利用者は1人。0016 の 14)。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sync"
	"time"

	"github.com/secondarykey/aibow"
	"github.com/secondarykey/aibow/log"
)

// browserMethods は、ブラウザから呼べる Service のメソッド。
//
// 出さないもの:GPU の層数・降ろす・置き場所(サーバーの PC の事情。0016 の 6)、ツールと MCP・確認の答え(ブラウザではツールを使わない。0016 の 7)。
// ServerHost も断るが、口そのものにも出さない(断るだけだと、ブラウザの画面がそれを呼ぶ作りになっても気づけないため)。
var browserMethods = []string{
	// モデルと設定
	"Models", "SwitchModel", "AppSettings", "TryLimits", "ModelSettings", "SetDisplay", "Version",
	// プロファイル
	"Profiles", "CreateProfile", "UpdateProfile", "SetProfileArchived", "DeleteProfile", "SetDefaultProfile",
	"ProfileDefaults", "SaveToProfile", "SaveAsProfile",
	// 会話
	"Conversations", "Conversation", "Usage", "NewConversation", "DeleteConversation", "RenameConversation",
	"SetExcluded", "UpdateConversationSettings",
	// 送信(生成が終わるまで返らない長い呼び出し。経過は SSE の chat で送る)
	"Send", "Regenerate", "Resend", "Compact", "StopSummary",
}

// Handler は、画面用の口(/aibow/ui/ の下)を返す。ev は New に Notify として渡したもの(SSE で送る)。
func Handler(s *Service, ev *Events) http.Handler {
	methods := map[string]reflect.Value{}
	v := reflect.ValueOf(s)
	for _, name := range browserMethods {
		m := v.MethodByName(name)
		if !m.IsValid() {
			panic("ui.Service に " + name + " がありません(browserMethods を直してください)")
		}
		methods[name] = m
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /aibow/ui/call/{method}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("method")
		m, ok := methods[name]
		if !ok {
			writeError(w, http.StatusNotFound, "ブラウザからは呼べないメソッドです: "+name)
			return
		}
		call(w, r, name, m)
	})
	mux.HandleFunc("GET /aibow/ui/events", ev.serve)
	mux.HandleFunc("/aibow/ui/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "このパスはありません: "+r.Method+" "+r.URL.Path+
			"(あるのは POST /aibow/ui/call/{メソッド}・GET /aibow/ui/events です)")
	})
	return mux
}

var (
	typeContext = reflect.TypeFor[context.Context]()
	typeError   = reflect.TypeFor[error]()
)

// call は、本文の JSON の配列を引数にしてメソッドを呼び、戻り値を JSON で返す。
// 最初の引数が context.Context なら、リクエストの context を渡す(つながりが切れたら止まる。Wails の停止と同じ意味)。
func call(w http.ResponseWriter, r *http.Request, name string, m reflect.Value) {
	var raw []json.RawMessage
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&raw); err != nil {
		writeError(w, http.StatusBadRequest, name+" の引数が読めません(本文は JSON の配列で書いてください): "+err.Error())
		return
	}
	t := m.Type()
	var args []reflect.Value
	first := 0
	if t.NumIn() > 0 && t.In(0) == typeContext {
		args = append(args, reflect.ValueOf(r.Context()))
		first = 1
	}
	if len(raw) != t.NumIn()-first {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("%s の引数は %d 個です(%d 個ありました)", name, t.NumIn()-first, len(raw)))
		return
	}
	for i, a := range raw {
		p := reflect.New(t.In(first + i))
		if err := json.Unmarshal(a, p.Interface()); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("%s の %d 番目の引数が読めません: %v", name, i+1, err))
			return
		}
		args = append(args, p.Elem())
	}

	out := m.Call(args)
	// 戻り値は () / (error) / (T) / (T, error) のどれか(Wails のバインディングと同じ約束)
	var result any
	for _, o := range out {
		if o.Type() == typeError {
			if !o.IsNil() {
				writeError(w, http.StatusInternalServerError, o.Interface().(error).Error())
				return
			}
			continue
		}
		result = o.Interface()
	}
	writeJSON(w, http.StatusOK, result)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// writeError は、aibow-server の OpenAI 互換の口と同じ形のエラー({"error": {"message", "type"}})を返す。
func writeError(w http.ResponseWriter, status int, msg string) {
	typ := "server_error"
	if status < 500 {
		typ = "invalid_request_error"
	}
	writeJSON(w, status, map[string]any{"error": map[string]string{"message": msg, "type": typ}})
}

// ---- SSE ------------------------------------------------------------------------------------

// Events は、Go から画面への知らせを、SSE でブラウザに送る係(Notifier)。
type Events struct {
	mu   sync.Mutex
	subs map[chan sseEvent]struct{}
}

type sseEvent struct {
	name string
	data []byte
}

// eventBuffer は、1つのブラウザに送り切れずに溜めておける知らせの数。
// 生成中は答えの断片が1トークンごとに来るので、少し多めにする。溢れたら、そのつながりを切る(ブラウザはつなぎ直す)。
// 黙って捨てると、答えの途中が抜けたまま気づけないため
const eventBuffer = 1024

// keepAlive は、何も送らないときに送るコメント行の間隔(途中の機器やブラウザに、つながりを切られないように)。
const keepAlive = 15 * time.Second

// NewEvents は、まだ誰もつないでいない係を作る。
func NewEvents() *Events {
	return &Events{subs: map[chan sseEvent]struct{}{}}
}

// Chat は chat イベントを、つないでいるブラウザ全部に送る。
func (e *Events) Chat(ev aibow.ChatEvent) { e.send(ChatEventName, ev) }

// ModelLoaded は models イベントを送る(中身は無い)。
func (e *Events) ModelLoaded() { e.send(ModelsEventName, nil) }

func (e *Events) send(name string, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		log.Errorf("画面への知らせ %s を JSON にできません: %v", name, err)
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for ch := range e.subs {
		select {
		case ch <- sseEvent{name: name, data: data}:
		default:
			log.Warnf("ブラウザが知らせを受け取り切れないので、つながりを切ります(溜まった知らせ %d 件。ブラウザはつなぎ直します)", eventBuffer)
			delete(e.subs, ch)
			close(ch)
		}
	}
}

func (e *Events) subscribe() chan sseEvent {
	ch := make(chan sseEvent, eventBuffer)
	e.mu.Lock()
	e.subs[ch] = struct{}{}
	n := len(e.subs)
	e.mu.Unlock()
	log.Infof("ブラウザが知らせ(SSE)につながりました(つないでいる数: %d)", n)
	return ch
}

func (e *Events) unsubscribe(ch chan sseEvent) {
	e.mu.Lock()
	if _, ok := e.subs[ch]; ok {
		delete(e.subs, ch)
		close(ch)
	}
	n := len(e.subs)
	e.mu.Unlock()
	log.Infof("ブラウザの知らせ(SSE)のつながりが切れました(つないでいる数: %d)", n)
}

// serve は GET /aibow/ui/events。つながりが切れるまで(ブラウザを閉じた・aibow-server を止めた)返らない。
func (e *Events) serve(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "このつながりでは SSE を送れません")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	// 先に受け取る側に入ってから、つながったことを最初のコメント行で知らせる(ブラウザの EventSource は、これで open になる)。
	// 逆にすると、知らせてから入るまでの間の知らせを取りこぼす
	ch := e.subscribe()
	defer e.unsubscribe(ch)
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	tick := time.NewTicker(keepAlive)
	defer tick.Stop()
	var b bytes.Buffer
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return // 受け取り切れずに切られた
			}
			b.Reset()
			fmt.Fprintf(&b, "event: %s\ndata: %s\n\n", ev.name, ev.data)
			if _, err := w.Write(b.Bytes()); err != nil {
				return
			}
			flusher.Flush()
		case <-tick.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}
