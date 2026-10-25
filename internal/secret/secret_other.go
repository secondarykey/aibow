//go:build !windows

package secret

// secret_other.go: Windows 以外。包まずにそのまま返す(mac の Keychain・Linux の Secret Service は作っていない。0019 のやらないこと)。

// Supported は、この OS で包めるか。
const Supported = false

// Kind は、この OS で「包んだ」ときの形(包んでいない)。
const Kind = KindPlain

// Protect は data をそのまま返す。
func Protect(data []byte) ([]byte, error) { return append([]byte(nil), data...), nil }

// Unprotect は data をそのまま返す。
func Unprotect(data []byte) ([]byte, error) { return append([]byte(nil), data...), nil }
