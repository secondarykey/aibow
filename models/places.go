package models

// places.go: モデルの置き場所(0015 の 8-4)。
//
// 置き場所は3種類ある:
//
//	models フォルダ … 手で決める場所(アプリは AIBOW_MODELS_DIR、aibow-server は server.json の modelsDir など)
//	Ollama          … Ollama の保存フォルダ(OLLAMA_MODELS)
//	足した置き場所  … 画面で足したフォルダか GGUF のファイル1つ。覚えておくのは使う側
//	                  (アプリは手元の models.json、aibow-server は server-models.json)
//
// 手で決める場所は画面からは変えない(表示だけ)。画面で足した場所だけを足したり外したりできる(0015 の 22・26)。
// 足した置き場所が見つからなくなっても(消した・ドライブを外した)覚えたまま残し、一覧から外すだけにする
// (ドライブをつなぎ直せば戻るように。0015 の 28)。

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// Origin の値(モデルがどこから来たか)。
const (
	OriginModels = "models" // models フォルダ
	OriginOllama = "ollama" // Ollama の保存フォルダ
	OriginAdded  = "added"  // 画面で足した置き場所
)

// Place は置き場所1つ(画面の一覧に出す)。
type Place struct {
	Path   string `json:"path"`   // 置き場所のパス(足した置き場所は絶対パス)
	Origin string `json:"origin"` // OriginModels / OriginOllama / OriginAdded。外せるのは added だけ
	Kind   string `json:"kind"`   // "dir"(フォルダ)か "file"(GGUF のファイル1つ)
	Models int    `json:"models"` // 見つかったモデルの数
	// Missing は、見つからない理由(見つかれば空)。足した置き場所が見つからないときは、そのモデルは一覧に出ない
	Missing string `json:"missing,omitempty"`
}

// Places は置き場所の一覧(models フォルダ → Ollama → 足した置き場所の順)。
func (c Catalog) Places() []Place {
	list := []Place{
		dirPlace(c.ModelsDir, OriginModels, len(listFileModels(c.ModelsDir))),
		dirPlace(c.OllamaDir, OriginOllama, len(listOllamaModels(c.OllamaDir))),
	}
	for _, p := range c.Added {
		l, err := listAdded(p)
		pl := Place{Path: p, Origin: OriginAdded, Kind: "dir", Models: len(l)}
		if strings.EqualFold(filepath.Ext(p), ".gguf") {
			pl.Kind = "file"
		}
		if err != nil {
			pl.Missing = err.Error()
		}
		list = append(list, pl)
	}
	return list
}

func dirPlace(path, origin string, n int) Place {
	if a, err := filepath.Abs(path); err == nil && path != "" {
		path = a // 相対パス(既定の "models")や "/" 区切りのままだと、どこを探しているのか分かりにくいため
	}
	pl := Place{Path: path, Origin: origin, Kind: "dir", Models: n}
	if _, err := os.Stat(path); err != nil {
		pl.Missing = "フォルダが見つかりません"
	}
	return pl
}

// Missing は、見つからない足した置き場所(一覧に出していない置き場所)。使う側が Warn のログを出すのに使う。
func (c Catalog) Missing() []Place {
	var list []Place
	for _, p := range c.Places() {
		if p.Origin == OriginAdded && p.Missing != "" {
			list = append(list, p)
		}
	}
	return list
}

// MissingWatch は、見つからない足した置き場所が変わったときだけ知らせる係
// (一覧は画面がウィンドウに戻るたびに作り直すので、ログを毎回は出さないため)。ログを出すのは使う側。
type MissingWatch struct {
	mu   sync.Mutex
	seen map[string]bool
}

