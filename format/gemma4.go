package format

// gemma4.go: Gemma 4 の書式(format.go の Format の実装。0026)。
//
// 手元の GGUF(_cmd/aibow/models/gemma-4-E4B-it-Q4_K_M.gguf)の tokenizer.chat_template を、
// aibow が渡す会話の形(文字の発言・思考・ツールの呼び出しと結果)で通る部分だけ Go で書き直したもの。
// 空白や改行の1文字まで同じになるよう、テンプレートを jinja2 で通した文字列と比べてテストしている(testdata/gemma4/)。
//
// Qwen3.5 と比べると、区切りの文字だけでなく会話の形が違う:
//
//	<|turn>system
//	<|think|>                         ← 思考の ON は、システムの発言の先頭の印(Qwen3.5 は返事の書き出しで切り替える)
//	あなたは…<|tool>declaration:calculate{…}<tool|><turn|>
//	<|turn>user
//	1234 × 5678 は?<turn|>
//	<|turn>model
//	<|channel>thought
//	計算ツールを使おう。
//	<channel|><|tool_call>call:calculate{expression:<|"|>1234 * 5678<|"|>}<tool_call|><|tool_response>response:calculate{value:<|"|>7006652<|"|>}<tool_response|><|channel>thought
//
// - ツールの結果は user の発言ではなく、model の発言の中のまま、呼び出しのすぐ後に書く。結果にはツールの名前が要る
// - 結果のあとは、同じ model の発言の続きを書く(思考 ON なら <|channel>thought の続きから)
// - ツールの説明・呼び出しの引数は JSON ではない独自の書き方(文字列は <|"|>…<|"|>、キーは名前の順、型は大文字)
// - 呼び出しのある返事の答えの文は、ツールの結果の後ろに置く(モデルは呼び出しの前に書くので、書いた順とは違う)
//
// bos(<bos>)は書かない。GGUF の add_bos_token が True なので、トークン化(addSpecial)が1つ足す(0026 の決定事項 25)。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/secondarykey/aibow/tool"
)

// gemma4 は Gemma 4 の書式。GGUF の general.architecture が "gemma4" のモデルに使う。
type gemma4 struct{}

func (gemma4) Name() string { return "Gemma 4" }

// Gemma 4 の書式の印。どれも語彙の中の特別な1トークン。
const (
	gemma4Str           = `<|"|>` // 文字列の囲み(ツールの説明・呼び出し・結果の中)
	gemma4ChannelStart  = "<|channel>"
	gemma4ChannelEnd    = "<channel|>"
	gemma4Thought       = "thought\n" // 思考の印(<|channel> の直後)
	gemma4CallStart     = "<|tool_call>"
	gemma4CallEnd       = "<tool_call|>"
	gemma4ResponseStart = "<|tool_response>"
	gemma4ResponseEnd   = "<tool_response|>"
)

// gemma4EmptyThought は、思考を切ったときに返事の書き出しに空の思考を入れるか(0026 の決定事項 8)。
// 公式の文書は、思考を切っても思考を書いてしまう大きいモデル(26B・31B)に勧めている。E4B には書いていないので入れない
const gemma4EmptyThought = false

// BuildPrompt は会話を Gemma 4 の書式の1本の文字列にする(bos は書かない)。
func (gemma4) BuildPrompt(p Prompt) string {
	text, _ := buildGemma4(p)
	return text
}

