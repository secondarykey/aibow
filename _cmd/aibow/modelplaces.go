package main

// modelplaces.go: 直接の形(aibow-server を使わないとき)で、画面から足したモデルの置き場所を覚える(0015 の 24・32)。
//
// 覚える先は %APPDATA%\aibow\model-places.json(AIBOW_MODEL_PLACES)。会話の DB には入れない。
// 置き場所は「画面で変えるが、その PC の人が決めること」で、tools.json と同じ扱い
// (会話をサーバーに置く形にしても手元に残るもの。AGENTS.md の技術方針)。
// 手で決める置き場所(AIBOW_MODELS_DIR・OLLAMA_MODELS)は環境変数のままで、ここには書かない(足す形で一覧に出す。0015 の 22)。
// aibow-server につないでいるときは、置き場所はサーバーの PC の server-models.json にあり、このファイルは使わない。

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
)

// placesFile は model-places.json。
type placesFile struct {
	path string

	mu     sync.Mutex
	places []string
	// broken は読めなかったファイルか(壊れたファイルは書き換えない。手で直せば戻るように。tools.json と同じ)
	broken error
}

// placesData はファイルに書く形。
type placesData struct {
	// Places は画面で足した置き場所(フォルダか GGUF のファイル。絶対パス)
	Places []string `json:"places"`
}

// loadPlacesFile はファイルを読む。無ければ空(まだ何も足していない)。
// 壊れていたらエラーを返し(画面のお知らせで伝える)、足した置き場所なしで動く。
func loadPlacesFile(path string) (*placesFile, error) {
	f := &placesFile{path: path}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err == nil {
		var d placesData
		if err = json.Unmarshal(b, &d); err == nil {
			f.places = d.Places
			return f, nil
		}
	}
	f.broken = fmt.Errorf("モデルの置き場所のファイル %s を読めません。画面で足した置き場所なしで動きます(直すか消すと戻ります): %w", path, err)
	return f, f.broken
}

func (f *placesFile) list() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.places)
}

// set は置き場所を書く。一時ファイルに書いてから名前を変える(書いている途中で落ちても、前のファイルが壊れないように)。
func (f *placesFile) set(places []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.broken != nil {
		return f.broken
	}
	if f.path == "" {
		return errors.New("モデルの置き場所を覚えるファイルの場所が決められません。AIBOW_MODEL_PLACES で場所を指定してください")
	}
	b, err := json.MarshalIndent(placesData{Places: places}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
		return fmt.Errorf("モデルの置き場所を %s に書けません: %w", f.path, err)
	}
	tmp := f.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return fmt.Errorf("モデルの置き場所を %s に書けません: %w", f.path, err)
	}
	if err := os.Rename(tmp, f.path); err != nil {
		return fmt.Errorf("モデルの置き場所を %s に書けません: %w", f.path, err)
	}
	f.places = places
	return nil
}
