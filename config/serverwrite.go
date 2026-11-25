package config

// serverwrite.go: 管理の画面で変えた値を server.json に書き戻す(0061)。
//
// server.json は手で書くファイルだが、並列数(parallel)・作業領域(ctx)・サーバーのワークスペース(workspaces。0065)の3つだけは、
// 管理の画面からも変えられる(0061 の決定事項 3)。手で書いた形をなるべく壊さないよう、次のように書く(決定事項 8):
//
//   - 書く直前にファイルを読み直し、画面で変えた鍵だけを差し替える。ほかの鍵は並びも値もそのまま残す(手で書いた値を画面が消さない)
//   - 読み直したファイルが壊れていたら(JSON として読めない・知らない欄がある)、書かずに断る
//   - 書く前の中身は隣の server.json.bak に1つだけ残す(手で書いた形に戻したいとき用)
//   - 一時ファイルに書いてから名前を変える(書いている途中で落ちても、server.json が半分にならないように)
//
// 変わるのは字下げだけ(値の中も2つの空白でそろえる)。JSON にはコメントが無いので、ほかに失うものは無い。

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// ServerEdit は、管理の画面から server.json に書く項目。nil(SetWorkspaces が false)の項目は書かない(ファイルの値のまま)。
type ServerEdit struct {
	Parallel *int
	Ctx      *int
	// Workspaces はサーバーのワークスペースの全部。SetWorkspaces のときだけ書く。空なら workspaces の鍵を消す。
	// 書くときは前の形の folders の鍵を消す(中身は Workspaces に移してあるため。0065 の決定事項 10)
	Workspaces    []ServerWorkspace
	SetWorkspaces bool
}

// serverFileMu は、server.json を書く順番を1つにする(並列数の保存とワークスペースの変更が同時に来ても、片方の変更が消えないように)。
var serverFileMu sync.Mutex

// ReadServerFile は、server.json を読む(無ければ found が false)。
// LoadServerFile と違い、JSON の形と欄の名前だけを確かめる(フォルダがあるかなどは見ない)。
// 画面に「次の起動の値」を出すときと、書き戻す前の確かめに使う。
func ReadServerFile(path string) (file ServerFile, found bool, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return ServerFile{}, false, nil
	}
	if err != nil {
		return ServerFile{}, false, fmt.Errorf("設定ファイル %s を読めません: %w", path, err)
	}
	file, err = decodeServerFile(path, data)
	return file, err == nil, err
}

func decodeServerFile(path string, data []byte) (ServerFile, error) {
	var file ServerFile
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&file); err != nil {
		return ServerFile{}, fmt.Errorf("設定ファイル %s を読めません(%s): %w", path, JSONErrorLine(data, err), err)
	}
	return file, nil
}

// SaveServerFile は、server.json(path)に edit の項目だけを書く。ファイルが無ければ、edit の項目だけのファイルを作る(決定事項 7)。
func SaveServerFile(path string, edit ServerEdit) error {
	if err := edit.check(path); err != nil {
		return err
	}
	serverFileMu.Lock()
	defer serverFileMu.Unlock()

	old, err := os.ReadFile(path)
	exists := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("設定ファイル %s を読めません: %w", path, err)
	}
	var fields []jsonField
	if exists {
		// 壊れたファイルには書かない(書き直すと、手で直したい中身が分からなくなるため)
		if _, err := decodeServerFile(path, old); err != nil {
			return fmt.Errorf("%w。ファイルを直してから、もう一度保存してください", err)
		}
		if fields, err = readFields(old); err != nil {
			return fmt.Errorf("設定ファイル %s を読めません: %w。ファイルを直してから、もう一度保存してください", path, err)
		}
	}
	if edit.Parallel != nil {
		fields = setField(fields, "parallel", *edit.Parallel)
	}
	if edit.Ctx != nil {
		fields = setField(fields, "ctx", *edit.Ctx)
	}
	if edit.SetWorkspaces {
		fields = deleteField(fields, "folders")
		if len(edit.Workspaces) == 0 {
			fields = deleteField(fields, "workspaces")
		} else {
			fields = setField(fields, "workspaces", edit.Workspaces)
		}
	}
	data, err := writeFields(fields)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("設定ファイル %s のフォルダを作れません: %w", path, err)
	}
	if exists {
		// 書く前の中身は .bak に1つだけ残す(毎回上書き。0061 の未決事項 3 は使ってみて決める)
		if err := os.WriteFile(path+".bak", old, 0o644); err != nil {
			return fmt.Errorf("設定ファイルの控え %s.bak を書けません(設定ファイルは書き換えていません): %w", path, err)
		}
	}
	return replaceFile(path, data)
}

// check は、書く値を確かめる(画面からの値でも、ファイルを読み直して起動できない値は書かない)。
func (e ServerEdit) check(path string) error {
	if e.Parallel != nil && *e.Parallel < 1 {
		return fmt.Errorf("並列数は 1 以上にしてください: %d", *e.Parallel)
	}
	if e.Ctx != nil && *e.Ctx < 1 {
		return fmt.Errorf("作業領域は 1 以上にしてください: %d", *e.Ctx)
	}
	if e.SetWorkspaces {
		if err := CheckServerWorkspaces(e.Workspaces); err != nil {
			return fmt.Errorf("設定ファイル %s に書けません: %w", path, err)
		}
	}
	return nil
}

// replaceFile は、一時ファイルに書いてから path に名前を変える(Windows でも、Go の os.Rename は今あるファイルを置き換える)。
func replaceFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("設定ファイル %s を書けません: %w", path, err)
	}
	name := tmp.Name()
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		os.Remove(name)
		return fmt.Errorf("設定ファイル %s を書けません: %w", path, err)
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return fmt.Errorf("設定ファイル %s を書けません: %w", path, err)
	}
	return nil
}

// ---- 鍵の並びを保ったまま、JSON のオブジェクトを読み書きする ----------------------------
//
// map に読むと鍵が名前の順に並び替わってしまうので、鍵と値(生の JSON)を並びのまま持つ。

type jsonField struct {
	key   string
	value json.RawMessage
}

// readFields は、{…} の一番外の鍵と値を、並びのまま読む。
func readFields(data []byte) ([]jsonField, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, errors.New("{…} の形ではありません")
	}
	var fields []jsonField
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := t.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		fields = append(fields, jsonField{key: key, value: v})
	}
	return fields, nil
}

// setField は、鍵 key の値を v にする。無ければ最後に足す。
func setField(fields []jsonField, key string, v any) []jsonField {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err) // int とワークスペースの一覧しか渡さない
	}
	for i := range fields {
		if fields[i].key == key {
			fields[i].value = raw
			return fields
		}
	}
	return append(fields, jsonField{key: key, value: raw})
}

func deleteField(fields []jsonField, key string) []jsonField {
	out := fields[:0]
	for _, f := range fields {
		if f.key != key {
			out = append(out, f)
		}
	}
	return out
}

// writeFields は、鍵と値を2つの空白の字下げで書く。
func writeFields(fields []jsonField) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString("{")
	for i, f := range fields {
		name, err := json.Marshal(f.key)
		if err != nil {
			return nil, err
		}
		if i > 0 {
			buf.WriteString(",")
		}
		buf.WriteString("\n  ")
		buf.Write(name)
		buf.WriteString(": ")
		if err := json.Indent(&buf, f.value, "  ", "  "); err != nil {
			return nil, err
		}
	}
	if len(fields) > 0 {
		buf.WriteString("\n")
	}
	buf.WriteString("}\n")
	return buf.Bytes(), nil
}