// buildGemma4 はプロンプトを組み立て、最後に書いたものの種類も返す(テンプレートの prev_message_type)。
// "tool_response" で終わったら、返事は同じ model の発言の続き(思考 ON なら思考の途中)から書かせる。
// 読み取り(NewParser)も、どこから書き始めるかを知るためにこれを使う。
func buildGemma4(p Prompt) (string, string) {
	var b strings.Builder
	msgs := p.Messages
	last := ""

	// 1. システムの発言。思考 ON・ツールあり・システムの発言あり、のどれかなら作る
	hasSystem := len(msgs) > 0 && msgs[0].Role == "system"
	if p.Think || len(p.Tools) > 0 || hasSystem {
		b.WriteString("<|turn>system\n")
		if p.Think {
			b.WriteString("<|think|>\n")
			last = "think"
		}
		if hasSystem {
			b.WriteString(strings.TrimSpace(msgs[0].Content))
			msgs = msgs[1:]
		}
		for _, t := range p.Tools {
			b.WriteString("<|tool>" + gemma4Declaration(t) + "<tool|>")
			last = "tool"
		}
		b.WriteString("<turn|>\n")
	}

	// 2. 発言。最後のユーザーの発言より後の返事(= ツールのループの途中の返事)だけ、思考も残す(Qwen3.5 と同じ)
	lastUser := -1
	for i, m := range msgs {
		if m.Role == "user" {
			lastUser = i
		}
	}
	prevRole := "" // 直前の tool でない発言の役
	for i, m := range msgs {
		if m.Role == "tool" {
			continue // ツールの結果は、呼び出した返事の中で書く(下の writeGemma4Responses)
		}
		last = ""
		role := m.Role
		if role == "assistant" {
			role = "model"
		}
		// ツールの結果のあとの返事は、新しい発言を始めずに同じ model の発言の続きを書く
		if !(role == "model" && prevRole == "assistant") {
			b.WriteString("<|turn>" + role + "\n")
		}
		if m.Thinking != "" && i > lastUser {
			b.WriteString(gemma4ChannelStart + gemma4Thought + m.Thinking + "\n" + gemma4ChannelEnd)
		}
		if len(m.ToolCalls) > 0 {
			writeGemma4Calls(&b, m.ToolCalls, p.Tools)
			last = "tool_call"
		}
		responded := false
		if len(m.ToolCalls) > 0 {
			responded = writeGemma4Responses(&b, m.ToolCalls, msgs[i+1:])
			if responded {
				last = "tool_response"
			}
		}

		// 答えの文。呼び出しのある返事でも、ツールの結果の後ろに置く(テンプレートの順)
		content := strings.TrimSpace(m.Content)
		if role == "model" {
			content = gemma4StripThinking(m.Content)
		}
		b.WriteString(content)

		next := "" // 次の tool でない発言の役
		for _, n := range msgs[i+1:] {
			if n.Role != "tool" {
				next = n.Role
				break
			}
		}
		continues := role == "model" && next == "assistant" && (len(m.ToolCalls) == 0 || responded)
		switch {
		case last == "tool_call" && !responded:
			b.WriteString(gemma4ResponseStart) // 結果の無い呼び出し(結果を待っている)
		case continues:
			// 次の返事が同じ model の発言の続きになるので、閉じない
		case !(responded && content == "" && next == ""):
			b.WriteString("<turn|>\n")
		}
		prevRole = m.Role
	}

	// 3. 返事の書き出し
	switch last {
	case "tool_response":
		if p.Think {
			// ツールの結果のあとは、思考の続きから書かせる
			b.WriteString(gemma4ChannelStart + gemma4Thought)
		}
	case "tool_call":
	default:
		b.WriteString("<|turn>model\n")
		if !p.Think && gemma4EmptyThought {
			b.WriteString(gemma4ChannelStart + "thought" + gemma4ChannelEnd)
		}
	}
	return b.String(), last
}

// writeGemma4Calls は返事の中のツール呼び出しを書く。引数はキーの名前の順に並べ直す(テンプレートの dictsort)。
//
//	<|tool_call>call:read_file{offset:12,path:<|"|>a.go<|"|>}<tool_call|>
func writeGemma4Calls(b *strings.Builder, calls []ToolCall, tools []tool.Def) {
	for _, c := range calls {
		if c.Raw != "" {
			// 書式が崩れて読み取れなかった呼び出しは、モデルが書いたままを入れる(Qwen3.5 と同じ)
			b.WriteString(c.Raw)
			continue
		}
		types := tool.ArgTypesFor(tools, c.Name)
		args := slices.Clone(c.Args)
		slices.SortStableFunc(args, func(x, y tool.Arg) int { return dictCompare(x.Name, y.Name) })
		b.WriteString(gemma4CallStart + "call:" + c.Name + "{")
		for i, a := range args {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(a.Name + ":" + gemma4Argument(gemma4Value(types[a.Name], a.Value), false))
		}
		b.WriteString("}" + gemma4CallEnd)
	}
}

