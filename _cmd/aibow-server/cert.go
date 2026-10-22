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

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"

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
func runCert(c config, hosts []string) error {
	if len(hosts) == 0 {
		return errors.New("証明書に書くホスト名か IP アドレスを1つ以上書いてください(ほかの PC からつなぐときのアドレス)\n" + certUsage)
	}
	dir := filepath.Dir(c.StatePath)
	certPath, keyPath := filepath.Join(dir, certFileName), filepath.Join(dir, keyFileName)
	for _, p := range []string{certPath, keyPath} {
		if _, err := os.Stat(p); err == nil {
			return fmt.Errorf("%s はもうあります。作り直すときは、先に消してください(使っているアプリの serverCert も、新しいものに替えます)", p)
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
	log.Noticef("自己署名の証明書を作りました: %s(%d 日有効。ホスト: %v)", certPath, certDays, hosts)
	fmt.Printf("証明書: %s\n秘密鍵: %s\n", absPath(certPath), absPath(keyPath))
	fmt.Println("server.json の tlsCert・tlsKey にこの2つを書くと、HTTPS で待ち受けます。")
	fmt.Println("アプリの PC には証明書(秘密鍵ではない)だけを写し、settings.json の serverCert に書きます。")
	return nil
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
