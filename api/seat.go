package api

// seat.go: 生成の順番待ちの席。
//
// 席の数は、作業領域の中のシーケンス(KV キャッシュの列)の数(0023。既定 1)。席が全部ふさがっていたら、空くまで待たせる(順番待ち。断らない)。
// ステップ9で、aibow-server の中で動くブラウザの会話(ui パッケージ)も同じ席を通すように、Server の外に出した(0016 の 11)。
// HTTP で自分自身に頼まずに席だけを分け合うので、Cline とブラウザの会話が KV キャッシュと GPU を取り合っても壊れない。
//
// モデルの読み直し(入れ替え・降ろす・GPU の層数)は、作業領域を作り直して全員の KV キャッシュを捨てるので、
// 席を持った人が、ほかの席が全部空くまで待ってから一人で行う(Exclusive。決定事項 9)。
// 一人で使いたい人が待っている間は、新しく来た人を座らせない(いつまでも空かないことが無いように)。

import (
	"context"
	"sync"
)

// Seat は順番待ちの席。
type Seat struct {
	mu      sync.Mutex
	n       int           // 席の数
	taken   int           // 座っている数
	waiting int           // 一人で使うのを待っている数
	alone   bool          // 一人で使っている
	changed chan struct{} // 座っている数などが変わったら閉じて作り直す(待っている人を起こす)
}

// NewSeat は席が1つの、空いた席を作る。
func NewSeat() *Seat { return NewSeats(1) }

// NewSeats は席が n 個(1 未満なら 1)の、空いた席を作る。
func NewSeats(n int) *Seat {
	return &Seat{n: max(1, n), changed: make(chan struct{})}
}

// Size は席の数。
func (s *Seat) Size() int { return s.n }

// Take は席が空くまで待って取る。待っている間に ctx が終わったら(つながりが切れた・停止した)false(列から外れる)。
// 取れたら、終わったときに Release で返す。
func (s *Seat) Take(ctx context.Context) bool {
	for {
		s.mu.Lock()
		if s.free() {
			s.taken++
			s.mu.Unlock()
			return true
		}
		ch := s.changed
		s.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return false
		}
	}
}

// TryTake は、席が空いていれば取る(待たない)。取れなければ false。
// 待つことになると分かったときにだけ「待っています」と知らせるために使う。
func (s *Seat) TryTake() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.free() {
		return false
	}
	s.taken++
	return true
}

// Release は席を返す。
func (s *Seat) Release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.taken--
	s.wake()
}

// Busy は、席が全部ふさがっているか(新しく来た人が待つことになるか。ログに「順番待ち」と出すため)。
func (s *Seat) Busy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.free()
}

// Exclusive は、席を1つ持っている人が、ほかの席が全部空くまで待って、一人で使う(モデルの読み直し)。
// 返した関数を呼ぶと、ほかの人も座れるように戻す(自分の席は持ったまま。あとで Release で返す)。
//
// 待っている間は自分の席も空けておく(2人が同時に一人で使おうとして、お互いを待ち続けないように)。
// 席が1つなら、もう一人なので待たない(今までと同じ)。
func (s *Seat) Exclusive() (done func()) {
	s.mu.Lock()
	s.taken--
	s.waiting++
	s.wake()
	for s.taken > 0 || s.alone {
		ch := s.changed
		s.mu.Unlock()
		<-ch
		s.mu.Lock()
	}
	s.waiting--
	s.alone = true
	s.taken++
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.alone = false
		s.wake()
	}
}

// free は、新しく来た人が座れるか。s.mu を持った状態で呼ぶ。
func (s *Seat) free() bool { return s.taken < s.n && !s.alone && s.waiting == 0 }

// wake は待っている人を起こす。s.mu を持った状態で呼ぶ。
func (s *Seat) wake() {
	close(s.changed)
	s.changed = make(chan struct{})
}