// writeGemma4Responses は、呼び出しのすぐ後に続く tool の発言(ツールの結果)を書く。結果が1つでもあれば true。
//
//	<|tool_response>response:calculate{value:<|"|>7006652<|"|>}<tool_response|>
//
// 結果の名前は、会話の仕組みが入れた Message.Name。無ければ(0026 より前の会話など)呼び出しと順番で組にする(0026 の決定事項 5)。
// 結果は文字1つとして value に入れる(テンプレートの「結果が文字のとき」の形。決定事項 6)。前後の空白は落とさない。
func writeGemma4Responses(b *strings.Builder, calls []ToolCall, rest []Message) bool {
	n := 0
	for _, m := range rest {
		if m.Role != "tool" {
			break
		}
		name := m.Name
		if name == "" && n < len(calls) {
			name = calls[n].Name
		}
		if name == "" {
			name = "unknown" // テンプレートと同じ(名前の分からない結果)
		}
		b.WriteString(gemma4ResponseStart + "response:" + name + "{value:" + gemma4Argument(m.Content, false) + "}" + gemma4ResponseEnd)
		n++
	}
	return n > 0
}

// gemma4StripThinking は、返事の答えから思考の部分(<|channel>…<channel|>)を除いて前後の空白を落とす(テンプレートの strip_thinking)。
func gemma4StripThinking(text string) string {
	var b strings.Builder
	for part := range strings.SplitSeq(text, gemma4ChannelEnd) {
		before, _, _ := strings.Cut(part, gemma4ChannelStart)
		b.WriteString(before)
	}
	return strings.TrimSpace(b.String())
}

// ---- 値の書き方 ----------------------------------------------------------------------
//
// テンプレートは Python で動くので、値は Python の値として書かれる。ここではそれを JSON の値(json.Number 付き)で再現する:
//
//	文字列 … <|"|>中身<|"|>(エスケープしない)
//	数     … Python の str()(整数はそのまま、小数は repr。1e3 は 1000.0)
//	真偽   … true / false、null … null
//	配列   … [a,b](空白なし)
//	辞書   … {key:value}(キーは名前の順。escapeKeys ならキーも <|"|> で囲む)

// gemma4Value は tool.Arg の形の値を、書く値にする。型のある引数で JSON として読める値は JSON の値、それ以外は文字列。
func gemma4Value(typ, value string) any {
	v, err := decodeJSON(tool.ArgJSON(typ, value))
	if err != nil {
		return value
	}
	return v
}

// gemma4Argument は値を Gemma 4 の書き方にする(テンプレートの format_argument)。
func gemma4Argument(v any, escapeKeys bool) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return gemma4Str + x + gemma4Str
	case bool:
		if x {
			return "true"
		}
		return "false"
	case json.Number:
		return pyNumber(x)
	case map[string]any:
		var b strings.Builder
		b.WriteString("{")
		for i, k := range dictKeys(x) {
			if i > 0 {
				b.WriteString(",")
			}
			if escapeKeys {
				b.WriteString(gemma4Str + k + gemma4Str)
			} else {
				b.WriteString(k)
			}
			b.WriteString(":" + gemma4Argument(x[k], escapeKeys))
		}
		b.WriteString("}")
		return b.String()
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = gemma4Argument(e, escapeKeys)
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
	return fmt.Sprint(v)
}

// ---- ツールの説明 --------------------------------------------------------------------

// gemma4Declaration はツールの定義1つを書く(テンプレートの format_function_declaration)。
//
//	declaration:calculate{description:<|"|>…<|"|>,parameters:{properties:{expression:{description:<|"|>数式<|"|>,type:<|"|>STRING<|"|>}},required:[<|"|>expression<|"|>],type:<|"|>OBJECT<|"|>}}
//
// JSON Schema は JSON ではない独自の書き方に直す。キーは名前の順、型は大文字。
// テンプレートの癖もそのまま写す(type が配列なら Python のリストの見た目 ['BOOLEAN', 'NULL'] になる・properties の無い object には properties:{} が付く)。
func gemma4Declaration(t tool.Def) string {
	var b strings.Builder
	b.WriteString("declaration:" + t.Name + "{description:" + gemma4Str + t.Description + gemma4Str)
	params, _ := decodeJSON(string(t.Parameters))
	if truthy(params) {
		b.WriteString(",parameters:{")
		if props, ok := get(params, "properties").(map[string]any); ok && len(props) > 0 {
			b.WriteString("properties:{" + gemma4Parameters(props, false) + "},")
		}
		if req := get(params, "required"); truthy(req) {
			b.WriteString("required:[" + gemma4Names(req) + "],")
		}
		if typ := get(params, "type"); truthy(typ) {
			b.WriteString("type:" + gemma4Str + upperType(typ) + gemma4Str + "}")
		}
	}
	b.WriteString("}")
	return strings.TrimSpace(b.String())
}

