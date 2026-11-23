package main

// cert.go: 自己署名の証明書を作るコマンド(0017 の決定事項 2。10-2)。
//
//	aibow-server [-config 設定ファイル] cert <ホスト名か IP アドレス>...
//
// HTTPS では、サーバーが「自分は本当にこのアドレスのサーバーだ」という証明書を見せ、通信を暗号化する鍵を決める。
// ふつうの証明書は、ブラウザや OS が信頼している認証局に保証(署名)してもらう。社内の PC などでは手間がかかるので、
// ここでは自分で自分を保証した「自己署名」の証明書を作る。暗号化はふつうの証明書と同じだが、誰も保証していないので、
//
//	ブラウザ      … 初めて開くと「この接続ではプライバシーが保護されません」と警告が出る(内容を確かめて先へ進む)
//	アプリ        … サーバーの PC から写した証明書のファイルを settings.json の serverCert に書くと、その証明書だけを信頼してつなぐ
//	Cline など    … Node.js なら環境変数 NODE_EXTRA_CA_CERTS に証明書のファイルを書く
//
// 標準ライブラリ(crypto/x509)だけで作る(openssl などの道具を入れなくてよいように)。
// 証明書(server-cert.pem)と秘密鍵(server-key.pem)は設定ファイルの隣に書く。秘密鍵は人に渡さない。
//
// 作ったあと、server.json に貼る tlsCert・tlsKey の2行をコンソールに出す(0058)。server.json は人が手で書くファイルなので、
// コマンドは書き換えず「見せる」だけにする(並び順やほかの欄の書き方を崩さないため)。今の設定と違うときだけ一言添える。

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/secondarykey/aibow/auth"
	"github.com/secondarykey/aibow/config"
	"github.com/secondarykey/aibow/log"
)

const (
	certFileName = "server-cert.pem"
	keyFileName  = "server-key.pem"
	// certDays は証明書の有効な日数。ブラウザが受け付ける長さ(398 日まで)に収める。切れたら作り直す
	certDays = 397
)

const certUsage = `使い方:
  aibow-server [-config 設定ファイル] cert <ホスト名か IP アドレス>...
  例: aibow-server cert 192.168.1.10 my-pc.local`

// runCert は cert のコマンドを実行する。hosts はほかの PC からつなぐときに使う名前とアドレス(127.0.0.1 と localhost は足す)。
// 案内は w に書く(run からは標準出力。テストでは中身を確かめる)。
func runCert(w io.Writer, c serverConfig, hosts []string) error {
	if len(hosts) == 0 {
		return errors.New("証明書に書くホスト名か IP アドレスを1つ以上書いてください(ほかの PC からつなぐときのアドレス)\n" + certUsage)
	}
	dir := filepath.Dir(c.StatePath)
	certPath, keyPath := filepath.Join(dir, certFileName), filepath.Join(dir, keyFileName)
	for _, p := range []string{certPath, keyPath} {
		if _, err := os.Stat(p); err == nil {
			return fmt.Errorf("%s はもうあります。作り直すときは、先に削除してください(使っているアプリの serverCert も、新しいものに替えます)", p)
		}
	}
	certPEM, keyPEM, err := selfSigned(append(hosts, "127.0.0.1", "localhost"), time.Now())
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil { // 秘密鍵はほかの人に読ませない
		return err
	}
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return err
	}
	log.Noticef("自己署名の証明書を作りました: %s(%d 日有効。ホスト: %s)", certPath, certDays, strings.Join(hosts, ", "))

	// 作ったもの → server.json に書くもの → アプリの側の案内、の順に出す
	certAbs, keyAbs := absPath(certPath), absPath(keyPath)
	fmt.Fprintf(w, "証明書: %s\n秘密鍵: %s\n", certAbs, keyAbs)
	block, _ := pem.Decode(certPEM)
	fmt.Fprintf(w, "証明書のフィンガープリント(SHA-256): %s\n", auth.Fingerprint(block.Bytes))

	// server.json は resolve でもう読めているが、何と書いてあったか(引数・環境変数と混ざる前の値)は残していないので読み直す。
	// cert のためだけに起動の設定(serverConfig)を広げないため
	jsonPath := c.File
	if jsonPath == "" {
		jsonPath = filepath.Join(dir, configFileName)
	}
	file, found, err := config.LoadServerFile(jsonPath)
	state := compareTLS(file, found, certAbs, keyAbs)
	if err != nil {
		// 証明書は作れているので、エラーで終わらせず、比べるのをやめて2行だけ出す
		log.Warnf("server.json の今の設定と比べられませんでした: %v", err)
		state = tlsUnknown
	}
	fmt.Fprintln(w)
	writeTLSGuide(w, state, absPath(jsonPath), file, certAbs, keyAbs)
	if c.Local() {
		fmt.Fprintln(w)
		writeListenGuide(w, c, err == nil && found && file.Addr != "")
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "画面のアプリからつなぐときは、初めてつなぐときに出るフィンガープリントがこのフィンガープリントと同じかを確かめて、信頼します(起動のときのログにも出ます)。")
	fmt.Fprintln(w, "settings.json に手で書くときは、アプリの PC に証明書(秘密鍵ではない)だけを写し、serverCert に書きます。")
	return nil
}

