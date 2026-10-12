# 画面(UI)

`_cmd/aibow/frontend/src/` のチャット画面(React + TypeScript)の説明です。
Go とのやり取り(バインディング・イベント・停止)とアプリの起動は [_cmd/aibow/AGENTS.md](../_cmd/aibow/AGENTS.md) にあります。

## レイアウト

- 左: 会話の一覧。ウィンドウの上から下まで。上のハンバーガーで開閉し、閉じるとアイコンだけの細い列になる。
  右の仕切り(`Splitter.tsx`)で幅を変えられ、幅は localStorage に覚える
- 右上: タイトルバー(`TitleBar.tsx`)。Frameless のため自前で作る。開いている会話の名前と、右端に最小化・最大化・閉じる。
  一覧の上の空いているところと合わせてドラッグで移動できる
- 中央: チャット
  - 返事の最後にコピー(アイコンだけ)と「詳細」(統計行。ふだんは畳む)
  - コードブロックの右上に、そのコードだけのコピー(`CopyButton.tsx`)
  - 入力欄の下にモデルの選択、右端に「今すぐ要約」とメーター(`Meter.tsx`)
  - 停止ボタンは、生成中の返事の吹き出しの中に出す
- 設定の画面(`SettingsView.tsx`): 一覧の下の「設定」で開閉する。別の画面として扱い、チャットの代わりに会話の場所に出す。
  一覧で会話を選ぶ・「+ 新規」でも閉じる。入力欄の打ちかけの文は App が持つので消えない
- ウィンドウは起動時 720×820、最小 480×480(`main.go`)

## ファイルの分け方

- `useModels.ts` / `useConversations.ts` / `useChat.ts` / `useAppSettings.ts` が Go とのやり取り
- `components/` が表示
- `stickToBottom.ts` が「一番下にいるときだけ追いかける」スクロール
- `App.tsx` が全体をまとめる

## 新しい会話の画面

- 起動時と「+ 新規」は新しい会話の画面(`NewChat.tsx`、入力欄が真ん中)
- 会話はそこで最初に送信したときに作る(空の会話を作らない)

## お知らせとエラー

- モデルの読み込み・設定の変更などのお知らせは、会話の表示ではなく右上のトースト(`toast.tsx`)に出す
- 時間のかかる処理は出したままにし、同じ key で置き換える
- エラーは会話の表示に出す

## フォーカス

- 入力欄へ自動でフォーカスを移すとき(会話を開いた・「+ 新規」・返事が終わった)は、ほかの入力欄で打っている最中なら移さない(`Composer.tsx` の `focusUnlessTyping`)。
  会話の名前の変更(一覧のダブルクリック)の入力欄からフォーカスを奪って、編集が閉じてしまったため

## スクロール

- 生成中に届く断片で一番下へ送るのは、一番下にいるときだけ(`useStickToBottom`。チャット・思考の箱・要約中の箱)。上にスクロールして読んでいる間は送らない
- 「一番下にいたか」は scroll イベントではなく描画のたびのスクロール位置で決める(イベントは次のフレームに届くので、数ミリ秒ごとの断片に間に合わない)
- スクロールバーは `::-webkit-scrollbar` でつまみだけのフラットな形にし、色はテーマの `--scrollbar` / `--scrollbar-hover`。
  標準の `scrollbar-color` は使わない(指定すると Chromium は `::-webkit-scrollbar` を無視する)

## ウィンドウの移動とリサイズ(Frameless)

- ドラッグできる場所は CSS の `--wails-draggable: drag`(`.titlebar`)、ボタンは `no-drag`
- `main.tsx` で `@wailsio/runtime` を import しておく(ドラッグとリサイズはランタイムがページ全体のマウス操作を見て行う)
- リサイズの判定はマウスの座標だけで行われるので、ルート要素に余白は要らない(wails3 スキルの「5px の余白が必須」は beta.26 では不要)
- ただし、ウィンドウの端に接してスクロールする要素(`.chat` / `.settings-view`)は、スクロールバーが端の数 px(Windows の SM_CXSIZEFRAME)の
  マウスを受け取ってリサイズできなくなる(ランタイムがずらすのはページ全体のスクロールバーだけ)。
  透明な枠線 `border-right: var(--edge-gap) solid transparent` でスクロールバーを内側に寄せる。**端に接してスクロールする要素を足したら同じようにする**

## IME(日本語入力)が効かなくなる問題

- Windows の WebView2 は、別のウィンドウから戻ると IME が効かなくなることがある
- Go が `events.Windows.WindowSetFocus` で `aibow:window:focus` を送り、画面の `imeFocus.tsx` が見えない input を経由して入力欄にフォーカスを付け直す(同じ要素への blur → focus では直らない)
- IME 変換中・ボタンにフォーカスがあるときは触らない
- 新しい入力欄を足しても、テキスト入力なら自動で対象になる

## 画面を外から操作して確かめる

- Windows で CDP を使う方法: [env-windows.md](env-windows.md)
- GUI の無い Linux で Wails の server モード + Playwright を使う方法: [env-cloud.md](env-cloud.md)
