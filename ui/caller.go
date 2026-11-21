package ui

// caller.go: 画面用の口を呼んだ人(0018 の 11-0)。
//
// 誰の会話かは aibow.WithUser で ctx に入れる(0017 の 12)。それに加えて、サーバーの PC の事情(モデルを降ろす・層の数・置き場所・ツールの既定)を
// 見せたり変えたりしてよい人かも ctx で運ぶ。決めるのは aibow-server の who(cookie のセッションと、ログインが要る形か)で、
// 画面から送られてきた値では決めない(ほかの人のふりができてしまうため)。
//
// デスクトップのアプリ(Wails)からの呼び出しの ctx には入らない。アプリモードはその PC の人が使い、サーバーモードは
// サーバーの PC の事情を見せないので、どちらもこの印を見ない(DirectHost・RemoteHost)。見るのは ServerHost だけ。

import (
	"context"
	"uuid"
)

// Caller は画面用の口を呼んだ人。
type Caller struct {
	ID uuid.UUID
	// Name は利用者の名前(ログに出す。ツールを誰が動かしたか。0024 の決定事項 11)
	Name string
	// Admin は管理者か(aibow-server user admin で決めた人)
	Admin bool
	// NoLogin は、ログインなしの形(aibow-server の login: false)の呼び出しか。そこには管理者がいないので、管理者と同じに扱う(0046 の決定事項 3)。
	// server.db の利用者 local の管理者の印(users.admin)は変えない(グループ・配るプロファイルの「管理者か」に影響させないため)
	NoLogin bool
}

// ManagesPC は、サーバーの PC の事情を見せたり変えたりしてよいか。ログインが要る形では管理者だけ、要らない形ではいつも(0046)。
//
// 0018 の決定事項 8 では「サーバーの PC の上(127.0.0.1)から開いた画面」にも許していたが、どこから来たかはプロキシなどで簡単に
// 「サーバーの PC の上」に見えてしまうので、0046 で「誰か」だけで決めるようにした(OpenAI 互換の口の api.pcSide と同じ線)。
func (c Caller) ManagesPC() bool { return c.Admin || c.NoLogin }

type callerKey struct{}

func withCaller(ctx context.Context, c Caller) context.Context {
	return context.WithValue(ctx, callerKey{}, c)
}

// callerOf は、ctx の呼んだ人(画面用の口から呼ばれたときだけ入っている)。
func callerOf(ctx context.Context) (Caller, bool) {
	c, ok := ctx.Value(callerKey{}).(Caller)
	return c, ok
}

// managesPC は、ctx の呼んだ人が、サーバーの PC の事情を見せたり変えたりしてよいか。呼んだ人が入っていなければ false。
func managesPC(ctx context.Context) bool {
	c, ok := callerOf(ctx)
	return ok && c.ManagesPC()
}
