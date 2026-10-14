# log の AGENTS.md

**aibow のログ。** aibow のコードは `log.Info(msg, "key", v)`(slog と同じ形)・
`log.Infof(format, ...)`(printf)などで出す。土台は `github.com/wenteasy/log`(v0.3.1)。
形は shinte/ikkyoku の `log` に揃え、ライブラリとして使う人のために `SetLogger` を足してある。

| ファイル | 役割 |
|---|---|
| `log.go` | 出す関数(Trace / Debug / Info / Notice / Warn / Error と `…f`)。段階(`Level*`。値は wenteasy/log と同じ `slog.Level`)。出し先の `SetLogger` / `Logger` |
| `setup.go` | `Init`。出口を組んで `slog.Default()` を向ける(アプリの main の最初に 1 回)。今は標準エラー出力だけ |

## どこに出るか

- **アプリ(`_cmd/aibow`)**: `main` の最初に `log.Init()`。`slog.Default()` が wenteasy/log の `SimpleHandler`
  (標準エラー出力。Info 以上)になり、aibow のログもアプリのログもそこに出る。
  リリース版(`-H windowsgui`)には標準エラー出力の行き先が無いので見えない(ファイルに書くのは次の段階。TODO.md)
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

## テスト(`go test ./log/`)

`log_test.go` —— 出す関数とレベルの足切り・**記録される位置が呼んだ側になること**(ここの関数を通しても位置がずれない歯止め)・
`SetLogger` をしなければ `slog.Default()` に出ること・`Init` が `slog.Default()` を向け、Notice などが名前で出ること。
