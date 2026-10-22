package auth

// password.go: パスワードの保存のしかた(0017 の決定事項 1)。
//
// パスワードそのものを保存すると、DB が漏れたときにそのまま使われる(ほかのサービスで同じパスワードを使っていれば、そちらも)。
// そこで、元に戻せない「ハッシュ」だけを保存し、ログインのときは打たれたパスワードを同じようにハッシュにして比べる。
// ただのハッシュ(SHA-256 を1回)だと、よくあるパスワードのハッシュの表を作っておけば一瞬で引けてしまうので、2つ工夫する:
//
//	塩(salt)   … 利用者ごとに違うでたらめな値を混ぜてからハッシュにする。同じパスワードでも違う値になり、作っておいた表が使えない
//	重ねる回数 … ハッシュを何十万回も重ねる(PBKDF2)。1回試すのに時間がかかるので、片っ端から試すのが遅くなる
//
// PBKDF2 は Go 1.24 で標準の crypto/pbkdf2 に入った。bcrypt・argon2 は golang.org/x/crypto(外部パッケージ)なので使わない。
//
// 保存する形は「pbkdf2-sha256$回数$塩$値」(塩と値は base64)。回数を一緒に書いておくと、あとで回数を増やしても古い値を確かめられる。

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

const (
	hashScheme = "pbkdf2-sha256"
	// hashIter はハッシュを重ねる回数。OWASP(Web のセキュリティの指針)の PBKDF2-HMAC-SHA256 の目安(2023 年)。
	// ログイン1回に 0.1〜0.5 秒ほどかかる(ログインのときだけなので気にならない)
	hashIter = 600_000
	saltLen  = 16 // 塩の長さ(バイト)。128 ビット
	keyLen   = 32 // ハッシュの長さ(バイト)。SHA-256 と同じ
)

var b64 = base64.RawStdEncoding

// HashPassword は、パスワードを保存する形(塩を付けて重ねたハッシュ)にする。
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	rand.Read(salt) // crypto/rand の Read は失敗しない(Go 1.24 から)
	key, err := pbkdf2.Key(sha256.New, password, salt, hashIter, keyLen)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s$%d$%s$%s", hashScheme, hashIter, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// CheckPassword は、password が保存した形 hash と同じパスワードか。hash が空(パスワードを作っていない)なら false。
func CheckPassword(hash, password string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 4 || parts[0] != hashScheme {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter <= 0 {
		return false
	}
	salt, err1 := b64.DecodeString(parts[2])
	want, err2 := b64.DecodeString(parts[3])
	if err1 != nil || err2 != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iter, len(want))
	if err != nil {
		return false
	}
	// 比べるのにかかる時間が「何文字目まで合っていたか」で変わらないよう、決まった時間で比べる
	return subtle.ConstantTimeCompare(got, want) == 1
}

// dummyHash は、いない名前でログインしようとしたときに比べる相手。いる名前と同じだけ時間をかけるため
// (すぐに断ると、返事の速さで「この名前の人はいる」と分かってしまう)。
var dummyHash, _ = HashPassword("aibow には、この名前の人はいません")

// passwordAlphabet は、作るパスワードに使う文字。見間違えやすい 0/o・1/l/i は入れない(31 文字。1 文字で約 5 ビット)
const passwordAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

// NewPassword は、でたらめなパスワードを作る(aibow-server の user add・user passwd。0017 の 10-1 でユーザーと決めた)。
// 4 文字ずつ - でつないだ 16 文字(約 79 ビット)。手で打ち写せるように区切る。
func NewPassword() string {
	var b strings.Builder
	for i := range 16 {
		if i > 0 && i%4 == 0 {
			b.WriteByte('-')
		}
		b.WriteByte(passwordAlphabet[randIndex(len(passwordAlphabet))])
	}
	return b.String()
}

// randIndex は 0 以上 n 未満のでたらめな数。
func randIndex(n int) int {
	// 256 を n で割った余りの分を捨てて、どの文字も同じ確率で出るようにする
	limit := 256 - 256%n
	var c [1]byte
	for {
		rand.Read(c[:])
		if int(c[0]) < limit {
			return int(c[0]) % n
		}
	}
}
