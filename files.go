package aibow

// files.go: ファイルを読み込む(0030)。入力欄で読み込んだファイルを発言に付け、モデルに渡すときに発言の文へ展開する。
//
// テキストのファイルは、モデルから見ればただの文字。書式もエンジンも何も知らず、会話の仕組みが
// プロンプトを作るとき(promptMessages)に、発言の頭へ「ファイル名 + 中身」を書き足すだけ。
// 画像は文字にできないので、発言の画像の列(format.Message.Images)としてエンジンに渡す。書式は画像の場所に印を書き、
// エンジンが画像エンコーダー(mmproj)で画像トークンの埋め込みにして読ませる(engine の vision.go・chunks.go)。
// 保存は発言とは別の表(store の files)にしておき、画面の吹き出しには札(ファイル名・画像は小さな表示)だけを出す。

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

	"github.com/secondarykey/aibow/engine"
	"github.com/secondarykey/aibow/format"
	"github.com/secondarykey/aibow/log"
)

// File は、発言に付けて読み込んだファイル1つ(0030)。
type File struct {
	ID   uuid.UUID `json:"id"`   // UUIDv7。発言の中の並びもこれで決まる
	Kind string    `json:"kind"` // 種類。FileText / FileImage
	Name string    `json:"name"` // ファイル名(フォルダは含まない)。テキストはモデルにも見せる
	// Format は形式。テキストは、読んだときの文字コード(utf-8 / utf-8-bom / utf-16le / utf-16be)。
	// Data はもう UTF-8 にしてあるので、記録のためだけに残す。画像は MIME の形式(image/png など。画面の表示に使う)
	Format string `json:"format"`
	// Hash は Data の SHA-256(16進)。画像は、エンジンが KV キャッシュに入っている画像と同じかを、これ(画像の ID)で比べる
	Hash string `json:"hash"`
	Data []byte `json:"data"` // 中身。テキストは UTF-8(BOM を外したもの)。画像は読み込んだファイルのまま
	// Discarded は、ユーザーが送ったあとに捨てたファイル(決定事項 26〜28)。中身(Data)は消してあり、名前と種類だけが残る。
	// モデルには、発言の文の前に「[画像: 名前(消去済み)]」のような置き換えの文字で渡す
	Discarded bool `json:"discarded,omitempty"`
}

// ファイルの種類(File.Kind)
const (
	FileText  = "text"
	FileImage = "image"
)

// 読み込むファイルの上限(決定事項 18・24)。トークン数の確かめ(CheckFile)とは別に、DB と転送を守るための上限。
// 1 MB のテキストは数十万トークンになり、どのみち会話の上限には入らない。画像は大きくても、mtmd が画像1枚のトークン数の上限に縮める
// (スマートフォンの写真の 3〜8 MB が入る大きさ)。1つの発言に付けられる数は、テキストと画像を合わせて数える
const (
	FileMaxBytes   = 1 << 20
	ImageMaxBytes  = 10 << 20
	FilesPerSend   = 10
	fileMaxNameLen = 255
)

// MessageMaxBytes は、発言1件を HTTP で運ぶときの本文の上限(ブラウザの送信・aibow-server の保存の口)。
// 読み込んだファイルを最大まで付けた発言(画像 10 MB × 10 個)を、JSON の base64(4/3 倍)で運べる大きさに、文などの分を足したもの。
// 大きな画像を何枚も付けると、サーバーは JSON を読むあいだ、その数倍のメモリを使う
const MessageMaxBytes = FilesPerSend*ImageMaxBytes*4/3 + 16<<20

// imageFormat は、中身の先頭の印(マジックナンバー)で画像の形式を決める(決定事項 9。拡張子では決めない)。画像でなければ空。
// 読めるのは llama.cpp(stb_image)が読める PNG・JPEG・GIF・BMP。WebP は stb_image が読めないので、画像だと分かるがエラーにする(webp=true)。
func imageFormat(data []byte) (format string, webp bool) {
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png", false
	case bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF}):
		return "image/jpeg", false
	case bytes.HasPrefix(data, []byte("GIF87a")), bytes.HasPrefix(data, []byte("GIF89a")):
		return "image/gif", false
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "", true
	case len(data) >= 6 && string(data[:2]) == "BM" && int(binary.LittleEndian.Uint32(data[2:6])) == len(data):
		// BMP は印が2文字だけなので、「BM」で始まるテキストと見分けるため、ヘッダーに書かれたファイルの大きさも確かめる
		return "image/bmp", false
	}
	return "", false
}

