# ツール呼び出し(エージェントループ)と書式

モデルが「ツールを使いたい」と書いたら aibow が実行し、結果を渡して続きを書かせる仕組みと、
それを支える「書式」(チャットテンプレート)の interface の説明です。仕様は [features/0004-tools.md](features/0004-tools.md)(ツールを `tool/` パッケージに切り出したのは [features/0005-coding-agent.md](features/0005-coding-agent.md) の 3-0)。

## ファイル

| ファイル | 役割 |
|---|---|
| `format/format.go` | `format` パッケージ。書式の interface(`Format` / `OutputParser`)と、会話の仕組みとエンジンの間で受け渡す形(`Message` / `Prompt` / `Output` / `ToolCall`・画像 `Image` と印 `ImageMarker`。0030)。モデルの種類で書式を選ぶ表(`format.For`) |
| `format/qwen35.go` | Qwen3.5 の書式。プロンプトの組み立て(ツールの説明・呼び出し・結果・ループ中の思考)と、出力の振り分け(思考・答え・`<tool_call>`) |
| `format/gemma4.go` | Gemma 4 の書式(0026)。プロンプトの組み立て(思考の印・独自の書き方のツールの説明と呼び出し・model の発言の中のツールの結果)と、出力の振り分け(`<|channel>thought`・答え・`<|tool_call>`) |
| `format/llama3.go` | Llama 3 の書式(0027)。プロンプトの組み立て(日付・最初の発言の頭のツールの説明・JSON の呼び出し・ipython の結果・呼び出しを1つずつに分ける)と、返事の全体が JSON なら呼び出しとして読む読み取り |
| `format/granite.go` | Granite 4(4.0・4.1)の書式(0029)。プロンプトの組み立て(ツールの説明・`<tool_call>` で囲んだ JSON の呼び出し・user の発言にまとめる結果・4.0 の既定のシステムの文)と、`<tool_call>` の中の JSON の読み取り |
| `format/granite42.go` | Granite 4.2 の書式(0029 の段3)。ChatML の区切りと Qwen3.5 と同じツールの説明・呼び出しで、システムの発言をいつも作る・空の思考の印 `<think></think>`・前の返事の思考を空の印に置き換える。読み取りは Qwen3.5 と同じ部品 |
| `format/parts.go` | 2つ以上の書式で使う部品(タグの書きかけを持っておく `partialSuffix` / `partialSuffixAny`・コードブロックの中か `insideCodeFence`・崩れた呼び出し `brokenCall`・行の頭の `<tool_call>` から後ろを呼び出しとして取っておく読み取り `toolCallParser`(Qwen3.5・Granite 4)・ツールの定義の1行の JSON `toolDefJSON`) |
| `tool/tool.go` | `tool` パッケージ。ツールの interface(`Tool`)・定義(`Def`)・呼び出し(`Call`: 名前と引数だけ)・引数(`Arg`)。引数を JSON Schema と照らし合わせる `CheckArgs` |
| `tool/arg.go` | 引数の値の持ち方(書式によらない形。下の「引数の値の持ち方」)と、書式・JSON との行き来(`ArgFromText` / `ArgText`・`ArgFromJSON` / `ArgJSON`・`ArgsFromJSON` / `ArgsJSON`・`ArgTypes`) |
| `tool/time.go` / `tool/calc.go` | 組み込みのツール `get_current_time` / `calculate` |
| `tool/fetch.go` / `tool/fetchhtml.go` | URL を読むツール `fetch_url`(取りに行く・アドレスを確かめる・文字コード・切り方 / 本文の取り出しと Markdown。0036)。実行前に確認する |
| `tool/files.go` | 作業フォルダのツールの共通部分(`Files`・`os.Root` で開く・パスを作業フォルダからの相対にする) |
| `tool/list.go` / `tool/read.go` | 作業フォルダのツール `list_files`(浅い順にたどる `walk` もここ)/ `read_file` |
| `tool/search.go` / `tool/glob.go` | ファイルの中を探す `search_files` / 名前の形(glob。`**` を自分で合わせる)。`list_files` の `pattern` と `search_files` の `glob` で使う(0037) |
| `tool/plan.go` | 作業の予定を書く `update_plan`(0037)と、履歴の呼び出しから予定を読む `PlanFromCall` |
| `plan.go` | compact で渡さなくなった部分の最後の予定を、システムの文の最後に残す(`lastPlan`・`planNote`・`withPlan`。0037) |
| `tool/write.go` | 書き換えるツール `write_file`(丸ごと書く)/ `edit_file`(1か所だけ置き換える)。どちらも実行前に確認する |
| `tool/editmatch.go` | `edit_file` の `old_string` が字下げだけ違うときに、ファイルの字下げに合わせて見つける(`looseMatch`) |
| `tool/git.go` | 読むだけの Git のツール `git_status` / `git_diff`(0041 の段2。リポジトリで git があるときだけ出す `GitIn`)。確認しない |
| `tool/command.go` | コマンドのツール `run_command`(時間の上限 120 秒・出力の切り方・結果の書き方)。実行前に確認する |
| `tool/shell_windows.go` / `tool/shell_unix.go` | どのシェルで動かすか(cmd.exe / `/bin/sh`) |
| `internal/proc/` | 子プロセスまでまとめて止める仕組み(Windows はジョブオブジェクト、それ以外はプロセスグループ)。`run_command` と MCP のサーバーで使う(0006 で `tool` から移した) |
| `tool/confirm.go` | 実行前に確認が要るツールの印(`Confirmable`)と、確認の画面に出す内容(`Preview`: パス・上書きか・差分)。差分は `github.com/pkg/diff` |
| `agent.go` | エージェントループ(`runTurn`)とツールの実行(10 秒の上限・20 回の上限・停止・会話の上限に入らない結果・実行前の確認の待ち) |
| `tools.go` | 会話で使えるツールを並べる(`toolsFor`)・扱いの決め方(`toolPolicy`・`policyOf`)・設定の画面に出す状態(`ToolStates`)・組み込みのツールと MCP のツールの受け取り(`LocalTools`・`RemoteTools`) |
| `workrules.go` | 作業の決まり(`# Working rules`。0041)。作業フォルダと書き換えるツールがある会話のシステムプロンプトに足す。下の「作業の決まり」 |
| `worktree.go` | worktree で作業する会話(0039)。最初の書き換えの前に作る(`ensureWorktree`)・確認の既定・差分・取り込む・捨てる・会話を消したときの片付け。下の「worktree で作業する」 |
| `git/git.go` / `git/worktree.go` / `git/read.go` | `git` パッケージ。アプリが行う Git の操作(`git` の exe を `os/exec` で呼ぶ)。worktree を作る・本体の状態・差分・`git apply` で当てる・取り込み済みか・消す。読むだけの `Status`・`WorkDiff`(`git_status`・`git_diff` が中で呼ぶ。索引を変えない。0041)。ルートの `aibow`・`tool` を import しない |
| `format/qwen35_test.go` / `format/gemma4_test.go` / `format/llama3_test.go` / `format/granite_test.go` / `format/granite42_test.go` | 書式のテスト。プロンプトは公式テンプレートを jinja2 で通した期待値と1文字まで比べる(`testPromptMatchesTemplate`。書式によらない。ツールの結果の名前が無いときも比べる) |
| `format/testdata/` | 書式のテストの会話(`cases.json`。書式をまたいで共通)と、期待値を作るスクリプト(`gen_expected.py`)。期待値は書式ごとのフォルダ(`qwen35/*.txt`・`gemma4/*.txt`・`llama3/*.txt`・`granite4/*.txt`・`granite42/*.txt`。Granite 4.0 は `granite40/*.txt`) |
| `tool/tool_test.go` / `tool/files_test.go` / `tool/write_test.go` / `agent_test.go` / `workdir_test.go` / `confirm_test.go` | ツール(外に出られないこと・読み方の切り方を含む)とループ・作業フォルダのテスト(偽物の生成器を使う。モデル不要) |
| `format/format_test.go` | `ToolCall` の JSON が切り出す前と同じ平らな形か(保存済みの会話を読めるか)のテスト |

## 書式(Format)

チャットテンプレート(会話の区切り方・思考の書き方・ツールの説明と呼び出しの書き方)はモデルの系統ごとに違う。
そこで系統ごとに違う部分を `Format` にまとめ、**エンジンだけ**が使う:

```
Conversations ─ Prompt(発言・ツールの定義・思考の ON/OFF)→ Engine ─ Format.BuildPrompt → 文字列 → llama.cpp
Conversations ← Output(思考・答え・ToolCall)            ← Engine ← Format.NewParser ← 生成した文字列
```

- 会話の仕組み(`conversation.go` / `compact.go` / `agent.go`)は `<|im_start|>` や `<think>` や `<tool_call>` を一切扱わない
- エンジンはモデルを読み込んだときに GGUF の `general.architecture` と、要るならチャットテンプレートの印で書式を選ぶ(`format.ForModel`。Qwen3.5 は `qwen35`、Gemma 4 は `gemma4`、Llama 3 は `llama` でテンプレートに `<|start_header_id|>` があるもの。0027。Granite 4 は `granite` でテンプレートに `<|start_of_role|>` と `<tool_call>` があるもの、Granite 4.2 は `granite` でテンプレートに `<|im_start|>` と `<function=` があるもの。0029)。
  未対応の種類なら `ModelInfo.Format` が空で、生成とトークン数えがエラーになる(ログとトーストでも伝える。文には対応している書式の一覧を出す)
