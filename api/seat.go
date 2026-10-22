package api

// seat.go: 生成の順番待ちの席。
//
// 作業領域(KV キャッシュ)が1つなので、生成とモデルの読み込みは1つずつしかできない。
// 席が1つのチャンネルにして、空くまで待たせる(順番待ち。断らない)。
// ステップ9で、aibow-server の中で動くブラウザの会話(ui パッケージ)も同じ席を通すように、Server の外に出した(0016 の 11)。
// HTTP で自分自身に頼まずに席だけを分け合うので、Cline とブラウザの会話が KV キャッシュと GPU を取り合っても壊れない。

import "context"

// Seat は順番待ちの席。
type Seat struct {
	ch chan struct{}
}

// NewSeat は空いた席を作る。
func NewSeat() *Seat {
	return &Seat{ch: make(chan struct{}, 1)}
}

// Take は席が空くまで待って取る。待っている間に ctx が終わったら(つながりが切れた・停止した)false(列から外れる)。
// 取れたら、終わったときに Release で返す。
func (s *Seat) Take(ctx context.Context) bool {
	select {
	case s.ch <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

// TryTake は、席が空いていれば取る(待たない)。取れなければ false。
// 待つことになると分かったときにだけ「待っています」と知らせるために使う。
func (s *Seat) TryTake() bool {
	select {
	case s.ch <- struct{}{}:
		return true
	default:
		return false
	}
}

// Release は席を返す。
func (s *Seat) Release() { <-s.ch }

// Busy は、誰かが席を使っているか(ログに「順番待ち」と出すため)。
func (s *Seat) Busy() bool { return len(s.ch) > 0 }
