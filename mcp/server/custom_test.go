package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/secondarykey/aibow/tool"
)

// ほかのアプリのツール(0045)。メモを1つ持つだけの小さなもの。

var noteParams = json.RawMessage(`{"type": "object", "properties": {"text": {"type": "string"}}, "required": ["text"]}`)

// getNote は読むだけ(tool.Hinted で宣言する)。
type getNote struct{ note *string }

func (getNote) Def() Def {
	return Def{Name: "get_note", Description: "Read the note.", Parameters: json.RawMessage(`{"type": "object", "properties": {}}`)}
}
func (t getNote) Run(context.Context, tool.Args) (string, error) { return *t.note, nil }
func (getNote) ReadOnly() bool                                   { return true }

// appendNote は書き換えるが、確認はアプリが自分で行う((c)。Confirmable でも Hinted でもない)。
type appendNote struct{ note *string }

func (appendNote) Def() Def {
	return Def{Name: "append_note", Description: "Append text to the note.", Parameters: noteParams}
}
func (t appendNote) Run(_ context.Context, args tool.Args) (string, error) {
	*t.note += args.String("text")
	return "ok", nil
}

// setNote は書き換える前にクライアントで確かめる(Confirmable。組み込みと同じ MRTR)。
type setNote struct{ note *string }

func (setNote) Def() Def {
	return Def{Name: "set_note", Description: "Replace the note.", Parameters: noteParams}
}
func (t setNote) Preview(args tool.Args) (tool.Preview, error) {
	return tool.Preview{Path: "note", Note: "書き換え", Diff: tool.UnifiedDiff("note", *t.note, args.String("text"))}, nil
}
func (t setNote) Run(_ context.Context, args tool.Args) (string, error) {
	*t.note = args.String("text")
	return "ok", nil
}

// Def は tool.Def の短い名前(テストを読みやすくするため)。
type Def = tool.Def

func noteTools(note *string) []tool.Tool {
	return []tool.Tool{getNote{note}, appendNote{note}, setNote{note}}
}

func connectOpts(t *testing.T, opts Options, copts *mcp.ClientOptions) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	ss, err := New(opts).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, copts).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

// Custom を決めたら、そのツールだけを出す。名前・説明・Name・Instructions は渡したもの(完了の条件 1)。
// readOnlyHint は Hinted で読むだけと宣言したものにだけ付く(完了の条件 2)
func TestCustomTools(t *testing.T) {
	note := "hello"
	cs := connectOpts(t, Options{Name: "memo", Version: "v1", Instructions: "Edit the user's note.", Custom: noteTools(&note)}, nil)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, tl := range res.Tools {
		got[tl.Name] = tl.Annotations != nil && tl.Annotations.ReadOnlyHint
		if tl.Name == "append_note" && tl.Description != "Append text to the note." {
			t.Errorf("説明 = %q", tl.Description)
		}
	}
	want := map[string]bool{"get_note": true, "append_note": false, "set_note": false}
	if len(got) != len(want) {
		t.Errorf("ツール = %v, want %v だけ", got, want)
	}
	for name, ro := range want {
		if g, ok := got[name]; !ok || g != ro {
			t.Errorf("%s: 出したか %v・readOnlyHint %v, want readOnlyHint %v", name, ok, g, ro)
		}
	}
	init := cs.InitializeResult()
	if init.ServerInfo.Name != "memo" || init.ServerInfo.Version != "v1" || init.Instructions != "Edit the user's note." {
		t.Errorf("serverInfo = %+v, instructions = %q", init.ServerInfo, init.Instructions)
	}

	// 確認しないツールは、そのまま実行する
	if text, isErr := call(t, cs, "append_note", map[string]any{"text": " world"}); isErr || note != "hello world" {
		t.Errorf("append_note = %q (isError %v), note %q", text, isErr, note)
	}
	// 引数の確かめは組み込みと同じ文
	if text, isErr := call(t, cs, "append_note", map[string]any{}); !isErr || !strings.Contains(text, "必須の引数 text がありません") {
		t.Errorf("引数が足りない = %q (isError %v)", text, isErr)
	}
}