// gemma4StandardKeys は、properties の無い object の引数で、引数として書かないキー(テンプレートの standard_keys)。
var gemma4StandardKeys = []string{"description", "type", "properties", "required", "nullable"}

// gemma4Parameters は引数の定義(properties)を書く(テンプレートの format_parameters)。
func gemma4Parameters(props map[string]any, filterKeys bool) string {
	var b strings.Builder
	first := true
	for _, key := range dictKeys(props) {
		if filterKeys && slices.Contains(gemma4StandardKeys, key) {
			continue
		}
		value := props[key]
		if !first {
			b.WriteString(",")
		}
		first = false
		b.WriteString(key + ":{")
		comma := false
		sep := func() { // 2つ目からの項目の前に , を書く
			if comma {
				b.WriteString(",")
			}
			comma = true
		}
		if d := get(value, "description"); truthy(d) {
			b.WriteString("description:" + gemma4Str + fmt.Sprint(d) + gemma4Str)
			comma = true
		}
		typ := upperType(get(value, "type"))
		switch typ {
		case "STRING":
			if e := get(value, "enum"); truthy(e) {
				sep()
				b.WriteString("enum:" + gemma4Argument(e, true))
			}
		case "ARRAY":
			if items, ok := get(value, "items").(map[string]any); ok && len(items) > 0 {
				sep()
				b.WriteString("items:{" + gemma4Items(items) + "}")
			}
		}
		if truthy(get(value, "nullable")) {
			sep()
			b.WriteString("nullable:true")
		}
		if typ == "OBJECT" {
			if sub, ok := get(value, "properties").(map[string]any); ok {
				sep()
				b.WriteString("properties:{" + gemma4Parameters(sub, false) + "}")
			} else if m, ok := value.(map[string]any); ok {
				sep()
				b.WriteString("properties:{" + gemma4Parameters(m, true) + "}")
			}
			if req := get(value, "required"); truthy(req) {
				sep()
				b.WriteString("required:[" + gemma4Names(req) + "]")
			}
		}
		sep()
		b.WriteString("type:" + gemma4Str + typ + gemma4Str + "}")
	}
	return b.String()
}

// gemma4Items は配列の引数の items を書く。
func gemma4Items(items map[string]any) string {
	var parts []string
	for _, k := range dictKeys(items) {
		v := items[k]
		if v == nil {
			continue
		}
		switch k {
		case "properties":
			sub, _ := v.(map[string]any)
			parts = append(parts, "properties:{"+gemma4Parameters(sub, false)+"}")
		case "required":
			parts = append(parts, "required:["+gemma4Names(v)+"]")
		case "type":
			if s, ok := v.(string); ok {
				parts = append(parts, "type:"+gemma4Argument(strings.ToUpper(s), true))
			} else {
				list, _ := v.([]any)
				up := make([]any, len(list))
				for i, e := range list {
					up[i] = strings.ToUpper(fmt.Sprint(e))
				}
				parts = append(parts, "type:"+gemma4Argument(up, true))
			}
		default:
			parts = append(parts, k+":"+gemma4Argument(v, true))
		}
	}
	return strings.Join(parts, ",")
}

// gemma4Names は required の名前の並びを <|"|>a<|"|>,<|"|>b<|"|> にする。
func gemma4Names(v any) string {
	list, _ := v.([]any)
	parts := make([]string, len(list))
	for i, e := range list {
		parts[i] = gemma4Str + fmt.Sprint(e) + gemma4Str
	}
	return strings.Join(parts, ",")
}