- 実装は Qwen3.5・Gemma 4(0026)・Llama 3(0027)・Granite 4・Granite 4.2(0029)。GGUF の中の Jinja を実行する汎用の実装は作らない(書式は自分で書く方針)
- 画像(0030):書式は `Message.Images` の場所に印 `<__media__>` を書くだけで、画像の中身は見ない。画像を書ける書式は `WritesImages()` を持つ(今は Qwen3.5 だけ)。
  仕組みは [engine.md](engine.md) の「画像を読む」
- `Format.NewParser` は `BuildPrompt` に渡したのと同じ `Prompt` を受け取る。ツールの定義(引数の型)と、プロンプトがどこで終わったか
  (Gemma 4 は、ツールの結果の後ろで思考 ON なら `<|channel>thought` まで書いてあるので、思考の途中から読み始める)を見るため
- ツールの結果(role `tool`)は `Message.Name` に元の呼び出しの名前を持つ(会話の仕組みが `ToolResult.Call.Name` から入れる。Gemma 4 が結果に名前を書くため)。
  名前が無いとき(0026 より前の会話)は、書式が直前の返事の呼び出しと順番で組にする
- 生成の途中で書式が「止める」と言ったら止める(`OutputParser.Stop`)。終了理由は `stop`

### Qwen3.5 の書式

- ツールを使う会話では、システムプロンプトの先頭に `# Tools` とツールの定義(1つにつき1行の JSON)と英語の指示文を置く。指示文は訳さない(モデルが学習で見た文のため)
- ツールの定義の JSON は transformers の `tojson` と同じ形(`", "` / `": "` の区切り・キーは書いた順・`< > &` をエスケープしない)。
  Go の `json.Marshal` とは見た目が違うので `pyJSON` で書き直す。順番を保つため、JSON Schema は `json.RawMessage` で持つ
- ツールの結果は role `tool` で持ち、プロンプトでは `user` の発言の中に `<tool_response>` で包む(続けて複数なら1つの `user` に並べる)
- **最後のユーザーの質問より後の返事(= ループの途中の返事)は思考も残す**。それより前の返事は答えだけ。
  「何のためにツールを呼んだか」を覚えたまま続きを書かせるため
- 発言の中身は前後の空白を落とす(テンプレートの `|trim`)
- 読み取れなかった呼び出しは、モデルが書いたまま(`ToolCall.Raw`)をプロンプトに戻す。結果の「エラー: …」と並べて、モデルが自分の間違いを見られるようにする
- 呼び出しの引数の値はいつも文字で書く(`<parameter=n>\n5\n</parameter>`)。読むときも書くときも、ツールの定義の型を見て `tool.Arg` の形と行き来する(`tool.ArgFromText` / `tool.ArgText`。下の「引数の値の持ち方」)

### Gemma 4 の書式(0026)

手元の GGUF(Unsloth が量子化したもの)の `tokenizer.chat_template` を、aibow が渡す会話の形で通る部分だけ書き直した(Google の元のテンプレートとの違いは aibow の出力に出ない。0026 の「段2で分かったこと」)。

- 会話の区切りは `<|turn>role\n … <turn|>\n`(役は system / user / **model**)。bos は書かない(トークン化が足す。[engine.md](engine.md))
- 思考の ON は、システムの発言の先頭の `<|think|>\n`(ON ならシステムの発言が無くても作る)。思考は `<|channel>thought\n … \n<channel|>` で、前後の空白を落とさない。
  最後のユーザーの発言より後の返事だけ思考を残す(Qwen3.5 と同じ)。思考を切ったときに空の思考を入れるかは `gemma4EmptyThought`(E4B では入れない。決定事項 8)
- ツールの説明は `<|tool>declaration:名前{description:…,parameters:{…}}<tool|>` で、JSON ではない独自の書き方(文字列は `<|"|>…<|"|>`、キーは名前の順(大文字と小文字を区別しない dictsort)、型は大文字)。
  テンプレートの癖も写す:`type` が配列なら Python のリストの見た目(`['BOOLEAN', 'NULL']`)、properties の無い object の引数は `properties:{}`、`type` の無い parameters は閉じの `}` が無い
- 呼び出しは `<|tool_call>call:名前{キー:値,…}<tool_call|>`。キーは名前の順に並べ直し、値は型で書き分ける(文字列 `<|"|>…<|"|>`、数は Python の `str()`(`1e3` は `1000.0`。`pyNumber`)、真偽・null はそのまま、辞書のキーは囲まない)
- **ツールの結果は model の発言の中のまま**、呼び出しのすぐ後に `<|tool_response>response:名前{value:<|"|>結果<|"|>}<tool_response|>`。結果の前後の空白は落とさない
- **呼び出しのある返事の答えの文は、ツールの結果の後ろに置く**(テンプレートの順)。結果の後ろの返事は、同じ model の発言の続きとして区切りなしにつながる
- 返事の書き出し:ふつうは `<|turn>model\n`。ツールの結果で終わったら新しい発言を始めず、思考 ON なら `<|channel>thought\n` まで書く
- 読み取り:答えの中の `<|channel>`(思考の始まり)と `<|tool_call>`(呼び出しの始まり)を待つ。呼び出しは行の頭とは限らないので、行の頭の決まりは使わず、コードブロックの外かだけを見る(決定事項 10)。
  `<|tool_response>` が出たら止める(公式の停止シーケンス。aibow が結果を続けて書く。決定事項 7)。思考の後ろの改行1つは思考に入れない(書き戻すと同じになる)
- 呼び出しの値は JSON の値にしてから `tool.ArgFromJSON` で `tool.Arg` の形にする。配列・辞書は Python の `json.dumps` と同じ見た目(`["b.go", "c.go"]`)の JSON にするので、モデルを Qwen3.5 に替えてもテンプレートの `tojson` と同じ文字で書ける
- 書式が崩れた呼び出し(閉じていない・`call:` が無い・文字列を囲んでいない・JSON で書いた など)は、`Raw` と `Error`(正しい書き方の例つき)を持たせる

### Llama 3 の書式(0027)

Ollama の `llama3.2:3b` の GGUF のテンプレート(Meta のもの。Unsloth の写しと1文字も違わない)を、aibow が渡す会話の形で通る部分だけ書き直した。

- **書式の選び方**:種類が `llama` で、テンプレートに `<|start_header_id|>` があるときだけ(`format.ForModel`)。`llama` の種類には、Llama 3 の書式でないモデル(Hermes 3 の ChatML)もあるため。
  テンプレートが `strftime_now` を使えば今日の日付(3.2)、使わなければ `26 Jul 2024`(3.1)をシステムの発言に書く
- 会話の区切りは `<|start_header_id|>role<|end_header_id|>\n\n … <|eot_id|>`。システムの発言はいつも作り、`Cutting Knowledge Date: December 2023` と `Today Date: 日付` を書く(ツールがあれば先頭に `Environment: ipython`)。bos は書かない
- **思考が無い**。思考の ON / OFF は無視する。思考しない書式は `NoThinking()` を持ち(`format.Thinks`)、一覧の `models.Entry` の `no_thinking` で画面に伝える。画面の「思考する」は押せなくなる
- ツールの説明は、テンプレートの既定どおり**最初の発言の頭**に英語の指示文と、ツールごとに字下げ 4 の JSON(`pyJSONIndent`。Python の `json.dumps(indent=4)` の見た目)。
  発言が無いとき(ツールの説明だけを数える)は、テンプレートのもう一つの書き方でシステムに書く
- **呼び出しに印が無い**。返事の全体を `{"name": "名前", "parameters": {…}}` にして呼ぶ。1回の返事に呼び出しは1つ、呼び出しのある返事の答えの文は書かない。
  aibow の会話はそうとは限らない(ほかのモデルの会話を引き継いだ)ので、答えの文は別の返事にし、呼び出しは「呼び出し → 結果」の組1つずつに並べ直す(`splitForLlama3`。0027 の決定事項 7・8)
- **ツールの結果は ipython の役の発言**で、名前は書かない。テンプレートは結果の文字を JSON の文字列にして書く(`"7006652"`。Jinja では文字列も繰り返せるものなので tojson を通る)
- 呼び出しの後ろは `<|eot_id|>`(発言の終わり。EOG)なので、書式が止める印は要らない
- 読み取り:返事の頭(空白を除く)が `{` か `<|python_tag|>` なら画面に出さずに持ち、終わってから JSON として読む。`name` と、`parameters` か `arguments` があれば呼び出し(`;` で続く2つ目以降も読む)。
  `"name"` を含むのに読めなければ崩れた呼び出し、それ以外(答えとして JSON を書いた)は答えとして出す

### Granite 4 の書式(0029)

Ollama の `granite4.1:3b` の GGUF のテンプレート(Hugging Face の `ibm-granite/granite-4.1-3b` と1文字も違わない)を、aibow が渡す会話の形で通る部分だけ書き直した。

