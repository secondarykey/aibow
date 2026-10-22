package ui

// settings.go: DB にまだ値が無いときの、画面で変えられる設定の初期値。
//
// 画面のアプリ(_cmd/aibow)と aibow-server の中のブラウザの会話(0016)で、同じ既定から始めるためにここに置く。
// 環境変数で変えられる値は、呼ぶ側が読んで渡す(読み方と、間違っていたときの伝え方がそれぞれの exe で違うため)。

import "github.com/secondarykey/aibow"

// DefaultSystemPrompt は新しい会話の既定のシステムプロンプト(画面で変えられる)。
const DefaultSystemPrompt = "あなたは親切なアシスタントです。日本語で簡潔に答えてください。"

// InitialValues は、環境変数で変えられる初期値(AIBOW_GPU_LAYERS・AIBOW_PRESENCE_PENALTY・AIBOW_HISTORY_TOKENS・AIBOW_MAX_TOKENS)。
type InitialValues struct {
	GPULayers       int
	PresencePenalty float64
	HistoryTokens   int
	ReplyTokens     int
}

// InitialDisplay は、まだ変えていない人の画面の設定(表示の設定は人ごと。0017 の 6)。
// 質問を右上に出すのは初めはオン(長い返事を読んでいても、何を聞いたかが見えるように)。明るさは OS の設定に合わせる
func InitialDisplay() aibow.DisplaySettings {
	return aibow.DisplaySettings{PinQuestion: true, Theme: aibow.ThemeSystem}
}

// InitialAppSettings は、DB にまだ値が無いときのアプリの設定(aibow.LoadAppSettings に渡す)。
func InitialAppSettings(v InitialValues) aibow.AppSettings {
	return aibow.AppSettings{
		Workspace: aibow.WorkspaceSettings{GPULayers: v.GPULayers},
		Display:   InitialDisplay(),
		Defaults: aibow.ConversationSettings{
			System:          DefaultSystemPrompt,
			Temperature:     0.7,
			Think:           true,
			PresencePenalty: v.PresencePenalty,
			Compact:         aibow.CompactSummary,
			// 会話の上限・返事の上限は新しい会話の既定(会話を作るときに写す。0009)
			HistoryTokens: v.HistoryTokens,
			ReplyTokens:   v.ReplyTokens,
		},
	}
}
