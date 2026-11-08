package config

// env.go: 環境変数(AIBOW_ で始まるもの)を読む。アプリ(_cmd/aibow)と aibow-server で同じ読み方にする。
//
// 誤りは見つけたところで止めず、まとめて返す(Err)。書き間違いが3つあれば、1回の起動で3つとも分かるように。
// アプリのリリース版(wails3 build)はコンソールが無いので、ログに出しただけでは誰にも見えない。呼ぶ側が画面やエラーで知らせる。
// 値が無い(空)ときは既定の値を使い、誤りにしない。

import (
	"errors"
	"fmt"
	"os"
	"strconv"
)

// Env は環境変数を読む係。ゼロ値のまま使える。
type Env struct {
	errs []error
}

// String は key の値(無ければ def)。
func (e *Env) String(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// Int は key の値を整数で読む(無いか、整数でなければ def。整数でなければ誤りに足す)。
func (e *Env) Int(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		e.Add(fmt.Errorf("%s は整数で指定してください: %q", key, v))
		return def
	}
	return n
}

// Bool は key の値を真偽値で読む(1 / 0・true / false など。無いか、読めなければ def)。
func (e *Env) Bool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		e.Add(fmt.Errorf("%s は 1 か 0(true か false)で指定してください: %q", key, v))
		return def
	}
	return b
}

// Float は key の値を数値で読む(無いか、数値でなければ def)。
func (e *Env) Float(key string, def float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		e.Add(fmt.Errorf("%s は数値で指定してください: %q", key, v))
		return def
	}
	return f
}

// Add は誤りを足す。読んだ値を呼ぶ側で確かめた誤り(1 以上でない・フォルダが無いなど)も、ここにまとめる。
func (e *Env) Add(err error) {
	e.errs = append(e.errs, err)
}

// Err は、ここまでの誤りをまとめたもの(無ければ nil)。
func (e *Env) Err() error {
	return errors.Join(e.errs...)
}
