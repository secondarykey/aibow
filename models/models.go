// Package models は、使えるモデル(GGUF ファイル)の一覧を作る。
// 何にも依存しない(llama.cpp も読まない)。ファイルの場所を探すだけで、読み込むのは engine パッケージ。
package models

// models.go: 使えるモデルの一覧を作る部分。
//
// モデルは次の3か所から探す:
//
//  1. models フォルダに置いた *.gguf ファイル
//  2. Ollama が `ollama pull` で保存したモデル
//  3. 画面で足した置き場所(フォルダか GGUF のファイル1つ。0015 の 8-4。places.go)
//
// 2 は Ollama のサーバーを使うわけではなく、Ollama が保存したファイルを直接読むだけ。
// Ollama は起動していなくてよい。Ollama は「モデルの管理係」、aibow は「動かす係」という分担。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Entry は一覧の1件。
type Entry struct {
	ID     string  `json:"id"`      // 例: "ollama:qwen3.5:4b" / "file:models\Qwen_Qwen3.5-4B-Q4_K_M.gguf"
	Name   string  `json:"name"`    // 画面に出す名前
	Source string  `json:"source"`  // "ollama" か "file"(ID の形。どこから来たかは Origin)
	SizeGB float64 `json:"size_gb"` // ファイルサイズ
	Path   string  `json:"-"`       // GGUF ファイルの実際の場所

	// Origin はどこから来たか(OriginModels / OriginOllama / OriginAdded)。画面の一覧に出す(0015 の 27)
	Origin string `json:"origin"`
	// Place は、足した置き場所のどれから来たか(Origin が added のときだけ。足したときのパス)
	Place string `json:"place,omitempty"`
}

// ---- models フォルダの *.gguf ----------------------------------------------------------

func listFileModels(dir string) []Entry {
	files, _ := filepath.Glob(filepath.Join(dir, "*.gguf"))
	var list []Entry
	for _, f := range files {
		e := fileEntry(f)
		e.Origin = OriginModels
		list = append(list, e)
	}
	return list
}

func fileEntry(path string) Entry {
	e := Entry{ID: "file:" + path, Name: filepath.Base(path), Source: "file", Path: path}
	if st, err := os.Stat(path); err == nil {
		e.SizeGB = float64(st.Size()) / (1 << 30)
	}
	return e
}

// ---- Ollama が保存したモデル -------------------------------------------------------------
//
// Ollama の保存フォルダはこうなっている:
//
//	models\
//	  manifests\registry.ollama.ai\library\qwen3.5\4b   … 目録(JSON)。名前ごとに1つ
//	  blobs\sha256-1a2b3c…                              … 中身。ファイル名はハッシュ値
//
// 目録には「このモデルはどの blob でできているか」が並んでいる。
// その中で mediaType が "application/vnd.ollama.image.model" のものが GGUF 本体。
// (ほかにチャットテンプレートやライセンスの blob もあるが、aibow は使わない)

// OllamaDir は Ollama の保存フォルダを返す。
// Ollama と同じく、環境変数 OLLAMA_MODELS があればそちらを使う。
func OllamaDir() string {
	if d := os.Getenv("OLLAMA_MODELS"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".ollama", "models")
}

type ollamaManifest struct {
	Layers []struct {
		MediaType string `json:"mediaType"`
		Digest    string `json:"digest"` // "sha256:1a2b3c…"
		Size      int64  `json:"size"`
	} `json:"layers"`
}

const ollamaModelLayer = "application/vnd.ollama.image.model"

func listOllamaModels(dir string) []Entry {
	root := filepath.Join(dir, "manifests")
	var list []Entry
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) != 4 { // ホスト / 名前空間 / モデル名 / タグ
			return nil
		}
		blob, size, ok := readOllamaManifest(dir, path)
		if !ok {
			return nil // クラウド専用モデルなど、本体のファイルがないものは飛ばす
		}
		name := ollamaName(parts)
		list = append(list, Entry{
			ID: "ollama:" + name, Name: name, Source: "ollama", Origin: OriginOllama,
			SizeGB: float64(size) / (1 << 30), Path: blob,
		})
		return nil
	})
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list
}

