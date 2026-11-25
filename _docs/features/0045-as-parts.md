# 0045: aibow のツールと MCP のサーバーを、ほかのアプリの部品として使えるようにする

- 状態: ✅ 完了(2026-11-25)
- ロードマップ: ロードマップの外(パッケージの分け方の基準「使う相手が現れたときに分ける」(0005 の決定事項 11)を、ツールと MCP のサーバーに当てはめる回)
- 決めた日: 2026-11-25(2026-11-09 に Chat で相談を始め、11-11 に使う側の事情を足し、11-10 の Code の見直しを 11-25 に採った)
- 前提: 0005(ツールの形・`tool` は LLM と会話を import しない)、0007(`mcp/server`・確認の MRTR)、0008(`log.SetLogger`)、
  0035(フォルダの組)、0037(`edit_file` の字下げを合わせる)

## 目的

**使う相手が現れた**:ユーザーが作っている Wails 製の「構造を持つメモ帳」に、スキルを元にした AI の編集を足したい。
メモ帳のユーザーを aibow-server に依存させたくないので、メモ帳が **MCP のサーバー**になり、Claude Desktop・Claude Code・aibow のアプリなどからメモを読んで編集してもらう。
AI とのやり取りは相手(クライアント)の画面で行う(メモ帳の中にチャットの画面は持たない。2026-11-11)。

そのとき、メモ帳が aibow の `tool` と `mcp/server` を import して、次をそのまま使えるようにする。

- ツールの形(`tool.Tool`・`Def`・`CheckArgs`)と、MCP のツールへの変換・時間の上限・失敗を isError で返す仕組み(`mcp/server`)
- `edit_file` の当て方(1か所に決まらなければエラー・字下げのずれを合わせる・CRLF を合わせる)と差分の作り方

**部品は、特定の使い手に合わせて作らない**。使う側の事情(データの持ち方・画面・保存のしかた)はここに書かず、
aibow の形は「自分のツールを持つアプリなら誰でも使える」ことで決める(2026-11-11、ユーザーの指示)。

学ぶこと:

- **公開する名前の線を引く**:今まで aibow の中だけで使っていた関数を外に見せると、変えにくくなる。何を見せて何を隠すかを、実際の使い手を見て決める
- **ライブラリの「差し込み口」**:サーバーの作り方を決め打ちにせず、ツールの一覧・名前・説明を使う側が渡せるようにする(依存の向きは変えない)

## パッケージの構成への影響(2026-11-25 に確かめた)

- **新しいパッケージは切らない**。`tool` と `mcp/server` はもう独立していて、ルートの `aibow`(会話の仕組み)を import していない
  (`tool` が使う aibow の中のパッケージは `log`・`git`・`internal/proc`、`mcp/server` は `tool`・`mcp/internal/meta`・`log`)。
  この仕様は、今のパッケージの中で外に見せる名前を足すだけ
- `Replace` を `tool/textedit` などに分けることはしない(決定事項 7)

## 決定事項

ユーザーと決めたこと:

1. **確認の場所は (c) を先に通す**(2026-11-25)。aibow は「確認の要らない、書き換えるツール」を出せるようにするだけ(決定事項 5)。
   使う側がその先をどうするか(変更の案を溜めて自分の画面で適用する・すぐ実行して自分の仕組みで戻す、など)は aibow は知らなくてよい
   - (b)(`Options.Confirm` を足し、使う側が自分の画面で聞く)は、使う側が求めたときに足す。今は作らない(未決事項 1)
   - (a)(MRTR。クライアントが聞く)は、今のまま残る。`Custom` で渡したツールでも、`tool.Confirmable` なら組み込みと同じに MRTR で確かめる(下の 2)
2. **`mcp/server` に、外からツールを渡す口を足す**。`Options` に次を足す
   - `Name`:サーバーの名前(MCP の serverInfo.name)。空なら今までどおり `aibow-mcp`
   - `Instructions`:クライアントに渡す説明(MCP の instructions)
   - `Custom []tool.Tool`:決めたら、**組み込み・フォルダのツールの代わりに、これだけを出す**(`Options.Tools()` もこれだけを返す)。組み込みも出したいときは、使う側が `append(tool.Builtin(), …)` で足す
   - 「足す」ではなく「代わりに」にするのは、メモ帳に `read_file` や `run_command` が混ざると、モデルがメモ帳の外のファイルを触ろうとするため
