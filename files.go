package aibow

// files.go: ファイルを読み込む(0030)。入力欄で読み込んだファイルを発言に付け、モデルに渡すときに発言の文へ展開する。
//
// テキストのファイルは、モデルから見ればただの文字。書式もエンジンも何も知らず、会話の仕組みが
// プロンプトを作るとき(promptMessages)に、発言の頭へ「ファイル名 + 中身」を書き足すだけ。
// 保存は発言とは別の表(store の files)にしておき、画面の吹き出しには札(ファイル名)だけを出す。

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
	"uuid"
)

// File は、発言に付けて読み込んだファイル1つ(0030)。
type File struct {
	ID   uuid.UUID `json:"id"`   // UUIDv7。発言の中の並びもこれで決まる
	Kind string    `json:"kind"` // 種類。FileText(画像は段2で足す)
	Name string    `json:"name"` // ファイル名(フォルダは含まない)。モデルにも見せる
	// Format は形式。テキストは、読んだときの文字コード(utf-8 / utf-8-bom / utf-16le / utf-16be)。
	// Data はもう UTF-8 にしてあるので、記録のためだけに残す
	Format string `json:"format"`
	Hash   string `json:"hash"` // Data の SHA-256(16進)。画像の KV キャッシュの比べ方(段2)にも使う
	Data   []byte `json:"data"` // 中身。テキストは UTF-8(BOM を外したもの)
}

// ファイルの種類(File.Kind)
const (
	FileText = "text"
)

// 読み込むファイルの上限(決定事項 18)。トークン数の確かめ(CheckFile)とは別に、DB と転送を守るための上限。
// 1 MB のテキストは数十万トークンになり、どのみち会話の上限には入らない
const (
	FileMaxBytes   = 1 << 20
	FilesPerSend   = 10
	fileMaxNameLen = 255
)

// ReadFile は、読み込んだファイルの中身を確かめて File にする(まだ保存はしない。ID は新しく振る)。
//
// テキストかどうかは拡張子ではなく中身で決める(決定事項 8)。決まりは作業フォルダのツール(tool/files.go の readText)と同じで、
// NUL を含まず UTF-8 として読めること。それに加えて、BOM のある UTF-16 だけは読んで UTF-8 にする(決定事項 7)。
// Shift_JIS などは標準ライブラリで読めない(golang.org/x/text が要る)ので、UTF-8 で保存し直してもらう。
func ReadFile(name string, data []byte) (File, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return File{}, errors.New("ファイル名がありません")
	case len(name) > fileMaxNameLen:
		return File{}, fmt.Errorf("ファイル名が長すぎます(%d バイトまで)", fileMaxNameLen)
	case len(data) > FileMaxBytes:
		return File{}, fmt.Errorf("%s は大きすぎるため読み込めません(%s。1 つ %d MB まで)。大きいファイルは作業フォルダに置き、read_file で範囲を指定して読ませてください",
			name, sizeText(len(data)), FileMaxBytes>>20)
	case len(data) == 0:
		return File{}, fmt.Errorf("%s は空のファイルです", name)
	}
	text, enc, err := decodeText(data)
	if err != nil {
		return File{}, fmt.Errorf("%s を読み込めません: %w", name, err)
	}
	b := []byte(text)
	sum := sha256.Sum256(b)
	return File{ID: uuid.NewV7(), Kind: FileText, Name: name, Format: enc, Hash: hex.EncodeToString(sum[:]), Data: b}, nil
}

// BOM(バイト順マーク)。ファイルの先頭にあれば、文字コードがはっきり分かる
var (
	bomUTF8    = []byte{0xEF, 0xBB, 0xBF}
	bomUTF16LE = []byte{0xFF, 0xFE}
	bomUTF16BE = []byte{0xFE, 0xFF}
)

