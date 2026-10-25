package auth

import "time"

// SetNow は、テストでセッションの時刻を進めるため(auth_test パッケージから使う)。
func SetNow(s *Sessions, now func() time.Time) { s.now = now }

// SetKeysNow は、テストでアプリ用のトークンの期限を見る時刻を進めるため。
func SetKeysNow(k *Keys, now func() time.Time) { k.now = now }