// Check は c の足した置き場所を調べ、前に調べたときから見つからなくなったもの(lost)と、また見つかったもの(found)を返す。
// 外した置き場所は found に入れない。
func (w *MissingWatch) Check(c Catalog) (lost, found []string) {
	now := map[string]bool{}
	for _, p := range c.Missing() {
		now[p.Path] = true
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, p := range c.Added {
		switch {
		case now[p] && !w.seen[p]:
			lost = append(lost, p)
		case !now[p] && w.seen[p]:
			found = append(found, p)
		}
	}
	w.seen = now
	return lost, found
}

// listAdded は、足した置き場所1つのモデル。見つからなければエラー(一覧には出さない)。
func listAdded(p string) ([]Entry, error) {
	st, err := os.Stat(p)
	if err != nil {
		return nil, errors.New("見つかりません(消したか、ドライブがつながっていない)")
	}
	var list []Entry
	if st.IsDir() {
		list = listFileModels(p)
	} else {
		list = []Entry{fileEntry(p)}
	}
	for i := range list {
		list[i].Origin, list[i].Place = OriginAdded, p
	}
	return list, nil
}

// CheckPlace は、足そうとしている置き場所 path を確かめ、絶対パスにして返す。
// フォルダか GGUF のファイルで、まだ置き場所に入っていないこと(0015 の 25・31)。
// aibow-server 越しのときは、サーバーの PC で確かめる(パスはサーバーの PC のもの)。
func (c Catalog) CheckPlace(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("置き場所のパスを書いてください")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("置き場所のパス %q を読めません: %w", path, err)
	}
	st, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("置き場所 %s が見つかりません。フォルダか GGUF のファイルのパスを書いてください", abs)
	}
	if !st.IsDir() {
		if !strings.EqualFold(filepath.Ext(abs), ".gguf") {
			return "", fmt.Errorf("%s は GGUF のファイル(.gguf)ではありません。フォルダか .gguf のファイルを指定してください", abs)
		}
		if err := checkGGUF(abs); err != nil {
			return "", err
		}
	}
	for _, d := range []struct{ path, what string }{{c.ModelsDir, "models フォルダ"}, {c.OllamaDir, "Ollama の保存フォルダ"}} {
		if samePath(abs, d.path) {
			return "", fmt.Errorf("%s は%sなので、もう一覧に出ています", abs, d.what)
		}
	}
	for _, p := range c.Added {
		if samePath(abs, p) {
			return "", fmt.Errorf("%s はもう足してあります", abs)
		}
	}
	return abs, nil
}

// IsAdded は path が足した置き場所か(外せるのは足した置き場所だけ。0015 の 26)。足したときの書き方を返す。
func (c Catalog) IsAdded(path string) (string, bool) {
	for _, p := range c.Added {
		if samePath(path, p) {
			return p, true
		}
	}
	return "", false
}

// checkGGUF は、ファイルの先頭が GGUF の印("GGUF" の4バイト)かを確かめる。
// 拡張子だけでは、名前を変えただけの別のファイルを足せてしまい、読み込むときまで分からないため。
func checkGGUF(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("%s を開けません: %w", path, err)
	}
	defer f.Close()
	head := make([]byte, 4)
	if _, err := io.ReadFull(f, head); err != nil || !bytes.Equal(head, []byte("GGUF")) {
		return fmt.Errorf("%s は GGUF のファイルではありません(先頭が GGUF ではない)", path)
	}
	return nil
}

// dedup は、同じファイル(絶対パスで比べる)を2回出さない。先に見つかったほうを残す。
// 違うファイルでも ID(ファイル名)が同じなら、先に見つかったほうだけを残し、後ろのほうは hidden に入れる(0018 の決定事項 15)。
func dedup(list []Entry) (out, hidden []Entry) {
	seen := map[string]bool{}
	ids := map[string]bool{}
	out = list[:0]
	for _, e := range list {
		key := e.Path
		if a, err := filepath.Abs(e.Path); err == nil {
			key = a
		}
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key) // Windows のパスは大文字と小文字を区別しない
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		if ids[e.ID] {
			hidden = append(hidden, e)
			continue
		}
		ids[e.ID] = true
		out = append(out, e)
	}
	return out, hidden
}

// HiddenWatch は、同じ名前で隠したモデルが変わったときだけ知らせる係(MissingWatch と同じ考え方。ログを出すのは使う側)。
type HiddenWatch struct {
	mu   sync.Mutex
	seen map[string]bool
}

// Check は、前に調べたときから新しく隠したモデルを返す。
func (w *HiddenWatch) Check(c Catalog) []Entry {
	var fresh []Entry
	now := map[string]bool{}
	for _, e := range c.Hidden() {
		now[e.Path] = true
		w.mu.Lock()
		if !w.seen[e.Path] {
			fresh = append(fresh, e)
		}
		w.mu.Unlock()
	}
	w.mu.Lock()
	w.seen = now
	w.mu.Unlock()
	return fresh
}

// samePath は、2つのパスが同じ場所か(Windows は大文字と小文字を区別しない)。
func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	aa, err1 := filepath.Abs(a)
	bb, err2 := filepath.Abs(b)
	if err1 != nil || err2 != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(aa, bb)
	}
	return aa == bb
}