// decodeText は data をテキストとして読み、UTF-8 の文字列と、読んだ文字コードの名前を返す。
func decodeText(data []byte) (string, string, error) {
	switch {
	case bytes.HasPrefix(data, bomUTF8):
		data = data[len(bomUTF8):]
		if err := checkUTF8(data); err != nil {
			return "", "", err
		}
		return string(data), "utf-8-bom", nil
	case bytes.HasPrefix(data, bomUTF16LE):
		s, err := decodeUTF16(data[2:], binary.LittleEndian)
		return s, "utf-16le", err
	case bytes.HasPrefix(data, bomUTF16BE):
		s, err := decodeUTF16(data[2:], binary.BigEndian)
		return s, "utf-16be", err
	}
	if err := checkUTF8(data); err != nil {
		return "", "", err
	}
	return string(data), "utf-8", nil
}

// checkUTF8 は tool/files.go の readText と同じ決まり。NUL はテキストのファイルにはまず出てこないので、バイナリ(画像・実行ファイルなど)の目印にする。
// BOM の無い UTF-16 も NUL を含むので、ここでバイナリとして断る(BOM が無いと、どちらの並びかを決められない)
func checkUTF8(data []byte) error {
	if bytes.IndexByte(data, 0) >= 0 {
		return errors.New("テキストのファイルではありません(バイナリのファイルか、BOM の無い UTF-16 です)")
	}
	if !utf8.Valid(data) {
		return errors.New("文字コードが UTF-8 ではありません(Shift_JIS などは読めません)。UTF-8 で保存し直してください")
	}
	return nil
}

// decodeUTF16 は、BOM を外した UTF-16 を UTF-8 の文字列にする。
// 2 バイトずつの数(コードユニット)に読み、サロゲートペア(2 つで1文字になる組)は utf16.Decode がまとめる。
func decodeUTF16(data []byte, order binary.ByteOrder) (string, error) {
	if len(data)%2 != 0 {
		return "", errors.New("UTF-16 のファイルが途中で切れています(バイト数が奇数です)")
	}
	units := make([]uint16, len(data)/2)
	for i := range units {
		units[i] = order.Uint16(data[2*i:])
	}
	runes := utf16.Decode(units)
	for _, r := range runes {
		if r == 0 {
			return "", errors.New("テキストのファイルではありません(NUL の文字を含みます)")
		}
		if r == utf8.RuneError {
			// 対になっていないサロゲートは utf16.Decode が U+FFFD にする。黙って置き換えず、読めないことを伝える
			return "", errors.New("UTF-16 として読めない部分があります。UTF-8 で保存し直してください")
		}
	}
	return string(runes), nil
}

// sizeText は大きさを人が読める形にする(エラーの文に使う)。
func sizeText(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d バイト", n)
}

// withFiles は、発言の文の前に、付いているテキストのファイルを展開したもの(決定事項 5・19)。
// ファイルが無ければ text のまま(今までの発言と同じ文字列。KV キャッシュの再利用も崩さない)。
//
//	ファイル: main.go
//	```
//	(中身)
//	```
//
//	(発言の文)
func withFiles(text string, files []File) string {
	var b strings.Builder
	for _, f := range files {
		if f.Kind != FileText {
			continue
		}
		body := string(f.Data)
		fence := fenceFor(body)
		b.WriteString("ファイル: " + f.Name + "\n" + fence + "\n" + body)
		if !strings.HasSuffix(body, "\n") {
			b.WriteString("\n")
		}
		b.WriteString(fence + "\n\n")
	}
	if b.Len() == 0 {
		return text
	}
	b.WriteString(text)
	return b.String()
}