// ReadFile は、読み込んだファイルの中身を確かめて File にする(まだ保存はしない。ID は新しく振る)。
//
// 画像かテキストかは拡張子ではなく中身で決める(決定事項 8・9)。先頭が画像の印なら画像、そうでなければテキストとして読む。
// テキストの決まりは作業フォルダのツール(tool/files.go の readText)と同じで、NUL を含まず UTF-8 として読めること。
// それに加えて、BOM のある UTF-16 だけは読んで UTF-8 にする(決定事項 7)。
// Shift_JIS などは標準ライブラリで読めない(golang.org/x/text が要る)ので、UTF-8 で保存し直してもらう。
func ReadFile(name string, data []byte) (File, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return File{}, errors.New("ファイル名がありません")
	case len(name) > fileMaxNameLen:
		return File{}, fmt.Errorf("ファイル名が長すぎます(%d バイトまで)", fileMaxNameLen)
	case len(data) == 0:
		return File{}, fmt.Errorf("%s は空のファイルです", name)
	}
	if img, webp := imageFormat(data); img != "" || webp {
		switch {
		case webp:
			return File{}, fmt.Errorf("%s は WebP の画像です。WebP は読めません。PNG か JPEG で保存し直してください", name)
		case len(data) > ImageMaxBytes:
			return File{}, fmt.Errorf("%s は大きすぎるため読み込めません(%s。画像は 1 枚 %d MB まで)", name, sizeText(len(data)), ImageMaxBytes>>20)
		}
		sum := sha256.Sum256(data)
		return File{ID: uuid.NewV7(), Kind: FileImage, Name: name, Format: img, Hash: hex.EncodeToString(sum[:]), Data: data}, nil
	}
	if len(data) > FileMaxBytes {
		return File{}, fmt.Errorf("%s は大きすぎるため読み込めません(%s。1 つ %d MB まで)。大きいファイルは作業フォルダに置き、read_file で範囲を指定して読ませてください",
			name, sizeText(len(data)), FileMaxBytes>>20)
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
		if f.Discarded {
			// 捨てたファイルは、何があったかだけを書く(「この画像は?」のような発言だけが残ると、モデルが戸惑うため)
			b.WriteString(discardedNote(f) + "\n")
			continue
		}
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
	if s := b.String(); !strings.HasSuffix(s, "\n\n") {
		b.WriteString("\n") // 置き換えの文字のあとも、発言の文との間を1行あける
	}
	b.WriteString(text)
	return b.String()
}