3. **`Options` の誤りを先に確かめる**(Code の見直し 2。2026-11-25 に採った)。SDK の `Server.AddTool` は、引数の定義が空か `type` が `object` でないと panic し、
   同じ名前のツールは黙って後のもので置き換える。そこで次を確かめる関数を足す
   - 名前が空・名前が重なる・`Def.Parameters` が JSON として読めない・`type` が `object` でない
4. **`edit_file` の当て方を、ファイルによらない関数として公開する**(`tool` パッケージ)
   - `tool.Replace(text, old, new string) (Replaced, error)`:`Replaced` は置き換えた後の全体・行番号・字下げを合わせたか
   - 1か所に決まらない・見つからない・`old` が空・`old` と `new` が同じ、は決まったエラー(`errors.Is` で見分けられる)。
     **何か所あったかは、エラーの型から取り出せる**(`errors.As`。Code の見直し 6)。`edit_file` が今と同じ「N か所あります」の文を作れるようにするため
   - エラーの文は「ファイル」「read_file」に触れない言い方にし、`edit_file` はそれに自分の文(パスと read_file の案内)を足して返す。**モデルに見える文は今と変わらない**
   - CRLF を合わせる処理も `Replace` に入れる
5. **書き換えるかどうかの印を、確認と分ける**:任意の interface `tool.Hinted`(`ReadOnly() bool`)を足し、あればそちらを使う
   - (c) では、使う側のツールが全部 `Confirmable` でなくなるので、これが無いと書き換えるツールにも readOnlyHint が付く。(c) を先に通すので、これは要る
   - **`Custom` で渡したツールは、`tool.Hinted` で「読むだけ」と宣言したときだけ readOnlyHint を付ける**(既定は付けない。Code の見直し 1)。組み込みは今のまま(`Confirmable` でなければ読むだけ)
   - `update_plan` は予定を書き換えるので、`Hinted` で「読むだけではない」と宣言して readOnlyHint を外す(Code の見直し 1 の「今もある例」)
6. **差分を作る関数を公開する**:`tool.UnifiedDiff(name, before, after string) string`(今の `unifiedDiff`。大きすぎれば空)
7. **`Replace`・`UnifiedDiff` は `tool` に置き、別のパッケージに分けない**(2026-11-25。未決事項 3 だったもの)。
   使う側は `mcp/server` を使う時点で `tool` を import するので、分けても依存は減らない。`Replace` だけを使いたい相手が現れたら、そのときに分ける(パッケージの分け方の基準どおり)
8. **使い方の見本を置く**:`mcp/server` に `Example` を足し、`_docs/mcp.md` に「ほかのアプリから使う」の節を書く
   - 節には、`New` の返す `*mcp.Server` を SDK の `mcp.NewStreamableHTTPHandler` で包む形も書く(画面のアプリは標準入出力を持たないため。未決事項 2)
   - **引数の定義の効く範囲**を書く(Code の見直し 4):`tool.CheckArgs` が見るのは `properties` の直下の `type` と `required` だけ。
     入れ子の中身・`enum`・最小値などは確かめないので、それはツールの `Run` で自分で確かめる
   - Go の版:aibow の `go.mod` は `go 1.27.0` なので、import したアプリも 1.27 以上になる(標準の `uuid` を使っているため)

話の流れから Chat が決めて書き、2026-11-25 にユーザーが確かめたこと(着手のとき。13 の補足は Code が足し、ユーザーが認めた):

9. **`Custom` とほかの欄の関係**(Code の見直し 3):`Custom` と一緒に `WorkDir`・`Folders`・`Fetch` を渡したら、決定事項 3 の確かめでエラーにする(黙って無視しない。どちらが効いているか分からなくなるため)。
   `Custom` を渡して `Instructions` が空なら、説明は出さない(今の説明はフォルダから作っているので、`Custom` には合わない)。`Custom` を渡さないときの `Instructions` は、空なら今までどおりフォルダの説明
