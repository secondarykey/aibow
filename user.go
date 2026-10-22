package aibow

// user.go: 会話の持ち主(利用者)を ctx で運ぶ仕組み(0017 の 12)。
//
// 会話・プロファイル・表示の設定は「その人のもの」。保存先(Store)は、誰のものを読み書きするかを ctx から知る。
// メソッドの引数に利用者を足さずに ctx で運ぶのは、利用者を決めるのが呼ぶ側の一番外(aibow-server の HTTP の層)だけだから。
// 間の会話の仕組みや画面向けの処理は、受け取った ctx をそのまま渡すだけで、利用者を作ったり差し替えたりしない
// (画面から送られてきた値で利用者を決めると、ほかの人のふりができてしまう)。
//
// ctx の値は、型を自分で作ったキー(userKey)で入れる。ほかのパッケージが同じキーを作れないので、ここ以外からは入れ替えられない。

import (
	"context"
	"errors"
	"uuid"
)

// LocalUser は、ログインなしの形の利用者(0017 の 8)。デスクトップのアプリの手元の DB と、
// 127.0.0.1 だけで待ち受ける aibow-server(ログインが要らない形)は、この1人の会話として扱う。
// 利用者の表が無かったころの会話・プロファイルも、この利用者のものにした(DB の版 6)。
//
// 決まった値にしているのは、DB の作り変え(SQL)にも同じ値を書くため。値を変えると、今ある会話の持ち主が分からなくなる。
var LocalUser = uuid.MustParse("00000000-0000-7000-8000-000000000001")

// ErrNoUser は、誰の会話かが ctx に無いときのエラー。利用者を決める層を通らずに保存先を呼んだ(作りの間違い)。
// 黙って誰かのものとして扱うと、ほかの人の会話が見えてしまうので、断る。
var ErrNoUser = errors.New("誰の会話かが分かりません(利用者が決まっていない呼び出しです)")

type userKey struct{}

// WithUser は、利用者 id を入れた ctx を返す。aibow-server の HTTP の層(ログインを確かめたところ)と、起動のときの準備だけで使う。
func WithUser(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, userKey{}, id)
}

// UserOf は、ctx に入っている利用者。入っていなければ ok=false。
func UserOf(ctx context.Context) (id uuid.UUID, ok bool) {
	id, ok = ctx.Value(userKey{}).(uuid.UUID)
	return id, ok
}
