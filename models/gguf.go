package models

// gguf.go: GGUF ファイルの先頭(メタデータ)から、モデルの種類(general.architecture)だけを読む。
//
// 書式(チャットテンプレート)はモデルの種類で決まる(format.For)。種類を知るのに llama.cpp でモデルを読み込むと、
// 重み(数 GB)を GPU に載せるまで待つことになる。起動のときに「最初のモデル」を選ぶ(書式に未対応のモデルを飛ばす)だけなら、
// ファイルの先頭の数 KB を読めば足りる。
//
// GGUF の先頭はこう並んでいる(数はすべてリトルエンディアン):
//
//	"GGUF"(4バイト) / 版(uint32) / テンソルの数(uint64) / メタデータの数(uint64)
//	メタデータ × 数: キー(文字列) / 値の型(uint32) / 値
//	文字列 = 長さ(uint64) + UTF-8 のバイト列。配列 = 要素の型(uint32) + 要素の数(uint64) + 要素
//
// general.architecture はふつう最初のほうにあるが、決まりではないので、見つかるまで値を読み飛ばす。

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// GGUF の値の型
const (
	ggufUint8 = iota
	ggufInt8
	ggufUint16
	ggufInt16
	ggufUint32
	ggufInt32
	ggufFloat32
	ggufBool
	ggufString
	ggufArray
	ggufUint64
	ggufInt64
	ggufFloat64
)

// ggufMaxString は、読む文字列の長さの上限(壊れたファイルで、巨大な長さを読んでメモリを確保しないため)
const ggufMaxString = 1 << 20

// Header は GGUF の先頭(メタデータの general.*)から読んだもの。重みは読まない。
type Header struct {
	Arch     string    // general.architecture(例: "qwen35"・"gemma4")
	Sampling *Sampling // general.sampling.*(モデルを作った人が勧めるサンプリングの値。無ければ nil)
}

// Sampling は GGUF が勧めるサンプリングの値(0026 の決定事項 37。画面に出すだけで、生成には使わない)。書いていない値は nil。
type Sampling struct {
	Temp *float64 `json:"temp,omitempty"`
	TopK *float64 `json:"top_k,omitempty"`
	TopP *float64 `json:"top_p,omitempty"`
}

// Arch は GGUF ファイルの general.architecture(例: "qwen35"・"gemma4")を返す。重みは読まない。
func Arch(path string) (string, error) {
	h, err := ReadHeader(path)
	if err != nil {
		return "", err
	}
	if h.Arch == "" {
		return "", fmt.Errorf("%s に general.architecture がありません", path)
	}
	return h.Arch, nil
}

// ReadHeader は GGUF の先頭の general.* を読む。general.* はメタデータの先頭にまとめて書かれる決まりなので、
// それ以外のキー(語彙の大きな配列など)が出たところでやめる。
func ReadHeader(path string) (Header, error) {
	var h Header
	f, err := os.Open(path)
	if err != nil {
		return h, err
	}
	defer f.Close()
	r := bufio.NewReader(f)

	var head struct {
		Magic   [4]byte
		Version uint32
		Tensors uint64
		KVs     uint64
	}
	if err := binary.Read(r, binary.LittleEndian, &head); err != nil {
		return h, fmt.Errorf("%s の GGUF の先頭を読めません: %w", path, err)
	}
	if string(head.Magic[:]) != "GGUF" {
		return h, fmt.Errorf("%s は GGUF のファイルではありません", path)
	}
	if head.Version < 2 {
		return h, fmt.Errorf("%s は古い GGUF(版 %d)です", path, head.Version)
	}
	for range head.KVs {
		key, err := readGGUFString(r)
		if err != nil {
			return h, fmt.Errorf("%s のメタデータを読めません: %w", path, err)
		}
		if !strings.HasPrefix(key, "general.") && h.Arch != "" {
			return h, nil
		}
		var typ uint32
		if err := binary.Read(r, binary.LittleEndian, &typ); err != nil {
			return h, fmt.Errorf("%s のメタデータを読めません: %w", path, err)
		}
		switch {
		case key == "general.architecture" && typ == ggufString:
			if h.Arch, err = readGGUFString(r); err != nil {
				return h, err
			}
			continue
		case strings.HasPrefix(key, "general.sampling."):
			if v, ok, err := readGGUFNumber(r, typ); err != nil {
				return h, err
			} else if ok {
				if h.Sampling == nil {
					h.Sampling = &Sampling{}
				}
				switch strings.TrimPrefix(key, "general.sampling.") {
				case "temp":
					h.Sampling.Temp = &v
				case "top_k":
					h.Sampling.TopK = &v
				case "top_p":
					h.Sampling.TopP = &v
				}
				continue
			}
		}
		if err := ggufSkip(r, typ); err != nil {
			return h, fmt.Errorf("%s のメタデータ %s を読み飛ばせません: %w", path, key, err)
		}
	}
	return h, nil
}

