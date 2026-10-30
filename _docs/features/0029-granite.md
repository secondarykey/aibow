# 0029: Granite の書式

- 状態: 🔧 作業中(2026-10-30 に段2を終えた)
- 決めた日: 2026-10-30(Code が調べて案を書き、未決だったもの(版・試すモデル・4.0)はユーザーが選んだ)
- ロードマップ: TODO.md の「モデルの対応(ほかと分けて扱う)」の「Granite 4 の書式」
- 前提: 0026(引数の値の持ち方 `tool.Arg`・書式によらない期待値のスクリプト・`format/parts.go`)、0027(書式を種類とテンプレートの印で選ぶ `format.ForModel`・思考しない書式 `NoThinking`)
- 関係する検討メモ:`_docs/backlog.md` の「ほかのモデルの書式に対応する」
- 番号:0028 は別のセッションの「同じ呼び出しの繰り返しを止める」が使っているので、0029 にした

## 目的

**4つ目・5つ目の書式として IBM の Granite 4.1(4.0 も)と 4.2 を手で書く**。Granite は関数呼び出しに向けて学習した小さいモデル(Apache 2.0)で、0027 で llama3.2:3b がツールの呼び出しを繰り返したのと比べられる。

学ぶこと:

- **JSON を印で囲んで呼び出す流儀**:`<tool_call>\n{"name": …, "arguments": {…}}\n</tool_call>`。Llama 3(印なしの JSON)と Qwen3.5(印の中が XML 風)の間の形で、Hermes 3・Qwen2.5 なども同じ流儀
- **同じ系統でも版で書式が変わる**:Granite 4.0・4.1 と 4.2 では、区切りも呼び出しの書き方も違う(下の表)。種類の名前ではなくテンプレートの印で選ぶ(0027)ことが要る例
- **ツールの結果を1つの発言にまとめる**:続けて返った結果は、1つの user の発言の中に `<tool_response>` を並べる

## 調べて分かったこと(2026-10-30、Code が調べた)

出典:Hugging Face の `ibm-granite/granite-4.0-micro`・`granite-4.1-8b`・`granite-4.2-8b` の `chat_template.jinja`、Ollama のライブラリの `granite4`・`granite4.1`・`granite4.2` のタグ、IBM の Granite 4.2 の文書。
**手元に Granite の GGUF はまだ無い**ので、GGUF の中のテンプレートと同じか、GGUF の種類(`general.architecture`)が何かは未確認(正とするのは使う GGUF の中のもの。0026 の決定事項 2)。

### 版ごとの違い

| | 4.0(2025) | 4.1 | 4.2(いちばん新しい) |
|---|---|---|---|
| Ollama | `granite4`(350m・1b・3b。`-h` は Mamba を混ぜた形。7b-a1b-h・32b-a9b-h も) | `granite4.1`(3b 2.1GB・8b 5.3GB・30b) | `granite4.2`(3b 2.2GB・8b 5.3GB・30b) |
| 会話の区切り | `<\|start_of_role\|>role<\|end_of_role\|> … <\|end_of_text\|>\n` | 4.0 と同じ | **ChatML**(`<\|im_start\|>role\n … <\|im_end\|>`。Qwen3.5 と同じ) |
| システムの発言 | 無ければ**既定の文**(`You are a helpful assistant. Please ensure …`)を書く | あるとき・ツールがあるときだけ | あるとき・ツールがあるときだけ |
| 思考 | 無い | 無い | **ある**(`<think>`。既定で ON。OFF は `<think></think>`) |
| ツールの説明 | システムに英語の指示文と、ツールごとに1行の JSON(`tojson`。OpenAI の `{"type": "function", "function": …}` のまま) | 4.0 と同じ | システムに `# Tools` と `<tools>`(Qwen3.5 とほぼ同じ文) |
| ツールの呼び出し | `<tool_call>\n{"name": "名前", "arguments": {…}}\n</tool_call>`。前に答えの文を書ける。2つ以上は改行で並べる | 4.0 と同じ | `<tool_call>\n<function=名前>\n<parameter=引数>\n値\n</parameter>\n</function>\n</tool_call>`(**Qwen3.5 と同じ形**) |
| ツールの結果 | 続けて返った結果を1つの user の発言にまとめ、`\n<tool_response>\n結果\n</tool_response>` を並べる。名前は書かない | 4.0 と同じ | 4.0 と似た形(改行の位置が違う) |

