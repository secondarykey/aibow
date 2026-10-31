package auth

// used.go: 最後に使った時刻・表示名・メールアドレス(0031)。

import (
	"context"
	"errors"
	"net/mail"
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

// MaxEmail はメールアドレスの長さの上限(文字数。SMTP の決まりの 254)
const MaxEmail = 254

// CheckEmail はメールアドレスの決まり(0031 の決定事項 43)。前後の空白を削ったものを返す。空はよい(付けない)。
// 形は最低限だけ確かめる(net/mail で名前の付かないアドレスとして読めて、空白と制御文字が無い)。本当に届くかは確かめない
func CheckEmail(email string) (string, error) {
	email = strings.TrimSpace(email)
	if email == "" {
		return "", nil
	}
	if utf8.RuneCountInString(email) > MaxEmail {
		return "", errors.New("メールアドレスは 254 文字までです")
	}
	if strings.IndexFunc(email, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return "", errors.New("メールアドレスに空白や改行は使えません")
	}
	// 「佐藤 <sato@example.com>」のような名前付きの形は断る(アドレスだけを持つ)
	if a, err := mail.ParseAddress(email); err != nil || a.Address != email {
		return "", errors.New("メールアドレスの形ではありません(例: sato@example.com)")
	}
	return email, nil
}
