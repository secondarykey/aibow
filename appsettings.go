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
	SettingHistoryTokens = "history_tokens"        // 会話の上限
	SettingReplyTokens   = "reply_tokens"          // 返事の上限
	SettingGPULayers     = "gpu_layers"            // GPU に載せる層の数
	SettingLastModel     = "last_model"            // 最後に使ったモデルの ID。次の起動で読み込む
	SettingDefaults      = "conversation_defaults" // 新しい会話の既定の設定

	// SettingNCtx は古い版の「コンテキスト長」。今は使わず、会話の上限への引き継ぎのためだけに読む
	SettingNCtx = "n_ctx"
)

// minHistoryTokens は、古い版のコンテキスト長から引き継ぐときの会話の上限の最低値。
// コンテキスト長より返事の上限のほうが大きかった場合でも、会話ができる大きさを残す。
const minHistoryTokens = 2048

// WorkspaceSettings は作業領域(コンテキスト)に関わる設定の塊。
//
// どれも変えるとエンジンに手を入れる必要がある(モデルの読み直しか、作業領域の作り直し)。
// あとで「プロファイル」(設定に名前を付けて切り替える)を作るときは、この塊をそのままプロファイルの中に移す。
// 今のアプリ全体の設定は「名前のない既定のプロファイル」という位置づけ。
type WorkspaceSettings struct {
	Model     string `json:"model"`      // 最後に使ったモデルの ID(app_settings のキーは last_model)
	GPULayers int    `json:"gpu_layers"` // GPU に載せる層の数

	// 利用者が気にするのは「会話をどれだけ続けられるか」なので、それを直接設定させる。
	// 作業領域の大きさは、この2つの合計を aibow が計算して確保する(ContextSize)。
	HistoryTokens int `json:"history_tokens"` // 会話の上限: モデルに渡す文字列のトークン数の上限。compact の基準
	ReplyTokens   int `json:"reply_tokens"`   // 返事の上限: 1回の返事で生成するトークン数の上限(思考 + 答え)
}

// ContextSize は確保する作業領域の大きさ(トークン数)。
// 会話の上限まで詰めたプロンプトのあとに、返事の上限まで書いても溢れない大きさにする。
func (w WorkspaceSettings) ContextSize() int {
	return w.HistoryTokens + w.ReplyTokens
}

// AppSettings はアプリ全体の設定。
type AppSettings struct {
	Workspace WorkspaceSettings    `json:"workspace"`
	Defaults  ConversationSettings `json:"defaults"` // 新しい会話の既定の設定
}

// LoadAppSettings は保存先からアプリ全体の設定を読む。
// 保存されていない項目は initial の値のまま(環境変数から作った初期値)。
// 画面で一度変えると保存先に入るので、以降は環境変数より保存した値が優先される。
//
// notes は、古い版の設定を引き継いだときのお知らせ(ログに出す)。
func LoadAppSettings(s Store, initial AppSettings) (a AppSettings, notes []string, err error) {
	a = initial
	found := map[string]bool{}
	for key, v := range map[string]any{
		SettingHistoryTokens: &a.Workspace.HistoryTokens,
		SettingReplyTokens:   &a.Workspace.ReplyTokens,
		SettingGPULayers:     &a.Workspace.GPULayers,
		SettingLastModel:     &a.Workspace.Model,
		SettingDefaults:      &a.Defaults,
	} {
		ok, err := loadAppSetting(s, key, v)
		if err != nil {
			return initial, nil, err
		}
		found[key] = ok
	}

	if !found[SettingHistoryTokens] && !found[SettingReplyTokens] {
		note, err := migrateNCtx(s, &a.Workspace)
		if err != nil {
			return initial, nil, err
		}
		if note != "" {
			notes = append(notes, note)
		}
	}
	a.Defaults = a.Defaults.asDefaults()
	return a, notes, nil
}

// migrateNCtx は、古い版の「コンテキスト長」(n_ctx)と、新しい会話の既定の「返事の上限」(max_tokens)を、
// 会話の上限と返事の上限に引き継ぐ。どちらも無ければ何もしない(初期値のまま)。
//
// 引き継いだ値はその場で保存する。保存しないと、あとで返事の上限だけ変えたときに、
// 次の起動で n_ctx から計算し直されて会話の上限がずれてしまう。
func migrateNCtx(s Store, w *WorkspaceSettings) (string, error) {
	var nCtx int
	ok, err := loadAppSetting(s, SettingNCtx, &nCtx)
	if err != nil || !ok {
		return "", err
	}
	// 返事の上限は、以前は会話ごとの設定だった。新しい会話の既定に入っていた値を使う
	var old struct {
		MaxTokens int `json:"max_tokens"`
	}
	if _, err := loadAppSetting(s, SettingDefaults, &old); err != nil {
		return "", err
	}
	if old.MaxTokens > 0 {
		w.ReplyTokens = old.MaxTokens
	}
	w.HistoryTokens = max(nCtx-w.ReplyTokens, minHistoryTokens)

	if err := SaveAppSetting(s, SettingHistoryTokens, w.HistoryTokens); err != nil {
		return "", err
	}
	if err := SaveAppSetting(s, SettingReplyTokens, w.ReplyTokens); err != nil {
		return "", err
	}
	return fmt.Sprintf("保存済みのコンテキスト長 %d を、会話の上限 %d + 返事の上限 %d に引き継ぎました",
		nCtx, w.HistoryTokens, w.ReplyTokens), nil
}

// loadAppSetting は設定を1つ読んで v に入れる。保存されていなければ ok=false で、v はそのまま。
func loadAppSetting(s Store, key string, v any) (ok bool, err error) {
	raw, ok, err := s.AppSetting(key)
	if err != nil || !ok {
		return false, err
	}
	if err := json.Unmarshal([]byte(raw), v); err != nil {
		return false, fmt.Errorf("保存されている設定 %s が読めません(%q): %w", key, raw, err)
	}
	return true, nil
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