- 4.0 と 4.1 の違いは、システムの発言が無いときに既定の文を書くかどうかだけ
- 4.2 は書式としては Qwen3.5 にとても近い(区切り・ツールの説明・呼び出しの形)。違うのは、システムの発言とツールの説明の順・空の思考の書き方(`<think></think>`。改行なし)・前の思考を消す決まり(`truncate_history_thinking`)・ツールの結果の改行
- 4.0・4.1 の `<tool_call>`・`<tool_response>` は語彙にあるが特別なトークンではない(`special=false`)。`<|start_of_role|>`・`<|end_of_role|>`・`<|end_of_text|>` は特別なトークン。`<|end_of_text|>` は bos と eos を兼ねる
- Granite 3.x(`granite3.3` など)も `<|start_of_role|>` を使うが、呼び出しは `<|tool_call|>` で、ツールの説明の置き方も違う。**種類の名前(`granite` の見込み)だけでは選べない**ので、テンプレートの印で分ける(0027 の決定事項 2 と同じ考え方)

## 決定事項(2026-10-30)

1. Granite の書式は `format/granite.go` に手で書く。Jinja を動かす汎用の形は作らない(0026・0027 と同じ)
2. 書式の選び方:GGUF の種類が Granite のもの(段1で確かめる)で、テンプレートに `<|start_of_role|>` と `<tool_call>` の両方があるとき(4.0・4.1)。Granite 3.x はどちらかが無いので未対応のまま
3. 期待値は 0026 のスクリプト(`format/testdata/gen_expected.py`)と共通の会話(`cases.json`)で作る
4. bos は書式が書かない(0026 の決定事項 25)。段1で、トークン化が先頭に何を足すか(`<|end_of_text|>` が bos を兼ねている)を確かめる
5. 呼び出しの引数の JSON は、テンプレートの `tojson` と同じ区切り(`", "`・`": "`)で書く。Llama 3 の書式の部品(`pyJSONString` など)を使う
6. 4.0・4.1 は思考しない書式(`NoThinking`。0027 の決定事項 18)
7. **(ユーザーが選んだ)版は両方。4.1 を先に書き、4.2 をあとの段で足す**。2つは区切りも呼び出しも違うので別の書式にする(書式の名前は「Granite 4」と「Granite 4.2」)。
   4.2 はテンプレートに `<|im_start|>` と `<function=` があるときに選び、Qwen3.5 の書式の部品を使い回せるところは使い回す(どこまで共通にするかは 4.2 の段で決める)
8. **(ユーザーが選んだ)試すモデルは Ollama の `granite4.1:3b`**(2.1GB。llama3.2:3b・Qwen3.5 4B と同じくらいの大きさで比べる)。4.2 は同じ大きさの `granite4.2:3b`(2.2GB)で試す(Code の案)
9. **(ユーザーが選んだ)4.0 も 4.1 と同じ書式で扱う**。テンプレートに既定のシステムの文があれば、システムの発言が無いときにその文を書く(Llama 3 の日付と同じく、書式の表の `make(template)` で書き分ける)。
   手元に 4.0 の GGUF は無いので、期待値は Hugging Face の `granite-4.0-micro` のテンプレートで作り、テストは書き分けの差だけを見る

### 段1で分かったこと(2026-10-30。Ollama の `granite4.1:3b` と `granite4.2:3b`)

- **GGUF の中のテンプレートは、Hugging Face のもの(`granite-4.1-3b`・`granite-4.2-3b`)と1文字も違わない**(未決事項の「違ったとき」は起きなかった)
- **GGUF の種類はどちらも `granite`**(`general.basename` は `granite-4.1` / `granite-4.2`)。決定事項 2・7 のとおり、テンプレートの印で分ける。
  期待値のスクリプトの書式の表(`FORMATS`)は印を2つ以上持てるようにし、`granite4` は `<|start_of_role|>` と `<tool_call>`、`granite42` は `<|im_start|>` と `<function=` にした。4.1 の GGUF を `granite42` で渡すとエラーになる
