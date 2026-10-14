# log の AGENTS.md

**aibow のログ。** aibow のコードは `log.Info(msg, "key", v)`(slog と同じ形)・
`log.Infof(format, ...)`(printf)などで出す。土台は `github.com/wenteasy/log`(v0.3.2)。
形は shinte/ikkyoku の `log` に揃え、ライブラリとして使う人のために `SetLogger` を足してある。
決めたこと・やり残しは [_docs/features/0008-log.md](../_docs/features/0008-log.md)。作り方の決まりごとの正は wenteasy/log の README「使う側のやり方(自分の log パッケージを作る)」(ひな形・よくある間違い)。この形を崩すときは、先にそちらと揃える。

| ファイル | 役割 |
|---|---|
| `log.go` | 出す関数(Trace / Debug / Info / Notice / Warn / Error と `…f`)。段階(`Level*`。値は wenteasy/log と同じ `slog.Level`)。出し先の `SetLogger` / `Logger` |
| `setup.go` | `Init(Options)`。出口(ファイル `Dir/aibow_<日付>.log`・標準エラー出力)を組んで `slog.Default()` を向ける(アプリの main の最初に 1 回)。`FilePath` は今日のファイルの場所 |
| `crash.go` | `CatchCrash`。異常終了(Go の panic・ランタイムの致命的なエラー)の内容をログの隣の `aibow_crash.log` にも書かせる(`debug.SetCrashOutput`)。前回の記録が残っていれば `aibow_crash_<日時>.log` に改名して返す(= 前回は異常終了した)。形は ikkyoku の `CatchCrash` と同じ |

## どこに出るか

- **アプリ(`_cmd/aibow`)**: `main` の最初に `startLog` が `log.Init` を呼ぶ。`slog.Default()` が wenteasy/log の `SimpleHandler`
  (`Options.Root` 以上。dev は Info、exe は Notice)になり、aibow のログもアプリのログも、Wails 自身のログもそこに出る(`main.go` が `application.Options.Logger` に `slog.Default()` を渡す。WebView2 の行は標準の log で来るが、`slog.SetDefault` のあとは同じ出口に流れる。標準の log はレベルを持たないので、`Init` が `slog.SetLogLoggerLevel(Root)` で出すレベルに合わせ、exe でも捨てない)
  - `wails3 dev`(`isDev`): ターミナル(標準エラー出力)。`AIBOW_LOG_DIR` を指定したときだけファイルにも
  - exe(`wails3 build`): ファイル `%APPDATA%\aibow\logs\aibow_<日付>.log`(wenteasy/log の `RollingFileWriter`。日ごと。`AIBOW_LOG_DIR` で変えられる)。
    exe(`-H windowsgui`)には標準エラー出力の行き先が無く、ファイルにしか残らないため。**Notice 以上だけ**を書く(常に残したいもの。生成の統計などの Info は wails3 dev で見る)。起動時に古いファイルを消し、今日を含めて 10 日分を残す(`Options.KeepDays`。日付はファイル名から読む。`aibow_<日付>.log` の形でないファイルは消さない)
- **aibow をライブラリとして使う人**: `Init` は呼ばなくてよい。aibow のログは `slog.Default()` に出るので、
  自分の `slog.SetDefault` の出口に集まる。aibow のログだけ別にしたいときは `log.SetLogger(l)`

## ⚠️ 崩さないこと

- ⚠️ **aibow を使う人に wenteasy/log の型を求めないこと。** レベルは `slog.Level`、出し先は `*slog.Logger` のまま。
  wenteasy/log を import するのはこのパッケージだけ(ほかのパッケージと `_cmd/aibow` は `aibow/log` から出す)
- ⚠️ **出す関数の中で別の関数を挟まないこと**(`output` は公開関数から直接呼ぶ)。記録の位置を段数で数えているので、
  挟むと全部のログがこのパッケージから出たことになる。**wenteasy/log の `Info` などを包まない・変数で指さない**のも同じ理由に加え、
  あちらは `slog.Default()` に固定で書くので `SetLogger` が効かなくなる(binder の log は変数で指す形だが、`SetLogger` が無いのでそれで足りている)
- ⚠️ **`SetLogger` を消さないこと**(2026-10-14、ユーザーと決めた)。ikkyoku はアプリなので `slog.Default()` だけだが、
  aibow はライブラリとしても使われる
- ⚠️ **ライブラリの部分(ルートと下のパッケージ)から `Init` や `slog.SetDefault` を呼ばないこと。** 出口を決めるのはアプリ
- ⚠️ **ログが書けなくてもアプリは動かす。** `Init` はファイルを作れなくても出せる出口だけで組み、理由をエラーで返す(アプリは画面のお知らせに出す)。
  返す `io.Closer` は nil にしない
- ⚠️ **前回の異常終了の記録は改名して残すこと**(消さない・上書きしない)。次の `CatchCrash` が `aibow_crash.log` を空で作り直すので、退けておかないと消える。
  改名した記録は `aibow_<日付>.log` の形ではないので、古いログを消すとき(`removeOld`)にも消えない
- 拾える異常終了は Go のランタイムを通るものだけ。画面から呼ばれたメソッドの中の panic は Wails が recover して Logger に Error で書く(ログファイルに入る)。
  llama.cpp の C の部分の異常終了(GGML_ASSERT など)は拾えない([_docs/known-issues.md](../_docs/known-issues.md))

## テスト(`go test ./log/`)

`log_test.go` —— 出す関数とレベルの足切り・**記録される位置が呼んだ側になること**(ここの関数を通しても位置がずれない歯止め)・
`SetLogger` をしなければ `slog.Default()` に出ること・`Init` が `slog.Default()` を向け、Notice などが名前で出ること・
ファイルに書くこと・フォルダを作れなくても止まらないこと・古いファイルを消して決めた日数を残すこと(名前の形が違うファイルは消さない)・`Root` より下を出さず、標準の log の行は捨てないこと。
`crash_test.go` —— 本当に panic させた子プロセス(テストの実行ファイル自身)の内容が `aibow_crash.log` に残ること・前回の記録を改名して残すこと・空のファイルは異常終了と見なさないこと・古いログの片付けで記録が消えないこと。
