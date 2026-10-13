package aibow

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"aibow/tool"
)

// fakeRemote は MCP のサーバーの代わり(本物は mcpclient.Manager)。Start で起動したことにして、ツールを出す。
type fakeRemote struct {
	tools    []tool.Tool
	startErr error
	started  bool
	workDirs []string // Start に渡された作業フォルダ
}

func (r *fakeRemote) Start(_ context.Context, workDir string) ([]string, []error) {
	r.workDirs = append(r.workDirs, workDir)
	if r.startErr != nil {
		return nil, []error{r.startErr}
	}
	if r.started {
		return nil, nil
	}
	r.started = true
	return []string{"fake"}, nil
}

func (r *fakeRemote) Tools(string) []tool.Tool {
	if !r.started {
		return nil
	}
	return r.tools
}

// remoteTool は MCP のサーバーのツールの代わり。実行したら、受け取った引数を返す。
type remoteTool struct {
	name string
	ran  bool
}

func (t *remoteTool) Def() tool.Def {
	return tool.Def{Name: t.name, Description: "Remote tool.", Parameters: []byte(`{"type": "object", "properties": {"paths": {"type": "array"}}}`)}
}
func (t *remoteTool) Server() string { return "fake" }
func (t *remoteTool) Preview(args tool.Args) (tool.Preview, error) {
	return tool.Preview{Note: "MCP", Server: "fake", Args: "{}"}, nil
}
func (t *remoteTool) Run(_ context.Context, args tool.Args) (string, error) {
	t.ran = true
	p, _ := args["paths"].([]any)
	return strings.Repeat("x", len(p)), nil
}

// 送信の前にサーバーを起動し、そのツールをプロンプトに入れる。実行の前に確認し、結果にサーバーの名前を残す
func TestRemoteTools(t *testing.T) {
	g := &fakeGen{replies: []fakeReply{
		{toolCalls: []ToolCall{toolCall("remote_list", "paths", `["a", "b"]`)}},
		{content: "2つあります"},
	}}
	cs, c, dir := newWorkDirConversation(t, g)
	rt := &remoteTool{name: "remote_list"}
	clash := &remoteTool{name: "read_file"} // 組み込みのツールと同じ名前
	r := &fakeRemote{tools: []tool.Tool{rt, clash}}
	cs.SetRemoteTools(r)

	var req *ConfirmRequest
	evs := sendAnswering(cs, context.Background(), c, "並べて", func(q *ConfirmRequest) bool {
		req = q
		return cs.Confirm(c.ID, q.ID, true, "") == nil
	})
	if errs := errorsOf(evs); len(errs) != 0 {
		t.Fatalf("エラー: %v", errs)
	}
	if !slices.Equal(r.workDirs, []string{dir}) {
		t.Errorf("会話の作業フォルダで起動する: %v", r.workDirs)
	}

	var names []string
	for _, d := range g.calls[0].tools {
		names = append(names, d.Name)
	}
	if !slices.Contains(names, "remote_list") || slices.Index(names, "remote_list") < slices.Index(names, "run_command") {
		t.Errorf("MCP のツールは組み込みの後ろに並ぶ: %v", names)
	}
	if n := len(slices.DeleteFunc(slices.Clone(names), func(s string) bool { return s != "read_file" })); n != 1 {
		t.Errorf("名前が重なったツールは1つだけ(組み込み): %v", names)
	}

	if req == nil || req.Preview.Server != "fake" {
		t.Errorf("MCP のツールは確認する: %+v", req)
	}
	rs := toolResults(t, cs, c)
	if len(rs) != 1 || rs[0].Content != "xx" || rs[0].ToolResult.Server != "fake" || !rt.ran || clash.ran {
		t.Errorf("結果: %+v", rs)
	}
}

// サーバーの名前を落として呼んだら、正しい名前を「〜のことですか?」でモデルに返す(勝手に読み替えない)
func TestRemoteToolDidYouMean(t *testing.T) {
	g := &fakeGen{replies: []fakeReply{
		{toolCalls: []ToolCall{toolCall("list_directory", "path", ".")}},
		{content: "呼び直します"},
	}}
	cs, c, _ := newWorkDirConversation(t, g)
	rt := &remoteTool{name: "filesystem__list_directory"}
	cs.SetRemoteTools(&fakeRemote{tools: []tool.Tool{rt}})

	evs := sendAnswering(cs, context.Background(), c, "並べて", func(*ConfirmRequest) bool { return false })
	if errs := errorsOf(evs); len(errs) != 0 {
		t.Fatalf("エラー: %v", errs)
	}
	rs := toolResults(t, cs, c)
	if len(rs) != 1 || !strings.Contains(rs[0].Content, "ツール list_directory はありません。filesystem__list_directory のことですか?") || rt.ran {
		t.Errorf("結果: %+v", rs)
	}
}

// 起動できないサーバーはお知らせに出し、そのサーバー無しで送信を続ける。ツールが OFF なら起動しない
func TestRemoteToolsStartError(t *testing.T) {
	g := &fakeGen{content: "こんにちは"}
	cs, _, c := newToolConversation(t, g)
	r := &fakeRemote{startErr: errors.New("MCP のサーバー fake を起動できません: 見つかりません")}
	cs.SetRemoteTools(r)

	var notices []string
	cs.Send(context.Background(), c.ID, "やあ", func(ev ChatEvent) {
		if ev.Type == "notice" {
			notices = append(notices, ev.Text)
		}
	})
	if len(notices) != 1 || !strings.Contains(notices[0], "起動できません") {
		t.Errorf("お知らせ: %v", notices)
	}
	if len(g.calls) != 1 {
		t.Errorf("送信は続ける: %d", len(g.calls))
	}

	c.Settings.Tools = false
	if err := cs.UpdateSettings(c.ID, c.Settings); err != nil {
		t.Fatal(err)
	}
	cs.Send(context.Background(), c.ID, "もう一度", func(ChatEvent) {})
	if len(r.workDirs) != 1 {
		t.Errorf("ツールが OFF の会話では起動しない: %v", r.workDirs)
	}
}