- **bos はどちらも付かない**:語彙だけを読み、エンジンと同じ形(`addSpecial=true`)でトークン化して確かめた。
  4.1 は `add_bos_token = False`(bos は `<|end_of_text|>`)。4.2 は `add_bos_token` を書いていない(bos は `<s>`)が、llama.cpp は足さない。どちらのテンプレートも bos を書かない
- **印のトークン**:`<|start_of_role|>`・`<|end_of_role|>`・`<|end_of_text|>`・`<|im_start|>`・`<|im_end|>`・`<tools>` は特別なトークン、`<tool_call>`・`</tool_call>`・`<tool_response>`・`</tool_response>`・`<think>`・`</think>` は語彙に足した普通のトークン(type 4)で、どれも1トークン。`<function=`・`<parameter=` は Qwen3.5 と同じく何トークンかに分かれる
- **発言の終わり(EOG)は `<|end_of_text|>`(4.1)・`<|im_end|>`(4.2)だけ**。`<tool_response>` は EOG ではないので、呼び出しの後ろでモデルが結果を書き始めたら書式が止める(Qwen3.5 の `Stop` と同じ)
- 期待値は 4.1(`granite4/`)・4.0(`granite40/`。Hugging Face の `granite-4.0-micro` の `chat_template.jinja` から)・4.2(`granite42/`)で11件ずつ。
  **4.0 と 4.1 で違うのは、システムの発言もツールも無い `no_system_think_off` の1件だけ**(4.0 は既定の文のシステムの発言を書く)。スクリプトに、テンプレートのファイル(`.jinja`)と出力の名前を渡せるようにした。Qwen3.5・Gemma 4・Llama 3 の期待値は作り直しても変わらない

**段2で Granite 4(4.0・4.1)の書式を書くときに気をつけること**(テンプレートの動き):

- 答えの文は**そのまま**書き(前後の空白を削らない)、呼び出しがあれば答えの文が空でないときだけ `\n` を挟んで `<tool_call>` を書く。2つ目からは `\n` で並べる
- 呼び出しは `<tool_call>\n{"name": "名前", "arguments": {…}}\n</tool_call>`。引数は書いた順で、区切りは `", "`・`": "`。真偽は JSON の `true`(Qwen3.5 の `True` とは違う)。型に合わない値(` 5`)は JSON の文字列
- 続けて返った結果は1つの user の発言にまとめ、結果ごとに `\n<tool_response>\n結果\n</tool_response>`(結果の中身はそのまま)。結果の名前は書かない
- 思考はどこにも書かない(前の返事の思考も捨てる)
- システムの発言は「システムの文」・「システムの文 + `\n\n` + ツールの説明」・「ツールの説明だけ」・(無い)のどれか。4.0 は無いときに既定の文

**段3で 4.2 の書式を書くときに気をつけること**(Qwen3.5 との違い):

- ツールの説明の行は `{"type": "function", "function": …}` で包まず、中身(`{"name": …}`)だけを書く
- システムの文が先で、ツールの説明が後(Qwen3.5 は逆)
- 空の思考は `<think></think>`(改行なし)。返事の書き出しは、思考 ON なら `<think>\n`、OFF なら `<think></think>`
- 最後のユーザーの発言より前の返事は思考を消して `<think></think>` にする(`truncate_history_thinking`)。残す思考は `<think>\n思考\n</think>\n` の形
- `</tool_call>` と `</tool_response>` の後ろに `\n` を置いてから `<|im_end|>`
- 引数の書き方(文字列はそのまま、真偽は `True`、配列・オブジェクトは JSON)は Qwen3.5 と同じ

### 段2で決めたこと(2026-10-30)