10. **確かめる関数の形**:`func (o Options) Check() error` を公開する。`New` の形(エラーを返さない)は変えず、`New` の中でも `Check` を呼び、誤りなら分かる文で panic する
    (SDK の panic より先に、何が悪いかを書いて止める。`regexp.MustCompile` と同じ考え方。使う側は起動の前に `Check` を呼べば panic を避けられる)。
    組み込みのツールだけのとき(`Custom` が空)は今までどおり通る
11. **エラーの形**:`tool.ErrNotFound`・`tool.ErrAmbiguous`・`tool.ErrEmptyOld`・`tool.ErrSameText` の4つの決まったエラーと、何か所あったかを持つ `*tool.AmbiguousError`(`Count int`。`errors.Is(err, tool.ErrAmbiguous)` も真)。名前は Code が実装のときに整えてよい
12. **`Replaced` の形**:`Text string`(置き換えた後の全体)・`Line int`(置き換えた場所の行番号。1 始まり)・`Reindented bool`(字下げを合わせたか)
13. **`Hinted` の欄は `ReadOnly` だけ**。MCP のほかの印(destructiveHint・idempotentHint・openWorldHint)は、使う側が求めたときに足す。
    `Hinted` があれば、組み込みのツールでも `Confirmable` よりそちらを優先する(`update_plan` はこれで readOnlyHint を外す)
14. **`UnifiedDiff` の書き方は今と同じ**:見出しは `a/<name>` と `b/<name>`、`before` が空なら新しく作るものとして `/dev/null`(git diff と同じ)。行数の上限(前後の合計 5000 行)を超えたら空
15. **`Example` は `mcp/server/example_test.go` の `ExampleNew`**:自分のツールを1つ(`Custom`)持つサーバーを作り、メモリ内のつなぎ方(`mcp.NewInMemoryTransports`)で SDK のクライアントからつないで、
    `tools/list` と `tools/call` の結果を `// Output:` で確かめる(標準入出力で動かす見本だと、テストで出力を確かめられないため)。標準入出力・HTTP で動かす形は `_docs/mcp.md` に書く

## やらないこと

- 確認の口 (b)(`Options.Confirm`)。使う側が求めたときに足す(決定事項 1・未決事項 1)
- エージェントのループ(`agent.go`)・`api/client`・`engine`・`format` を部品にすること(会話・保存・aibow-server との結び付きが強い)
- 新しいパッケージを切ること(`tool/textedit` など。決定事項 7)。`tool` を別のモジュール(`tool/go.mod`)に分けること。同じモジュールのまま import してもらう(使わないパッケージ(yzma・sqlite)はビルドに入らない)
- `tool` の依存(`aibow/log`→wenteasy/log・`aibow/git`・`internal/proc`・`golang.org/x/net`)を切り離すこと。cgo は要らず、重くない
  - ログは `SetLogger` を呼ばなければ `slog.Default()` に出るので、使う側のログにそのまま混ざる(0008)
  - Go の版を下げること(決定事項 8)
- 使う側(メモ帳)の設計(ノードの ID・ツールの一覧・スキル・画面・つなぎ方)。それは使う側のリポジトリで決める
- 版の約束(semver で公開 API を守る、など)。当面はユーザー自身のメモ帳だけが使い手なので、壊したら使う側を直す
- HTTP で実際につないで確かめること(未決事項 2)。`_docs/mcp.md` に包み方を書くまで

## 未決事項

1. **確認の口 (b) をいつ足すか**:使う側が求めたとき。足すときは、`tool/ask.go` の `tool.Ask`(`Preview` を渡して `Answer` を受け取る)を `Options.Confirm` の型にすれば新しい型は要らない。
   足すのは、見せた差分と同じかを実行の直前に確かめる処理(MRTR の `requestState` と同じ役目。Code の見直し 5)
2. **使う側とクライアントのつなぎ方**:Windows の画面のアプリは標準入出力を持たないので、Streamable HTTP で待ち受けるか、小さな exe を別に作るかは使う側で決める。
   SDK(go-sdk v1.8.0)に `mcp.NewStreamableHTTPHandler` があり、`*mcp.Server` をそのまま包める。localhost で待ち受けるときの DNS リバインディングの保護は既定で有効(`DisableLocalhostProtection` で外せる)。
   確認(MRTR)が見るクライアントの能力はリクエストごとの `_meta` からも読む作りなので、ステートレスの HTTP でも動く見込み。**実際につないでは確かめていない**