// fenceFor は、中身を囲むコードブロックの囲み。中身にバッククォートが3つ以上続くところがあれば、それより1つ長くする
// (Markdown の決まりで、囲みは同じ長さ以上のバッククォートの並びでしか閉じない。中身の ``` で囲みが閉じないように)。
func fenceFor(body string) string {
	longest, run := 0, 0
	for _, r := range body {
		if r == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	return strings.Repeat("`", max(3, longest+1))
}

// filesNote は、要約に渡すときにファイルの代わりに書く文(決定事項 4)。中身は渡さない(重いので。要約の役目は話の流れを残すこと)。
func filesNote(files []File) string {
	var parts []string
	for _, f := range files {
		parts = append(parts, "[ファイル: "+f.Name+"]")
	}
	return strings.Join(parts, " ")
}

// renewFiles は、ファイルに新しい ID を振った写し(会話を写す・送り直すで、別の発言に付け直すとき)。
// 中身のバイト列は同じものを指す(書き換えないので写さなくてよい)。
func renewFiles(files []File) []File {
	if len(files) == 0 {
		return nil
	}
	out := make([]File, len(files))
	for i, f := range files {
		f.ID = uuid.NewV7()
		out[i] = f
	}
	return out
}

// checkFiles は、送る発言に付けるファイルを確かめる(画面を通さない呼び出しでも、上限と形を守るため)。
func checkFiles(files []File) error {
	if len(files) > FilesPerSend {
		return fmt.Errorf("1 つの発言に付けられるファイルは %d 個までです(%d 個)", FilesPerSend, len(files))
	}
	for _, f := range files {
		if f.Kind != FileText {
			return fmt.Errorf("%s: 種類 %q のファイルはまだ読み込めません", f.Name, f.Kind)
		}
		if len(f.Data) > FileMaxBytes {
			return fmt.Errorf("%s は大きすぎます(1 つ %d MB まで)", f.Name, FileMaxBytes>>20)
		}
		if err := checkUTF8(f.Data); err != nil {
			return fmt.Errorf("%s: %w", f.Name, err)
		}
	}
	return nil
}

// FileCheck は、読み込んだファイルを確かめた結果(画面の札に出す)。
type FileCheck struct {
	File   File `json:"file"`
	Tokens int  `json:"tokens"` // このファイルを足すと増えるトークン数(展開した見出しと囲みを含む)
}

// CheckFile は、読み込んだファイルを確かめて、会話の上限に入るかを数える(決定事項 6)。
//
// 入らなければ、黙って切らずに「○○ トークンで、残りは △△ です」のエラーにする(大きいものは作業フォルダの read_file へ案内する)。
// 数えるのは、今の履歴 + 付けるつもりのファイル(pending)+ このファイルの発言を作ったとき、compact しても必ず渡す部分(turnTokens)。
// 文はまだ入力中なので数えない(送信のときに checkFits でもう一度確かめる)。
// id がゼロなら、まだ作っていない新しい会話。新しい会話の画面で変えた設定 draft で数える(nil なら新しい会話の既定)。
func (cs *Conversations) CheckFile(ctx context.Context, id uuid.UUID, draft *ConversationSettings, name string, data []byte, pending []File) (FileCheck, error) {
	f, err := ReadFile(name, data)
	if err != nil {
		return FileCheck{}, err
	}
	if len(pending) >= FilesPerSend {
		return FileCheck{}, fmt.Errorf("1 つの発言に付けられるファイルは %d 個までです", FilesPerSend)
	}
	var set ConversationSettings
	var history []StoredMessage
	if id == (uuid.UUID{}) {
		if draft != nil {
			set, err = cs.withLimits(ctx, *draft)
		} else {
			set, err = cs.Defaults(ctx)
		}
		if err != nil {
			return FileCheck{}, err
		}
	} else {
		conv, saved, err := cs.Get(ctx, id)
		if err != nil {
			return FileCheck{}, err
		}
		set, history = conv.Settings, forModel(saved)
	}
	limit := cs.limitsOf(set).history
	user := func(files []File) []StoredMessage {
		return append(slices.Clip(history), StoredMessage{Role: "user", Files: files})
	}
	before, err := cs.turnTokens(set, user(pending))
	if err != nil {
		return FileCheck{}, err
	}
	after, err := cs.turnTokens(set, user(append(slices.Clip(pending), f)))
	if err != nil {
		return FileCheck{}, err
	}
	tokens := after - before
	if after > limit {
		return FileCheck{}, fmt.Errorf("%s は %d トークンで、会話の上限(%d トークン)の残りは %d トークンです。"+
			"大きいファイルは作業フォルダに置き、read_file で範囲を指定して読ませてください", f.Name, tokens, limit, max(0, limit-before))
	}
	return FileCheck{File: f, Tokens: tokens}, nil
}