// tlsState は、server.json の TLS の設定が、作った証明書から見てどうなっているか(0058 の決定事項 6)。
type tlsState int

const (
	tlsNoFile    tlsState = iota // server.json が無い
	tlsNotSet                    // server.json はあるが tlsCert・tlsKey を書いていない
	tlsSame                      // tlsCert・tlsKey の両方が、作ったファイルを指している
	tlsDifferent                 // どちらかが違うファイルを指している(片方だけ書いてある・片方だけ同じ、も入れる)
	tlsUnknown                   // server.json を読み直せなかった(比べずに2行だけ出す)
)

// compareTLS は、server.json に書いてある tlsCert・tlsKey と、作った証明書・秘密鍵(絶対パス)を比べる。
// 比べるのは server.json の値だけ(引数・環境変数は見ない)。将来 cert -write を足すときは、この結果で書き換えるか断るかを決める。
func compareTLS(file config.ServerFile, found bool, certPath, keyPath string) tlsState {
	switch {
	case !found:
		return tlsNoFile
	case file.TLSCert == "" && file.TLSKey == "":
		return tlsNotSet
	case samePath(file.TLSCert, certPath) && samePath(file.TLSKey, keyPath):
		return tlsSame
	}
	return tlsDifferent
}

// samePath は、server.json に書いた値 written が、絶対パス abs と同じファイルを指すか。
// 相対パスは今のカレントフォルダから解く(aibow-server を起動するときのカレントフォルダが違えば、本当は別のファイルを指すこともある。
// そこまでは見ない。0058 の未決事項 1)。Windows のパスは大文字と小文字を区別しない。
func samePath(written, abs string) bool {
	if written == "" {
		return false
	}
	a, b := filepath.Clean(absPath(written)), filepath.Clean(abs)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// tlsSnippet は、server.json の { } の中に貼る tlsCert・tlsKey の2行を作る(最後の改行なし)。
// 区切りは / にそろえる(Go は Windows でも / のパスを読める。JSON で \ を書くには \\ と書く必要があり、そのまま貼ると壊れるため)。
// 行末のカンマは tlsCert の行だけに付ける(最後の欄に置いても JSON として正しいように。Go の JSON は最後のカンマを許さない)。
func tlsSnippet(certPath, keyPath string) string {
	return "  " + jsonField("tlsCert", filepath.ToSlash(certPath)) + ",\n  " + jsonField("tlsKey", filepath.ToSlash(keyPath))
}

// jsonField は `"名前": "値"` の1つを、JSON の文字列の決まりで書く(" や \ が入っても壊れないように encoding/json に任せる)。
// 人が読んで貼る文なので、& < > を & などにしない(SetEscapeHTML(false))。
func jsonField(name, value string) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.Encode(value) // 文字列の Encode は失敗しない
	return strconv.Quote(name) + ": " + strings.TrimSuffix(b.String(), "\n")
}

