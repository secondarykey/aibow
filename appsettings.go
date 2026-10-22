package aibow

// appsettings.go: アプリの設定(会話をまたいで1つだけのもの)と、人ごとの設定と、新しい会話の既定の設定。
//
// 保存先の app_settings(アプリ全体)と user_settings(人ごと)は「キー → 値」の表。値は JSON の文字列で入れる
// (数値も文字列も構造体も同じ形で入り、DB を直接のぞいても読める)。
//
// アプリ全体か人ごとかは、誰が決めることかで分ける(0017 の 6):
//   - アプリ全体: モデルと GPU の層数(モデルは VRAM に1つしか載らず、全員で分け合う)
//   - 人ごと: 表示の設定と、新しい会話で最初に選ばれるプロファイル(プロファイルが人ごとなので。0017 の 5)

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
)

// app_settings のキー(アプリ全体)
const (
	SettingGPULayers = "gpu_layers" // GPU に載せる層の数(まだ覚えていないモデルに使う値)
	// SettingModelGPULayers は、モデルごとの GPU に載せる層の数(モデルの ID → 層の数。0014 の 4)
	SettingModelGPULayers = "model_gpu_layers"
	SettingLastModel      = "last_model" // 最後に使ったモデルの ID。次の起動で読み込む

	// SettingDefaults は、プロファイルより前の「新しい会話の既定の設定」(会話の上限・返事の上限も入る)。
	// 今はプロファイル「既定」への引き継ぎ(PrepareProfiles)のためだけに読み、書かない(0014)
	SettingDefaults = "conversation_defaults"

	// 会話の上限・返事の上限は、以前はアプリ全体の設定で、ここに置いていた。
	// 今は新しい会話の既定(SettingDefaults)に入る。既定にまだ入っていないときの引き継ぎのためだけに読む(0009)
	SettingHistoryTokens = "history_tokens"
	SettingReplyTokens   = "reply_tokens"

	// SettingNCtx は古い版の「コンテキスト長」。今は使わず、会話の上限への引き継ぎのためだけに読む
	SettingNCtx = "n_ctx"
)

// user_settings のキー(人ごと。0017 の 6)。DB の版 6 より前は app_settings にあった
const (
	// SettingDefaultProfile は、新しい会話で最初に選ばれるプロファイルの ID(0014)。新しい会話の既定は、このプロファイルの値
	SettingDefaultProfile = "default_profile"
	// SettingPinQuestion は、返事を読んでいてスクロールしたときに、その質問を右上に出しておくか(画面の設定。DisplaySettings)
	SettingPinQuestion = "pin_question"
	// SettingTheme は、画面の明るさ(ライト / ダーク / システムに合わせる。DisplaySettings)
	SettingTheme = "theme"
)

// minHistoryTokens は、古い版のコンテキスト長から引き継ぐときの会話の上限の最低値。
// コンテキスト長より返事の上限のほうが大きかった場合でも、会話ができる大きさを残す。
const minHistoryTokens = 2048

// WorkspaceSettings はモデルの読み込みに関わる設定の塊。変えるとモデルの読み直しになる。
//
// モデルはプロファイルの値にも入る(0014。会話ごとにモデルを持つのはステップ 7 の 2)。GPU の層数はこの PC のものなので、プロファイルに入れない。
// 会話の上限・返事の上限(作業領域の大きさ)は、会話ごとの設定に移した(ConversationSettings。0009)。
type WorkspaceSettings struct {
	Model     string `json:"model"`      // 最後に使ったモデルの ID(app_settings のキーは last_model)
	GPULayers int    `json:"gpu_layers"` // GPU に載せる層の数。まだ覚えていないモデルに使う
	// ModelGPULayers は、モデルごとに覚えた GPU に載せる層の数(モデルの ID → 層の数)。
	// 載せられる層の数はモデルの大きさと PC の VRAM で決まるので、モデルごとにこの PC に覚える(プロファイルには入れない。0014 の 4)
	ModelGPULayers map[string]int `json:"model_gpu_layers"`
}

// LayersFor は、モデル model を読み込むときに GPU に載せる層の数。覚えていなければ全体の値(GPULayers)。
func (w WorkspaceSettings) LayersFor(model string) int {
	if n, ok := w.ModelGPULayers[model]; ok {
		return n
	}
	return w.GPULayers
}

// DisplaySettings は画面の見せ方の設定。会話によらず人ごとに1つ(会話の設定にもプロファイルにも入れない。0017 の 6)。
// 生成には関わらないので、変えてもモデルや作業領域は読み直さない。
type DisplaySettings struct {
	// PinQuestion は、長い返事をスクロールして質問が画面の外に出たとき、その質問を右上に出しておくか
	PinQuestion bool `json:"pin_question"`
	// Theme は画面の明るさ。ThemeSystem(OS の設定に合わせる)・ThemeLight・ThemeDark。空は ThemeSystem として扱う
	Theme string `json:"theme"`
}

