package client

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/secondarykey/aibow/tool"
)

// aibow の中のサーバーのツールは、組み込みと同じ名前・同じ定義で、MCP のサーバーのツール(tool.Remote)には見せない
func TestLocalTools(t *testing.T) {
	l := NewLocal(Options{Version: "test"})
	t.Cleanup(l.Close)
	dir := t.TempDir()

	names := func(tools []tool.Tool) []string {
		var out []string
		for _, x := range tools {
			out = append(out, x.Def().Name)
			if _, remote := x.(tool.Remote); remote {
				t.Errorf("%s は MCP のサーバーのツールに見せない", x.Def().Name)
			}
		}
		slices.Sort(out)
		return out
	}
	if got := names(l.Tools("")); !slices.Equal(got, []string{"calculate", "get_current_time"}) {
		t.Errorf("作業フォルダが無ければ時刻と計算だけ: %v", got)
	}
	tools := l.Tools(dir)
	if got := names(tools); !slices.Equal(got, []string{"calculate", "edit_file", "get_current_time", "list_files", "read_file", "run_command", "write_file"}) {
		t.Errorf("作業フォルダのツールも: %v", got)
	}
	if d := tool.Find(tools, "read_file").Def(); d.Description != tool.Find(tool.Files(dir), "read_file").Def().Description {
		t.Error("定義は組み込みと同じ")
	}
	for _, name := range []string{"write_file", "edit_file", "run_command"} {
		if _, ok := tool.Find(tools, name).(tool.SelfConfirming); !ok {
			t.Errorf("%s は確認をサーバーに任せる", name)
		}
	}
	if _, ok := tool.Find(tools, "read_file").(tool.Confirmable); ok {
		t.Error("読むツールは確認の要るツールにしない")
	}
	if got := tool.Find(tools, "run_command").(tool.TimeLimiter).TimeLimit(); got != 125*time.Second {
		t.Errorf("run_command の上限はサーバーの上限 + 5 秒: %v", got)
	}
	if l.Tools(dir)[0] != tools[0] {
		t.Error("同じ作業フォルダなら、つなぎ直さない")
	}
}

// 書き換えるツールは MCP 越しに実行し、サーバーが送ってきた差分で聞く(カードにサーバーの名前は出さない)
func TestLocalWrite(t *testing.T) {
	l := NewLocal(Options{Version: "test"})
	t.Cleanup(l.Close)
	dir := t.TempDir()
	write := tool.Find(l.Tools(dir), "write_file").(tool.Interactive)

	var got []tool.Preview
	out, err := write.RunInteractive(context.Background(), tool.Args{"path": "a.txt", "content": "hello\n"}, func(_ context.Context, pv tool.Preview) (tool.Answer, error) {
		got = append(got, pv)
		return tool.Answer{Approved: true}, nil
	})
	if err != nil || out == "" {
		t.Fatalf("書く: %q, %v", out, err)
	}
	if len(got) != 1 || got[0].Path != "a.txt" || got[0].Diff == "" || got[0].Server != "" {
		t.Errorf("サーバーの差分で1回聞く(サーバーの名前は出さない): %+v", got)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(b) != "hello\n" {
		t.Errorf("ファイル: %q", b)
	}

	read := tool.Find(l.Tools(dir), "read_file")
	if out, err := read.Run(context.Background(), tool.Args{"path": "a.txt"}); err != nil || out != "1\thello" {
		t.Errorf("読むツールは聞かずに MCP 越しに実行: %q, %v", out, err)
	}
}

// 閉じたあとは、tool パッケージのツールを直接呼ぶものを返す(予備)
func TestLocalClosed(t *testing.T) {
	l := NewLocal(Options{Version: "test"})
	l.Close()
	tools := l.Tools(t.TempDir())
	if _, ok := tool.Find(tools, "write_file").(tool.SelfConfirming); ok || len(tools) != 7 {
		t.Errorf("直接呼ぶツール: %v", tools)
	}
}
