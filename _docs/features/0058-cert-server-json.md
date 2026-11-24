# 0058: 証明書を作ったら、server.json に貼る TLS の設定を出す

- 状態: ✅ 完了(2026-11-24。貼った server.json で HTTPS で起動するところも 2026-11-24 にユーザーが確かめた)
- ロードマップ: ロードマップの外(使い勝手の直し。0017 の 10-2 の続き)
- 決めた日: 2026-11-23(Chat でユーザーと決めた。Chat がコード(`_cmd/aibow-server/cert.go`・`config.go`・`config/server.go`)を読んで、今の作りを確かめた)
- 前提: 0017(`aibow-server cert`・`tlsCert`・`tlsKey`。決定事項 2)、0057(証明書を作り直したあとにアプリで信頼し直す)

## 目的

`aibow-server cert` で証明書を作ったあと、`server.json` に書く `tlsCert`・`tlsKey` を、そのまま貼れる形でコンソールに出す。
今は「証明書: …」「秘密鍵: …」とパスを出し、「server.json の tlsCert・tlsKey にこの2つを書くと…」と言うだけなので、
JSON の形(欄の名前・`\` の書き方)を自分で組み立てる必要がある。Windows のパスを JSON にそのまま貼ると `\` がエスケープにならず壊れる。

学ぶこと:

- **設定を「書く」のでなく「見せる」**:`server.json` は人が手で書くファイルで、aibow-server は書かない(`config.go` の頭の決まり)。コマンドが勝手に書き換えると、
  コメント代わりの並び順や、ほかの欄の書き方が崩れることがある。だから、貼る文を見せ、今の設定と違うときだけ一言添える
- **JSON の文字列とパス**:JSON では `\` は `\\` と書く。Go は Windows でも `/` 区切りのパスを読めるので、`/` にそろえて出す(usage.md の例と同じ書き方)

## 今の作り(2026-11-23、Chat がコードを読んで確かめた)

- `run`(main.go)は、`cert` のときに `runCert(c, hosts)` を呼ぶ。設定ファイルが読めなければ(`cfgErr`)その前に止まる
- `runCert`(cert.go)は、`server.json` の隣(`filepath.Dir(c.StatePath)`)に `server-cert.pem`・`server-key.pem` を書く。どちらかがもうあれば、作らずにエラー
- 書いたあと `fmt.Printf` で、証明書と秘密鍵の絶対パス・フィンガープリント・「server.json の tlsCert・tlsKey にこの2つを書くと、HTTPS で待ち受けます。」などを出す
- `c.TLSCert`・`c.TLSKey` は、引数 > 設定ファイル > 環境変数 の順で決めたあとの値で、**server.json に何と書いてあるかは残っていない**。`c.File` は見つかった server.json のパス(無ければ空)
- server.json の欄は `tlsCert`・`tlsKey`(`config.ServerFile`)。相対パスは、aibow-server を起動したときのカレントフォルダから読まれる(`tls.LoadX509KeyPair` にそのまま渡す)

## 決定事項

### ユーザーと決めたこと

1. 証明書を作ったあと、**server.json にそのまま貼れる TLS の部分**(`tlsCert`・`tlsKey` の2行)をコンソールに出す
2. server.json にすでに TLS の設定があり、作った証明書と違うファイルを指していれば、**「今の設定と違います」と一言添える**
3. **server.json には書き込まない**。将来、引数を付けたとき(たとえば `cert -write`)だけ書き込む形を足せる余地は残す(今回は作らない)

### 話の流れから Chat が決めて書いたこと(2026-11-24、着手の前にユーザーが「仕様どおりでよい」と確認した)

4. **パスは絶対パスで、区切りは `/`**(`filepath.ToSlash`)。JSON の文字列にするのは `encoding/json` で行う(`"` などが入っても壊れないように。`SetEscapeHTML(false)` で `&` などを `\u0026` にしない)
5. **貼る形**:2行を、行末のカンマを `tlsCert` の行だけに付けて出す(最後の欄に置いても JSON として正しいように。Go の JSON は最後のカンマを許さない)。
   前に字下げ2つ。例(Windows):

   ```
   server.json(C:\Users\<ユーザー>\AppData\Roaming\aibow\server.json)の { } の中に、次の2行を書くと HTTPS で待ち受けます(ほかの欄の後ろに置くときは、前の行の最後に , を足す):

     "tlsCert": "C:/Users/<ユーザー>/AppData/Roaming/aibow/server-cert.pem",
     "tlsKey": "C:/Users/<ユーザー>/AppData/Roaming/aibow/server-key.pem"
   ```

6. **server.json の状態で、添える一言を変える**(比べるのは server.json に書いてある値だけ。引数・環境変数は見ない):

   | server.json | 出すもの |
   |---|---|
   | 無い | 「server.json はまだありません(<探した場所>)。作るときは、次の形で書きます」と、`{` `}` で囲んだ全体を出す(新しいファイルにそのまま貼れる) |
   | あるが `tlsCert`・`tlsKey` を書いていない | 上の例のとおり(2行だけ) |
   | 書いてあり、作ったファイルと同じ | 2行は出さず、「server.json の tlsCert・tlsKey は、もうこの証明書を指しています。aibow-server を起動し直すと新しい証明書を使います(つないでいるアプリは、新しいフィンガープリントを確かめて信頼し直します)」 |
   | 書いてあり、作ったファイルと違う | 2行を出したあとに「server.json の tlsCert・tlsKey は今の設定と違います(今: tlsCert <今の値>・tlsKey <今の値>)。この証明書を使うなら、上の2行に書き換えてください」 |

   - 「同じ」は、`tlsCert` と `tlsKey` の**両方**が同じとき。片方だけ同じなら「違う」に入れる
   - 比べ方:server.json の値を `absPath`(今のカレントフォルダから絶対パスに)→ `filepath.Clean` し、作ったファイルの絶対パスと比べる。Windows では大文字小文字を区別しない(`strings.EqualFold`。`runtime.GOOS == "windows"` のとき)
   - 相対パスは「cert を実行したときのカレントフォルダ」から解く。起動のときのカレントフォルダが違えば、本当は別のファイルを指すことがある。今回はそこまで見ない(未決事項 2)
   - 作り直し(古い pem を消してから cert)では、ふつう「同じ」になる。0057 の「信頼し直す」につながる一言を添えるのはそのため

7. **server.json の読み方**:`runCert` の中で `config.LoadServerFile` で読み直す。読む場所は `c.File`、空なら `filepath.Join(filepath.Dir(c.StatePath), configFileName)`(main.go の「設定ファイル: なし(… を探しました)」と同じ)。
   `serverConfig` に「ファイルに書いてあった TLS の値」の欄を足す形でもよいが、cert のためだけに起動の設定を広げないよう、読み直しにする。
   ここまで来ていれば resolve で一度読めているので、読み直しのエラーはふつう起きない。起きたら比べるのをやめて2行だけ出す(証明書は作れているので、エラーで終わらせない)
8. **今の行の扱い**:「server.json の tlsCert・tlsKey にこの2つを書くと、HTTPS で待ち受けます。」の1行は、決定事項 5・6 の文に置き換える。
   「証明書: …」「秘密鍵: …」・フィンガープリント・画面のアプリと settings.json の案内の行はそのまま残す。並びは「作ったもの(証明書・秘密鍵・フィンガープリント)→ server.json に書くもの → アプリの側の案内」
9. **作りの分け方**(将来の書き込みに備える):
   - `tlsSnippet(certPath, keyPath string) string` … 貼る2行を作る(決定事項 4・5)
   - `compareTLS(file config.ServerFile, found bool, certPath, keyPath string)` … 状態(無い・書いていない・同じ・違う)を返す(決定事項 6)
   - `runCert` は、これらで出す文を組み立てる。出力先は `io.Writer` で受け取り(`run` からは `os.Stdout`)、テストで中身を確かめられるようにする
   - 将来 `-write` を足すときは、`compareTLS` の結果を見て、書き換える・断るを決めればよい
10. **待ち受けのアドレスの添え書き**(ユーザーと決めた。2026-11-23):待ち受けのアドレスが PC の中だけ(`c.Local()` が true。引数・server.json・環境変数で決めたあとの値)なら、
    TLS の案内のあとに「ほかの PC から使うなら、待ち受けのアドレスを 127.0.0.1 以外にしてください」と添え、server.json の書き方の例を出す。
    - 0.0.0.0 と決め打ちしない(どのアドレスで待ち受けるかは人による)。例として出す形は、server.json の今の書き方に合わせる:
      - server.json に `addr` を書いている … `"addr": "0.0.0.0:<今のポート>"` の例(addr と host・port は同じファイルに一緒に書けないため。`listenLevels`)
      - それ以外(host を書いている・どちらも書いていない・server.json が無い) … `"host": "0.0.0.0"` の例と、「`addr`(例 `"0.0.0.0:8089"`)でまとめて書いてもよい。同じファイルに両方は書けない」
    - 例の文は「0.0.0.0 は全部のネットワーク。特定のアドレスだけで待ち受けるなら、そのアドレスを書く」と一言添える
    - 待ち受けのアドレスがもう PC の外なら、何も添えない
    - この行は貼る2行とは分けて出す(2行の貼り付けに混ざらないように)
11. **ドキュメント**:`_docs/usage.md` の「ほかの PC から使う」の 1・2 と、`_docs/setup.md` の「ほかの PC から使えるようにする」の 1・2 に、「cert が server.json に貼る2行を出す」ことを書く。`cert.go` の頭のコメントにも一言足す

### 作ったあとにユーザーと決めたこと(2026-11-24)

12. **作り直す `-f`**:`aibow-server cert -f <ホスト>...` で、証明書と秘密鍵がもうあっても作り直す。`-f` が無ければ、今までどおり止め、エラーで `-f` を案内する。
    今のファイルは消さず、名前の後ろに日時(yyyymmddhhmmss)を付けて残す(`server-cert.pem.20261124153000`。`.old` ではなく日時にする、はユーザーの指定)。
    - Code が決めたこと:新しい証明書が作れてから今のファイルの名前を変える(作れなかったときに今の証明書を無くさないように)。片方だけあるときは、あるほうだけ残す。
      残す先がもうあれば(同じ秒に2回など)上書きせずに止める。名前を変えたことは Notice のログと、フィンガープリントの下の1行に出す。`-f` は `cert` のすぐ後ろに書く(ホストの後ろには書けない)

## やらないこと

- server.json に書き込む(決定事項 3。`-write` などは将来、必要になったら別の仕様で)
- 待ち受けのアドレスを server.json に書き込む・どのアドレスにするかを決める(添え書きで例を見せるだけ。決定事項 10)
- 引数(`-tls-cert`)・環境変数(`AIBOW_SERVER_TLS_CERT`)の値と比べる
- ~~秘密鍵・証明書がもうあるときの扱い(今までどおりエラーで止める)~~ → 2026-11-24 にユーザーと決めて作った(決定事項 12)
- アプリの settings.json(`serverCert`)に貼る形を出す

## 未決事項

1. 相対パスで書かれた `tlsCert` を、どのフォルダから解くか(決定事項 6 では cert を実行したときのカレントフォルダ)。困る例が出たら考える

## 完了の条件

1. `go test ./_cmd/aibow-server/` が通る。テストで、決定事項 6 の4つの状態それぞれの出力を確かめる(server.json なし・TLS なし・同じ・違う。同じかどうかで Windows の大文字小文字の違いも)と、決定事項 10 の添え書き(127.0.0.1 で host の例・addr を書いているときは addr の例・PC の外なら出さない)
2. 出した2行を server.json に貼ると、`LoadServerFile` で読めて、aibow-server が HTTPS で起動する(テストでは、出力から取り出した2行を `{}` に入れて `config.LoadServerFile` で読めることを確かめる。パスに `\` や `"` が入っても壊れない)
3. 実際に一時フォルダの server.json(`-config`)で `aibow-server cert 192.168.1.10` を実行し、出力を貼って起動できることを見る(ユーザーの確認)

## 作業の順番

1. `tlsSnippet`・`compareTLS`・待ち受けのアドレスの添え書きとそのテスト
2. `runCert` に組み込み(出力先を `io.Writer` に。今の `TestRunCert` も合わせる)
3. usage.md・setup.md・cert.go の頭のコメント
4. 一時フォルダで実際に動かして、出力を確かめる

## 経過

- 2026-11-24(Code):着手の前に、決定事項 4〜9(Chat が決めて書いたこと)をユーザーに確かめた(仕様どおりでよい)。作業の順番 1〜4 を作った。
  - `cert.go`:`runCert(w io.Writer, c, hosts)` にし(`run` からは `os.Stdout`)、`compareTLS`(状態 `tlsNoFile`・`tlsNotSet`・`tlsSame`・`tlsDifferent`)・`tlsSnippet`・`samePath`・`jsonField`・`writeTLSGuide`・`writeListenGuide` を足した。
    server.json を読み直せなかったときは `tlsUnknown` にして Warn を出し、「書いていない」と同じ2行だけを出す(決定事項 7)
  - 仕様に書いていなかった細かいところ(Code が決めた):「違う」のときも2行の前に「書いていない」と同じ前置き(server.json のパスと貼り方)を出す。
    片方だけ書いてあるときの「今:」は、書いていないほうを「(なし)」と出す。待ち受けの添え書きには今の待ち受け(`今: 127.0.0.1:8089`)を添え、addr の例のポートは今のポートにする
  - `cert_test.go`:`TestTLSSnippetLoads`(`\`・`"`・`&` の入ったパスでも `{ }` に入れて `LoadServerFile` で読める。ほかの欄の後ろに置いても読める)・
    `TestCompareTLS`(4つの状態・片方だけ同じ・片方だけ書いてある・Windows の大文字小文字)・`TestRunCertGuide`(状態ごとの出力・addr / host の例・PC の外なら添えない・並び)
  - usage.md の「ほかの PC から使う」の 1・2、setup.md の「ほかの PC から使えるようにする」の 1・2、`cert.go` の頭のコメントに書いた
  - 確かめたこと:`gofmt -l .`・`go test ./...`・`go vet`。一時フォルダの server.json(`-config`)で `aibow-server cert 192.168.1.10` を、TLS なし → 出た2行を貼る → pem を消して作り直す(「もうこの証明書を指しています」)→ 違う値と `addr` の順に動かし、出力が決定事項 5・6・10 のとおりになることを見た。
    貼った server.json は `aibow-server -config … user list` で読めた。aibow-server を起動して HTTPS で待ち受けるところは、モデルを GPU に読み込むので試していない(完了の条件 3。ユーザーの確認)
- 2026-11-24(Code):ユーザーの指示で、作り直す `-f` を足した(決定事項 12)。`runCert` は cert の後ろの引数を受け取り、自分の `flag.FlagSet` で `-f` を読む。今のファイルを退けるのは `backupFiles`。
  テストは `TestRunCertForce`(作り直し・日時の形・中身・片方だけあるとき)・`TestBackupFilesExists`(残す先があれば止める)と、`TestRunCert` に「`-f` を案内する」「知らない引数は使い方を添えて断る」を足した。
  一時フォルダで `cert` → `cert`(止まる)→ `cert -f` を動かし、`server-cert.pem.20261124094603`・`server-key.pem.20261124094603` が残ることを見た。usage.md・setup.md に書いた
- 2026-11-24(ユーザー):完了の条件 3(出した2行を貼った server.json で aibow-server が HTTPS で起動する)を確かめた。TODO.md の「確かめていないこと」の行を消した