// 画面の明るさ(DisplaySettings.Theme)
const (
	ThemeSystem = "system"
	ThemeLight  = "light"
	ThemeDark   = "dark"
)

// Validate は、知らない値を保存しないように確かめる。
func (d DisplaySettings) Validate() error {
	switch d.Theme {
	case "", ThemeSystem, ThemeLight, ThemeDark:
		return nil
	}
	return fmt.Errorf("画面の明るさ %q は選べません(system / light / dark のどれか)", d.Theme)
}

// AppSettings はアプリの設定と、新しい会話の既定の設定。
// 表示の設定と新しい会話の既定は人ごと(読んだ人のもの)、作業領域はアプリ全体のもの。
type AppSettings struct {
	Workspace WorkspaceSettings    `json:"workspace"`
	Display   DisplaySettings      `json:"display"`
	Defaults  ConversationSettings `json:"defaults"` // 新しい会話の既定の設定(新しい会話で最初に選ばれるプロファイルの値)
}

// LoadAppSettings は保存先から設定を読む。人ごとの設定(表示の設定・新しい会話の既定)は ctx の利用者のもの。
// 保存されていない項目は initial の値のまま(環境変数から作った初期値)。
// 画面で一度変えると保存先に入るので、以降は環境変数より保存した値が優先される。
//
// notes は、古い版の設定を引き継いだときのお知らせ(ログに出す)。
//
// 新しい会話の既定の会話の上限・返事の上限は、古い置き場(history_tokens / reply_tokens、もっと古い n_ctx)→
// 既定の設定(conversation_defaults)→ 新しい会話で最初に選ばれるプロファイルの順に読み、後から読んだものが勝つ。
// プロファイルが無ければ(この機能より前の DB か、初めての起動)、プロファイルの手前までの値になる。
// その値から、起動したときに PrepareProfiles がプロファイル「既定」を作る。
func LoadAppSettings(ctx context.Context, s Store, initial AppSettings) (a AppSettings, notes []string, err error) {
	a = initial
	found := map[string]bool{}
	// 順番が要るので、マップではなく並びで読む(既定の設定は最後。古い置き場の値を上書きする)
	for _, kv := range []struct {
		key string
		v   any
	}{
		{SettingHistoryTokens, &a.Defaults.HistoryTokens},
		{SettingReplyTokens, &a.Defaults.ReplyTokens},
		{SettingGPULayers, &a.Workspace.GPULayers},
		{SettingLastModel, &a.Workspace.Model},
		{SettingModelGPULayers, &a.Workspace.ModelGPULayers},
	} {
		ok, err := loadAppSetting(ctx, s, kv.key, kv.v)
		if err != nil {
			return initial, nil, err
		}
		found[kv.key] = ok
	}

	// モデルごとの層の数より前の DB:今の層の数を、最後に使ったモデルの値として覚える(0014 の 10)
	if !found[SettingModelGPULayers] && found[SettingGPULayers] && a.Workspace.Model != "" {
		a.Workspace.ModelGPULayers = map[string]int{a.Workspace.Model: a.Workspace.GPULayers}
		if err := SaveAppSetting(ctx, s, SettingModelGPULayers, a.Workspace.ModelGPULayers); err != nil {
			return initial, nil, err
		}
		notes = append(notes, fmt.Sprintf("GPU に載せる層の数 %d を、モデル %s の値として覚えました", a.Workspace.GPULayers, a.Workspace.Model))
	}
	if a.Display, err = LoadDisplay(ctx, s, initial.Display); err != nil {
		return initial, nil, err
	}
	if a.Workspace.ModelGPULayers == nil {
		a.Workspace.ModelGPULayers = map[string]int{}
	}

	if !found[SettingHistoryTokens] && !found[SettingReplyTokens] {
		note, err := migrateNCtx(ctx, s, &a.Defaults)
		if err != nil {
			return initial, nil, err
		}
		if note != "" {
			notes = append(notes, note)
		}
	}
	legacy := a.Defaults
	if _, err := loadAppSetting(ctx, s, SettingDefaults, &a.Defaults); err != nil {
		return initial, nil, err
	}
	// 既定に会話の上限・返事の上限が入っていなければ(0 なら)、古い置き場か初期値のまま
	a.Defaults.HistoryTokens = cmp.Or(a.Defaults.HistoryTokens, legacy.HistoryTokens)
	a.Defaults.ReplyTokens = cmp.Or(a.Defaults.ReplyTokens, legacy.ReplyTokens)

	// プロファイルがあれば、新しい会話の既定はその値(作業領域の最初の大きさにも使うので、ここで読む)
	if p, ok, err := storedDefaultProfile(ctx, s); err != nil {
		return initial, nil, err
	} else if ok {
		a.Defaults = p.Values.settings()
	}
	a.Defaults = a.Defaults.asDefaults()
	return a, notes, nil
}