// readOllamaManifest は目録を読み、GGUF 本体の blob のパスを返す。
func readOllamaManifest(dir, manifestPath string) (blob string, size int64, ok bool) {
	b, err := os.ReadFile(manifestPath)
	if err != nil {
		return "", 0, false
	}
	var m ollamaManifest
	if json.Unmarshal(b, &m) != nil {
		return "", 0, false
	}
	for _, l := range m.Layers {
		if l.MediaType != ollamaModelLayer {
			continue
		}
		// "sha256:abc…" → blobs\sha256-abc…(Windows のファイル名に ":" は使えないため "-" になっている)
		blob = filepath.Join(dir, "blobs", strings.Replace(l.Digest, ":", "-", 1))
		if _, err := os.Stat(blob); err != nil {
			return "", 0, false
		}
		return blob, l.Size, true
	}
	return "", 0, false
}

// ollamaName は目録のパスを、`ollama list` と同じ表示名にする。
//
//	registry.ollama.ai/library/qwen3.5/4b  → qwen3.5:4b
//	registry.ollama.ai/someone/model/tag   → someone/model:tag
//	hf.co/someone/repo/Q4_K_M              → hf.co/someone/repo:Q4_K_M
func ollamaName(p []string) string {
	host, ns, model, tag := p[0], p[1], p[2], p[3]
	switch {
	case host == "registry.ollama.ai" && ns == "library":
		return model + ":" + tag
	case host == "registry.ollama.ai":
		return ns + "/" + model + ":" + tag
	default:
		return host + "/" + ns + "/" + model + ":" + tag
	}
}

// ---- 一覧の作成と検索 ---------------------------------------------------------------------

// Catalog は使えるモデルの一覧を作る。画面を開くたびに作り直すので、
// aibow を起動したまま `ollama pull` しても、再読み込みで一覧に出てくる。
type Catalog struct {
	ModelsDir string // models フォルダ
	OllamaDir string // Ollama の保存フォルダ
	// Added は画面で足した置き場所(フォルダか GGUF のファイル。絶対パス)。ModelsDir と OllamaDir に足す形で持つ
	// (手で決めた場所と画面で足した場所を別の値にして、ぶつからないようにする。0015 の 22)。覚えておくのは使う側(places.go)
	Added []string
}

// List は使えるモデルの一覧。models フォルダ → Ollama → 足した置き場所の順。
// 同じファイルが2か所から見つかったら(足したフォルダの中のファイルを、ファイルとしても足した、など)、先に見つかったほうだけを出す。
// 見つからない足した置き場所は飛ばす(どれが見つからないかは Missing。0015 の 28)。
func (c Catalog) List() []Entry {
	list := []Entry{} // 0件でも JSON で null ではなく [] になるように
	list = append(list, listFileModels(c.ModelsDir)...)
	list = append(list, listOllamaModels(c.OllamaDir)...)
	for _, p := range c.Added {
		l, _ := listAdded(p)
		list = append(list, l...)
	}
	return dedup(list)
}

// Find は ID でモデルを探す。
// "ollama:名前" か "file:パス" のほか、GGUF ファイルのパスを直接書いてもよい(AIBOW_MODEL 用)。
// models フォルダの外にあるファイルも、パスを指定すれば使える。
func (c Catalog) Find(id string) (Entry, error) {
	for _, m := range c.List() {
		if m.ID == id {
			return m, nil
		}
	}
	if !strings.HasPrefix(id, "ollama:") {
		path := strings.TrimPrefix(id, "file:")
		if _, err := os.Stat(path); err == nil {
			return fileEntry(path), nil
		}
	}
	return Entry{}, fmt.Errorf("モデル %q が見つかりません", id)
}
