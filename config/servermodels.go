package config

// servermodels.go: aibow-server が書くファイル(server-models.json)。モデルごとの GPU の層数・画面で足したモデルの置き場所・
// 管理者が使えないようにしたモデル。手で書く server.json とは分ける(0015 の 19)。性格はアプリの tools.json と同じ
// (画面で変えるが、その PC の人が決めること)。aibow-server の models.go が、モデルを扱う係からこれを読み書きする。

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/secondarykey/aibow/log"
	"github.com/secondarykey/aibow/models"
)

// ServerModels は aibow-server が書くファイル(server-models.json)。手で書く server.json とは分ける(0015 の 19)。
type ServerModels struct {
	path string
	mu   sync.Mutex
	data stateData
}

// stateData はファイルの中身。
type stateData struct {
	// GPULayers はモデルの ID → GPU に載せる層の数
	GPULayers map[string]int `json:"gpuLayers"`
	// Places は画面で足したモデルの置き場所(フォルダか GGUF のファイル。サーバーの PC の絶対パス。8-4)
	Places []string `json:"places,omitempty"`
	// Disabled は、管理者が使えないようにしたモデルの ID(0018 の決定事項 17。置き場所に新しく増えたモデルは使える)
	Disabled []string `json:"disabled,omitempty"`
}

// LoadServerModels はファイルを読む。無ければ空(まだ何も覚えていない)。
func LoadServerModels(path string) (*ServerModels, error) {
	s := &ServerModels{path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s を読めません: %w", path, err)
	}
	if err := json.Unmarshal(data, &s.data); err != nil {
		return nil, fmt.Errorf("%s を読めません(aibow-server が書くファイルです。壊れているなら消すと、覚えた GPU の層数を忘れて起動します): %w", path, err)
	}
	// 以前の形のモデルの ID(file:<パス>)で覚えた層の数を、今の形(file:<ファイル名>)に書き換える(0018 の決定事項 14・15)
	if layers := models.LayersByID(s.data.GPULayers); !maps.Equal(layers, s.data.GPULayers) {
		next := s.data
		next.GPULayers = layers
		if err := writeJSON(path, next); err != nil {
			return nil, fmt.Errorf("%s の層の数のモデルの ID を今の形に書き換えられません: %w", path, err)
		}
		s.data = next
		log.Noticef("%s の層の数のモデルの ID を、今の形(file:<ファイル名>)に書き換えました", path)
	}
	return s, nil
}

// Path は読み書きするファイルの場所(ログと画面に出す)。
func (s *ServerModels) Path() string { return s.path }

// GPULayers は、モデル id に覚えた GPU の層の数(覚えていなければ ok が false)。
func (s *ServerModels) GPULayers(id string) (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.data.GPULayers[id]
	return n, ok
}

// SetGPULayers は、モデル id の GPU の層の数を覚えて書く。
func (s *ServerModels) SetGPULayers(id string, n int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	layers := maps.Clone(s.data.GPULayers)
	if layers == nil {
		layers = map[string]int{}
	}
	layers[id] = n
	next := s.data
	next.GPULayers = layers
	if err := writeJSON(s.path, next); err != nil {
		return fmt.Errorf("GPU の層数を %s に書けません(次の起動では覚えていません): %w", s.path, err)
	}
	s.data = next
	return nil
}

// Disabled は、モデル id を管理者が使えないようにしたか。
func (s *ServerModels) Disabled(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Contains(s.data.Disabled, id)
}

// SetDisabled は、モデル id を使えないようにする / 戻して書く。
func (s *ServerModels) SetDisabled(id string, disabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := slices.DeleteFunc(slices.Clone(s.data.Disabled), func(d string) bool { return d == id })
	if disabled {
		list = append(list, id)
	}
	next := s.data
	next.Disabled = list
	if err := writeJSON(s.path, next); err != nil {
		return fmt.Errorf("使えないモデルを %s に書けません: %w", s.path, err)
	}
	s.data = next
	return nil
}

// Places は、画面で足したモデルの置き場所。
func (s *ServerModels) Places() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.data.Places)
}

// SetPlaces は、画面で足したモデルの置き場所を書く。
func (s *ServerModels) SetPlaces(places []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.data
	next.Places = places
	if err := writeJSON(s.path, next); err != nil {
		return fmt.Errorf("モデルの置き場所を %s に書けません: %w", s.path, err)
	}
	s.data = next
	return nil
}

// writeJSON は、一時ファイルに書いてから名前を変える(書いている途中で落ちても、前のファイルが壊れないように)。
func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
