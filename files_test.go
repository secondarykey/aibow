package aibow

import (
	"bytes"
	"context"
	"encoding/binary"
	"strings"
	"testing"
	"unicode/utf16"
	"uuid"
)

// テキストかどうかと文字コードは中身で決める(0030 の決定事項 7・8)。UTF-8(BOM ありも)と BOM のある UTF-16 だけを読む
func TestReadFileEncodings(t *testing.T) {
	utf16With := func(order binary.AppendByteOrder, bom []byte, s string) []byte {
		b := bytes.Clone(bom)
		for _, u := range utf16.Encode([]rune(s)) {
			b = order.AppendUint16(b, u)
		}
		return b
	}
	text := "こんにちは 🍣\nfunc main() {}\n" // 🍣 は UTF-16 でサロゲートペアになる
	for _, c := range []struct {
		name, format string
		data         []byte
	}{
		{"UTF-8", "utf-8", []byte(text)},
		{"UTF-8 の BOM", "utf-8-bom", append(bytes.Clone(bomUTF8), text...)},
		{"UTF-16LE", "utf-16le", utf16With(binary.LittleEndian, bomUTF16LE, text)},
		{"UTF-16BE", "utf-16be", utf16With(binary.BigEndian, bomUTF16BE, text)},
	} {
		f, err := ReadFile("a.txt", c.data)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if string(f.Data) != text || f.Format != c.format || f.Kind != FileText || f.Hash == "" {
			t.Errorf("%s: %q %s %s", c.name, f.Data, f.Format, f.Kind)
		}
	}
}

// 読めないものは、理由の分かるエラーにする(黙って化けた文字を渡さない)
func TestReadFileErrors(t *testing.T) {
	for _, c := range []struct {
		name, want string
		data       []byte
	}{
		{"Shift_JIS", "UTF-8 で保存し直してください", []byte{0x82, 0xa0, 0x82, 0xa2}}, // 「あい」
		{"バイナリ", "テキストのファイルではありません", []byte{0x89, 'P', 'N', 'G', 0, 0, 0, 0}},
		{"BOM の無い UTF-16", "テキストのファイルではありません", []byte{'a', 0, 'b', 0}},
		{"奇数バイトの UTF-16", "途中で切れています", []byte{0xFF, 0xFE, 'a'}},
		{"対になっていないサロゲート", "UTF-16 として読めない", []byte{0xFF, 0xFE, 0x00, 0xD8, 'a', 0}},
		{"空", "空のファイル", nil},
		{"大きすぎる", "大きすぎる", bytes.Repeat([]byte("a"), FileMaxBytes+1)},
	} {
		_, err := ReadFile("x.txt", c.data)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v(期待: %q を含む)", c.name, err, c.want)
		}
	}
	if _, err := ReadFile("x.txt", bytes.Repeat([]byte("a"), FileMaxBytes)); err != nil {
		t.Errorf("上限ちょうどは読める: %v", err)
	}
}

// 展開: ファイルを発言の文の前に置き、「ファイル: 名前」とコードブロックで囲む(決定事項 19)
func TestWithFiles(t *testing.T) {
	if got := withFiles("質問", nil); got != "質問" {
		t.Errorf("ファイルが無ければそのまま: %q", got)
	}
	files := []File{
		{Kind: FileText, Name: "a.go", Data: []byte("package a\n")},
		{Kind: FileText, Name: "b.md", Data: []byte("# B\n```go\nx\n```")}, // 中身に ``` があり、最後に改行が無い
	}
	want := "ファイル: a.go\n```\npackage a\n```\n\n" +
		"ファイル: b.md\n````\n# B\n```go\nx\n```\n````\n\n" +
		"質問"
	if got := withFiles("質問", files); got != want {
		t.Errorf("展開:\n%s\n期待:\n%s", got, want)
	}
}

// 送ったファイルは保存され、モデルには展開して渡す。吹き出し(保存した発言の Content)には中身を入れない
func TestSendWithFiles(t *testing.T) {
	g := &fakeGen{content: "答え"}
	cs, _ := newTestConversations(t, g)
	c := mustCreate(t, cs)
	f, err := ReadFile("main.go", []byte("package main\n"))
	if err != nil {
		t.Fatal(err)
	}
	evs := collect(func(emit func(ChatEvent)) {
		cs.Send(context.Background(), c.ID, "何をしていますか", []File{f}, emit)
	})
	if errs := errorsOf(evs); len(errs) != 0 {
		t.Fatalf("エラー: %v", errs)
	}
	last := g.gotMessages[len(g.gotMessages)-1]
	if want := "ファイル: main.go\n```\npackage main\n```\n\n何をしていますか"; last.Content != want {
		t.Errorf("モデルに渡した発言: %q", last.Content)
	}
	conv, ms, _ := cs.Get(ctx, c.ID)
	if ms[0].Content != "何をしていますか" || len(ms[0].Files) != 1 || ms[0].Files[0].Name != "main.go" {
		t.Errorf("保存した発言: %+v", ms[0])
	}
	if conv.Title != "何をしていますか" {
		t.Errorf("タイトル: %q", conv.Title)
	}

	// ファイルだけでも送れる。タイトルはファイル名
	c2 := mustCreate(t, cs)
	f2, _ := ReadFile("memo.txt", []byte("メモ"))
	cs.Send(context.Background(), c2.ID, "", []File{f2}, func(ChatEvent) {})
	if conv, ms, _ := cs.Get(ctx, c2.ID); len(ms) != 2 || conv.Title != "memo.txt" {
		t.Errorf("ファイルだけの送信: %d 件・タイトル %q", len(ms), conv.Title)
	}

	// 上限より多いファイルは断る(画面を通さない呼び出しでも)
	many := make([]File, FilesPerSend+1)
	for i := range many {
		many[i], _ = ReadFile("x.txt", []byte("x"))
	}
	if errs := errorsOf(collect(func(emit func(ChatEvent)) { cs.Send(context.Background(), c2.ID, "多い", many, emit) })); len(errs) != 1 {
		t.Errorf("%d 個は断る: %v", len(many), errs)
	}
}