// upperType は JSON Schema の type を大文字にする(テンプレートの value['type'] | upper)。
// 配列の type は、Python がリストを文字にした見た目を大文字にしたものになる(['BOOLEAN', 'NULL'])。無ければ空。
func upperType(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return strings.ToUpper(x)
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = pyRepr(fmt.Sprint(e))
		}
		return strings.ToUpper("[" + strings.Join(parts, ", ") + "]")
	}
	return strings.ToUpper(fmt.Sprint(v))
}

// ---- 出力の読み取り -----------------------------------------------------------------
//
// モデルが書く返事は、次のような1本の文字列:
//
//	<|channel>thought            ← 思考(思考 ON のとき。ツールの結果の後ろでは、プロンプトがここまで書いてある)
//	計算ツールを使おう。
//	<channel|>計算します。       ← 答え(画面に出す)
//	<|tool_call>call:calculate{expression:<|"|>1234 * 5678<|"|>}<tool_call|><|tool_response>   ← 呼び出し。<|tool_response> で止める
//
// 呼び出しは Qwen3.5 と違って行の頭とは限らない(思考の直後や答えの続きに書く)ので、行の頭の決まりは使わない。
// コードブロックの中の <|tool_call> は呼び出しと見ない(Qwen3.5 と同じ。0026 の決定事項 10)。

// gemma4CallHelp は、書式が崩れていたときにモデルに返すエラーに添える、正しい書き方。
const gemma4CallHelp = `正しい書式: <|tool_call>call:関数名{引数名:<|"|>文字列<|"|>,数の引数:5}<tool_call|>`

func (gemma4) NewParser(p Prompt) OutputParser {
	q := &gemma4Parser{tools: p.Tools}
	if _, last := buildGemma4(p); last == "tool_response" && p.Think {
		q.state = gemma4InThought // プロンプトが <|channel>thought まで書いてある
	}
	return q
}

// gemma4Parser の読み取りの状態。
const (
	gemma4InContent     = iota // 答え
	gemma4InThoughtHead        // <|channel> の直後(thought の印を待っている)
	gemma4InThought            // 思考
)

// gemma4Parser は生成した文字列を思考・答え・呼び出しの部分に振り分け、全体も覚えておく(Result 用)。
type gemma4Parser struct {
	tools             []tool.Def
	state             int
	held              string // タグの書きかけかもしれないので保留している文字列
	thinking, content strings.Builder
	calls             strings.Builder // <|tool_call> から後ろ(呼び出しの部分)。空なら、まだ呼び出しは出ていない
}

func (q *gemma4Parser) Push(text string) (thinking, content string) {
	return q.push(text, false)
}

func (q *gemma4Parser) Flush() (thinking, content string) {
	return q.push("", true)
}

// push は届いた文字列を状態に沿って振り分ける。last なら書きかけを持たずに全部出す。
func (q *gemma4Parser) push(text string, last bool) (thinking, content string) {
	if q.calls.Len() > 0 {
		q.calls.WriteString(text)
		return "", ""
	}
	q.held += text
	for {
		switch q.state {
		case gemma4InThoughtHead:
			if !last && len(q.held) < len(gemma4Thought) && strings.HasPrefix(gemma4Thought, q.held) {
				return q.keep(thinking, content) // まだ thought の印の途中
			}
			q.held = strings.TrimPrefix(strings.TrimPrefix(q.held, "thought"), "\n")
			q.state = gemma4InThought

		case gemma4InThought:
			if i := strings.Index(q.held, gemma4ChannelEnd); i >= 0 {
				thinking += q.held[:i]
				q.held = q.held[i+len(gemma4ChannelEnd):]
				q.state = gemma4InContent
				continue
			}
			keep := 0
			if !last {
				keep = partialSuffix(q.held, gemma4ChannelEnd)
			}
			thinking += q.held[:len(q.held)-keep]
			q.held = q.held[len(q.held)-keep:]
			return q.keep(thinking, content)

		default: // 答え
			tag, i := gemma4NextTag(q.held)
			if i < 0 {
				keep := 0
				if !last {
					keep = partialSuffixAny(q.held, gemma4ChannelStart, gemma4CallStart)
				}
				content += q.held[:len(q.held)-keep]
				q.held = q.held[len(q.held)-keep:]
				return q.keep(thinking, content)
			}
			before := q.held[:i]
			if insideCodeFence(q.content.String() + content + before) {
				// コードブロックの中のタグは、答えの文字としてそのまま出す
				content += q.held[:i+len(tag)]
				q.held = q.held[i+len(tag):]
				continue
			}
			content += before
			rest := q.held[i+len(tag):]
			q.held = ""
			if tag == gemma4CallStart {
				q.calls.WriteString(tag + rest)
				return q.keep(thinking, content)
			}
			q.held = rest
			q.state = gemma4InThoughtHead
		}
	}
}

