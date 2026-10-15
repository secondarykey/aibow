package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// connect は、プロセスを起動せずに(SDK の NewInMemoryTransports で)サーバーにつないだクライアントを返す。
// クライアントは SDK のものをそのまま使う(aibow の mcp/client を通さず、サーバーの答えだけを確かめるため)
func connect(t *testing.T, opts Options) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	ss, err := New(opts).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, nil)
	cs, err := c.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

// call はツールを呼び、結果の文と isError を返す。
func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), res.IsError
}

func TestListTools(t *testing.T) {
	dir := t.TempDir()
	cs := connect(t, Options{WorkDir: dir, Version: "v1.2.3"})
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	readOnly := map[string]bool{}
	for _, tl := range res.Tools {
		names = append(names, tl.Name)
		readOnly[tl.Name] = tl.Annotations != nil && tl.Annotations.ReadOnlyHint
	}
	// SDK は名前の順に返す
	want := []string{"calculate", "edit_file", "get_current_time", "list_files", "read_file", "run_command", "write_file"}
	if !slices.Equal(names, want) {
		t.Errorf("ツール = %v, want %v", names, want)
	}
	// 読むだけのツールにだけ readOnlyHint を付ける
	for name, ro := range readOnly {
		wantRO := !slices.Contains([]string{"write_file", "edit_file", "run_command"}, name)
		if ro != wantRO {
			t.Errorf("%s の readOnlyHint = %v, want %v", name, ro, wantRO)
		}
	}

	// 引数の JSON Schema は、組み込みの定義と同じものが届く
	var schema struct {
		Required []string `json:"required"`
	}
	for _, tl := range res.Tools {
		if tl.Name != "read_file" {
			continue
		}
		b, _ := json.Marshal(tl.InputSchema)
		if err := json.Unmarshal(b, &schema); err != nil {
			t.Fatal(err)
		}
	}
	if !slices.Equal(schema.Required, []string{"path"}) {
		t.Errorf("read_file の required = %v", schema.Required)
	}

	// 名乗り(serverInfo)と、Environment の説明(作業フォルダ・シェル)
	init := cs.InitializeResult()
	if init.ServerInfo.Name != Name || init.ServerInfo.Version != "v1.2.3" {
		t.Errorf("serverInfo = %+v", init.ServerInfo)
	}
	if !strings.Contains(init.Instructions, dir) || !strings.Contains(init.Instructions, "run_command") {
		t.Errorf("instructions = %q", init.Instructions)
	}
}

func TestListToolsWithoutWorkDir(t *testing.T) {
	cs := connect(t, Options{})
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range res.Tools {
		names = append(names, tl.Name)
	}
	if !slices.Equal(names, []string{"calculate", "get_current_time"}) {
		t.Errorf("作業フォルダが無いときのツール = %v", names)
	}
}

func TestCallTools(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cs := connect(t, Options{WorkDir: dir})

	if text, isErr := call(t, cs, "calculate", map[string]any{"expression": "(1 + 2) * 3"}); isErr || !strings.Contains(text, "9") {
		t.Errorf("calculate = %q (isError %v)", text, isErr)
	}
	// 整数の引数は JSON の数で届く(Qwen3.5 の書式と違い、文字列ではない)
	text, isErr := call(t, cs, "read_file", map[string]any{"path": "a.txt", "offset": 2, "limit": 1})
	if isErr || !strings.Contains(text, "two") || strings.Contains(text, "three") {
		t.Errorf("read_file = %q (isError %v)", text, isErr)
	}
	// null は書かなかったのと同じ
	if text, isErr := call(t, cs, "read_file", map[string]any{"path": "a.txt", "offset": nil}); isErr || !strings.Contains(text, "one") {
		t.Errorf("offset が null の read_file = %q (isError %v)", text, isErr)
	}
	if text, isErr := call(t, cs, "list_files", nil); isErr || !strings.Contains(text, "a.txt") {
		t.Errorf("list_files = %q (isError %v)", text, isErr)
	}
}

// ツールの失敗は、呼び出しのエラーではなく isError の結果で返り、組み込みと同じ文がモデルに届く
func TestCallErrors(t *testing.T) {
	dir := t.TempDir()
	cs := connect(t, Options{WorkDir: dir})
	tests := []struct {
		name string
		tool string
		args map[string]any
		want string
	}{
		{"知らない引数", "read_file", map[string]any{"path": "a.txt", "line": 1}, "使える引数: limit, offset, path"},
		{"足りない引数", "read_file", map[string]any{}, "必須の引数 path がありません"},
		{"型が違う", "read_file", map[string]any{"path": "a.txt", "offset": "abc"}, "整数で書いてください"},
		{"作業フォルダの外", "read_file", map[string]any{"path": "../secret.txt"}, "作業フォルダの外です"},
		{"無いファイル", "read_file", map[string]any{"path": "none.txt"}, "見つかりません"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text, isErr := call(t, cs, tt.tool, tt.args)
			if !isErr || !strings.Contains(text, tt.want) {
				t.Errorf("%s = %q (isError %v), want %q を含むエラー", tt.tool, text, isErr, tt.want)
			}
		})
	}
}