// Custom で Instructions が空なら、説明は出さない(フォルダの説明は合わない。決定事項 9)
func TestCustomNoInstructions(t *testing.T) {
	note := ""
	cs := connectOpts(t, Options{Custom: noteTools(&note)}, nil)
	if init := cs.InitializeResult(); init.Instructions != "" || init.ServerInfo.Name != Name {
		t.Errorf("instructions = %q, name = %q", init.Instructions, init.ServerInfo.Name)
	}
}

// Confirmable の Custom のツールは、組み込みと同じに MRTR で確かめてから実行する(完了の条件 2)
func TestCustomConfirm(t *testing.T) {
	note := "old"
	var asked []*mcp.ElicitParams
	cs := connectOpts(t, Options{Custom: noteTools(&note)}, answering(&asked, func(*mcp.ElicitParams) *mcp.ElicitResult {
		return &mcp.ElicitResult{Action: "decline", Content: map[string]any{}}
	}))
	text, isErr := call(t, cs, "set_note", map[string]any{"text": "new"})
	if isErr || !strings.Contains(text, "拒否しました") || note != "old" {
		t.Errorf("拒否したら書かない: %q (isError %v), note %q", text, isErr, note)
	}
	if len(asked) != 1 || !strings.Contains(asked[0].Message, "+new") {
		t.Errorf("差分を見せて聞く: %d 回 %v", len(asked), asked)
	}
}

// Check は誤りをエラーで返す。組み込みだけのときは通る(完了の条件 3)
func TestCheck(t *testing.T) {
	note := ""
	ok := []Options{{}, {WorkDir: t.TempDir(), Fetch: &tool.FetchOptions{}}, {Custom: noteTools(&note)}}
	for _, o := range ok {
		if err := o.Check(); err != nil {
			t.Errorf("通るはず: %v", err)
		}
	}
	bad := []struct {
		name string
		opts Options
		want string
	}{
		{"名前が空", Options{Custom: []tool.Tool{defTool{Def{Parameters: noteParams}}}}, "名前が空"},
		{"名前が重なる", Options{Custom: []tool.Tool{getNote{&note}, getNote{&note}}}, "get_note が2つ"},
		{"定義が空", Options{Custom: []tool.Tool{defTool{Def{Name: "x"}}}}, "空です"},
		{"定義が読めない", Options{Custom: []tool.Tool{defTool{Def{Name: "x", Parameters: json.RawMessage(`[1]`)}}}}, "読めません"},
		{"object でない", Options{Custom: []tool.Tool{defTool{Def{Name: "x", Parameters: json.RawMessage(`{"type": "string"}`)}}}}, `"object"`},
		{"WorkDir と一緒", Options{WorkDir: "/w", Custom: noteTools(&note)}, "WorkDir"},
		{"Folders と一緒", Options{Folders: []tool.Folder{{Name: "a", Path: "/a"}}, Custom: noteTools(&note)}, "Folders"},
		{"Fetch と一緒", Options{Fetch: &tool.FetchOptions{}, Custom: noteTools(&note)}, "Fetch"},
	}
	for _, b := range bad {
		err := b.opts.Check()
		if err == nil || !strings.Contains(err.Error(), b.want) {
			t.Errorf("%s: %v, want %q を含むエラー", b.name, err, b.want)
		}
	}
}

// New は誤りがあれば、Check の文で panic する(SDK の panic より先に止める)
func TestNewPanicsOnBadOptions(t *testing.T) {
	defer func() {
		r := recover()
		if s, _ := r.(string); !strings.Contains(s, "名前が空") {
			t.Errorf("panic = %v", r)
		}
	}()
	New(Options{Custom: []tool.Tool{defTool{Def{Parameters: noteParams}}}})
}

// defTool は定義だけのツール(Check のテスト用)。
type defTool struct{ def Def }

func (t defTool) Def() Def                                     { return t.def }
func (defTool) Run(context.Context, tool.Args) (string, error) { return "", nil }
