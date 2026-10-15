package tool

// ask.go: 実行の途中でユーザーに確認を求めるツール(ステップ5。MCP のサーバーが MRTR で聞いてくる)。
//
// 組み込みのツール(Confirmable)は、実行する「前」に会話の仕組みが確認する。何を見せるかはツールが作り(Preview)、
// 聞くのは会話の仕組み。MCP のサーバーのツールは中身が見えないので、何を確認すべきかはサーバーにしか分からない。
// そこでサーバーが実行の途中で「これでよいか」を聞いてくる(MRTR: サーバーは聞きたいことを結果として返し、クライアントは答えを付けて同じ呼び出しを送り直す)。
// 聞くのは会話の仕組みのままにしたいので、ツールには「聞く関数」(Ask)を渡す。

import "context"

// Answer はユーザーの確認の答え。
type Answer struct {
	Approved bool
	Reason   string // 拒否したときの理由(空でもよい)
}

// Ask は、ツールの実行の途中でユーザーに確認を求める関数。会話の仕組みが渡す。答えが出るまで待ち、ctx が止まったらエラーを返す。
type Ask func(ctx context.Context, pv Preview) (Answer, error)

// Interactive は、実行の途中でユーザーに確認を求めることがあるツール(MCP のサーバーのツール)。
//
// 時間の上限は、ツールが自分で数える(ユーザーが確認のカードを読んでいる時間を数えないため)。
// 会話の仕組みは Run の代わりに RunInteractive を呼び、時間の上限をかけない(停止の ctx だけを渡す)。
type Interactive interface {
	Tool
	RunInteractive(ctx context.Context, args Args, ask Ask) (string, error)
}

// RejectedText は、ユーザーが実行を拒否したときにモデルに返す結果の文。
// 会話の仕組み(組み込みのツールの確認)と MCP のサーバー(サーバーからの確認)で同じ文にする。
func RejectedText(reason string) string {
	if reason == "" {
		reason = "(書かれていません)"
	}
	return "ユーザーが実行を拒否しました。理由: " + reason
}