## 完了の条件

1. `mcp/server.New` に `Custom` のツールだけを渡したとき、tools/list にそれだけが出て、名前と説明が渡したものになる。`Name`・`Instructions` が serverInfo.name と instructions に出る(テスト)
2. `Custom` のツールに readOnlyHint が付くのは、`tool.Hinted` で読むだけと宣言したときだけ。`Confirmable` の `Custom` のツールは、組み込みと同じに MRTR で確かめてから実行する(テスト)
3. `Options.Check` が、名前が空・重なる・定義が読めない・`type` が `object` でない・`Custom` と `WorkDir`/`Folders`/`Fetch` を一緒に渡した、をエラーで返す。組み込みだけのときは通る(テスト)
4. `edit_file` のモデルに見える文と動きが今と変わらない(今のテストがそのまま通る)。`tool.Replace` 単体のテストがある(1か所・見つからない・N か所(数を取り出せる)・空・同じ・字下げを合わせる・CRLF)
5. `update_plan` に readOnlyHint が付かない。ほかの組み込みのツールの印は今と同じ(テスト)
6. `aibow-mcp` と aibow のアプリ(組み込みのツール。`mcp/client` の `Local`)の動きが今と変わらない
7. `ExampleNew` が通り、`_docs/mcp.md` に「ほかのアプリから使う」の節がある(標準入出力・HTTP での動かし方、引数の定義の効く範囲、Go の版)

## 作業の順番

1. `tool.Replace`・`tool.UnifiedDiff` を公開し、`edit_file`(と `write_file` の差分)をそれで書き直す(動きは変えない)
2. `tool.Hinted` を足し、`mcp/server` の `mcpTool` でそれを見る。`update_plan` に付ける
3. `mcp/server.Options` に `Name`・`Instructions`・`Custom` と `Check` を足す(`Custom` の既定の印は 2 のとおり)
4. `ExampleNew` と `_docs/mcp.md`。`AGENTS.md`・`_docs/structure.md` の `tool`・`mcp/server` の説明に、外から使えることを1行足す

## 経過

- 2026-11-09:Chat で相談を始めた。AI とのやり取りをどちらの画面で行うかが決まらず、確認の場所を保留にした
- 2026-11-10:Code が今のコードと SDK(v1.8.0)のソースに照らして見直した(readOnlyHint の既定・`AddTool` の panic・`Custom` とほかの欄・引数の定義の効く範囲・`tool.Ask` を使える・`Replace` のエラーから数を取り出す)
- 2026-11-11:やり取りは相手の画面で行うと決まった。部品は特定の使い手に合わせて作らない、と決めた
- 2026-11-25:確認の場所は (c) を先に通す・Code の見直しを全部採る・`textedit` に分けない、と決めて仕様にした。
  あわせて、TODO の「パッケージの分け方(手元に残る設定のファイルをルートから分けるか)」は 2026-11-08 に `config` パッケージへ移して済んでいると確かめ、TODO と backlog から消した
- 2026-11-25:Code が実装した。決定事項 9〜15(Chat の案)は着手のときにユーザーが確かめ、13 に「`Hinted` があれば組み込みでも優先する」を足した。
  `tool/replace.go`(`Replace` と4つのエラー・`AmbiguousError`)・`tool.UnifiedDiff`・`tool.Hinted`(`update_plan` は読むだけではない)・
  `mcp/server` の `Name`・`Instructions`・`Custom`・`Check`(`New` は誤りがあれば `Check` の文で panic)。`edit_file` は `Replace` のエラーを見て今までの文を作る(`editError`)。
  完了の条件 1〜5・7 はテスト(`tool/replace_test.go`・`mcp/server/custom_test.go`・`example_test.go`・`server_test.go` の印)で確かめた。
  6 は `mcp/client`(`Local`)と `mcp/server` の今のテストがそのまま通ることで確かめた。画面に関わる変更は無いので `wails3 build` は流していない