// LoadDisplay は、ctx の利用者の表示の設定を読む。保存されていない項目は initial の値のまま。
func LoadDisplay(ctx context.Context, s Store, initial DisplaySettings) (DisplaySettings, error) {
	d := initial
	if _, err := loadUserSetting(ctx, s, SettingPinQuestion, &d.PinQuestion); err != nil {
		return initial, err
	}
	if _, err := loadUserSetting(ctx, s, SettingTheme, &d.Theme); err != nil {
		return initial, err
	}
	if d.Validate() != nil || d.Theme == "" {
		d.Theme = ThemeSystem // 知らない値(手で書き換えた DB など)はシステムに合わせる
	}
	return d, nil
}

// storedDefaultProfile は、新しい会話で最初に選ばれるプロファイルを読む。まだ無ければ ok=false。
func storedDefaultProfile(ctx context.Context, s Store) (Profile, bool, error) {
	list, err := s.ListProfiles(ctx)
	if err != nil || len(list) == 0 {
		return Profile{}, false, err
	}
	i, err := markIndex(ctx, s, list)
	if err != nil || i < 0 {
		return Profile{}, false, err // 印が無ければ、PrepareProfiles が付け直す
	}
	return list[i], true, nil
}

// migrateNCtx は、古い版の「コンテキスト長」(n_ctx)と、新しい会話の既定の「返事の上限」(max_tokens)を、
// 会話の上限と返事の上限に引き継ぐ。どちらも無ければ何もしない(初期値のまま)。
//
// 引き継いだ値はその場で保存する。保存しないと、あとで返事の上限だけ変えたときに、
// 次の起動で n_ctx から計算し直されて会話の上限がずれてしまう。
func migrateNCtx(ctx context.Context, s Store, w *ConversationSettings) (string, error) {
	var nCtx int
	ok, err := loadAppSetting(ctx, s, SettingNCtx, &nCtx)
	if err != nil || !ok {
		return "", err
	}
	// 返事の上限は、以前は会話ごとの設定だった。新しい会話の既定に入っていた値を使う
	var old struct {
		MaxTokens int `json:"max_tokens"`
	}
	if _, err := loadAppSetting(ctx, s, SettingDefaults, &old); err != nil {
		return "", err
	}
	if old.MaxTokens > 0 {
		w.ReplyTokens = old.MaxTokens
	}
	w.HistoryTokens = max(nCtx-w.ReplyTokens, minHistoryTokens)

	if err := SaveAppSetting(ctx, s, SettingHistoryTokens, w.HistoryTokens); err != nil {
		return "", err
	}
	if err := SaveAppSetting(ctx, s, SettingReplyTokens, w.ReplyTokens); err != nil {
		return "", err
	}
	return fmt.Sprintf("保存済みのコンテキスト長 %d を、会話の上限 %d + 返事の上限 %d に引き継ぎました",
		nCtx, w.HistoryTokens, w.ReplyTokens), nil
}

// loadAppSetting はアプリ全体の設定を1つ読んで v に入れる。保存されていなければ ok=false で、v はそのまま。
func loadAppSetting(ctx context.Context, s Store, key string, v any) (ok bool, err error) {
	return decodeSetting(key, v)(s.AppSetting(ctx, key))
}

// loadUserSetting は ctx の利用者の設定を1つ読んで v に入れる。保存されていなければ ok=false で、v はそのまま。
func loadUserSetting(ctx context.Context, s Store, key string, v any) (ok bool, err error) {
	return decodeSetting(key, v)(s.UserSetting(ctx, key))
}

// decodeSetting は、保存先から読んだ値(JSON)を v に入れる関数を返す(読む先がアプリ全体か人ごとかによらない部分)。
func decodeSetting(key string, v any) func(raw string, ok bool, err error) (bool, error) {
	return func(raw string, ok bool, err error) (bool, error) {
		if err != nil || !ok {
			return false, err
		}
		if err := json.Unmarshal([]byte(raw), v); err != nil {
			return false, fmt.Errorf("保存されている設定 %s が読めません(%q): %w", key, raw, err)
		}
		return true, nil
	}
}

// SaveAppSetting はアプリ全体の設定を1つ保存する。
func SaveAppSetting(ctx context.Context, s Store, key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := s.SetAppSetting(ctx, key, string(b)); err != nil {
		return fmt.Errorf("設定 %s を保存できません: %w", key, err)
	}
	return nil
}

// SaveUserSetting は ctx の利用者の設定を1つ保存する。
func SaveUserSetting(ctx context.Context, s Store, key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := s.SetUserSetting(ctx, key, string(b)); err != nil {
		return fmt.Errorf("設定 %s を保存できません: %w", key, err)
	}
	return nil
}
