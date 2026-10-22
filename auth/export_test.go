package auth

import "time"

// SetNow は、テストでセッションの時刻を進めるため(auth_test パッケージから使う)。
func SetNow(s *Sessions, now func() time.Time) { s.now = now }