// 送り直すときは、直すのは文だけで、元の発言のファイルを付け直す。作り直すときはそのまま(決定事項 21)
func TestResendKeepsFiles(t *testing.T) {
	g := &fakeGen{content: "A"}
	cs, _ := newTestConversations(t, g)
	c := mustCreate(t, cs)
	f, _ := ReadFile("a.txt", []byte("中身"))
	cs.Send(context.Background(), c.ID, "Q", []File{f}, func(ChatEvent) {})

	cs.Regenerate(context.Background(), c.ID, func(ChatEvent) {})
	if _, ms, _ := cs.Get(ctx, c.ID); len(ms[0].Files) != 1 || ms[0].Files[0].ID != f.ID {
		t.Errorf("作り直しでファイルが変わった: %+v", ms[0].Files)
	}

	cs.Resend(context.Background(), c.ID, "Q 直した", func(ChatEvent) {})
	_, ms, _ := cs.Get(ctx, c.ID)
	if len(ms) != 2 || ms[0].Content != "Q 直した" || len(ms[0].Files) != 1 || ms[0].Files[0].Name != "a.txt" || ms[0].Files[0].ID == f.ID {
		t.Errorf("送り直した発言: %+v", ms[0])
	}
	if !strings.HasPrefix(g.gotMessages[len(g.gotMessages)-1].Content, "ファイル: a.txt\n") {
		t.Errorf("送り直しでもファイルを渡す: %q", g.gotMessages[len(g.gotMessages)-1].Content)
	}
}

// 会話の上限に入らないファイルは、トークン数と残りを書いて断る(決定事項 6)。入るものはトークン数を返す
func TestCheckFile(t *testing.T) {
	g := &fakeGen{content: "A"}
	cs, _ := newTestConversations(t, g)
	c := mustCreate(t, cs)
	setLimits(t, cs, &c, 200, 100) // fakeGen は1文字を1トークンと数える

	got, err := cs.CheckFile(ctx, c.ID, nil, "a.txt", []byte("短い"), nil)
	if err != nil {
		t.Fatal(err)
	}
	// 増えるのは、見出し・囲み・中身(「ファイル: a.txt\n```\n短い\n```」)。文はまだ無いので、後ろの空行は
	// Qwen3.5 の書式が発言の前後の空白を削る分だけ数えない
	if want := len([]rune("ファイル: a.txt\n```\n短い\n```")); got.Tokens != want || got.File.Name != "a.txt" {
		t.Errorf("トークン数 %d(期待: %d)%+v", got.Tokens, want, got.File)
	}

	_, err = cs.CheckFile(ctx, c.ID, nil, "big.txt", []byte(strings.Repeat("長", 300)), nil)
	if err == nil || !strings.Contains(err.Error(), "トークンで、会話の上限(200 トークン)の残りは") {
		t.Errorf("入らないファイル: %v", err)
	}

	// 付けるつもりのファイルも一緒に数える
	half := []byte(strings.Repeat("半", 70))
	first, err := cs.CheckFile(ctx, c.ID, nil, "1.txt", half, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cs.CheckFile(ctx, c.ID, nil, "2.txt", half, []File{first.File}); err == nil {
		t.Error("付けるつもりのファイルと合わせて入らなければ断る")
	}

	// 新しい会話の画面(ID がゼロ)は、画面の設定で数える
	draft := testDefaults
	draft.HistoryTokens = 50
	if _, err := cs.CheckFile(ctx, uuid.UUID{}, &draft, "1.txt", half, nil); err == nil {
		t.Error("新しい会話の画面の設定の上限で数える")
	}
	if _, err := cs.CheckFile(ctx, uuid.UUID{}, nil, "1.txt", half, nil); err != nil {
		t.Errorf("新しい会話の既定の上限なら入る: %v", err)
	}
}

// 要約に渡すときは、ファイルの中身ではなく「[ファイル: 名前]」にする(決定事項 4)
func TestSummaryMessagesFiles(t *testing.T) {
	msgs := summaryMessages("", []StoredMessage{
		{Role: "user", Content: "読んで", Files: []File{{Kind: FileText, Name: "a.go", Data: []byte("秘密の中身")}}},
		{Role: "assistant", Content: "読みました"},
	}, 1000)
	body := msgs[1].Content
	if !strings.Contains(body, "ユーザー: [ファイル: a.go] 読んで\n") || strings.Contains(body, "秘密の中身") {
		t.Errorf("要約に渡す文:\n%s", body)
	}
}
