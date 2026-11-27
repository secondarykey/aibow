package server

import (
	"context"
	"slices"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/secondarykey/aibow/tool"
)

// FilesOnly は、ファイルのツールと update_plan だけを出す(時刻・計算・コマンド・Git は出さない。0067)。Custom・Fetch とは一緒に使えない
func TestFilesOnly(t *testing.T) {
	dir := t.TempDir()
	var names []string
	for _, x := range (Options{Folders: tool.Single(dir), FilesOnly: true}).Tools() {
		names = append(names, x.Def().Name)
	}
	slices.Sort(names)
	if want := []string{"edit_file", "list_files", "read_file", "search_files", "update_plan", "write_file"}; !slices.Equal(names, want) {
		t.Errorf("ツール: %v", names)
	}
	if tools := (Options{FilesOnly: true}).Tools(); len(tools) != 0 {
		t.Errorf("フォルダが無ければ何も出さない: %v", tools)
	}
	if err := (Options{FilesOnly: true, Fetch: &tool.FetchOptions{}}).Check(); err == nil {
		t.Error("FilesOnly と Fetch は一緒に使えない")
	}
	if err := (Options{FilesOnly: true, Custom: tool.Builtin()}).Check(); err == nil {
		t.Error("FilesOnly と Custom は一緒に使えない")
	}
}

// Wrote は、書き換えるツールが認められて成功したあとにだけ呼ぶ(読むツール・拒否したときは呼ばない)
func TestWrote(t *testing.T) {
	dir := t.TempDir()
	var wrote []string
	opts := Options{Folders: tool.Single(dir), FilesOnly: true, Wrote: func(_ context.Context, name string, args tool.Args) {
		wrote = append(wrote, name+" "+args.String("path"))
	}}
	approve := true
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	ss, err := New(opts).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	var asked []*mcp.ElicitParams
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, answering(&asked, func(*mcp.ElicitParams) *mcp.ElicitResult {
		if approve {
			return &mcp.ElicitResult{Action: "accept", Content: map[string]any{}}
		}
		return &mcp.ElicitResult{Action: "decline", Content: map[string]any{}}
	})).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })

	call(t, cs, "write_file", map[string]any{"path": "a.md", "content": "x\n"})
	call(t, cs, "read_file", map[string]any{"path": "a.md"})
	approve = false
	call(t, cs, "write_file", map[string]any{"path": "b.md", "content": "y\n"})
	if !slices.Equal(wrote, []string{"write_file a.md"}) {
		t.Errorf("書いたときだけ: %v", wrote)
	}
}
