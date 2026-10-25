package auth

// fingerprint.go: 証明書の指紋(0019 の決定事項 6・14)。
//
// 自己署名の証明書は、どの認証局も「本物」と保証していない。そこで、初めてつなぐときに証明書の指紋(中身の SHA-256)を人の目で
// 比べて「これはサーバーの PC で作った証明書だ」と確かめ、アプリが覚えておく(TOFU。Trust On First Use。SSH の known_hosts と同じ考え方)。
// 指紋は aibow-server が起動のときと cert のコマンドで出し、アプリが画面に出す。両方で同じ形にするため、ここに1つだけ置く。
// サーバーにとっての「誰か(認証)」なので auth に置く。

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

// Fingerprint は、証明書(DER の形。x509.Certificate.Raw)の SHA-256 の指紋を AB:CD:… の形にする。
func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":")
}
