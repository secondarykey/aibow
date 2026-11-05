package aibow

// workrules.go: エージェント向けの作業の決まり(0041)。
//
// 作業フォルダがあり、ファイルを書き換えるツールがある会話では、システムプロンプトの環境の説明の後ろに
// 「# Working rules」の節を足す。どこまで続けるか・何を変えてよいか・最後に確かめるか、を短く書く。
//
// ここで学べること:エージェントの振る舞いはシステムプロンプトで決まる。同じモデルでも、決まりの数行で
// 「どこで止まるか」「何を確かめるか」が変わる(Cline は「最後に必ず確かめる」「終わるまでツールを呼び続ける」と書いてあり、
// 4B でも自分でテストを実行した。0012 の 6-3)。
//
// 決まりは1行ずつ、0040 の試しで残った失敗に当てている(_docs/features/0041-agent-prompt.md の決定事項 5)。
// 小さなモデルに効くよう、長い説明ではなく、守れたか外から確かめられる短い文にする。英語(環境の説明・ツールの説明と同じ)。
//
// 会話の設定の System(ユーザーが書く)には入れず、コードが組み立てる:
// ツールの有無で書き分けたい・既定の文を変えると今までの会話や配ったプロファイルとずれる・「日本語で簡潔に」はそのまま効かせたい(決定事項 3)。
// 会話の中で変わらないので、環境の説明と同じく先頭に置く(KV キャッシュの使い回しを崩さない。決定事項 6)。

import (
	"strings"

	"github.com/secondarykey/aibow/tool"
)

// workingRules は、ツールの一覧 tools に合わせた作業の決まり。書き換えるツールが無ければ空(読むだけの会話・質問だけの会話のプロンプトを増やさない。決定事項 4)。
//
// 作業フォルダがあるかは呼ぶ側(systemPrompt)が見る(環境の説明が無い会話には書かない)。
func workingRules(tools []tool.Tool) string {
	if tool.Find(tools, "edit_file") == nil && tool.Find(tools, "write_file") == nil {
		return ""
	}
	command := tool.Find(tools, "run_command") != nil
	lines := []string{
		// 直したところで止めて「直しました」と報告する(0012 の 6-3)。ツールを呼ばない返事で終わることを書く
		"Keep working with tools until the task is done. A reply without a tool call ends your turn, so do not stop halfway to report progress.",
		// テストのファイルのほうを書き換えて「直した」と答えた(0040 の試しで1回)
		"Read the code before you change it. Change the code the task is about; do not edit tests to make them pass unless the user asks.",
		// 「もう書いてある」と答えて直さなかった(0040 の試しで1回)。根拠の行を見せさせる
		"If you think no change is needed, quote the line you read that shows it.",
		// search_files を同じ形で続けて呼び、同じエラーの繰り返しで止まった(0040 の試しで1回。0025 の止める仕組みに頼る前に)
		"When a tool returns an error, read it and change the call. Do not repeat a call that failed.",
	}
	if command {
		// 確かめずに終える。確かめる道具(run_command)があるときだけ書く(aibow-server の資料のフォルダには無い。決定事項 4)
		check := "After you change files, check your work before you answer: run the test or build the user mentioned (or the usual one for the project) with run_command, and fix what fails."
		if tool.Find(tools, "git_diff") != nil {
			check += " You can review your changes with git_diff." // 読むだけの Git のツールがあるときだけ(段2。決定事項 14)
		}
		lines = append(lines, check)
		// 答えが空・まとめの間違い
		lines = append(lines, "End with a short answer: what you changed and the result of the check.")
	} else {
		// 確かめる道具が無いときは、確かめた結果を求めない(できないことを頼むと、小さなモデルは結果を作って書く)
		lines = append(lines, "End with a short answer: what you changed.")
	}
	return "# Working rules\n- " + strings.Join(lines, "\n- ")
}