// gemma4NextTag は、答えの中で次に出てくる印(思考の始まりか呼び出しの始まり)と位置を返す。無ければ -1。
func gemma4NextTag(s string) (string, int) {
	c, t := strings.Index(s, gemma4ChannelStart), strings.Index(s, gemma4CallStart)
	switch {
	case c < 0 && t < 0:
		return "", -1
	case t < 0 || (c >= 0 && c < t):
		return gemma4ChannelStart, c
	}
	return gemma4CallStart, t
}

// keep は画面に出す分を覚えておく。答えの書き出しの空白(思考の後ろの改行など)は落とす。
func (q *gemma4Parser) keep(thinking, content string) (string, string) {
	if q.content.Len() == 0 {
		content = strings.TrimLeftFunc(content, unicode.IsSpace)
	}
	q.thinking.WriteString(thinking)
	q.content.WriteString(content)
	return thinking, content
}

// Stop は、呼び出しの後ろでモデルが <|tool_response> を書いたら true を返す(公式の文書の「追加の停止シーケンス」。決定事項 7)。
// 結果は aibow が続けて書くので、次の送信で KV キャッシュの頭がそろう。
func (q *gemma4Parser) Stop() bool {
	return strings.Contains(q.calls.String(), gemma4ResponseStart)
}

func (q *gemma4Parser) Result() Output {
	// テンプレートは思考の後ろに改行を1つ足して <channel|> を書くので、その改行は思考に入れない(書き戻すと同じになる)
	out := Output{
		Thinking: strings.TrimSuffix(q.thinking.String(), "\n"),
		Content:  strings.TrimRightFunc(q.content.String(), unicode.IsSpace),
	}
	if q.calls.Len() > 0 {
		out.ToolCalls = parseGemma4Calls(q.calls.String(), q.tools)
	}
	return out
}

// parseGemma4Calls は「呼び出しの部分」(<|tool_call> から後ろ)から、呼び出しを全部読み取る。呼び出しの間の文は捨てる。
func parseGemma4Calls(s string, tools []tool.Def) []ToolCall {
	// モデルが書き始めた結果は捨てる(Stop で止めたときに、印だけ残っている)
	if i := strings.Index(s, gemma4ResponseStart); i >= 0 {
		s = s[:i]
	}
	var calls []ToolCall
	for {
		i := strings.Index(s, gemma4CallStart)
		if i < 0 {
			return calls
		}
		s = s[i:]
		end := strings.Index(s, gemma4CallEnd)
		if end < 0 {
			// 閉じないまま終わった(返事の上限・停止など)
			raw := strings.TrimRightFunc(s, unicode.IsSpace)
			return append(calls, brokenCall(raw, "", gemma4CallEnd+" で閉じていません", gemma4CallHelp))
		}
		block := s[:end+len(gemma4CallEnd)]
		calls = append(calls, parseGemma4Call(block, tools))
		s = s[len(block):]
	}
}