// readGGUFNumber は数の型なら値を読んで ok を返す。数でなければ何も読まない(呼ぶ側が読み飛ばす)。
func readGGUFNumber(r *bufio.Reader, typ uint32) (float64, bool, error) {
	var v any
	switch typ {
	case ggufUint8:
		v = new(uint8)
	case ggufInt8:
		v = new(int8)
	case ggufUint16:
		v = new(uint16)
	case ggufInt16:
		v = new(int16)
	case ggufUint32:
		v = new(uint32)
	case ggufInt32:
		v = new(int32)
	case ggufFloat32:
		v = new(float32)
	case ggufUint64:
		v = new(uint64)
	case ggufInt64:
		v = new(int64)
	case ggufFloat64:
		v = new(float64)
	default:
		return 0, false, nil
	}
	if err := binary.Read(r, binary.LittleEndian, v); err != nil {
		return 0, false, err
	}
	var f float64
	switch x := v.(type) {
	case *uint8:
		f = float64(*x)
	case *int8:
		f = float64(*x)
	case *uint16:
		f = float64(*x)
	case *int16:
		f = float64(*x)
	case *uint32:
		f = float64(*x)
	case *int32:
		f = float64(*x)
	case *float32:
		// float32 の 0.95 は float64 にすると 0.949999988… になるので、float32 の短い10進の表し方に戻す
		f, _ = strconv.ParseFloat(strconv.FormatFloat(float64(*x), 'g', -1, 32), 64)
	case *uint64:
		f = float64(*x)
	case *int64:
		f = float64(*x)
	case *float64:
		f = *x
	}
	return f, true, nil
}

// readGGUFString は文字列を1つ読む。
func readGGUFString(r *bufio.Reader) (string, error) {
	var n uint64
	if err := binary.Read(r, binary.LittleEndian, &n); err != nil {
		return "", err
	}
	if n > ggufMaxString {
		return "", fmt.Errorf("文字列が長すぎます(%d バイト)", n)
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return "", err
	}
	return string(b), nil
}

// ggufSkip は、型 typ の値を1つ読み飛ばす(配列はトークンの一覧などで大きいので、確保せずに捨てる)。
func ggufSkip(r *bufio.Reader, typ uint32) error {
	if size := ggufSize(typ); size > 0 {
		_, err := r.Discard(size)
		return err
	}
	switch typ {
	case ggufString:
		var n uint64
		if err := binary.Read(r, binary.LittleEndian, &n); err != nil {
			return err
		}
		_, err := io.CopyN(io.Discard, r, int64(n))
		return err
	case ggufArray:
		var elem uint32
		var count uint64
		if err := binary.Read(r, binary.LittleEndian, &elem); err != nil {
			return err
		}
		if err := binary.Read(r, binary.LittleEndian, &count); err != nil {
			return err
		}
		if size := ggufSize(elem); size > 0 {
			_, err := io.CopyN(io.Discard, r, int64(count)*int64(size))
			return err
		}
		for range count {
			if err := ggufSkip(r, elem); err != nil {
				return err
			}
		}
		return nil
	}
	return errors.New("知らない値の型です")
}

// ggufSize は、大きさが決まっている型のバイト数(文字列と配列は 0)。
func ggufSize(typ uint32) int {
	switch typ {
	case ggufUint8, ggufInt8, ggufBool:
		return 1
	case ggufUint16, ggufInt16:
		return 2
	case ggufUint32, ggufInt32, ggufFloat32:
		return 4
	case ggufUint64, ggufInt64, ggufFloat64:
		return 8
	}
	return 0
}

// headerCache は、ファイルごとの先頭(一覧は画面がウィンドウに戻るたびに取り直すので、毎回ファイルを開かないように)。
// ファイルの大きさと更新の時刻が同じなら、同じファイルとみなす。
var headerCache sync.Map // パス → headerCached

type headerCached struct {
	size int64
	mod  time.Time
	h    Header
}

// cachedHeader は ReadHeader を覚えておいて返す。読めなければ空(一覧には出す。読み込むときに llama.cpp がエラーにする)。
func cachedHeader(path string) Header {
	st, err := os.Stat(path)
	if err != nil {
		return Header{}
	}
	if c, ok := headerCache.Load(path); ok {
		if c := c.(headerCached); c.size == st.Size() && c.mod.Equal(st.ModTime()) {
			return c.h
		}
	}
	h, _ := ReadHeader(path)
	headerCache.Store(path, headerCached{size: st.Size(), mod: st.ModTime(), h: h})
	return h
}
