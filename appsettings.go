package aibow

// appsettings.go: アプリ全体の設定(会話をまたいで1つだけのもの)。
//
// 保存先の app_settings は「キー → 値」の表。値は JSON の文字列で入れる
// (数値も文字列も構造体も同じ形で入り、DB を直接のぞいても読める)。

import (
	"encoding/json"
	"fmt"
)

// app_settings のキー
const (
	SettingNCtx      = "n_ctx"                 // コンテキスト長
	SettingGPULayers = "gpu_layers"            // GPU に載せる層の数
	SettingLastModel = "last_model"            // 最後に使ったモデルの ID。次の起動で読み込む
	SettingDefaults  = "conversation_defaults" // 新しい会話の既定の設定
)

// AppSettings はアプリ全体の設定。
type AppSettings struct {
	NCtx      int                  `json:"n_ctx"`
	GPULayers int                  `json:"gpu_layers"`
	LastModel string               `json:"last_model"`
	Defaults  ConversationSettings `json:"defaults"`
}

// LoadAppSettings は保存先からアプリ全体の設定を読む。
// 保存されていない項目は initial の値のまま(環境変数から作った初期値)。
// 画面で一度変えると保存先に入るので、以降は環境変数より保存した値が優先される。
func LoadAppSettings(s Store, initial AppSettings) (AppSettings, error) {
	a := initial
	for key, v := range map[string]any{
		SettingNCtx:      &a.NCtx,
		SettingGPULayers: &a.GPULayers,
		SettingLastModel: &a.LastModel,
		SettingDefaults:  &a.Defaults,
	} {
		raw, ok, err := s.AppSetting(key)
		if err != nil {
			return initial, err
		}
		if !ok {
			continue
		}
		if err := json.Unmarshal([]byte(raw), v); err != nil {
			return initial, fmt.Errorf("保存されている設定 %s が読めません(%q): %w", key, raw, err)
		}
	}
	return a, nil
}

// SaveAppSetting はアプリ全体の設定を1つ保存する。
func SaveAppSetting(s Store, key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := s.SetAppSetting(key, string(b)); err != nil {
		return fmt.Errorf("設定 %s を保存できません: %w", key, err)
	}
	return nil
}
