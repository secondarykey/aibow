package ui

// appsettings.go: 画面から呼ぶ、アプリ全体の設定・画面の設定・つないでいる利用者・版のメソッド。DB にまだ値が無いときの初期値は settings.go。

import (
	"context"
	"errors"
	"fmt"

	"github.com/secondarykey/aibow"
	"github.com/secondarykey/aibow/engine"
	"github.com/secondarykey/aibow/log"
)

// ---- アプリ全体の設定 -------------------------------------------------------------------

// AppSettings はアプリの設定(GPU の層数・最後に使ったモデル)と、画面の設定・新しい会話の既定の設定(会話の上限・返事の上限も入る)を返す。
// GPU の層数は、読み込んでいるモデルの値(モデルごとに覚えている。0014 の 4。aibow-server 越しならサーバーの PC の値)。
// 画面の設定と新しい会話の既定は、ctx の利用者のもの(0017 の 6)。
// 設定の画面はこれを出して変える(SetGPULayers)。
func (s *Service) AppSettings(ctx context.Context) (aibow.AppSettings, error) {
	if s.startupErr != nil {
		return aibow.AppSettings{}, s.startupErr
	}
	layers := s.host.gpuLayers() // 直接の形は appMu を使うので、持つ前に聞く
	s.appMu.Lock()
	a := s.app
	s.appMu.Unlock()
	a.Workspace.GPULayers = layers
	var err error
	if a.Display, err = aibow.LoadDisplay(ctx, s.store, InitialDisplay()); err != nil {
		return aibow.AppSettings{}, err
	}
	if a.Defaults, err = s.convs.Defaults(ctx); err != nil {
		return aibow.AppSettings{}, err
	}
	return a, nil
}

// TryLimits は、会話の上限 + 返事の上限の大きさの作業領域を、今の作業領域として作り直してみる(モデルは読み直さない)。
// 画面は、会話の設定の画面・新しい会話の既定で上限を「適用」するときに呼び、確保できたら設定を保存する
// (VRAM に入らないことを、送信したときではなく変えたときに分かるようにするため。0009)。
// 作り直したら true。確保できなければ元の大きさに戻してエラーを返す。モデルが未読み込みなら、次に読み込むときの大きさだけ変わる。
// aibow-server 越しなら作り直さず、サーバーの作業領域に入るかを確かめるだけ(0015 の 3。VRAM の確認はサーバーの起動と読み直しのとき)。
func (s *Service) TryLimits(history, reply int) (bool, error) {
	if s.startupErr != nil {
		return false, s.startupErr
	}
	if history <= 0 || reply <= 0 {
		return false, errors.New("会話の上限と返事の上限は 1 以上にしてください")
	}
	n := history + reply
	changed, err := s.host.tryContext(n)
	if err != nil {
		log.Error(err.Error())
		return false, fmt.Errorf("作業領域(会話の上限 %d + 返事の上限 %d = %d トークン)を確保できません。%w", history, reply, n, err)
	}
	if changed {
		log.Noticef("会話の上限 %d + 返事の上限 %d = 作業領域 %d トークンに作り直しました(モデルは読み直していません)", history, reply, n)
	}
	return changed, nil
}

// SetGPULayers は GPU に載せる層の数を変え、今のモデルを読み込み直す。
// 失敗したときは元の層の数で読み直している。モデルが未読み込みなら nil を返す。
// 値は読み込んでいるモデルの値として覚える(モデルごと。0014 の 4)。モデルが未読み込みなら、まだ覚えていないモデルに使う全体の値を変える。
// aibow-server 越しなら、サーバーが読み直し、サーバーの PC に覚える(0015 の 9)。
func (s *Service) SetGPULayers(n int) (*engine.ModelInfo, error) {
	if s.startupErr != nil {
		return nil, s.startupErr
	}
	i, err := s.host.setGPULayers(n)
	if err != nil {
		log.Error(err.Error())
		return nil, err
	}
	if i != nil {
		logModel("GPU に載せる層を変えて読み直しました", i)
	}
	return i, nil
}

// SetDisplay は画面の設定(質問を右上に出すか・明るさ)を変える。生成には関わらないので、モデルは読み直さない。
// 設定は ctx の利用者のもの(0017 の 6)。変わった項目だけ保存し、ログに残す。
func (s *Service) SetDisplay(ctx context.Context, d aibow.DisplaySettings) error {
	if s.startupErr != nil {
		return s.startupErr
	}
	if err := d.Validate(); err != nil {
		return err
	}
	old, err := aibow.LoadDisplay(ctx, s.store, InitialDisplay())
	if err != nil {
		return err
	}
	if d.PinQuestion != old.PinQuestion {
		if err := aibow.SaveUserSetting(ctx, s.store, aibow.SettingPinQuestion, d.PinQuestion); err != nil {
			return err
		}
		log.Noticef("スクロールしたときに質問を右上に出す: %v", d.PinQuestion)
	}
	if d.Theme != old.Theme {
		if err := aibow.SaveUserSetting(ctx, s.store, aibow.SettingTheme, d.Theme); err != nil {
			return err
		}
		log.Noticef("画面の明るさ: %s", d.Theme)
	}
	return nil
}

// saveAppSetting はアプリ全体の設定を1つ保存する。
// 保存に失敗しても、変えた設定は今の起動中は効いているので、ログに出すだけにする。
// アプリ全体の設定は利用者によらないので、呼び出しの ctx は渡さない(モデルの読み込みは、会話の仕組みからも ctx なしで頼まれる)。
func (s *Service) saveAppSetting(key string, v any, apply func(*aibow.AppSettings)) {
	s.appMu.Lock()
	defer s.appMu.Unlock()
	apply(&s.app)
	if err := aibow.SaveAppSetting(context.Background(), s.store, key, v); err != nil {
		log.Error(err.Error())
	}
}

// Account は、aibow-server に誰としてつないでいるか(Account の戻り値。0017 の 10-4)。
type Account struct {
	// Login は、利用者として(API キーで)つないでいるか。アプリの中で動かすとき・キーの要らない aibow-server なら false(名前を出さない)
	Login bool   `json:"login"`
	Name  string `json:"name"` // 利用者の名前(API キーの持ち主)
}

// Account は、aibow-server に誰としてつないでいるか(画面の一覧の下に名前を出す)。
// アプリはログインの画面を持たず、settings.json の serverKey(API キー)で利用者が決まるので、その持ち主を aibow-server に聞く。
// アプリの中で動かしているときは、利用者は1人なので Login が false。
func (s *Service) Account() (Account, error) {
	if s.startupErr != nil {
		return Account{}, s.startupErr
	}
	r, ok := s.host.(*RemoteHost)
	if !ok {
		return Account{}, nil
	}
	name, login, err := r.c.Me()
	if err != nil {
		return Account{}, err
	}
	return Account{Login: login, Name: name}, nil
}

// Version はアプリのバージョン(画面の左下に出す)。起動に失敗していても返す。
func (s *Service) Version() string {
	return s.version
}