- **書式の選び方**:種類が `granite` で、テンプレートに `<|start_of_role|>` と `<tool_call>` の両方があるときだけ(書式の表の印は2つ以上持てる)。
  同じ `granite` の種類でも、3.x は呼び出しが `<|tool_call|>` なので選ばない。4.2 は区切りが ChatML で呼び出しが Qwen3.5 と同じ形なので、別の書式(下の Granite 4.2)にした。
  テンプレートに既定のシステムの文(`You are a helpful assistant. Please ensure …`)があれば 4.0 で、システムの発言もツールも無いときにその文を書く(4.1 との違いはこれだけ)
- 会話の区切りは `<|start_of_role|>role<|end_of_role|> … <|end_of_text|>\n`。bos は書かない(テンプレートも書かず、llama.cpp も足さない)
- **思考が無い**(Llama 3 と同じく `NoThinking()`)
- ツールの説明はシステムの発言の後ろ(システムの文があれば `\n\n` を挟む)に、英語の指示文と、ツールごとに1行の JSON(`{"type": "function", "function": …}`。Qwen3.5 と同じ形)
- 呼び出しは答えの文の後ろに `\n` を挟んで `<tool_call>\n{"name": "名前", "arguments": {…}}\n</tool_call>`。2つ目からは `\n` で並べる。引数は書いた順で、真偽は JSON の `true`
- ツールの結果は、続けて返ったものを1つの `user` の発言にまとめ、`\n<tool_response>\n結果\n</tool_response>` を並べる(Qwen3.5 と同じ)。名前は書かない
- **答えの文も結果も、前後の空白を削らずにそのまま書く**(テンプレートが `trim` しない。Qwen3.5・Llama 3 は削る)
- 読み取り:Qwen3.5 と同じく、答えの行の頭(コードブロックの外)の `<tool_call>` から後ろを呼び出しとして取っておき(`toolCallParser`)、中の JSON を読む。
  `<tool_response>` は発言の終わりのトークンではないので、モデルが結果を書き始めたら書式が止める

### Granite 4.2 の書式(0029 の段3)

Ollama の `granite4.2:3b` の GGUF のテンプレート(Hugging Face の `ibm-granite/granite-4.2-3b` と1文字も違わない)を書き直した。区切りは ChatML、ツールの説明の文と呼び出しの書き方(`<function=…><parameter=…>`)・引数の値の書き方(真偽は `True`)は Qwen3.5 と同じで、Qwen3.5 の部品を使う。Qwen3.5 との違いは次のとおり。

- **システムの発言はいつも作る**(システムの文もツールも無ければ中身は空)。システムの文が先で、ツールの説明が後(Qwen3.5 は逆)。ツールの定義の行は `{"type": "function", …}` で包まない
- **空の思考は `<think></think>`**(改行なし)。返事の書き出しは、思考 ON なら `<think>` と改行、OFF なら `<think></think>`
- **最後のユーザーの発言より前の返事は、思考を `<think></think>` に置き換える**(テンプレートの `truncate_history_thinking`)。ループの途中の返事は思考を残し、思考の前後の空白は削らない
- 呼び出しと結果の後ろに改行を置いてから `<|im_end|>` で閉じる。ツールの結果の中身も削らない
- 読み取り:Qwen3.5 と同じ(`toolCallParser` と `parseQwen35Calls`)。思考の後ろの改行1つは思考に入れない(テンプレートは思考を削らずに書き戻すので、入れると改行が増え、KV キャッシュを使い回せなくなる。Gemma 4 と同じ)
- テンプレートの `low_effort`(思考を短くする)は使わない

### 引数の値の持ち方(0026 の段1)

書式によってモデルが値を書く形は違う(Qwen3.5 はいつも文字、OpenAI 互換の口は JSON、Gemma 4 は型のある独自の書き方)。
会話に保存し、モデルを替えても前の呼び出しを新しい書式で書けるように、`tool.Arg.Value` は**書式によらない1つの形**にそろえている:

| 引数の型(JSON Schema の `type`) | `Arg.Value` |
|---|---|
| `string`・型を決めていない(`anyOf` など)・ツールの定義が無い | 文字列の中身そのまま |
| `integer`・`number`・`boolean`・`array`・`object` | JSON の文字(`5`・`true`・`["a", "b"]`) |

- どちらの形かは値だけでは決まらず、ツールの定義の型で決まる。書式は読むときにこの形にし(`ArgFromText`・`ArgFromJSON`)、書くときにこの形から自分の書き方に直す(`ArgText`・`ArgJSON`)。
  そのため `Format.NewParser` はモデルに知らせたツールの定義を受け取り、`BuildPrompt` は `Prompt.Tools` で型を見る
- **型のある引数にモデルが型に合わない文字を書いたとき**(整数に ` 5` や `5.0`、真偽に `yes`)は、書いた文字を JSON の文字列にして持つ(`" 5"`・`"yes"`。Qwen3.5 の `True` はテンプレートの書き方なので true として読む)。
  直さずに持つので、同じ書式に書き戻すと1文字も変わらない(KV キャッシュを使い回せる)。`CheckArgs` は中身の文字を今までどおり大目に見て変換する(` 5` → 5、`True` → true)
- 0026 より前に保存した会話は、型のある引数でもモデルが書いたままの文字(`True` など)を持っている。JSON の文字列でない値は書いたままの文字として扱うので、そのまま読めて、プロンプトも前と同じになる
- Qwen3.5 の書式は、テンプレートどおり真偽を `True` / `False`、null を `None` で書き、モデルが書いた `True` / `False` / `None` は JSON の true / false / null として読む
  (`qwen35ArgText` / `qwen35ArgFromText`。0026 の決定事項 26。段1では `true` のまま書いていた)。モデルが小文字の `true` を書いたときは、書き戻すと `True` になる
- 書くときにツールの定義が無い(使わないにしたツールの前の呼び出し・`tool_choice: "none"`)と型が分からないので、値をそのまま書く。型に合わない文字を書いていた値だけ、`"True"` のように引用符つきで書かれる(まれなので受け入れる)

### 出力の読み取り

