package api

import (
	"context"
	"testing"
	"time"
)

// 席が2つなら2人まで座れ、3人目は待つ。全部ふさがっているときだけ Busy(0023 の決定事項 16)
func TestSeatsTwo(t *testing.T) {
	s := NewSeats(2)
	if !s.TryTake() || s.Busy() {
		t.Fatal("1人目は座れて、まだ空きがあるはず")
	}
	if !s.TryTake() || !s.Busy() {
		t.Fatal("2人目は座れて、全部ふさがるはず")
	}
	if s.TryTake() {
		t.Fatal("3人目は座れないはず")
	}
	got := make(chan bool)
	go func() { got <- s.Take(context.Background()) }()
	select {
	case <-got:
		t.Fatal("空くまで待つはず")
	case <-time.After(50 * time.Millisecond):
	}
	s.Release()
	if !<-got {
		t.Fatal("空いたら座れるはず")
	}
}

// 一人で使う(モデルの読み直し)のは、ほかの席が全部空いてから。待っている間は新しく来た人を座らせない
func TestSeatExclusive(t *testing.T) {
	s := NewSeats(2)
	s.TryTake() // A(生成中)
	s.TryTake() // B(モデルを読み直したい)

	alone := make(chan func())
	go func() { alone <- s.Exclusive() }()
	select {
	case <-alone:
		t.Fatal("A が席を持っている間は、一人で使えないはず")
	case <-time.After(50 * time.Millisecond):
	}
	if s.TryTake() {
		t.Fatal("一人で使うのを待っている間は、新しく来た人は座れないはず")
	}
	s.Release() // A が終わる
	done := <-alone
	if s.TryTake() {
		t.Fatal("一人で使っている間は、新しく来た人は座れないはず")
	}
	done()
	if !s.TryTake() {
		t.Fatal("一人で使い終わったら座れるはず(B はまだ席を持っている)")
	}
	if s.TryTake() {
		t.Fatal("B と C で2つともふさがっているはず")
	}
}

// 2人が同時に一人で使おうとしても、お互いを待ち続けない
func TestSeatExclusiveBoth(t *testing.T) {
	s := NewSeats(2)
	s.TryTake()
	s.TryTake()
	finished := make(chan struct{}, 2)
	for range 2 {
		go func() {
			done := s.Exclusive()
			done()
			s.Release()
			finished <- struct{}{}
		}()
	}
	for range 2 {
		select {
		case <-finished:
		case <-time.After(2 * time.Second):
			t.Fatal("一人で使うのを待ち続けている")
		}
	}
	if s.Busy() {
		t.Fatal("全員が席を返したはず")
	}
}

// 席が1つなら、持っている人はそのまま一人で使える(今までと同じ)
func TestSeatExclusiveSingle(t *testing.T) {
	s := NewSeat()
	s.TryTake()
	done := s.Exclusive()
	done()
	if !s.Busy() {
		t.Fatal("席は持ったままのはず")
	}
	s.Release()
	if s.Busy() {
		t.Fatal("返したら空くはず")
	}
}