10. 書式の表の行は、テンプレートの印を2つ以上持てる(`formatEntry.marks`。どれもあるときに選ぶ)。Granite 4 は `granite` で `<|start_of_role|>` と `<tool_call>`。対応している書式の文(`format.Supported`)は「テンプレートに A と B があるもの」と書く
11. **行の頭の `<tool_call>` から後ろを呼び出しとして取っておく読み取りを、Qwen3.5 と共通の部品にした**(`parts.go` の `toolCallParser`・`toolCallBlocks`。ツールの定義の1行の JSON も `toolDefJSON` で共通)。
    `<tool_call>` の中の読み方(Qwen3.5 は XML 風、Granite 4 は JSON)だけを書式が渡す。思考の無い Granite 4 は、思考の振り分けを「全部が答え」にして使う。Qwen3.5 の動きは変えていない(テストはそのまま通る)
12. 4.0 かどうかは、テンプレートに既定のシステムの文(`You are a helpful assistant. Please ensure …`)があるかで見る(`newGranite4`)
13. テンプレートのとおり、システムの文・答えの文・ツールの結果は前後の空白を削らずに書く
14. 崩れた呼び出し:`<tool_call>` の中が JSON として読めない・`"name"` が無い・`"arguments"` がオブジェクトでない・`</tool_call>` で閉じていない。どれもモデルが書いたまま(`Raw`)を次のプロンプトに戻す。`"arguments"` が無い・`null` なら引数なし
15. 読み取った呼び出しを書き戻すと、モデルが書いた文字と同じになる(次の送信で KV キャッシュを使い回せる)ことをテストで確かめた(日本語・`< & >`・整数・配列・真偽)

段2の結果:4.1(`granite4/`)と 4.0(`granite40/`)の期待値11件ずつと1文字まで一致した。Qwen3.5・Gemma 4・Llama 3 の期待値のテストもそのまま通る。

## やらないこと(先回りしない)

- Granite 3.x の書式
- `documents`(資料を渡す欄。aibow は使わない)
- Mamba を混ぜた形(`-h`)が llama.cpp で動くかを調べること(試すモデルに選んだときだけ見る)
- GGUF の Jinja を動かす汎用の書式

## 未決事項(Code は勝手に決めず、案を示してユーザーに確認する)

- (2026-10-30 の3つは決めた。上の 7〜9)
- GGUF の中のテンプレートが Hugging Face のものと違ったとき、どちらに合わせるか(0026 の決定事項 24 では手元の GGUF に合わせた)

## 完了の条件(案)

- アプリ(直接・aibow-server 越し)・ブラウザ・OpenAI 互換の口で、Granite と会話できる
- Granite でツールを使える(時刻・計算・作業フォルダのツール・MCP)。結果のあとに続けて答える
- Granite 4.2 で思考の ON / OFF が効く
- 会話の途中で Qwen3.5・Gemma 4・Llama 3・Granite 4・Granite 4.2 を替えても、前の発言とツールの呼び出しが新しい書式で書かれ、会話が続く
- Granite のプロンプトの組み立てが、使う GGUF のテンプレートを jinja2 で通した期待値と1文字まで一致する。ほかの書式の期待値のテストも今のまま通る
- Granite 3.x のような、種類が同じで書式の違うモデルは未対応と出る
- 0026・0027 と同じ頼みごとを Granite でも送り、`_docs/tuning.md` の比べに並べる(特に llama3.2:3b が繰り返した場面)
- `gofmt -l .`・`go test ./...`・`wails3 build`・`go vet` が通る
- ドキュメント:`_docs/tools.md`・`_docs/engine.md`・`_docs/usage.md`・`_docs/tuning.md`・TODO.md

## 作業の順番(案)

1. ✅ **段1 モデルと期待値**(2026-10-30。上の「段1で分かったこと」):モデルを取ってきて(ユーザー。`granite4.1:3b` と `granite4.2:3b`)、GGUF の種類とテンプレート・bos を確かめ、4.0・4.1・4.2 の期待値を作る
2. ✅ **段2 Granite 4 の書式**(2026-10-30。上の 10〜15):`format/granite.go`(プロンプト・既定のシステムの文・ツールの説明・呼び出し・結果のまとめ方・読み取り)と、書式の表の行
3. **段3 Granite 4.2 の書式**:4.2 の書式(思考・Qwen3.5 との共通の部品)と、書式の表の行
4. **段4 確かめる・比べる**:実際のモデルで、アプリ・ブラウザ・OpenAI 互換の口・書式の切り替え・tuning.md の比べ
