package auth

// limiter.go: ログインを続けて間違えたときに、しばらく受け付けない(0017 の 10-2。127.0.0.1 の外に出すときに足した)。
//
// パスワードを片っ端から試されても、1回に時間がかかる(PBKDF2)うえに、ここで回数を絞るので、ほとんど試せない。
// 数えるのは相手のアドレスごと。名前ごとに数えると、ほかの人がわざと間違えて、その人をログインできなくできてしまうため。
// 覚えるのはメモリの中だけ(aibow-server を起動し直すと数え直し。手間をかけずに済む範囲で十分なため)。

import (
	"sync"
	"time"
)

const (
	maxFailures = 5                // この回数まで続けて間違えると
	lockout     = 15 * time.Minute // 最後に間違えてから、この間は受け付けない
)

type limiter struct {
	mu  sync.Mutex
	now func() time.Time
	// fails はアドレス → 続けて間違えた回数と最後に間違えた時刻
	fails map[string]failure
}

type failure struct {
	n    int
	last time.Time
}

func newLimiter(now func() time.Time) *limiter {
	return &limiter{now: now, fails: map[string]failure{}}
}

// wait は、addr からのログインを受け付けるまでの残り時間(受け付けるなら 0)。
func (l *limiter) wait(addr string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	f, ok := l.fails[addr]
	if !ok || f.n < maxFailures {
		return 0
	}
	left := f.last.Add(lockout).Sub(l.now())
	if left <= 0 {
		delete(l.fails, addr) // 待ち終わったら数え直し
		return 0
	}
	return left
}

// fail は、addr からのログインを間違えたことを数える。
func (l *limiter) fail(addr string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	f := l.fails[addr]
	if now.Sub(f.last) > lockout {
		f.n = 0 // 前に間違えてから時間がたっていれば、続けて間違えたことにしない
	}
	l.fails[addr] = failure{n: f.n + 1, last: now}
}

// ok は、addr からのログインが合っていたので数え直す。
func (l *limiter) ok(addr string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, addr)
}