- 答えの中に `<tool_call>` が出たら、そこから先は答えの欄に出さずに取っておく。タグが分かれて届いても読めるよう、書きかけかもしれない末尾は持っておく(思考の `</think>` と同じ考え方)
  - 呼び出しと見るのは、**行の頭**(答えの最初か改行の直後)で、**コードブロック(``` / ~~~)の外**にある `<tool_call>` だけ(`isCallPosition`)。テンプレートは呼び出しを必ず行の頭から書く。
    文の中の `` `<tool_call>` `` は仕組みを説明する答えなどで書かれたもので、呼び出しと見ると答えがそこで切れ、「読み取れない呼び出し」のエラーを返されたモデルが同じ答えを書き直し続けた(2026-10-16。AGENTS.md を読ませてシステムの説明を書かせたとき)
- 生成が終わったら `<tool_call>…</tool_call>` を全部読む。値の前後の改行1つずつは区切りなので落とし、値の中の改行と字下げは残す
- `</tool_call>` の後にモデルが `<tool_response>` を書き始めたら、そこで生成を止める(結果を想像させない)
- 書式が崩れた呼び出し(閉じていない・`<function=…>` が無い・Qwen3 の JSON の書き方など)は、`Raw` と `Error`(正しい書式の例つき)を持たせる

### 期待値の作り方(テスト)

プロンプトの組み立ては、公式テンプレートと空白・改行の1文字まで一致させる。
期待値は GGUF の中のテンプレートを jinja2 で実際に通して作り、`format/testdata/<書式の名前>/*.txt` に置いている。
スクリプトは書式によらない1つで、書式の名前(GGUF の `general.architecture`)と GGUF のパスを渡す(0026 の段2)。

```bash
python format/testdata/gen_expected.py qwen35 ~/.ollama/models/blobs/sha256-<qwen3.5 の GGUF>
```

```bash
python format/testdata/gen_expected.py gemma4 /d/Go/Projects/aibow/_cmd/aibow/models/gemma-4-E4B-it-Q4_K_M.gguf
```

GGUF が手元に無い版は、Hugging Face の `chat_template.jinja` を渡し、出力の名前を足す(Granite 4.0。0029)。

```bash
python format/testdata/gen_expected.py granite4 <granite-4.0-micro の chat_template.jinja> granite40
```

- 会話は `format/testdata/cases.json` に書き、Python と Go のテストが同じものを読む。**ケースは書式をまたいで共通**にして、同じ会話を書式ごとに見比べられるようにする。
  ある書式だけのケースは `"formats": ["gemma4"]`。ケースの `"tools"` には知らせるツールの名前を並べる。ケースを足したら、全部の書式の期待値を作り直す
- 引数の値は `tool.Arg` の形で書く。スクリプトはツールの定義の型を見て Python の値にしてテンプレートに渡す(テンプレートが書式ごとの書き方で書く)
- ツールの結果は、直前の返事の呼び出しと順番で組にして `tool_call_id` と `name` を付けて渡す(Gemma 4 は結果に名前を書く)
- 違う種類の GGUF を渡したらエラーにする(書式の名前と `general.architecture`、要るならテンプレートの印を比べる。スクリプトの `FORMATS`)
- **bos**:書式は bos を文字で書かず、トークン化(`addSpecial`)に任せる。GGUF の `tokenizer.ggml.add_bos_token` が True(Gemma 4)なら、テンプレートが先頭に書いた bos を1つ外して期待値にする(Qwen3.5 は False で bos も無い)
- `testdata/` は `.gitattributes` で改行コードを変換しない(1バイトまで比べるため)

## エージェントループ(agent.go)

```
1. 準備(checkFits と compact の prepare)→ 2. 生成して保存 → 3. 呼び出しが無ければ終わり
→ 4. 順に実行して、結果を role tool の発言として保存 → 1 に戻る
```

- 毎回の生成の前に compact の準備を通す(ツールの結果でプロンプトが伸びるため)。今の組(最後のユーザーの発言から後ろ)だけで会話の上限を超えたら止めてエラー
- ツールの結果を足すと今の組が会話の上限を超えるときは、残りの 3/4 に入る長さで結果を切って渡し、ループを続ける(`fitResult`・`cutToFit`。0025。上限を超えたまま会話が残らないように)。
  長さは、モデルに渡すのと同じ形で数えながら二分探索で決め、行の切れ目で切る。read_file の結果なら「N 行目までで切りました。続きは offset=N+1 で読めます」、
  ほかの結果(MCP など)なら「ここで切りました。渡したのは結果の約 P% です」を書き添える。1行も入らなければ、渡さずに残りのトークン数を書いたエラーの結果にし、その送信の次の生成ではツールを見せずに答えさせる(それでも呼んだら止める)。
  以前(0005)は結果を捨てて「範囲を絞って呼び直してください」とだけ返していたが、小さいモデルはどう変えればよいか決められず、同じ呼び出しを繰り返した(`_docs/tuning.md` の「仕様の相談を試す」)
- 同じ呼び出し(名前と引数)が同じエラーで2回続いたら、1回の送信の上限(20 回)を待たずに止め、理由と対処を画面に出す(`repeatedFailure`。0025。書式の崩れは別に `maxBrokenRounds` で止める)
- 同じ呼び出し(名前と引数。結果は見ない)が**成功のまま続けて**呼ばれたら、2回目も実行して結果を渡したうえで、次の生成ではツールを見せず、最後に「同じ呼び出しを繰り返しています。ここまでの結果で答えてください」の頼みを足す(`repeatedCall`・`repeatedNote`。0028。保存しない)。それでも呼んだら止めて理由を画面に出す。残りが無いときと同じ手当てで、理由は `answerNow` で分ける。
  結果を比べないのは、`get_current_time` の結果は秒が変わり、llama3.2:3b の繰り返しを見つけられないため。続けて、だけを見るのは、「直す → テストを動かす」のように間に別の呼び出しを挟む正しい繰り返しを止めないため(交互の繰り返し・引数を変えた呼び直しは 20 回の上限に任せる)。同じエラーで続いたときは、こちらより先に `repeatedFailure` で止める
- **失敗しても送信は止めない**: 書式の崩れ・知らないツール・引数の間違い・ツールのエラー・時間切れは、「エラー: …」の結果としてモデルに返し、モデルに立て直させる
- 実行は1回の送信で 20 回まで(コーディングは回数がかさむため。0004 では 8 回。書式の崩れは実行していないので数えない)。超えた分の呼び出しにも「実行していません」の結果を保存してから止め、理由を画面に出す
- 書式の崩れは 20 回に数えないので、呼び出しが全部読み取れなかった回が **3 回続いたら止める**(`maxBrokenRounds`。間に読み取れた呼び出しがあれば数え直す)。
  数えないままだと、モデルが同じ崩れ方を繰り返したときに終わらない(2026-10-16 に起きた)。止めたら理由を画面に出す
- 停止ボタンはループごと止める。実行しなかった・実行中に止めた呼び出しにも「停止」の結果を残す
  (**どの呼び出しにも必ず結果がある**形にしておくと、次の送信で渡す履歴が崩れない)
- ツール1回の実行は 10 秒まで。ツールが ctx を見なくても、待つのは上限まで
- 「答えが出ないまま終わった回」の理由の文は、ツールを呼ばなかった回にだけ付ける(ツールを呼んで答えが空なのは正常)
- 「使わない」にしたツール(会話の設定の画面の「ツール」。プロファイルで最初は使わないツールは、会話を作るときに写る)と、プロファイルで使えないツールは説明をプロンプトに入れず、呼ばれても「ツール … はありません」のエラーを返す。以前は会話ごとの「ツールを使う」で全部を切り替えていたが、設定の画面の「ツール」と混乱するので 0006 で無くした

## ツール

ツールはルートの `tool/` パッケージ(`tool`)にある。`tool` は LLM・書式・会話・保存のどれも import しない(使う外部パッケージは、確認の差分を作る `github.com/pkg/diff` だけ)。
依存の向きが import としてそのまま見える:

```
書式(format/qwen35.go)── 定義(tool.Def)をプロンプトに書き、呼び出しを読み取る ──→ tool
会話の仕組み(agent.go)── 実行する(回数・時間の上限・停止・結果の保存はこちら)──→ tool
tool ── 何も知らない
```

- 切り出した理由:ステップ5の MCP サーバーはツールだけを外に出すプログラムで、ルートの `aibow` を import すると LLM(yzma)や会話の仕組みまで入ってしまう。
  ステップ4の MCP クライアントも「遠くのツールを `tool.Tool` として見せる」ので、interface が独立しているほうが素直
- 呼び出しは2層。`tool.Call` は名前と引数だけ。書式の崩れ(モデルが書いたままの `Raw` と理由の `Error`)は書式の話なので、
  ルートの `ToolCall` が `tool.Call` を埋め込んで足している。埋め込みなので JSON は `{"name", "args", "raw", "error"}` の平らな形のまま
  (切り出す前と同じ。保存済みの会話をそのまま読める。Wails が作る TS の型も同じ形)
- エージェントループ(回数の上限・時間の上限・停止・結果の保存)は会話の仕組みなので、ルートの `agent.go` に残している
- 型の名前はパッケージ名と重ねない(`tool.ToolDef` ではなく `tool.Def`。Go の慣習)。interface だけは `tool.Tool`(`context.Context` と同じく、ほかに良い名前が無いため)
- `Tool` は定義(`Def`: 名前・説明・引数の JSON Schema)と実行(`Run`)を持つ。名前は英語の snake_case、説明も英語
- 引数は `tool.CheckArgs` が JSON Schema の `properties` / `required` と照らし合わせる。間違いは「使える引数: …」付きのエラーで返す
- `CheckArgs` は `tool.Arg` の形の値を、JSON Schema の `type` を見て変換する(`integer` → int、`number` → float64、`boolean` → bool、`array` / `object` → JSON を読んだ値、ほかは文字列)。
  型に合わない文字(JSON の文字列で持っている)は、中身を大目に見て読む。書き方は、公式テンプレートが履歴に呼び出しを書き戻すときの形に合わせる(配列・オブジェクトは `tojson`、それ以外は文字列。テンプレートは Python で動くので真偽値は `True` / `False`。0006 で MCP のツールのために足した)。
  `type` が `["integer", "null"]` のような配列なら null でない最初の型、`anyOf` などで型を決めていなければ文字列のまま
  `Run` は変換済みの `tool.Args`(`map[string]any`。`String` / `Int` で取り出す)を受け取る。保存とプロンプトへの書き戻しは `tool.Arg`(上の「引数の値の持ち方」)を使う
- ステップ3(ファイル操作)のツールも `tool.Tool` で足し、`Conversations.tools` に入れる。
  ステップ4の MCP のサーバーのツールも `tool.Tool`(`tool.Remote`)で、`Conversations.toolsFor` が組み込みのツールの後ろに並べる([mcp.md](mcp.md))
- ツールを使うか・実行前に確認するかは、組み込みのツールも MCP のツールも `Conversations.toolPolicy` で決める(プロファイルで使えない → 会話の上書き → `tools.json` → 決まりの既定。[mcp.md](mcp.md) の「ツールの扱い」)。決まりの既定は今までどおり(書き換えるツールとコマンドは確認する、読むだけのツールは確認しない)

| 名前 | 中身 |
|---|---|
| `get_current_time` | 現在の日時・曜日・タイムゾーン。`timezone`(IANA の名前)を省略すると PC のタイムゾーン。配布先でも名前を引けるよう `time/tzdata` を埋め込む |
| `calculate` | `go/parser` で式を読み、数・`+ - * /`・括弧だけを受け付ける。整数を小数に書き換えてから `types.Eval` で定数式として計算する(`7/2` が `3.5`、`0.1+0.2` が `0.3`) |

### 作業フォルダのツール(0005 の 3-1)

- 作業フォルダは、会話が選んだ**ワークスペース**(その PC の `workspaces.json` に登録した、名前付きのフォルダの組。0035)。
  会話の設定はワークスペースの ID(`ConversationSettings.Workspace`)だけを持ち、パスは PC の手元に置く(`config.Workspaces`。`config/workspaces.go`)。
  この PC に無い ID(別の PC で選んだ・消した)なら、作業フォルダが無いものとして扱う(フォルダのツール・`${workdir}` の MCP・環境の説明を出さない)
- ツールはフォルダの組を持った値として、送信のたびに `tool.FilesIn(folders)` で作る(`Conversations.toolFolders` → `localTools`)。
  `Tool` の interface は作業フォルダを知らない。MCP のサーバー(`mcp/server` の `Options.Folders`)も、渡されたフォルダの組で同じツールを作る
- 作業フォルダを決めた会話だけ、ファイルのツールと環境の説明をプロンプトに入れる。環境の説明は使うツールに合わせ、時刻・計算のほかにツールが無ければ書かず、シェルと cd の注意は `run_command` を使うときだけ書く
- **外に出られないことは仕組みで守る**: `os.Root`(決めたフォルダの下しか開けないファイル操作の口)で開く。開くのは呼び出しごと(フォルダが消えたらエラーの結果を返す)
  - `../` や外の絶対パスは、開く前に `localPath` が分かりやすいエラーにする。作業フォルダの中を指す絶対パスは受け付ける(4B は絶対パスで書きがちなため)
  - 外を指すシンボリックリンク・ジャンクションは `os.Root` が止める(テストは Windows ではジャンクション、Linux ではシンボリックリンクで確かめる。
    Windows のシンボリックリンクは開発者モードか管理者が要るので、作れなければそのテストは飛ばす)
- `list_files`: 下のフォルダまでたどり、作業フォルダからの相対パスを `/` 区切りで並べる(フォルダは末尾 `/`)。`.git` / `node_modules` は名前も出さない
  - **500 件か 8,000 文字の早いほうで切る**。件数だけだとパスの長い深いファイルで大きくなりすぎる(aibow のリポジトリで 500 件・約 43,000 文字になった)
  - **浅い順に並べる**(直下を全部 → 1つ下を全部 → …。それぞれの中は名前の順)。切ったときも全体の形が残るように。奥までたどる順だと、最初の大きなフォルダで上限に達する
  - **隠しフォルダ(`.` で始まる)は名前だけ出して中に入らない**(「(中は省略)」と書く)。path に指定すれば中を並べる。`.gitignore` は読まない
  - **`pattern`(0037)を付けると、名前の形に合うファイルだけを並べる**(フォルダは並べない。たどり方・上限・順は同じ)。`/` を含まない形(`*.go`)は名前だけと、
    含む形(`**/*_test.go`・`cmd/**/main.go`)は作業フォルダからの相対パスと比べる。`**` は0段以上(`path.Match` に無いので `glob.go` で自分で合わせる)。`\` は区切りに直さず、記号をそのまま合わせる印(`*\.go` は `main.go` に合う)。新しいツールにしないのは、小さなモデルのプロンプトを増やさないため。
    最後に「pattern … に合うファイルは全部で N 件です」と書く(上限で切っても数えきる。4B は並んだ名前を数え間違えた。2026-11-03、[tuning.md](tuning.md))
- `search_files`(0037):正規表現(Go の RE2)に合う行を `パス:行番号: 行` で返す。たどり方は `list_files` と同じ `walk`。
  100 件か 8,000 文字で切り、数えるのは最後まで続けて「一致は全部で N 件(M ファイル)」を書く。1 行は 300 文字で切る。
  NUL を含む・UTF-8 でない・1MB を超えるファイルは黙って飛ばし、数だけを最後に書く。読むだけなので確認しない。
  ツールの説明には「中を探す。名前で探すなら list_files の pattern」と書き、`list_files` の説明にも「名前で探すなら pattern」と書く
  (4B が名前で探すのに search_files を選んで迷ったため。2026-11-03、[tuning.md](tuning.md))
- `read_file`: `行番号 + タブ + 行`。200 行か 8,000 文字の早いほうで切り、「続きは offset=N で読めます」を書き添える。
  1,000 文字を超える行は 1,000 文字ごとに分けて、別の行として番号を付ける(`splitLong`。0025。長い行の続きも offset で読めるように。番号はエディタの行番号とずれる)。
  NUL を含むもの・UTF-8 でないもの・10MB を超えるものはエラー。CRLF の CR は見せない

### 環境の説明

作業フォルダを決めた会話では、会話の仕組みがシステムプロンプトの後ろ(要約より前。会話の中で変わらないので KV キャッシュの再利用を崩さない位置)に英語で足す(`environment`):

```
# Environment
Working directory: D:\Work\example
OS: windows
Shell for run_command: cmd.exe (use dir and type; ls, cat and grep are not available. To search inside files, use search_files instead of findstr)
Use paths relative to the working directory.
Commands already run inside the working directory, so do not cd into it. "example" refers to the working directory itself.
```

書式に関係ない情報なので、書式ではなく会話の仕組みの側で組み立てる。シェルの行は OS ごとに `tool.ShellNoteFor`(Linux なら `/bin/sh`)。
`search_files` を使う会話では、Windows の行で findstr を案内せず `search_files` に向ける(0037 の決定事項 8。4B が `run_command` で findstr を呼び、確認が挟まるのを防ぐ)。
`search_files` を「使わない」にした会話では、前の行(`use dir, type, findstr; …`)のまま。aibow-mcp の instructions は、フォルダがあればいつも `search_files` を出すので、いつも新しい行

ワークスペースのフォルダが複数なら、フォルダを名前・印・パス・説明で並べる(`foldersEnvironment`・`tool.FolderLine`。印は main / writable か read-only / git。git は `.git` があるか。
説明はユーザーが設定の画面で書いたもの(1行・100 文字まで。無ければパスで終わる)を、日本語のまま ` — ` の後ろに付ける):

```
# Environment
Folders (pass the folder argument; omit it for the main folder):
- aibow (main, writable, git): D:\Go\Projects\aibow
- notes (read-only): D:\Docs\notes — 会議のメモ
OS: windows
Shell for run_command: cmd.exe (use dir and type; ls, cat and grep are not available. To search inside files, use search_files instead of findstr)
Use paths relative to the folder.
Each command starts in the main folder "aibow" unless you pass folder; a cd does not carry over to the next command. To run in another folder of the list, pass folder instead of cd. Each folder name refers to that folder itself, not to a subfolder.
```

### 作業の決まり(0041)

作業フォルダがあり、書き換えるツール(`edit_file`・`write_file`)がある会話では、環境の説明の後ろ(要約より前)に `# Working rules` を足す(`workingRules`)。
どこまで続けるか・何を変えてよいか・最後に確かめるか、を短い英語で書く(1行ずつ、試しで見えた失敗に当てている。[features/0041-agent-prompt.md](features/0041-agent-prompt.md))。

```
# Working rules
- Keep using tools until the task is done. A reply without a tool call ends your turn, so do not stop to report progress.
- Read the code before you change it. Change only the code the task is about; unless asked, do not edit tests to make them pass or create new test files.
- If you think no change is needed, quote the line you read that shows it.
- When a tool returns an error, fix the argument it names. Never repeat a failed call with the same arguments.
- After you change files, check your work: run the tests the user asked for (or the usual test or build) with run_command, and fix what fails. You can review your changes with git_diff.
- Then answer in a normal reply, not only in your thinking: what you changed and the result of the check.
```

- 会話の設定のシステムプロンプト(ユーザーが書くもの)には入れず、コードが組み立てる。ツールの有無で書き分けるため・既定の文を変えると今までの会話や配ったプロファイルとずれるため
- 書き換えるツールが無い会話(読むだけ・質問だけ)には付けない。システムプロンプトは決まりを足す前と1文字も変わらない
- 確かめる行(5行目)は `run_command` があるときだけ。無いときは最後の行も「what you changed.」まで(確かめた結果を求めない)。「You can review your changes with git_diff.」は `git_diff` があるときだけ
- 送信ごとに、環境の説明と作業の決まりが何トークンかを Info のログに出す(`logSystemParts`)。行は段1・段2 の 4B の試しで直した(tuning.md)
- aibow-mcp・OpenAI 互換の口(Cline など)には足さない(ほかのクライアントは自分の決まりを持っている)

### フォルダが複数のワークスペース(0035 の段2)

- ファイルのツール(`list_files`・`read_file`・`search_files`・`write_file`・`edit_file`)に引数 `folder`(省略可。名前を JSON Schema の `enum` で並べる)を足す。省けばメイン。
  名前は大文字と小文字を区別しない(ワークスペースの中でも、大文字と小文字だけの違いは重なりとして断る)。
  パスの先頭に名前を付ける形(`notes/a.md`)は、メインの中のサブフォルダと区別できないので採らない
- **フォルダが1つなら `folder` を出さない**(定義は段1・`tool.Files` と同じ。4B のプロンプトを増やさない。`withFolder`)
- 見つからないパスがほかのフォルダにあれば、エラーに「folder に <名前> を書いて」と添える(`elsewhere`。書き換えるツールでは読むだけのフォルダを案内しない)。
  パスの先頭にほかのフォルダの名前を付けた書き方(`spec/a.md`)は、メインにその名前のものが無ければ「folder と path に分けて」のエラーにする(`prefixed`)。
  選んだフォルダ自身の名前を付けた書き方(`aibow/agent.go`)も、同じ名前のサブフォルダが無ければ「パスの先頭に付けず」のエラーにする(フォルダが1つでも。2026-11-03)。
  どれも Qwen3.5 4B で試して足した([tuning.md](tuning.md))
- 絶対パスは、どのフォルダの中かを見て振り分ける(入れ子なら深いほう)。`folder` を書いたのにそのフォルダの外を指していれば「食い違い」のエラー
- **読むだけのフォルダ**には `write_file`・`edit_file` を向けられない。`Preview` の前(`filePath`)で「<名前> は読むだけのフォルダなので、書き換えられません。ほかのフォルダに写しを作らず、書き換えられないことをユーザーに伝えて、ここで止まってください」を返すので、確認のカードは出ない
  (止まるよう促すのは、4B が書けるフォルダに写しを作ることがあったため。[tuning.md](tuning.md))
- `run_command` にも引数 `folder` を付け、選んだフォルダをカレントにする(省けばメイン。`tool.RunCommandIn`。フォルダが1つなら定義は今までと同じ)。
  以前はメインだけで動かしていたが、書けるフォルダや git の印の付いたフォルダでビルドや Git を動かせず、モデルが `cd B && git status`(メインの下の B を探す)や、
  cmd.exe の `/d` 無しの別のドライブへの `cd`(カレントが変わらない)で失敗した(2026-11-02、ユーザーの報告。0035)。コマンドはどこでも書けるので、カレントをメインに縛っても守りにはならない
- **読むだけのフォルダでも `run_command` は動かせる**(`git log`・`git diff` のような調べるだけのコマンドのため。2026-11-02、ユーザーと決めた)。
  コマンドからは書けてしまうので、守りは今までどおり確認。確認のカードの「何をするか」を「コマンド(読むだけのフォルダ)」にする(環境の説明に書くのは read-only の印まで)
- `${workdir}` の MCP のサーバーはメインのパスで起動する(ほかのフォルダを渡す書き方は作らない)
- **`run_command` は呼ぶたびに新しいシェル**(`cmd.Dir` にフォルダを決めて起動し、終われば閉じる)。`cd` はその1回の中だけで、次の呼び出しに持ち越されない。
  モデルが同じシェルが続くと思い込むと、「`cd /d B`」のあとの `folder` なしの `git commit` がメインで動いてしまうので、ツールの説明と環境の説明にそれを書く。
  `cd` を避けるのは「一覧のほかのフォルダ」に絞り、サブフォルダへの `cd`(`cd frontend && npm test`)は禁じない
- それでもモデルは `folder` を渡さずに `cd "C:\...\B" && git log` と書く。cmd.exe の `cd` は `/d` が無いと別のドライブへ移らず成功扱いなので、
  メイン(D:)で動いて別のリポジトリのログが返った(2026-11-03、ユーザーの報告)。そこで、**先頭の `cd <絶対パス> &&` の行き先がワークスペースのフォルダ(の中)なら、
  そのフォルダをカレントにする**(`tool.leadingCd`。`folder` より強い)。コマンドは書き換えず、確認のカードにはそのフォルダの名前が出る
- 先頭の `cd` の行き先が**ワークスペースのフォルダの名前**(`cd aibow && go build ./...` のような相対パス)で、カレントにその名前のサブフォルダが無ければ、
  確認のカードを出す前にエラーで直し方を返す(カレントのフォルダ自身なら「cd を外して」、ほかのフォルダなら「folder に <名前>」。`cdName`。フォルダが1つでも)。
  4B は「"aibow" refers to the working directory itself.」と書いてもこう書き、cmd.exe の「The system cannot find the path specified.」を見て `/d` を足すなどして繰り返した(2026-11-03、[tuning.md](tuning.md))
- **フォルダの説明**(`WorkspaceFolder.Description`・`tool.Folder.Description`。2026-11-03、ユーザーと決めた):ユーザーが「マニュアルを直して」のように名前と違う言葉で呼んでも、
  モデルがどのフォルダかを当てる手がかりとして、環境の説明(と `aibow-mcp` の instructions)のフォルダの行の末尾に付ける。ツールの動きには使わない(守りは読むだけの印と確認)。
  送信のたびにシステムプロンプトに入るので、1行・100 文字まで(`folderDescriptionMax`。保存のときに改行と続く空白を1つの空白にし、長ければ断る)。
  説明だけ変えても aibow の中の MCP サーバーは作り直さない(`folderKey` に入れない)。フォルダが1つのときは使わない(段1の形を変えない)。4B での効き目はまだ試していない
- 確認のカードには、**フォルダが複数なら、メインでもそのフォルダの名前を出す**(`tool.Preview.Folder`。ファイルのツールも `run_command` も。フォルダが1つなら出さない)。
  以前はメイン以外のときだけ出していたが、それだと上の取り違えでメインで動くことにカードで気づけないので、そろえた(2026-11-02、ユーザーと決めた)
- aibow の中の MCP サーバー(`mcp/client` の `Local`)は、フォルダの組ごと(名前・パス・メイン・書けるかの組。`folderKey`)に1つ。設定の画面で組を変えたら、次の送信から新しい組のサーバーを使う

### URL を読むツール(0036)

- `fetch_url`(`tool/fetch.go`・`tool/fetchhtml.go`):URL のページを取ってきて、本文を返す。引数は `url` と `start_index`(0 から)。返す長さはモデルに選ばせず 5,000 文字(`fetchMaxRunes`)。
  結果は1行目に `URL: <最終的な URL>(<状態>・<種類>)`、空行をはさんで本文、切ったときは最後に「全体は N 文字です。続きは start_index=M で読めます」(`read_file` の offset と同じ考え方。0025)。読むたびにページを取り直す(保存・キャッシュはしない)。
  切ったときは、渡した範囲より後ろの大きい見出し(`##` まで。無ければ `###` まで。30 個まで)を start_index と一緒に並べる(`headingIndex`)。
  10 万文字のページの後ろの節を、4B が頭から 5,000 文字ずつ読んで会話の上限を使い切ったため(2026-11-03、[tuning.md](tuning.md))
- 設定で動きが変わる(PC の中のアドレスを許すか・User-Agent の版)ので `tool.Builtin` には入れず、動かす側が `mcp/server` の `Options.Fetch` で足す。
  アプリと aibow-server は aibow の中のサーバー(`mcp/client` の `Options.Fetch`)で出し、aibow-mcp は `-fetch` のときだけ出す。設定の画面の一覧は `tool.Folderless`(時刻・計算・URL を読む)。
  `SetLocalTools` をしない会話の仕組み(テストの直接の形)には入らない
- 中身の種類:HTML(`text/html`・`application/xhtml+xml`)は本文を Markdown に、`text/*`・JSON・XML はそのまま、ほかは「この種類は読めません」。種類は Content-Type で先に見て(画像を 2MB 受け取ってから断らない)、無ければ `http.DetectContentType`
- 文字コード:`charset.DetermineEncoding`(BOM → Content-Type → `<meta charset>`)。どれにも書いていなくて UTF-8 として正しければ UTF-8(既定の windows-1252 にしない)
- 本文の取り出し:`script`・`style`・`noscript`・`template`・`svg`・`iframe`・`nav`・`header`・`footer`・`aside`・`form`(と `head`・`button`・`select`・`img`)を落とし、
  最初の `main` → いちばん長い `article` → `body` の順に本文の場所を選ぶ。`<title>` は頭に `# 題`。リンクは絶対の URL にし、ページの中(`#…`)・`javascript:` は文だけ。表の最初の行が `th` だけなら区切りの行 `| --- |` を足す
- 上限:2MB(超えた分は読まずに切り、そう書き添える)・15 秒(`tool.TimeLimiter`)・リダイレクト 5 回・`http` と `https` だけ。プロキシは使わない(`Transport.Proxy` が nil)
- **PC の中・社内のアドレスを断る**:`net.Dialer.Control` で、名前を引いた後の実際につなぐアドレスを `tool.PublicAddr` で確かめる(loopback・private・link-local・100.64/10・未指定・マルチキャスト・説明用など)。
  名前を引いてから確かめて名前でつなぎ直す形だと、その間に答えを変えられる(DNS rebinding)ため。リダイレクトの先も同じ Transport を通るので確かめられる。
  テストは名前の引き方とつなぎ先を差し替える(`fetchURL.dial`。同じ `control` で確かめてからテストのサーバーにつなぐ)
- 扱いの既定:`tool.Confirmable` なので確認する(確認のカードにホストと URL。`tool.Preview.URL`)。aibow-server では管理者が `server-tools.json` でツールごとに有効にするまで禁止(`ToolPrefs.banned` の `optIn`。組み込みのグループの扱いでは有効にならない)
- ログ:取りに行った URL・状態・受け取ったバイト数・本文と渡した文字数・秒数を Info、断ったアドレスを Warn(誰の・どの会話かは `ui` の `logTool` が出す)

### 作業の予定(update_plan。0037 の段3)

- `update_plan`:モデルが作業の予定をチェックリストとして書く(Claude Code の TodoWrite に当たる)。引数 `items` は予定の全部(項目ごとに `text` と `status`(`pending` / `in_progress` / `done`))で、毎回まるごと書き直す。
  `in_progress` は1つまで(2つ以上はエラー)。何も実行せず、結果は「予定を覚えました(N 件中 M 件済み)」だけ(予定は呼び出しとして履歴にあるので繰り返さない)
- 作業フォルダのある会話だけで出す(フォルダのツールと `run_command` の間。`localTools` と `mcp/server` の `Options.Tools`)。aibow-server の資料のフォルダ・aibow-mcp でも出る。確認しない
- 予定は別に保存しない。「今の予定」は履歴の最後の、成功した `update_plan` の呼び出しの引数(`lastPlan`)。
  compact で呼び出しを含む発言をモデルに渡さなくなったら、渡さない部分の最後の予定を、システムの文の最後(要約の後ろ / truncate なら残した発言の前)に英語で足す(`passFrom` → `withPlan`。[compact.md](compact.md))
- 画面では、`update_plan` のカードを引数と結果ではなくチェックリストで出す(`PlanCard.tsx`。済みは打ち消し線、取りかかっているものは太字)。エラーの呼び出しはふつうのカード

### 読むだけの Git のツール(0041 の段2)

- `git_status`:作業ツリーの変わったファイルを「`modified: tool/glob.go`」の形で並べる(`added` / `modified` / `deleted` / `untracked`。ステージしたかは区別しない。無視しているファイルは出ない)
- `git_diff`:`HEAD` との差分(`git diff HEAD`。ステージしたものもしていないものも)。引数 `path` で絞れる。Git に入っていないファイルは含まない(そう説明に書き、`git_status` に向ける)。
  8,000 文字で行の切れ目で切り、入りきらなかったファイルの名前と「path で絞って」を添える
- どちらも索引を変えない(`status` と `diff` だけ)。worktree の差分の画面(`git.Changes`・`git.Diff`)は先に `add -A` でステージするが、こちらはユーザーの本体のフォルダでも動くため
- 出す条件:どれかのフォルダが Git のリポジトリ(`.git` がある)で、`git` の exe が PATH にあるとき(0039 の worktree と同じ見分け方)。読むだけなので確認しない。
  フォルダが複数なら引数 `folder` で選ぶ(リポジトリでないフォルダを選ぶとエラー)。パスの決め方はファイルのツールと同じ(`resolve`)
- worktree の会話では worktree の中で動く。`HEAD` は worktree を作ったときのコミットなので、会話で変えたものだけが出る(本体のコミットしていない変更は出ない)
- モデルに Git を書き換える操作(commit・push・branch)はさせない(0039 と同じ)。aibow-mcp にも出る(ツールの一覧は aibow の中のサーバーと同じ)

### コマンドのツール(0005 の 3-3)

- `run_command`: 作業フォルダ(ワークスペースのフォルダが複数なら、引数 `folder` で選んだフォルダ。省けばメイン)をカレントにしてシェルで実行し、出力と終了コードを返す。実行前に確認する(確認のカードにはコマンドを出す)
- コマンドは何でもできるので、ファイルのツールの `os.Root` のように仕組みでは縛れない。実行前の確認が唯一の守り
- **Windows**:`cmd.exe /d /v:on /s /c "chcp 65001 >nul && cmd.exe /d /s /c "!AIBOW_COMMAND!""`(コマンドは環境変数 `AIBOW_COMMAND`)。窓は出さない(`CREATE_NO_WINDOW`)
  - 同じ cmd.exe の中で `chcp 65001` しても組み込みのコマンドは Shift_JIS のままなので、chcp の後に起動した cmd.exe で動かす(cmd.exe 自身のメッセージは英語になる)
  - コマンドを遅延展開(`!変数!`)で渡すのは、外側の cmd.exe にコマンドの中の `" & |` を解釈させないため
  - シェルをジョブオブジェクトに入れ、時間切れ・停止のときはジョブごと止める(cmd.exe だけを止めると、子プロセスがパイプを持ったまま残り、Wait が返らない)。
    ジョブには KILL_ON_JOB_CLOSE を付け、終わった後に残ったプロセスも止める
- **Windows 以外**:`/bin/sh -c`。プロセスグループで起動し(`Setpgid`)、止めるときはグループごと(負の PID に SIGKILL)。macOS は未確認
- 時間の上限は 120 秒(`tool.TimeLimiter`。ほかのツールは 10 秒のまま)。上限を決めるツールは後片付けをして返る約束なので、ループは時間切れ・停止の後 5 秒まで結果を待つ
- 標準入力は空(NUL / `/dev/null`)。標準出力とエラー出力は出た順に1つにまとめる
- 結果は「出力 + `(終了コード N)`」。0 以外の終了コードもエラーではない(テストの失敗をモデルに読ませる)。時間切れ・停止はエラーで、ここまでの出力を付ける
- 出力は先頭 3,000 バイト・末尾 5,000 バイトを残して間を切る(エラーは末尾に出やすい)。CRLF は LF に、UTF-8 でないバイトは `?` に

### 書き換えるツールと実行前の確認(0005 の 3-2)

- `write_file`: 新しく作るか、丸ごと上書きする。親のフォルダが無ければ作る。すでにあるファイルはパーミッションを引き継ぐ
- `edit_file`: `old_string` がファイルの中に1か所だけなら `new_string` に置き換える。0 か所なら「見つかりません(read_file で…。行番号と、その後ろのタブは含めません)」、
  2 か所以上なら「N か所あります。前後の行を含めて…」のエラー。改行が CRLF のファイルは、LF で書かれた `old_string` / `new_string` をファイルに合わせてから探す
  (`read_file` は CR を見せないので、合わせないと複数行がいつまでも見つからない)。
  0 か所でも、**行頭の空白(字下げ)だけが違い、どの行も同じだけずれていて、1か所に決まるなら、ファイルの字下げに合わせて置き換える**(`new_string` も同じだけずらす。`tool/editmatch.go`)。
  結果の文で「字下げを合わせました」と伝え、確認のカードの差分には実際の変更が出る。4B は read_file の行番号の後ろのタブまで写して、タブを1つ多く書くことが多かった(7 回のうち 4 回。2026-11-03、[tuning.md](tuning.md))
- どちらも UTF-8 のテキストだけ(読むのと同じ `readText` の確かめ)。作業フォルダの外には `os.Root` で出られない
- **確認**: 書き換えるツールは `tool.Confirmable`(`Tool` + `Preview`)。エージェントループは実行の前に `Preview` で確認の内容(パス・「上書き」「新しく作る」「書き換え」・差分)を作り、
  `confirm` イベント(`ConfirmRequest`: ID・呼び出し・内容)で画面に送り、`Conversations.Confirm`(画面からは `ChatService.ConfirmTool`)の答えをチャネルで待つ
  - 「ループを途中で止めて、ユーザーの返事を待つ」のは、チャネルで待つだけでできる。1つの送信の中ではツールを順に実行するので、待っている確認も送信ごとに1つ(進行中の送信 `activeTurn.pending`。`turns.go`。送信は利用者ごとに1つ)
  - `Preview` がエラー(置き換える場所が見つからない、など)なら、聞かずにエラーの結果をモデルに返す
  - 拒否したら実行せず、「ユーザーが実行を拒否しました。理由: …」(理由が空なら「(書かれていません)」)を結果にしてループを続ける。エラーの結果ではない
  - 待つ間は時間の上限(10 秒)に数えない(秒数も実行だけを測る)。停止ボタンで止まり、「停止されたため実行していません」の結果を残す
  - 実行するときは、もう一度ファイルを読んで確かめる(確認を待つ間に変わっていたら、古い中身で書き戻さない)
- **組み込みのツールは MCP 越し**(0007 の 5-4):アプリでは、組み込みのツールを aibow の中の自作の MCP サーバー越しに呼ぶ(`Conversations.SetLocalTools`。[mcp.md](mcp.md) の「aibow の中で動かす」)。
  書き換えるツールは `tool.SelfConfirming` で、呼ぶ前に確認せず、サーバーが送ってきた差分で1回だけ聞く。`SetLocalTools` をしなければ `tool` のツールを直接呼び、下の確認のしかたになる
- **実行の途中で確かめるツール**(`tool.Interactive`。0007 の 5-3):MCP のサーバーのツールは中身が見えないので、サーバーが実行の途中で確認を求めてくる(MRTR。[mcp.md](mcp.md) の「サーバーからの確認」)。
  エージェントループは `Run` の代わりに `RunInteractive` を呼び、聞く関数(`tool.Ask`)を渡す。聞く関数は扱いが「確認しない」なら聞かずに受け入れ、それ以外は同じ確認のカードを出す(`askFor`)。
  時間の上限はツールが自分で数える(確認を待つ時間を数えないため)。拒否の文は `tool.RejectedText`(サーバーと同じ文)
- **差分**: `github.com/pkg/diff`(Myers の方法。ユーザーと決めて足した)で、git diff と同じ unified 形式(前後 3 行・`@@` の行番号付き)にする。
  画面は ```` ```diff ```` のコードブロックで出す(+ は緑、- は赤。highlight.js の diff)
  - 前後の行数の合計が 5,000 を超えたら差分を作らない(丸ごと書き直した大きなファイルは計算に時間がかかるため)
  - 共通の行が1つも無いと、`pkg/diff` は「消す」と「足す」を別の `@@` に分けて書く(`@@ -1,1 +0,0 @@` と `@@ -0,0 +1,1 @@`)。行の中身は正しいので、そのまま出している
  - `diff.Text` は文字列の代わりに nil を渡すとファイル名からファイルを読むので、必ず文字列を渡す(作業フォルダの外を読ませない)

### worktree で作業する(0039)

会話ごとに Git の worktree を作ってそこで書き換えさせ、最後に本体との差分を見て、取り込む / 捨てる形(仕様は [features/0039-worktree.md](features/0039-worktree.md))。
**Git の操作はアプリが決まった手順で行い、モデルには普通の作業フォルダとして見せるだけ**。モデルには worktree だと伝えない(プロンプトは1文字も増えない)。

- **切り替え**:会話の設定の `worktree`(入力欄の上のトグル。会話の DB)。使えるのは、ワークスペースのメインのフォルダが Git のリポジトリ(直下に `.git`)で、`git` が PATH にあるときだけ(`worktreeUsable`。使えなければ画面は灰色にして理由を出す)
- **作る時**:切り替えを入れた会話で、組み込みの `write_file`・`edit_file`・`run_command` がメインのフォルダを扱う(`folder` を省いたか、メインの名前)前に作る(`needsWorktree` → `ensureWorktree`。`runTool` の中)。読むだけなら作らない。
  `run_command` も入れるのは、コマンドもファイルを書き換えうるから(作る前に本体で動かすと、本体が変わってしまう)。MCP のサーバーのツールでは作らない(どこを書き換えるかが分からない)
- **作り方**(`git` パッケージ。呼んだコマンドは Info のログに出る):
  1. 本体の `.git/info/exclude` に `/.aibow/` を足す(`git.Exclude`。場所は `rev-parse --git-path info/exclude` で聞く。本体の `git status` に出ないように。Git に入っているファイルは書き換えない)
  2. 本体のコミットしていない変更を数える(`git.Uncommitted`。`status --porcelain`)
  3. `git worktree add -b aibow/<短い ID> <本体>/.aibow/worktrees/<短い ID> <HEAD のコミット>`(`git.Add`)。作ったときのコミット(`base`)と、そのとき本体がいたブランチの名前(`base_branch`。ブランチから外れていれば空)・コミットの件名(`base_subject`)を覚える(どちらも画面に出すだけ。`git.Head`。`log -1 --format=%H%x00%s` と `rev-parse --abbrev-ref HEAD`)。
     短い ID は「<worktree の名前>-<会話の ID の末尾 4 文字>」、名前が無ければ「<末尾 8 文字>」(`shortWorktreeID`)。
     末尾を使うのは、UUIDv7 の後ろ半分がランダムだから(先頭 12 文字は作った時刻のミリ秒で、上から 8 文字は約 65 秒ごとにしか変わらず、近い時刻に作った会話と重なる)。重なれば 4 文字ずつ延ばす
  4. 結び付け(会話・本体・パス・ブランチ・base)を `workspaces.json` のワークスペースの `worktrees` に書く(書けなければ worktree を消す)
- **worktree の名前**(`worktreename.go`):ブランチとフォルダの名前に使う英語の短い名前(`fix-login-error` など。会話の設定の `worktree_name`)。
  会話の最初の送信で、返事を書かせる前に1回だけモデルに書かせる(`nameWorktree`。ツールを見せない・思考なし・上限 24 トークン。頼みは英語で「2〜4語の英小文字を - でつないだ名前だけ」)。
  最初の送信にするのは、作業領域(KV キャッシュ)が1つだから:途中で別の頼みを挟むとその会話のキャッシュが崩れ、次の生成でプロンプトを丸ごと読み直すが、最初ならその会話のキャッシュはまだほとんど無い。
  書かせるのは、メインのフォルダが Git のリポジトリのワークスペースの会話だけ(切り替えを入れていなくても。あとで入れたときのため)。返ってきた文は `slugify` で英小文字・数字・`-` だけにする(何も残らなければ名前なし)。
  マルチバイトは使わない(会話のタイトルは使わない)。使用量の記録では返事に数える。
  会話の設定の画面で直せる(`SetWorktreeName`。作る前だけ。`UpdateSettings` では変えない:画面が持っている古い設定で、最初の送信で作った名前を消さないため)
- **作る元の表示**:切り替えを入れていてまだ作っていなければ、`WorktreeState` に本体の今のブランチ・コミット・件名を添える(`git.Head`。画面は「main d08244e」と出し、吹き出しに件名)
- **作ったあと**:その会話のメインのフォルダのパスを worktree にする(`toolFolders`・`workDir`)。ファイルのツール・`run_command`・`${workdir}` の MCP・環境の説明がすべて worktree を指す。
  フォルダの名前(モデルが呼ぶ名前)は変えない。フォルダが1つの環境の説明の「"<名前>" refers to the working directory itself.」も本体のフォルダの名前のまま(`mainPath`)。
  作った回の環境の説明はパスが変わるので、KV キャッシュはそこから作り直しになる
- **お知らせ**:作ったことと、本体のコミットしていない変更 n 件・Git に入っていないもの(`node_modules` など)は worktree に無いことを、そのツールの結果の `ToolResult.Notice` に入れる(画面のカードに出す。モデルに渡す結果の文には入れない)。あわせて `worktree` の chat イベントを送る(画面は入力欄の上を取り直す)
- **確認の既定**:worktree で作業する会話では、`write_file`・`edit_file` の決まりの既定を「確認しない」にする(`Conversations.defaultPolicy`。書き換えは worktree に入り、取り込む前に差分で見られるため)。
  会話・PC で「確認する」にしていればそちらが勝つ(`policyOf` の順のまま)。`run_command` は確認する(コマンドは worktree の外にも書けるため)。
  切り替えを入れていても、作れない状態(リポジトリでない・git が無い)なら本体を直接書くので、確認する
- **差分**(`git.Changes` / `git.Diff`):worktree の中を全部ステージして(`add -A`。worktree の索引はアプリのもの)、索引と `base` を比べる(`diff --cached --no-renames`)。新しいファイル・消したファイルも入る。名前を変えたファイルは「消した」と「足した」の2つ(ファイルごとに選べるように)。
  画面に出す差分は `--binary` を付けない(バイナリは「Binary files … differ」の1行)、当てる差分は付ける
- **取り込む**(`git.Apply`):選んだファイルの差分を本体の作業ツリーに `git apply`(`--3way` なし)。全部当たるか、何も変えないか。本体の索引には触れない。コミットはユーザーがする。
  当たらなければ、git のエラーの文(`LC_ALL=C` で英語に固定)から当たらないファイルを読み取って返す。取り込んだあとも worktree は残す(会話を続けられる)
- **取り込み済みか**(`git.Applied`):覚えておかず、そのつど本体を見る。消したファイルは本体に無いか、足した・変えたファイルは本体のハッシュ(`hash-object`。改行の変換を当ててから計算する)が worktree のものと同じか。
  変えたファイルで中身が違えば、そのファイルの差分を本体に逆向きに当てられるか(`apply --check -R`)。本体の同じファイルの別の行にコミットしていない変更があっても、取り込んだものが分かる
- **捨てる・消す**:`git worktree remove --force` と `git branch -D`(`git.Remove`。フォルダを手で消してあれば `worktree prune`)。捨てると結び付けを消し、会話の切り替えも外す(本体のフォルダに戻る)。
  会話を消したら一緒に消す(この PC に結び付けの無い会話なら何もしない)。生成中は捨てられない
- **守り**:作ったあとは切り替えを外せない(捨てるのは差分の画面)・ワークスペースを変えられない(`keepWorktree`)。worktree のある会話が残っているワークスペースは消せない(結び付けが消えると片付けられなくなるため)
- 作るのは画面のアプリだけ(aibow-server の資料のフォルダでは作らない。`noWorkDir` のときは何もしない)。画面の口(`ui/worktree.go`)もブラウザには出さない
- テスト:`git/worktree_test.go`(一時リポジトリを `git init` で作る。当たらないとき・autocrlf・同じファイルの別の変更・git が無いとき)、`worktree_test.go`(会話から:作る時・確認の既定・取り込む・捨てる・会話を消す)。git が無ければ飛ばす

## 保存

- 1回の送信で `user → assistant(tool_calls)→ tool … → assistant` と1件ずつ保存する(マイグレーション版 3 で `messages` に `tool_calls` / `tool_result` を足した)
- compact の「組」はユーザーの発言で区切るので、ツールの発言は組の中に入る(外すときも要約するときも組ごと)
- 要約に渡す文には、呼び出し(「(ツール calculate を呼んだ: expression=…)」)と結果(先頭 200 文字)も入れる
- 確認への答え(実行した / 拒否した・理由)は `ToolResult.Confirm` として結果と一緒に保存する(JSON の列なのでマイグレーションは要らない)。再起動してもカードに「拒否」と出る

## 画面とログ

- 1回の送信の返事は吹き出し1つ。中に「思考 → 答え → カード → 思考 → 答え …」と並べ、最後の答えより前は「思考の過程(N秒)・ツール N回」の枠にまとめて畳む([ui.md](ui.md))
- `chat` イベント: `reply`(返事を1件保存した)・`tool_start`・`tool_result`(結果を保存した)。`done` の `reply` は最後の返事
- ログには、生成の回ごとの統計(再利用の数・ツール呼び出しの件数)と、ツールの実行(名前・引数・結果の長さ・秒数・エラー)を出す。崩れた呼び出しはモデルが書いたままも出す

## 関連

- 実際のモデルで試して分かったこと(思考 ON/OFF の違い・KV キャッシュの再利用): [tuning.md](tuning.md)
