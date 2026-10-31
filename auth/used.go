package auth

// used.go: 最後に使った時刻と表示名(0031)。

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/secondarykey/aibow/log"
)

// usedEvery は、最後に使った時刻を書き直す間隔(0031 の決定事項 21)。
// 使うたび(画面の呼び出しは1回の操作で何度も来る)に DB へ書くと、書き込みが読み出しと同じだけ増える。
// 利用者のタブに出すのは「今日・昨日・n 日前」なので、1日に1回で足りる(セッションの期限を延ばす extendEvery と同じ考え方)
const usedEvery = 24 * time.Hour

// markUsed は、前に書いてから usedEvery 以上たっていれば、u の最後に使った時刻を now にする。
// 書けなくても使うのは止めない(記録のためのもので、ログインの可否には関わらないため。ログには出す)。
func markUsed(ctx context.Context, st Store, u *User, now time.Time) {
	if !u.LastUsedAt.IsZero() && now.Sub(u.LastUsedAt) < usedEvery {
		return
	}
	if err := st.SetLastUsed(ctx, u.ID, now); err != nil {
		log.Warnf("利用者 %s の最後に使った時刻を書けません: %v", u.Name, err)
		return
	}
	u.LastUsedAt = now
}

// MaxDisplayName は表示名の長さの上限(文字数。0031 の決定事項 19)
const MaxDisplayName = 64

// CheckDisplayName は表示名の決まり(0031 の決定事項 39)。前後の空白を削ったものを返す。
// 改行などの制御文字は断る(一覧の1行が崩れ、ログや画面で別の行に見せかけられるため)。空はよい(ログインの名前を出す)。
func CheckDisplayName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) > MaxDisplayName {
		return "", errors.New("表示名は 64 文字までです")
	}
	if strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return "", errors.New("表示名に改行やタブなどの制御文字は使えません")
	}
	return name, nil
}