// parseGemma4Call は <|tool_call>call:名前{キー:値,…}<tool_call|> を1つ読み取る。
// 値は型を持って書かれる(文字列は <|"|>…<|"|>、数・真偽・null はそのまま、{} と [] も)ので、JSON の値にしてから
// ツールの定義の型を見て tool.Arg の形にする(tool.ArgFromJSON。0026 の決定事項 1)。
func parseGemma4Call(block string, tools []tool.Def) ToolCall {
	inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(block, gemma4CallStart), gemma4CallEnd))
	rest, ok := strings.CutPrefix(inner, "call:")
	if !ok {
		return brokenCall(block, "", gemma4CallStart+" の中が call:関数名 で始まっていません", gemma4CallHelp)
	}
	brace := strings.Index(rest, "{")
	name := strings.TrimSpace(rest[:max(brace, 0)])
	if brace < 0 || name == "" {
		return brokenCall(block, "", "call:関数名{…} の関数名が読み取れません", gemma4CallHelp)
	}
	r := &gemma4Reader{s: rest[brace:]}
	keys, values, err := r.object()
	if err == nil && strings.TrimSpace(r.s[r.i:]) != "" {
		err = fmt.Errorf("引数の } の後ろに余分な文があります")
	}
	if err != nil {
		return brokenCall(block, name, err.Error(), gemma4CallHelp)
	}
	types := tool.ArgTypesFor(tools, name)
	call := ToolCall{Call: tool.Call{Name: name, Args: []tool.Arg{}}}
	for i, k := range keys {
		call.Args = append(call.Args, tool.Arg{Name: k, Value: tool.ArgFromJSON(types[k], json.RawMessage(values[i]))})
	}
	return call
}

// gemma4Reader は呼び出しの引数の書き方を読み、値を JSON の文字にする(配列・辞書は Python の json.dumps と同じ見た目。
// モデルを Qwen3.5 に替えたときに、テンプレートの tojson と同じ文字で書けるように)。
type gemma4Reader struct {
	s string
	i int
}

func (r *gemma4Reader) space() {
	for r.i < len(r.s) && strings.ContainsRune(" \t\r\n", rune(r.s[r.i])) {
		r.i++
	}
}

func (r *gemma4Reader) value() (string, error) {
	r.space()
	switch {
	case strings.HasPrefix(r.s[r.i:], gemma4Str):
		start := r.i + len(gemma4Str)
		end := strings.Index(r.s[start:], gemma4Str)
		if end < 0 {
			return "", fmt.Errorf("文字列が %s で閉じていません", gemma4Str)
		}
		r.i = start + end + len(gemma4Str)
		return pyJSONString(r.s[start : start+end]), nil
	case strings.HasPrefix(r.s[r.i:], "{"):
		keys, values, err := r.object()
		if err != nil {
			return "", err
		}
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = pyJSONString(k) + ": " + values[i]
		}
		return "{" + strings.Join(parts, ", ") + "}", nil
	case strings.HasPrefix(r.s[r.i:], "["):
		r.i++
		var parts []string
		for {
			r.space()
			if strings.HasPrefix(r.s[r.i:], "]") {
				r.i++
				return "[" + strings.Join(parts, ", ") + "]", nil
			}
			if len(parts) > 0 {
				if !strings.HasPrefix(r.s[r.i:], ",") {
					return "", fmt.Errorf("配列の要素の間に , がありません")
				}
				r.i++
			}
			v, err := r.value()
			if err != nil {
				return "", err
			}
			parts = append(parts, v)
		}
	}
	// 数・真偽・null は、次の区切りまで
	end := r.i
	for end < len(r.s) && !strings.ContainsRune(",}]", rune(r.s[end])) {
		end++
	}
	lit := strings.TrimSpace(r.s[r.i:end])
	r.i = end
	var n json.Number
	if lit == "true" || lit == "false" || lit == "null" || (json.Unmarshal([]byte(lit), &n) == nil && lit != "" && lit[0] != '"') {
		return lit, nil
	}
	return "", fmt.Errorf("値 %q が読めません(文字列は %s で囲みます)", lit, gemma4Str)
}

// object は {キー:値,…} を読む。キーは囲まない書き方(呼び出し)でも <|"|> で囲んだ書き方でもよい。
func (r *gemma4Reader) object() (keys, values []string, err error) {
	r.space()
	if !strings.HasPrefix(r.s[r.i:], "{") {
		return nil, nil, fmt.Errorf("引数が { で始まっていません")
	}
	r.i++
	for {
		r.space()
		if strings.HasPrefix(r.s[r.i:], "}") {
			r.i++
			return keys, values, nil
		}
		if len(keys) > 0 {
			if !strings.HasPrefix(r.s[r.i:], ",") {
				return nil, nil, fmt.Errorf("引数の間に , がありません")
			}
			r.i++
			r.space()
		}
		var key string
		if strings.HasPrefix(r.s[r.i:], gemma4Str) {
			start := r.i + len(gemma4Str)
			end := strings.Index(r.s[start:], gemma4Str)
			if end < 0 {
				return nil, nil, fmt.Errorf("キーが %s で閉じていません", gemma4Str)
			}
			key, r.i = r.s[start:start+end], start+end+len(gemma4Str)
		} else {
			colon := strings.Index(r.s[r.i:], ":")
			if colon < 0 {
				return nil, nil, fmt.Errorf("引数名の後ろに : がありません")
			}
			key = strings.TrimSpace(r.s[r.i : r.i+colon])
			r.i += colon
		}
		r.space()
		if key == "" || !strings.HasPrefix(r.s[r.i:], ":") {
			return nil, nil, fmt.Errorf("引数名:値 の形になっていません")
		}
		r.i++
		v, err := r.value()
		if err != nil {
			return nil, nil, err
		}
		keys, values = append(keys, key), append(values, v)
	}
}

