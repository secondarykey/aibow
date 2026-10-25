// Package secret は、手元のファイルに置く合言葉(aibow-server のアプリ用のトークン。0019)を、OS の仕組みで包む。
//
// トークンを平文でファイルに置くと、そのファイルを写した人は誰でも、その人として aibow-server につなげる。
// Windows には DPAPI(Data Protection API)という仕組みがあり、「今ログインしている Windows の利用者」の鍵で包んでくれる。
// 包んだものは、同じ PC の同じ Windows の利用者でしか戻せない(ファイルだけを写しても、ほかの PC・ほかの利用者では読めない)。
// 鍵は Windows が持っているので、aibow は鍵を作ったり置いたりしなくてよい(secret_windows.go)。
//
// Windows 以外(mac の Keychain・Linux の Secret Service)は作っていない。包まずにそのまま返し、Supported が false になる
// (呼ぶ側が「平文で保存しています」と画面とログに出す。0019 の決定事項 4)。
//
// internal に置いているのは、aibow のリポジトリの中だけで使う部品だから。
package secret

// Kind は、包んだ形の名前(ファイルに書いて、読むときにどう戻すかを決める)。
const (
	KindDPAPI = "dpapi" // Windows の DPAPI で包んだもの
	KindPlain = "plain" // 包んでいない(Windows 以外)
)
