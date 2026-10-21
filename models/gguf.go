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

// Arch は GGUF ファイルの general.architecture(例: "qwen35"・"gemma4")を返す。重みは読まない。
func Arch(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
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
		return "", fmt.Errorf("%s の GGUF の先頭を読めません: %w", path, err)
	}
	if string(head.Magic[:]) != "GGUF" {
		return "", fmt.Errorf("%s は GGUF のファイルではありません", path)
	}
	if head.Version < 2 {
		return "", fmt.Errorf("%s は古い GGUF(版 %d)です", path, head.Version)
	}
	for range head.KVs {
		key, err := readGGUFString(r)
		if err != nil {
			return "", fmt.Errorf("%s のメタデータを読めません: %w", path, err)
		}
		var typ uint32
		if err := binary.Read(r, binary.LittleEndian, &typ); err != nil {
			return "", fmt.Errorf("%s のメタデータを読めません: %w", path, err)
		}
		if key == "general.architecture" && typ == ggufString {
			return readGGUFString(r)
		}
		if err := ggufSkip(r, typ); err != nil {
			return "", fmt.Errorf("%s のメタデータ %s を読み飛ばせません: %w", path, key, err)
		}
	}
	return "", fmt.Errorf("%s に general.architecture がありません", path)
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