// writeTLSGuide は、server.json に書くもの(貼る2行と、今の設定との違い)を出す。
func writeTLSGuide(w io.Writer, state tlsState, jsonPath string, file config.ServerFile, certPath, keyPath string) {
	snippet := tlsSnippet(certPath, keyPath)
	switch state {
	case tlsNoFile:
		// 新しいファイルにそのまま貼れるように、{ } で囲んだ全体を出す
		fmt.Fprintf(w, "server.json はまだありません(%s)。作るときは、次の形で書くと HTTPS で待ち受けます:\n\n", jsonPath)
		fmt.Fprintf(w, "{\n%s\n}\n", snippet)
	case tlsSame:
		// 作り直し(古い pem を消してから cert)では、ふつうここに来る。アプリの側で信頼し直すこと(0057)につなげる
		fmt.Fprintln(w, "server.json の tlsCert・tlsKey は、もうこの証明書を指しています。aibow-server を起動し直すと新しい証明書を使います(つないでいるアプリは、新しいフィンガープリントを確かめて信頼し直します)。")
	default:
		fmt.Fprintf(w, "server.json(%s)の { } の中に、次の2行を書くと HTTPS で待ち受けます(ほかの欄の後ろに置くときは、前の行の最後に , を足す):\n\n", jsonPath)
		fmt.Fprintln(w, snippet)
		if state == tlsDifferent {
			fmt.Fprintf(w, "\nserver.json の tlsCert・tlsKey は今の設定と違います(今: tlsCert %s・tlsKey %s)。この証明書を使うなら、上の2行に書き換えてください。\n",
				orNone(file.TLSCert), orNone(file.TLSKey))
		}
	}
}

// writeListenGuide は、待ち受けのアドレスがこの PC の中だけのときに、ほかの PC から使うための書き方の例を出す(0058 の決定事項 10)。
// どのアドレスで待ち受けるかは人によるので、0.0.0.0 は例として見せるだけ。例の形は server.json の今の書き方(addr か host か)に合わせる
// (addr と host・port は同じファイルに一緒に書けないため。listenLevels)。
func writeListenGuide(w io.Writer, c serverConfig, fileUsesAddr bool) {
	fmt.Fprintf(w, "ほかの PC から使うなら、待ち受けのアドレスを 127.0.0.1 以外にしてください(今: %s)。"+
		"server.json には、たとえば次のように書きます(0.0.0.0 は全部のネットワーク。特定のアドレスだけで待ち受けるなら、そのアドレスを書く):\n\n",
		net.JoinHostPort(c.Host, strconv.Itoa(c.Port)))
	addr := "0.0.0.0:" + strconv.Itoa(c.Port)
	if fileUsesAddr {
		fmt.Fprintf(w, "  %s\n", jsonField("addr", addr))
		return
	}
	fmt.Fprintf(w, "  %s\n", jsonField("host", "0.0.0.0"))
	fmt.Fprintf(w, "\naddr(例 %s)でアドレスとポートをまとめて書いてもかまいません(同じファイルに host・port と addr の両方は書けません)。\n", strconv.Quote(addr))
}

// orNone は、書いていない値を「(なし)」にする。
func orNone(s string) string {
	if s == "" {
		return "(なし)"
	}
	return s
}

// selfSigned は、hosts に向けた自己署名の証明書と秘密鍵を PEM の形で作る。
// 鍵は ECDSA P-256(短くて速い。今のブラウザと Go はどれも使える)。
func selfSigned(hosts []string, now time.Time) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: hosts[0], Organization: []string{"aibow-server(自己署名)"}},
		NotBefore:    now.Add(-time.Hour), // PC の時計が少しずれていても使えるように
		NotAfter:     now.Add(certDays * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		// 自分で自分を保証するので、認証局の印も付ける(アプリが「信頼する証明書」として持つと、この証明書を根にしてつなぎ方を確かめられる)
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	// ブラウザと Go は、証明書の名前を CommonName ではなく SAN(DNSNames・IPAddresses)で確かめる
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("証明書を作れません: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), nil
}