// ---- Python の見た目を写す小さな道具 --------------------------------------------------

// decodeJSON は JSON を読む。数は書いたままの文字(json.Number)で受け取る(1.0 が 1 にならないように)。
func decodeJSON(s string) (any, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

// get は Jinja の value['key'] と同じく、辞書でなければ・キーが無ければ nil。
func get(v any, key string) any {
	m, _ := v.(map[string]any)
	return m[key]
}

// truthy は Jinja(Python)の真偽の判定。空の文字列・配列・辞書・0・null は偽。
func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case string:
		return x != ""
	case bool:
		return x
	case json.Number:
		f, _ := x.Float64()
		return f != 0
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	}
	return true
}

// dictKeys は辞書のキーを Jinja の dictsort と同じ順(大文字と小文字を区別しない名前の順)にする。
func dictKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, dictCompare)
	return keys
}

// dictCompare は dictsort の比べ方(小文字にして比べる。同じなら元の文字で決める)。
func dictCompare(a, b string) int {
	if c := strings.Compare(strings.ToLower(a), strings.ToLower(b)); c != 0 {
		return c
	}
	return strings.Compare(a, b)
}

// pyNumber は JSON の数を Python の str() と同じ見た目にする(テンプレートは json.loads した値を書く)。
// 整数はそのまま、小数(. や e を含む)は float の repr(0.5・1000.0・1e-05)。
func pyNumber(n json.Number) string {
	s := n.String()
	if !strings.ContainsAny(s, ".eE") {
		if s == "-0" {
			return "0"
		}
		return s
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return s
	}
	return pyFloat(f)
}

// pyFloat は Python の repr(float) と同じ見た目にする。一番短く元に戻る桁で、指数が -4 以上 16 未満なら小数の形(1.0 のように必ず小数点を書く)、
// それ以外は 1e+16・1.5e-05 の形(指数は2桁以上)。
func pyFloat(f float64) string {
	e := strconv.FormatFloat(f, 'e', -1, 64) // 例: "-1.2345e+03"
	sign := ""
	if e[0] == '-' {
		sign, e = "-", e[1:]
	}
	mant, expStr, _ := strings.Cut(e, "e")
	exp, _ := strconv.Atoi(expStr)
	digits := strings.Replace(mant, ".", "", 1)
	if -4 <= exp && exp < 16 {
		var whole, frac string
		if exp >= 0 {
			for len(digits) <= exp {
				digits += "0"
			}
			whole, frac = digits[:exp+1], digits[exp+1:]
		} else {
			whole, frac = "0", strings.Repeat("0", -exp-1)+digits
		}
		if frac == "" {
			frac = "0"
		}
		return sign + whole + "." + frac
	}
	m := digits[:1]
	if len(digits) > 1 {
		m += "." + digits[1:]
	}
	return fmt.Sprintf("%s%se%+03d", sign, m, exp)
}

// pyRepr は文字列を Python の repr の見た目にする('a'。' を含み " を含まなければ "a")。
func pyRepr(s string) string {
	if strings.Contains(s, "'") && !strings.Contains(s, `"`) {
		return `"` + s + `"`
	}
	return "'" + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), "'", `\'`) + "'"
}

// pyJSONString は文字列を JSON の文字列にする(Python の json.dumps(ensure_ascii=False) と同じく、< > & をエスケープしない)。
func pyJSONString(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.Encode(s)
	return strings.TrimSuffix(b.String(), "\n")
}
