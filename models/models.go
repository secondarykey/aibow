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
	ID     string  `json:"id"`      // 例: "ollama:qwen3.5:4b" / "file:Qwen_Qwen3.5-4B-Q4_K_M.gguf"(パスは入れない。IDOf)
	Name   string  `json:"name"`    // 画面に出す名前
	Source string  `json:"source"`  // "ollama" か "file"(ID の形。どこから来たかは Origin)
	SizeGB float64 `json:"size_gb"` // ファイルサイズ
	Path   string  `json:"-"`       // GGUF ファイルの実際の場所

	// Origin はどこから来たか(OriginModels / OriginOllama / OriginAdded)。画面の一覧に出す(0015 の 27)
	Origin string `json:"origin"`
	// Place は、足した置き場所のどれから来たか(Origin が added のときだけ。足したときのパス)
	Place string `json:"place,omitempty"`
	// Arch はモデルの種類(GGUF の general.architecture。例: "qwen35"・"gemma4")。読めなければ空。
	// 使う側が書式の名前に直して画面に出す(models は書式を知らない。0026 の決定事項 17)
	Arch string `json:"arch,omitempty"`
	// Sampling は GGUF が勧めるサンプリングの値(general.sampling.*。無ければ nil)。画面に出すだけで、生成には使わない(0026 の決定事項 37)
	Sampling *Sampling `json:"sampling,omitempty"`
	// Disabled は、aibow-server の管理者が使えないようにしたモデルか(0018 の決定事項 13・17)。
	// 管理者に見せる一覧でだけ立つ(ほかの人の一覧には、使えないモデルは出さない)
	Disabled bool `json:"disabled,omitempty"`
}

// DisabledError は、aibow-server の管理者が使えないようにしたモデルを頼んだときのエラー(0018 の決定事項 13)。
// OpenAI 互換の口は 404(model_not_found)で断り、会話の送信は注意で断る(Caution。勝手にほかのモデルに替えない)。
type DisabledError struct{ ID string }

func (e *DisabledError) Error() string {
	return fmt.Sprintf("モデル %s は、aibow-server の管理者が使えないようにしました", e.ID)
}

// Caution は、エラー(赤)ではなく注意として見せる印(会話の仕組みは models を見ずに、この約束で見分ける)。
func (e *DisabledError) Caution() bool { return true }

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
	e := Entry{ID: "file:" + filepath.Base(path), Name: filepath.Base(path), Source: "file", Path: path}
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
	list, _ = dedup(list)
	for i := range list {
		h := cachedHeader(list[i].Path)
		list[i].Arch, list[i].Sampling = h.Arch, h.Sampling
	}
	return list
}

// Hidden は、ほかの置き場所に同じ名前のファイルがあるので一覧に出していないモデル(使う側が Warn のログを出すのに使う)。
func (c Catalog) Hidden() []Entry {
	list := []Entry{}
	list = append(list, listFileModels(c.ModelsDir)...)
	list = append(list, listOllamaModels(c.OllamaDir)...)
	for _, p := range c.Added {
		l, _ := listAdded(p)
		list = append(list, l...)
	}
	_, hidden := dedup(list)
	return hidden
}

// ---- モデルの ID(0018 の決定事項 15) ---------------------------------------------------
//
// GGUF のファイルの ID は "file:<ファイル名>"。以前は "file:<パス>"(例 file:D:\models\x.gguf)で、aibow-server では
// サーバーの PC のパスが /v1/models で Cline にも、共有した会話の設定にも出てしまうので、ファイル名だけにした。
// 同じ名前のファイルが2か所にあれば、一覧の先(models フォルダ → Ollama → 足した置き場所)だけを出す(dedup・Hidden)。
// 保存済みの会話・プロファイル・層の数のキーにある以前の形は、IDOf で読み替える(DB は書き換えない)。

// IDOf は、モデルの ID を今の形にする。以前の形 "file:<パス>" はファイル名だけにし、ほかはそのまま返す。
// パスの区切りは、どの OS で作った ID でも読めるように \ と / の両方を見る。
func IDOf(id string) string {
	rest, ok := strings.CutPrefix(id, "file:")
	if !ok {
		return id
	}
	if i := strings.LastIndexAny(rest, `\/`); i >= 0 {
		return "file:" + rest[i+1:]
	}
	return id
}

// Find は ID でモデルを探す。
// "ollama:名前" か "file:ファイル名"(以前の形 "file:パス" も読み替える)のほか、GGUF ファイルのパスを直接書いてもよい(AIBOW_MODEL 用)。
// models フォルダの外にあるファイルも、パスを指定すれば使える(一覧には出ないので、ほかのモデルに替えたあとは ID では戻れない)。
func (c Catalog) Find(id string) (Entry, error) {
	want := IDOf(id)
	for _, m := range c.List() {
		if m.ID == want {
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