// discardedNote は、捨てたファイルの代わりにモデルに渡す文字(決定事項 28)。
func discardedNote(f File) string {
	if f.Kind == FileImage {
		return "[画像: " + f.Name + "(消去済み)]"
	}
	return "[ファイル: " + f.Name + "(消去済み)]"
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

// imagesOf は、発言の画像を、エンジンに渡す形(format.Image)にする。ID は中身のハッシュ(KV キャッシュで同じ画像かを比べる)。
func imagesOf(files []File) []format.Image {
	var out []format.Image
	for _, f := range files {
		if f.Kind == FileImage && !f.Discarded {
			out = append(out, format.Image{ID: f.Hash, Format: f.Format, Data: f.Data})
		}
	}
	return out
}

// filesNote は、要約に渡すときにファイルの代わりに書く文(決定事項 4)。中身は渡さない(重いので。要約の役目は話の流れを残すこと)。
func filesNote(files []File) string {
	var parts []string
	for _, f := range files {
		if f.Kind == FileImage {
			parts = append(parts, "[画像]")
		} else {
			parts = append(parts, "[ファイル: "+f.Name+"]")
		}
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
		if f.Discarded {
			// 捨てたファイル(送り直す・写すで付いていく)。中身は無い
			if len(f.Data) > 0 {
				return fmt.Errorf("%s は捨てたファイルなのに中身があります", f.Name)
			}
			continue
		}
		switch f.Kind {
		case FileText:
			if len(f.Data) > FileMaxBytes {
				return fmt.Errorf("%s は大きすぎます(1 つ %d MB まで)", f.Name, FileMaxBytes>>20)
			}
			if err := checkUTF8(f.Data); err != nil {
				return fmt.Errorf("%s: %w", f.Name, err)
			}
		case FileImage:
			if len(f.Data) > ImageMaxBytes {
				return fmt.Errorf("%s は大きすぎます(画像は 1 枚 %d MB まで)", f.Name, ImageMaxBytes>>20)
			}
			if img, _ := imageFormat(f.Data); img == "" {
				return fmt.Errorf("%s は画像として読めません(PNG・JPEG・GIF・BMP だけ読めます)", f.Name)
			}
			// ハッシュは画像の ID になる(KV キャッシュで比べる)。画面から来た値を信じず、中身から作り直す
			sum := sha256.Sum256(f.Data)
			if f.Hash != hex.EncodeToString(sum[:]) {
				return fmt.Errorf("%s のハッシュが中身と合いません", f.Name)
			}
		default:
			return fmt.Errorf("%s: 種類 %q のファイルは読み込めません", f.Name, f.Kind)
		}
	}
	return nil
}

// DiscardFile は、会話 id の発言に付けたファイル fileID を捨てる(決定事項 26〜28)。中身を保存先から消し、名前と種類だけを残す。
// 答えが出て要らなくなった画像・ファイルを、会話は残したまま消すため(自動では捨てない。残しておきたいこともあるので、人が選ぶ)。
// 生成中の会話では捨てられない(ツールのループの中で、答えが出るまで画像が要る)。捨てた中身は戻せない。
func (cs *Conversations) DiscardFile(ctx context.Context, id, fileID uuid.UUID) error {
	if cs.generatingIn(ctx, id) {
		return errors.New("返事を生成中の会話では、ファイルを捨てられません。終わってから捨ててください")
	}
	_, history, err := cs.store.Conversation(ctx, id)
	if err != nil {
		return err
	}
	for _, m := range history {
		for _, f := range m.Files {
			if f.ID != fileID {
				continue
			}
			if f.Discarded {
				return nil // もう捨ててある
			}
			if err := cs.store.DiscardFile(ctx, fileID); err != nil {
				return fmt.Errorf("%s を捨てられません: %w", f.Name, err)
			}
			log.Infof("会話 %s のファイル %s(%s)を捨てました", id, f.Name, f.Kind)
			return nil
		}
	}
	return errors.New("ファイルが見つかりません(削除された可能性があります)")
}

// FileCheck は、読み込んだファイルを確かめた結果(画面の札に出す)。
type FileCheck struct {
	File   File `json:"file"`
	Tokens int  `json:"tokens"` // このファイルを足すと増えるトークン数(テキストは展開した見出しと囲みを含む。画像は画像トークン数)
	// Uncounted は、会話のモデルをまだ読み込んでいないので数えなかった(Tokens は 0)。送信のときに、会話のモデルを読み込んでから数える
	Uncounted bool `json:"uncounted,omitempty"`
}

// CheckFile は、読み込んだファイルを確かめて、会話の上限に入るかを数える(決定事項 6)。
//
// 入らなければ、黙って切らずに「○○ トークンで、残りは △△ です」のエラーにする(大きいものは作業フォルダの read_file へ案内する)。
// 数えるのは、今の履歴 + このファイルだけの発言を作ったとき、compact しても必ず渡す部分(turnTokens)と、ファイルの無い発言との差。
// それに、この発言にもう付けているファイルの数(pending)とトークン数(pendingTokens。前に CheckFile が返したものの合計)を足して上限と比べる。
// もう付けているファイルの中身は受け取らない(画像は 1 枚 10 MB まであるので、読み込むたびに送り直させない)。
// 文はまだ入力中なので数えない(送信のときに checkFits でもう一度確かめる)。
// 画像を読めないモデル・書式なら、数えるところでエンジンがエラーにする(画像を付けない。決定事項 12)。
// ただし会話のモデルをまだ読み込んでいない(送信の前に読み込む)ときは、読み込んでいるモデルでは決められないので、数えずに受け付ける(Uncounted)。
// id がゼロなら、まだ作っていない新しい会話。新しい会話の画面で変えた設定 draft で数える(nil なら新しい会話の既定)。
func (cs *Conversations) CheckFile(ctx context.Context, id uuid.UUID, draft *ConversationSettings, name string, data []byte, pending, pendingTokens int) (FileCheck, error) {
	f, err := ReadFile(name, data)
	if err != nil {
		return FileCheck{}, err
	}
	if pending >= FilesPerSend {
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
	base, err := cs.turnTokens(set, user(nil))
	var with int
	if err == nil {
		with, err = cs.turnTokens(set, user([]File{f}))
	}
	if err != nil {
		// 会話のモデルと読み込んでいるモデルが違い、読み込んでいるモデルでは数えられない(画像を読めない・書式に未対応・降ろしている)なら、
		// 断らずに数えないまま受け付ける。送信の前に会話のモデルを読み込んでから数え直す(checkFits)ので、入らない・画像を読めないならそこで止まる。
		// 入力欄の下でモデルを選んでも読み込まない(0014 の 2026-10-31)ので、選んだ画像対応のモデルで画像を送れなくなっていた
		if cs.modelPending(set) && uncountable(err) {
			log.Infof("%s は、会話のモデル %s をまだ読み込んでいないため数えずに受け付けました(送信のときに数えます): %v", f.Name, set.Model, err)
			return FileCheck{File: f, Uncounted: true}, nil
		}
		return FileCheck{}, err
	}
	tokens := with - base
	if used := base + pendingTokens; used+tokens > limit {
		hint := "大きいファイルは作業フォルダに置き、read_file で範囲を指定して読ませてください"
		if f.Kind == FileImage {
			hint = "画像1枚のトークン数は AIBOW_IMAGE_MAX_TOKENS で小さくできます。会話の設定で会話の上限を増やすこともできます"
		}
		return FileCheck{}, fmt.Errorf("%s は %d トークンで、会話の上限(%d トークン)の残りは %d トークンです。%s",
			f.Name, tokens, limit, max(0, limit-used), hint)
	}
	return FileCheck{File: f, Tokens: tokens}, nil
}

// uncountable は、読み込んでいるモデルのせいで数えられないエラーか(モデルが無い・書式に未対応・画像を読めない)。
func uncountable(err error) bool {
	return errors.Is(err, engine.ErrNoModel) || errors.Is(err, format.ErrUnsupported) || errors.Is(err, engine.ErrNoVision)
}
